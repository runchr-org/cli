package memoryloop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/improve"
)

type FileLocation string

const (
	FileLocationProject  FileLocation = "project"
	FileLocationPersonal FileLocation = "personal"
)

type FileTarget struct {
	Path string
}

type SkillTargetInput struct {
	SkillName     string
	PreferredPath string
}

func RecordUsesSkillFileTargets(record MemoryRecord) bool {
	if record.Kind != KindSkillPatch {
		return false
	}
	if skillPath := strings.TrimSpace(record.SkillPath); skillPath != "" {
		return strings.EqualFold(filepath.Base(skillPath), "SKILL.md")
	}
	return strings.TrimSpace(record.SkillName) != ""
}

func ResolveFileTargetsForRecord(repoRoot string, location FileLocation, record MemoryRecord, input SkillTargetInput) ([]FileTarget, error) {
	if record.Kind == KindSkillPatch {
		if strings.TrimSpace(input.SkillName) != "" || strings.TrimSpace(input.PreferredPath) != "" {
			return ResolveSkillTargets(repoRoot, location, input)
		}
		if RecordUsesSkillFileTargets(record) {
			return ResolveSkillTargetsForRecord(repoRoot, location, record)
		}
	}
	return ResolveInstructionTargets(repoRoot, location)
}

func ResolveInstructionTargets(repoRoot string, location FileLocation) ([]FileTarget, error) {
	switch location {
	case FileLocationProject:
		return resolveProjectInstructionTargets(repoRoot)
	case FileLocationPersonal:
		return resolvePersonalInstructionTargets()
	default:
		return nil, fmt.Errorf("invalid file location: %s", location)
	}
}

func ResolveSkillTargets(repoRoot string, location FileLocation, input SkillTargetInput) ([]FileTarget, error) {
	switch location {
	case FileLocationProject:
		return resolveProjectSkillTargets(repoRoot, input)
	case FileLocationPersonal:
		return resolvePersonalSkillTargets(input)
	default:
		return nil, fmt.Errorf("invalid file location: %s", location)
	}
}

func ResolveSkillTargetsForRecord(repoRoot string, location FileLocation, record MemoryRecord) ([]FileTarget, error) {
	if record.Kind != KindSkillPatch {
		return nil, fmt.Errorf("memory %s is not a skill patch", record.ID)
	}

	input := SkillTargetInput{
		SkillName:     strings.TrimSpace(record.SkillName),
		PreferredPath: strings.TrimSpace(record.SkillPath),
	}
	if input.SkillName == "" && input.PreferredPath != "" {
		input.SkillName = filepath.Base(filepath.Dir(input.PreferredPath))
	}
	if input.SkillName == "" && input.PreferredPath == "" {
		return nil, fmt.Errorf("skill metadata is required for memory %q", record.Title)
	}
	return ResolveSkillTargets(repoRoot, location, input)
}

func resolveProjectInstructionTargets(repoRoot string) ([]FileTarget, error) {
	var targets []FileTarget
	for _, cf := range improve.DetectContextFiles(repoRoot) {
		if !cf.Exists {
			continue
		}
		switch cf.Type {
		case improve.ContextFileAGENTSMD, improve.ContextFileCLAUDEMD:
			targets = append(targets, FileTarget{Path: cf.Path})
		case improve.ContextFileCursorRules, improve.ContextFileGemini:
			continue
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("no project instruction files found")
	}
	sortTargets(targets)
	return targets, nil
}

func resolvePersonalInstructionTargets() ([]FileTarget, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	var targets []FileTarget
	if dirExists(filepath.Join(homeDir, ".claude")) {
		targets = append(targets, FileTarget{Path: filepath.Join(homeDir, ".claude", "CLAUDE.md")})
	}
	if codexHome := resolveCodexHomePath(homeDir); dirExists(codexHome) {
		targets = append(targets, FileTarget{Path: filepath.Join(codexHome, "AGENTS.md")})
	}
	if dirExists(filepath.Join(homeDir, ".gemini")) {
		targets = append(targets, FileTarget{Path: filepath.Join(homeDir, ".gemini", "AGENTS.md")})
	}
	if len(targets) == 0 {
		return nil, errors.New("no personal instruction targets found")
	}
	sortTargets(targets)
	return targets, nil
}

func resolveProjectSkillTargets(repoRoot string, input SkillTargetInput) ([]FileTarget, error) {
	preferred := strings.TrimSpace(input.PreferredPath)
	if preferred != "" {
		preferred = normalizePreferredPath(repoRoot, preferred)
		if preferred != "" && fileExists(preferred) {
			return []FileTarget{{Path: preferred}}, nil
		}
	}

	targets, err := matchingSkillTargets(projectSkillGlobs(repoRoot), input.SkillName)
	if err != nil {
		return nil, err
	}
	sortTargets(targets)
	return targets, nil
}

func resolvePersonalSkillTargets(input SkillTargetInput) ([]FileTarget, error) {
	globs, err := personalSkillGlobs()
	if err != nil {
		return nil, err
	}
	targets, err := matchingSkillTargets(globs, input.SkillName)
	if err != nil {
		return nil, err
	}
	sortTargets(targets)
	return targets, nil
}

func projectSkillGlobs(repoRoot string) []string {
	return []string{
		filepath.Join(repoRoot, ".claude", "skills", "*", "SKILL.md"),
		filepath.Join(repoRoot, ".codex", "skills", "*", "SKILL.md"),
	}
}

func personalSkillGlobs() ([]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	globs := make([]string, 0, 2)
	if dirExists(filepath.Join(homeDir, ".claude")) {
		globs = append(globs, filepath.Join(homeDir, ".claude", "skills", "*", "SKILL.md"))
	}
	if codexHome := resolveCodexHomePath(homeDir); dirExists(codexHome) {
		globs = append(globs, filepath.Join(codexHome, "skills", "*", "SKILL.md"))
	}
	return globs, nil
}

func matchingSkillTargets(globs []string, skillName string) ([]FileTarget, error) {
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return nil, errors.New("skill name is required")
	}

	var targets []FileTarget
	for _, pattern := range globs {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("glob skill targets: %w", err)
		}
		for _, match := range matches {
			if filepath.Base(filepath.Dir(match)) != skillName {
				continue
			}
			targets = append(targets, FileTarget{Path: match})
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no skill targets found for %q", skillName)
	}
	return targets, nil
}

func resolveCodexHomePath(homeDir string) string {
	if codexHome := os.Getenv("CODEX_HOME"); codexHome != "" {
		return codexHome
	}
	return filepath.Join(homeDir, ".codex")
}

func normalizePreferredPath(repoRoot, preferredPath string) string {
	repoRoot = filepath.Clean(repoRoot)
	if filepath.IsAbs(preferredPath) {
		preferredPath = filepath.Clean(preferredPath)
		if isPathWithinRoot(repoRoot, preferredPath) {
			return preferredPath
		}
		return ""
	}
	preferredPath = filepath.Clean(filepath.Join(repoRoot, preferredPath))
	if isPathWithinRoot(repoRoot, preferredPath) {
		return preferredPath
	}
	return ""
}

func isPathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sortTargets(targets []FileTarget) {
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Path < targets[j].Path
	})
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
