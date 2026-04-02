package memoryloop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/stretchr/testify/require"
)

func TestApplyRecordToFiles_ProjectInstructionTargetsArchiveMemory(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, "AGENTS.md", "# Agents\n")
	testutil.WriteFile(t, repoRoot, "CLAUDE.md", "# Claude\n")
	now := time.Date(2026, time.April, 1, 20, 0, 0, 0, time.UTC)
	records := []MemoryRecord{
		{
			ID:        "lint",
			Kind:      KindRepoRule,
			Title:     "Run lint before finishing",
			Body:      "Run golangci-lint before claiming completion.",
			ScopeKind: ScopeKindMe,
			Status:    StatusActive,
			UpdatedAt: now.Add(-time.Hour),
		},
	}

	updated, applied, targets, err := ApplyRecordToFiles(records, repoRoot, FileApplicationInput{
		ID:       "lint",
		Location: FileLocationProject,
	}, now)
	require.NoError(t, err)
	require.Len(t, targets, 2)
	require.Equal(t, StatusArchived, updated[0].Status)
	require.Equal(t, applied.ID, updated[0].ID)
	require.Equal(t, "applied_to_files", updated[0].History[len(updated[0].History)-1].Type)
	require.Contains(t, updated[0].History[len(updated[0].History)-1].Detail, "project")
	require.Contains(t, updated[0].History[len(updated[0].History)-1].Detail, filepath.Join(repoRoot, "AGENTS.md"))
	require.Contains(t, testutil.ReadFile(t, repoRoot, "AGENTS.md"), records[0].Body)
	require.Contains(t, testutil.ReadFile(t, repoRoot, "CLAUDE.md"), records[0].Body)
}

func TestApplyRecordToFiles_PersonalInstructionTargetsArchiveMemory(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".claude"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".codex"), 0o755))
	t.Setenv("HOME", homeDir)
	t.Setenv("CODEX_HOME", "")

	now := time.Date(2026, time.April, 1, 20, 15, 0, 0, time.UTC)
	records := []MemoryRecord{
		{
			ID:        "lint",
			Kind:      KindRepoRule,
			Title:     "Run lint before finishing",
			Body:      "Run golangci-lint before claiming completion.",
			ScopeKind: ScopeKindMe,
			Status:    StatusActive,
		},
	}

	updated, _, targets, err := ApplyRecordToFiles(records, t.TempDir(), FileApplicationInput{
		ID:       "lint",
		Location: FileLocationPersonal,
	}, now)
	require.NoError(t, err)
	require.Len(t, targets, 2)
	require.Equal(t, StatusArchived, updated[0].Status)
	require.Contains(t, testutil.ReadFile(t, homeDir, ".claude/CLAUDE.md"), records[0].Body)
	require.Contains(t, testutil.ReadFile(t, homeDir, ".codex/AGENTS.md"), records[0].Body)
}

func TestApplyRecordToFiles_SkillPatchUpdatesTargetSkillAndArchivesMemory(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, ".claude/skills/review/SKILL.md", "# Review\n")
	now := time.Date(2026, time.April, 1, 20, 30, 0, 0, time.UTC)
	records := []MemoryRecord{
		{
			ID:        "review-skill",
			Kind:      KindSkillPatch,
			Title:     "Tighten the review skill",
			Body:      "Add the missing retry step to the review skill instructions.",
			ScopeKind: ScopeKindMe,
			Status:    StatusActive,
		},
	}

	updated, _, targets, err := ApplyRecordToFiles(records, repoRoot, FileApplicationInput{
		ID:       "review-skill",
		Location: FileLocationProject,
		SkillTarget: SkillTargetInput{
			SkillName:     "review",
			PreferredPath: filepath.Join(repoRoot, ".claude", "skills", "review", "SKILL.md"),
		},
	}, now)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, StatusArchived, updated[0].Status)
	require.Contains(t, testutil.ReadFile(t, repoRoot, ".claude/skills/review/SKILL.md"), records[0].Body)
}

