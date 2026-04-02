package memoryloop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/skillimprove"
)

type FileApplicationInput struct {
	ID          string
	Location    FileLocation
	Targets     []FileTarget
	SkillTarget SkillTargetInput
}

func ApplyRecordToFiles(records []MemoryRecord, repoRoot string, input FileApplicationInput, now time.Time) ([]MemoryRecord, MemoryRecord, []FileTarget, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return records, MemoryRecord{}, nil, errors.New("memory id is required")
	}

	updated := append([]MemoryRecord(nil), records...)
	recordIdx := -1
	for i := range updated {
		if updated[i].ID == id {
			recordIdx = i
			break
		}
	}
	if recordIdx < 0 {
		return updated, MemoryRecord{}, nil, fmt.Errorf("memory not found: %s", id)
	}

	record := updated[recordIdx]
	targets := append([]FileTarget(nil), input.Targets...)
	if len(targets) == 0 {
		var err error
		targets, err = ResolveFileTargetsForRecord(repoRoot, input.Location, record, input.SkillTarget)
		if err != nil {
			return updated, MemoryRecord{}, nil, err
		}
	}

	for _, target := range targets {
		if err := applyRecordContentToTarget(record, target.Path); err != nil {
			return updated, MemoryRecord{}, targets, err
		}
	}

	record.Status = StatusArchived
	record.UpdatedAt = now
	record.LastReviewedAt = now
	record.History = append(record.History, HistoryEvent{
		Type:   "applied_to_files",
		At:     now,
		Detail: buildAppliedFilesDetail(input.Location, targets),
	})
	updated[recordIdx] = record
	return updated, record, targets, nil
}

func applyRecordContentToTarget(record MemoryRecord, path string) error {
	if err := ensureTargetFile(path); err != nil {
		return err
	}

	currentBytes, err := os.ReadFile(path) //nolint:gosec // resolved file target path
	if err != nil {
		return fmt.Errorf("read target file %s: %w", path, err)
	}
	current := string(currentBytes)
	next := updatedTargetContent(current, record)
	if next == current {
		return nil
	}

	diff := buildWholeFileDiff(path, current, next)
	if err := skillimprove.ApplyDiff(path, diff); err != nil {
		return fmt.Errorf("apply diff to %s: %w", path, err)
	}
	return nil
}

func ensureTargetFile(path string) error {
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); err != nil {
		return fmt.Errorf("target directory %s: %w", parent, err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, nil, 0o644); err != nil { //nolint:gosec // resolved file target path
			return fmt.Errorf("create target file %s: %w", path, err)
		}
	}
	return nil
}

func updatedTargetContent(current string, record MemoryRecord) string {
	entry := renderManagedEntry(record)
	if entry == "" {
		return current
	}

	section := extractManagedSection(current)
	if section == "" {
		return appendManagedSection(current, entry)
	}
	updatedSection := upsertManagedEntry(section, record)
	return replaceManagedSection(current, section, updatedSection)
}

func buildWholeFileDiff(path, current, next string) string {
	oldLines := splitDiffLines(current)
	newLines := splitDiffLines(next)

	oldStart := 1
	newStart := 1
	lines := make([]string, 0, len(oldLines)+len(newLines)+3)
	lines = append(lines,
		"--- a/"+filepath.Base(path),
		"+++ b/"+filepath.Base(path),
		fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, len(oldLines), newStart, len(newLines)),
	)
	for _, line := range oldLines {
		lines = append(lines, "-"+line)
	}
	for _, line := range newLines {
		lines = append(lines, "+"+line)
	}
	return strings.Join(lines, "\n")
}

const (
	managedSectionStart = "<!-- ENTIRE-MEMORY-LOOP START -->"
	managedSectionEnd   = "<!-- ENTIRE-MEMORY-LOOP END -->"
)

func renderManagedEntry(record MemoryRecord) string {
	body := strings.TrimSpace(record.Body)
	if body == "" {
		return ""
	}

	return managedEntryBlock(record)
}

func extractManagedSection(current string) string {
	startIdx := strings.Index(current, managedSectionStart)
	if startIdx < 0 {
		return ""
	}
	endIdx := strings.Index(current[startIdx:], managedSectionEnd)
	if endIdx < 0 {
		return ""
	}
	endIdx += startIdx + len(managedSectionEnd)
	if endIdx < len(current) && current[endIdx] == '\n' {
		endIdx++
	}
	return current[startIdx:endIdx]
}

func appendManagedSection(current, entry string) string {
	current = strings.TrimRight(current, "\n")
	if current != "" {
		current += "\n\n"
	}
	return current + managedSectionStart + "\n" + entry + managedSectionEnd + "\n"
}

func replaceManagedSection(current, oldSection, newSection string) string {
	return strings.Replace(current, oldSection, newSection, 1)
}

func upsertManagedEntry(section string, record MemoryRecord) string {
	block := managedEntryBlock(record)
	startMarker := managedEntryStartMarker(record)
	legacyStartMarker := legacyManagedEntryStartMarker(record)
	endMarker := managedEntryEndMarker()

	if idx := strings.Index(section, startMarker); idx >= 0 {
		blockEnd := strings.Index(section[idx:], endMarker)
		if blockEnd >= 0 {
			blockEnd += idx + len(endMarker)
			return section[:idx] + block + section[blockEnd:]
		}
	}

	if idx := strings.Index(section, legacyStartMarker); idx >= 0 {
		blockEnd := strings.Index(section[idx:], endMarker)
		if blockEnd >= 0 {
			blockEnd += idx + len(endMarker)
			return section[:idx] + block + section[blockEnd:]
		}
	}

	if idx := strings.LastIndex(section, managedSectionEnd); idx >= 0 {
		return section[:idx] + block + section[idx:]
	}

	return section
}

func managedEntryStartMarker(record MemoryRecord) string {
	return fmt.Sprintf(
		"<!-- ENTIRE-MEMORY-ENTRY id=%s kind=%s title=%s -->",
		strconv.Quote(strings.TrimSpace(record.ID)),
		strconv.Quote(string(record.Kind)),
		strconv.Quote(strings.TrimSpace(record.Title)),
	)
}

func legacyManagedEntryStartMarker(record MemoryRecord) string {
	return fmt.Sprintf(
		"<!-- ENTIRE-MEMORY-ENTRY kind=%s title=%s -->",
		strconv.Quote(string(record.Kind)),
		strconv.Quote(strings.TrimSpace(record.Title)),
	)
}

func managedEntryEndMarker() string {
	return "<!-- ENTIRE-MEMORY-ENTRY END -->"
}

func managedEntryBlock(record MemoryRecord) string {
	body := strings.TrimSpace(record.Body)
	return managedEntryStartMarker(record) + "\n" + body + "\n" + managedEntryEndMarker() + "\n"
}

func splitDiffLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

func buildAppliedFilesDetail(location FileLocation, targets []FileTarget) string {
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		paths = append(paths, target.Path)
	}
	return fmt.Sprintf("%s:%s", location, strings.Join(paths, ","))
}
