package cli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type whyBlameLine struct {
	CommitHash   string
	OriginalLine int
	FinalLine    int
	Author       string
	AuthorTime   time.Time
	Filename     string
	Source       string
}

type whyBlameBlock struct {
	CommitHash string
	StartLine  int
	EndLine    int
	StartRow   int
	EndRow     int
}

type whyBlameRow struct {
	whyBlameLine

	BlockIndex int
}

type whyBlameMetadata struct {
	Author     string
	AuthorTime time.Time
	Filename   string
}

func runGitBlame(ctx context.Context, repoRoot, gitPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "blame", "--line-porcelain", "--", gitPath)
	cmd.Dir = repoRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		details := strings.TrimSpace(stderr.String())
		if details == "" {
			return nil, fmt.Errorf("git blame failed for %s: %w", gitPath, err)
		}
		return nil, fmt.Errorf("git blame failed for %s: %w: %s", gitPath, err, details)
	}

	return stdout.Bytes(), nil
}

func parseBlamePorcelain(data []byte) ([]whyBlameLine, error) {
	if len(data) == 0 {
		return nil, nil
	}

	rawLines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
	metadataByCommit := make(map[string]whyBlameMetadata)
	lines := make([]whyBlameLine, 0)

	var current *whyBlameLine
	for lineNumber, raw := range rawLines {
		line := strings.TrimSuffix(string(raw), "\r")

		if hash, originalLine, finalLine, ok, err := parseBlameHeader(line); err != nil {
			return nil, fmt.Errorf("parse blame header on line %d: %w", lineNumber+1, err)
		} else if ok {
			if current != nil {
				return nil, fmt.Errorf("blame record for %s:%d missing source line", current.CommitHash, current.FinalLine)
			}
			next := whyBlameLine{
				CommitHash:   hash,
				OriginalLine: originalLine,
				FinalLine:    finalLine,
			}
			if metadata, exists := metadataByCommit[hash]; exists {
				applyBlameMetadata(&next, metadata)
			}
			current = &next
			continue
		}

		if current == nil {
			if strings.TrimSpace(line) == "" {
				continue
			}
			return nil, fmt.Errorf("blame metadata without header on line %d", lineNumber+1)
		}

		if strings.HasPrefix(line, "\t") {
			current.Source = strings.TrimPrefix(line, "\t")
			lines = append(lines, *current)
			metadataByCommit[current.CommitHash] = metadataFromBlameLine(*current)
			current = nil
			continue
		}

		if err := applyBlameMetadataLine(current, line); err != nil {
			return nil, fmt.Errorf("parse blame metadata on line %d: %w", lineNumber+1, err)
		}
		metadataByCommit[current.CommitHash] = metadataFromBlameLine(*current)
	}

	if current != nil {
		return nil, fmt.Errorf("blame record for %s:%d missing source line", current.CommitHash, current.FinalLine)
	}

	return lines, nil
}

func parseBlameHeader(line string) (string, int, int, bool, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 || !isBlameHash(fields[0]) {
		return "", 0, 0, false, nil
	}

	originalLine, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("invalid original line %q", fields[1])
	}
	finalLine, err := strconv.Atoi(fields[2])
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("invalid final line %q", fields[2])
	}

	return fields[0], originalLine, finalLine, true, nil
}

func isBlameHash(value string) bool {
	if len(value) < 7 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func applyBlameMetadataLine(line *whyBlameLine, metadata string) error {
	switch {
	case strings.HasPrefix(metadata, "author "):
		line.Author = strings.TrimPrefix(metadata, "author ")
	case strings.HasPrefix(metadata, "author-time "):
		raw := strings.TrimPrefix(metadata, "author-time ")
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid author-time %q", raw)
		}
		line.AuthorTime = time.Unix(seconds, 0)
	case strings.HasPrefix(metadata, "filename "):
		line.Filename = strings.TrimPrefix(metadata, "filename ")
	}
	return nil
}

func metadataFromBlameLine(line whyBlameLine) whyBlameMetadata {
	return whyBlameMetadata{
		Author:     line.Author,
		AuthorTime: line.AuthorTime,
		Filename:   line.Filename,
	}
}

func applyBlameMetadata(line *whyBlameLine, metadata whyBlameMetadata) {
	line.Author = metadata.Author
	line.AuthorTime = metadata.AuthorTime
	line.Filename = metadata.Filename
}

func collapseWhyBlameBlocks(lines []whyBlameLine) []whyBlameBlock {
	if len(lines) == 0 {
		return nil
	}

	blocks := make([]whyBlameBlock, 0)
	current := whyBlameBlock{
		CommitHash: lines[0].CommitHash,
		StartLine:  lines[0].FinalLine,
		EndLine:    lines[0].FinalLine,
		StartRow:   0,
		EndRow:     0,
	}

	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line.CommitHash == current.CommitHash {
			current.EndLine = line.FinalLine
			current.EndRow = i
			continue
		}

		blocks = append(blocks, current)
		current = whyBlameBlock{
			CommitHash: line.CommitHash,
			StartLine:  line.FinalLine,
			EndLine:    line.FinalLine,
			StartRow:   i,
			EndRow:     i,
		}
	}

	blocks = append(blocks, current)
	return blocks
}

func buildWhyBlameRows(lines []whyBlameLine, blocks []whyBlameBlock) []whyBlameRow {
	rows := make([]whyBlameRow, len(lines))
	for i, line := range lines {
		rows[i] = whyBlameRow{whyBlameLine: line}
	}
	for blockIndex, block := range blocks {
		for rowIndex := block.StartRow; rowIndex <= block.EndRow && rowIndex < len(rows); rowIndex++ {
			rows[rowIndex].BlockIndex = blockIndex
		}
	}
	return rows
}