func TestApplyRecordToFiles_SkillPatchUsesTargetsFromRecordMetadata(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, ".claude/skills/review/SKILL.md", "# Review\n")
	now := time.Date(2026, time.April, 1, 20, 35, 0, 0, time.UTC)
	records := []MemoryRecord{
		{
			ID:        "review-skill",
			Kind:      KindSkillPatch,
			Title:     "Tighten the review skill",
			Body:      "Add the missing retry step to the review skill instructions.",
			SkillName: "review",
			SkillPath: filepath.Join(repoRoot, ".claude", "skills", "review", "SKILL.md"),
			ScopeKind: ScopeKindMe,
			Status:    StatusActive,
		},
	}

	updated, _, targets, err := ApplyRecordToFiles(records, repoRoot, FileApplicationInput{
		ID:       "review-skill",
		Location: FileLocationProject,
	}, now)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, filepath.Join(repoRoot, ".claude", "skills", "review", "SKILL.md"), targets[0].Path)
	require.Equal(t, StatusArchived, updated[0].Status)
	require.Contains(t, testutil.ReadFile(t, repoRoot, ".claude/skills/review/SKILL.md"), records[0].Body)
}

func TestApplyRecordToFiles_WriteFailureDoesNotArchiveMemory(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, "AGENTS.md", "# Agents\n")
	now := time.Date(2026, time.April, 1, 20, 45, 0, 0, time.UTC)
	records := []MemoryRecord{
		{
			ID:        "lint",
			Kind:      KindRepoRule,
			Title:     "Run lint before finishing",
			Body:      "Run golangci-lint before claiming completion.",
			ScopeKind: ScopeKindMe,
			Status:    StatusActive,
		},
	}

	updated, _, _, err := ApplyRecordToFiles(records, repoRoot, FileApplicationInput{
		ID:       "lint",
		Location: FileLocationProject,
		Targets: []FileTarget{
			{Path: filepath.Join(repoRoot, "AGENTS.md")},
			{Path: filepath.Join(repoRoot, "missing", "CLAUDE.md")},
		},
	}, now)
	require.Error(t, err)
	require.Equal(t, StatusActive, updated[0].Status)
}

func TestApplyRecordToFiles_UpdatesManagedInstructionEntryInPlace(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, "AGENTS.md", "# Agents\n")
	now := time.Date(2026, time.April, 1, 21, 0, 0, 0, time.UTC)
	records := []MemoryRecord{
		{
			ID:        "lint",
			Kind:      KindRepoRule,
			Title:     "Run lint before finishing",
			Body:      "Run golangci-lint before claiming completion.",
			ScopeKind: ScopeKindMe,
			Status:    StatusActive,
		},
	}

	_, _, _, err := ApplyRecordToFiles(records, repoRoot, FileApplicationInput{
		ID:       "lint",
		Location: FileLocationProject,
	}, now)
	require.NoError(t, err)

	records[0].Body = "Run golangci-lint and go test before claiming completion."
	_, _, _, err = ApplyRecordToFiles(records, repoRoot, FileApplicationInput{
		ID:       "lint",
		Location: FileLocationProject,
	}, now.Add(time.Minute))
	require.NoError(t, err)

	content := testutil.ReadFile(t, repoRoot, "AGENTS.md")
	require.Equal(t, 1, strings.Count(content, "Run lint before finishing"))
	require.Contains(t, content, "Run golangci-lint and go test before claiming completion.")
	require.NotContains(t, content, "Run golangci-lint before claiming completion.")
}

