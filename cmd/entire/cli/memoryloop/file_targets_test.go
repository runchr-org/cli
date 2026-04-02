package memoryloop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/stretchr/testify/require"
)

func TestResolveInstructionTargets_ProjectUsesAllExistingRepoFiles(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, "AGENTS.md", "# Agents\n")
	testutil.WriteFile(t, repoRoot, "CLAUDE.md", "# Claude\n")

	targets, err := ResolveInstructionTargets(repoRoot, FileLocationProject)
	require.NoError(t, err)
	require.Len(t, targets, 2)
	require.Equal(t, filepath.Join(repoRoot, "AGENTS.md"), targets[0].Path)
	require.Equal(t, filepath.Join(repoRoot, "CLAUDE.md"), targets[1].Path)
}

func TestResolveInstructionTargets_ProjectUsesSingleExistingRepoFile(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, "CLAUDE.md", "# Claude\n")

	targets, err := ResolveInstructionTargets(repoRoot, FileLocationProject)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, filepath.Join(repoRoot, "CLAUDE.md"), targets[0].Path)
}

func TestResolveInstructionTargets_ProjectFailsWithoutInstructionFiles(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()

	targets, err := ResolveInstructionTargets(repoRoot, FileLocationProject)
	require.Error(t, err)
	require.Nil(t, targets)
}

func TestResolveInstructionTargets_PersonalUsesInstalledAgentRoots(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".claude"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".gemini"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".codex"), 0o755))
	t.Setenv("HOME", homeDir)
	t.Setenv("CODEX_HOME", "")

	targets, err := ResolveInstructionTargets(t.TempDir(), FileLocationPersonal)
	require.NoError(t, err)
	require.Len(t, targets, 3)
	require.Equal(t, filepath.Join(homeDir, ".claude", "CLAUDE.md"), targets[0].Path)
	require.Equal(t, filepath.Join(homeDir, ".codex", "AGENTS.md"), targets[1].Path)
	require.Equal(t, filepath.Join(homeDir, ".gemini", "AGENTS.md"), targets[2].Path)
}

func TestResolveSkillTargets_ProjectUsesPreferredRepoSkillPath(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	preferred := filepath.Join(repoRoot, ".claude", "skills", "review", "SKILL.md")
	other := filepath.Join(repoRoot, ".codex", "skills", "review", "SKILL.md")
	testutil.WriteFile(t, repoRoot, ".claude/skills/review/SKILL.md", "# Claude review\n")
	testutil.WriteFile(t, repoRoot, ".codex/skills/review/SKILL.md", "# Codex review\n")

	targets, err := ResolveSkillTargets(repoRoot, FileLocationProject, SkillTargetInput{
		SkillName:     "review",
		PreferredPath: preferred,
	})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, preferred, targets[0].Path)
	require.NotEqual(t, other, targets[0].Path)
}

func TestResolveSkillTargets_ProjectIgnoresExternalPreferredPath(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, ".claude/skills/review/SKILL.md", "# Claude review\n")

	externalRoot := t.TempDir()
	external := filepath.Join(externalRoot, "SKILL.md")
	require.NoError(t, os.WriteFile(external, []byte("# External review\n"), 0o644))

	targets, err := ResolveSkillTargets(repoRoot, FileLocationProject, SkillTargetInput{
		SkillName:     "review",
		PreferredPath: external,
	})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, filepath.Join(repoRoot, ".claude", "skills", "review", "SKILL.md"), targets[0].Path)
}

func TestResolveSkillTargets_ProjectFailsWhenSkillMissing(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()

	targets, err := ResolveSkillTargets(repoRoot, FileLocationProject, SkillTargetInput{
		SkillName: "review",
	})
	require.Error(t, err)
	require.Nil(t, targets)
}

func TestResolveSkillTargets_PersonalMatchesAllInstalledPersonalSkills(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".claude", "skills", "review"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".codex", "skills", "review"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".gemini"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".claude", "skills", "review", "SKILL.md"), []byte("# Claude review\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".codex", "skills", "review", "SKILL.md"), []byte("# Codex review\n"), 0o644))
	t.Setenv("HOME", homeDir)
	t.Setenv("CODEX_HOME", "")

	targets, err := ResolveSkillTargets(t.TempDir(), FileLocationPersonal, SkillTargetInput{
		SkillName: "review",
	})
	require.NoError(t, err)
	require.Len(t, targets, 2)
	require.Equal(t, filepath.Join(homeDir, ".claude", "skills", "review", "SKILL.md"), targets[0].Path)
	require.Equal(t, filepath.Join(homeDir, ".codex", "skills", "review", "SKILL.md"), targets[1].Path)
}

func TestResolveSkillTargetsForRecord_ProjectUsesExplicitSkillMetadata(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, ".claude/skills/review/SKILL.md", "# Claude review\n")
	testutil.WriteFile(t, repoRoot, ".codex/skills/review/SKILL.md", "# Codex review\n")

	targets, err := ResolveSkillTargetsForRecord(repoRoot, FileLocationProject, MemoryRecord{
		ID:        "review-skill",
		Kind:      KindSkillPatch,
		Title:     "Tighten the review skill",
		Body:      "Add the missing retry step to the review skill instructions.",
		SkillName: "review",
	})
	require.NoError(t, err)
	require.Len(t, targets, 2)
	require.Equal(t, filepath.Join(repoRoot, ".claude", "skills", "review", "SKILL.md"), targets[0].Path)
	require.Equal(t, filepath.Join(repoRoot, ".codex", "skills", "review", "SKILL.md"), targets[1].Path)
}

func TestResolveSkillTargetsForRecord_ProjectUsesExplicitPreferredPath(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	preferred := filepath.Join(repoRoot, ".claude", "skills", "review", "SKILL.md")
	testutil.WriteFile(t, repoRoot, ".claude/skills/review/SKILL.md", "# Claude review\n")
	testutil.WriteFile(t, repoRoot, ".codex/skills/review/SKILL.md", "# Codex review\n")

	targets, err := ResolveSkillTargetsForRecord(repoRoot, FileLocationProject, MemoryRecord{
		ID:        "review-skill",
		Kind:      KindSkillPatch,
		Title:     "Tighten the review skill",
		Body:      "Add the missing retry step to the review skill instructions.",
		SkillName: "review",
		SkillPath: preferred,
	})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, preferred, targets[0].Path)
}

func TestResolveSkillTargetsForRecord_ProjectFailsWithoutExplicitSkillMetadata(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	testutil.WriteFile(t, repoRoot, ".claude/skills/review/SKILL.md", "# Claude review\n")

	targets, err := ResolveSkillTargetsForRecord(repoRoot, FileLocationProject, MemoryRecord{
		ID:    "review-skill",
		Kind:  KindSkillPatch,
		Title: "Tighten the review skill",
		Body:  "Add the missing retry step to the review skill instructions.",
	})
	require.Error(t, err)
	require.Nil(t, targets)
	require.Contains(t, err.Error(), "skill metadata")
}