func TestApplyRecordToFiles_UpdatesManagedSkillEntryInPlace(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, ".claude/skills/review/SKILL.md", "# Review\n")
	now := time.Date(2026, time.April, 1, 21, 15, 0, 0, time.UTC)
	records := []MemoryRecord{
		{
			ID:        "review-skill",
			Kind:      KindSkillPatch,
			Title:     "Tighten the review skill",
			Body:      "Add the missing retry step to the review skill instructions.",
			ScopeKind: ScopeKindMe,
			Status:    StatusActive,
		},
	}

	_, _, _, err := ApplyRecordToFiles(records, repoRoot, FileApplicationInput{
		ID:       "review-skill",
		Location: FileLocationProject,
		SkillTarget: SkillTargetInput{
			SkillName:     "review",
			PreferredPath: filepath.Join(repoRoot, ".claude", "skills", "review", "SKILL.md"),
		},
	}, now)
	require.NoError(t, err)

	records[0].Body = "Add retry and timeout guidance to the review skill instructions."
	_, _, _, err = ApplyRecordToFiles(records, repoRoot, FileApplicationInput{
		ID:       "review-skill",
		Location: FileLocationProject,
		SkillTarget: SkillTargetInput{
			SkillName:     "review",
			PreferredPath: filepath.Join(repoRoot, ".claude", "skills", "review", "SKILL.md"),
		},
	}, now.Add(time.Minute))
	require.NoError(t, err)

	content := testutil.ReadFile(t, repoRoot, ".claude/skills/review/SKILL.md")
	require.Equal(t, 1, strings.Count(content, "Tighten the review skill"))
	require.Contains(t, content, "Add retry and timeout guidance to the review skill instructions.")
	require.NotContains(t, content, "Add the missing retry step to the review skill instructions.")
}

func TestApplyRecordToFiles_SameTitleDifferentIDsDoNotOverwrite(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, "AGENTS.md", "# Agents\n")
	now := time.Date(2026, time.April, 1, 21, 30, 0, 0, time.UTC)

	records := []MemoryRecord{
		{
			ID:        "lint-a",
			Kind:      KindRepoRule,
			Title:     "Run lint before finishing",
			Body:      "Run golangci-lint before claiming completion.",
			ScopeKind: ScopeKindMe,
			Status:    StatusActive,
		},
		{
			ID:        "lint-b",
			Kind:      KindRepoRule,
			Title:     "Run lint before finishing",
			Body:      "Run staticcheck before claiming completion.",
			ScopeKind: ScopeKindRepo,
			Status:    StatusActive,
		},
	}

	_, _, _, err := ApplyRecordToFiles(records[:1], repoRoot, FileApplicationInput{
		ID:       "lint-a",
		Location: FileLocationProject,
	}, now)
	require.NoError(t, err)

	_, _, _, err = ApplyRecordToFiles(records[1:], repoRoot, FileApplicationInput{
		ID:       "lint-b",
		Location: FileLocationProject,
	}, now.Add(time.Minute))
	require.NoError(t, err)

	content := testutil.ReadFile(t, repoRoot, "AGENTS.md")
	require.Contains(t, content, `id="lint-a"`)
	require.Contains(t, content, `id="lint-b"`)
	require.Contains(t, content, "Run golangci-lint before claiming completion.")
	require.Contains(t, content, "Run staticcheck before claiming completion.")
}

func TestApplyRecordToFiles_UpdatesLegacyManagedEntryInPlace(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	legacyContent := strings.Join([]string{
		"# Agents",
		"",
		managedSectionStart,
		`<!-- ENTIRE-MEMORY-ENTRY kind="repo_rule" title="Run lint before finishing" -->`,
		"Run golangci-lint before claiming completion.",
		managedEntryEndMarker(),
		managedSectionEnd,
		"",
	}, "\n")
	testutil.WriteFile(t, repoRoot, "AGENTS.md", legacyContent)

	now := time.Date(2026, time.April, 1, 21, 45, 0, 0, time.UTC)
	records := []MemoryRecord{
		{
			ID:        "lint",
			Kind:      KindRepoRule,
			Title:     "Run lint before finishing",
			Body:      "Run golangci-lint and go test before claiming completion.",
			ScopeKind: ScopeKindMe,
			Status:    StatusActive,
		},
	}

	_, _, _, err := ApplyRecordToFiles(records, repoRoot, FileApplicationInput{
		ID:       "lint",
		Location: FileLocationProject,
		Targets:  []FileTarget{{Path: filepath.Join(repoRoot, "AGENTS.md")}},
	}, now)
	require.NoError(t, err)

	content := testutil.ReadFile(t, repoRoot, "AGENTS.md")
	require.Contains(t, content, `id="lint"`)
	require.Contains(t, content, "Run golangci-lint and go test before claiming completion.")
	require.NotContains(t, content, "Run golangci-lint before claiming completion.")
	require.Equal(t, 1, strings.Count(content, managedSectionStart))
}
