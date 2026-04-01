package skilldb_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/skilldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func setIsolatedHome(t *testing.T, root string) {
	t.Helper()
	t.Setenv("HOME", root)
}

func TestDiscoverSkills_ClaudeSkills(t *testing.T) {
	root := t.TempDir()
	setIsolatedHome(t, root)

	writeTestFile(t, root, ".claude/skills/e2e/SKILL.md", "# E2E Skill\nRun e2e tests.")
	writeTestFile(t, root, ".claude/skills/test-repo/SKILL.md", "# Test Repo\nManage test repos.")

	skills, err := skilldb.DiscoverSkills(root)
	require.NoError(t, err)
	require.Len(t, skills, 2)

	assert.Equal(t, "e2e", skills[0].Name)
	assert.Equal(t, "claude-code", skills[0].SourceAgent)
	assert.Equal(t, "skill", skills[0].Kind)
	assert.Equal(t, filepath.Join(".claude", "skills", "e2e", "SKILL.md"), skills[0].Path)

	assert.Equal(t, "test-repo", skills[1].Name)
	assert.Equal(t, "claude-code", skills[1].SourceAgent)
	assert.Equal(t, "skill", skills[1].Kind)
	assert.Equal(t, filepath.Join(".claude", "skills", "test-repo", "SKILL.md"), skills[1].Path)
}

func TestDiscoverSkills_ClaudeCommands(t *testing.T) {
	root := t.TempDir()
	setIsolatedHome(t, root)

	writeTestFile(t, root, ".claude/commands/dev.md", "Development command")
	writeTestFile(t, root, ".claude/commands/reviewer.md", "Review command")

	skills, err := skilldb.DiscoverSkills(root)
	require.NoError(t, err)
	require.Len(t, skills, 2)

	assert.Equal(t, "dev", skills[0].Name)
	assert.Equal(t, "claude-code", skills[0].SourceAgent)
	assert.Equal(t, "command", skills[0].Kind)
	assert.Equal(t, filepath.Join(".claude", "commands", "dev.md"), skills[0].Path)

	assert.Equal(t, "reviewer", skills[1].Name)
	assert.Equal(t, "claude-code", skills[1].SourceAgent)
	assert.Equal(t, "command", skills[1].Kind)
	assert.Equal(t, filepath.Join(".claude", "commands", "reviewer.md"), skills[1].Path)
}

func TestDiscoverSkills_GeminiAgents(t *testing.T) {
	root := t.TempDir()
	setIsolatedHome(t, root)

	writeTestFile(t, root, ".gemini/agents/dev.md", "---\nname: developer\n---\nA developer agent.")

	skills, err := skilldb.DiscoverSkills(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)

	assert.Equal(t, "developer", skills[0].Name)
	assert.Equal(t, "gemini-cli", skills[0].SourceAgent)
	assert.Equal(t, "agent-def", skills[0].Kind)
	assert.Equal(t, filepath.Join(".gemini", "agents", "dev.md"), skills[0].Path)
}

func TestDiscoverSkills_GeminiCommands(t *testing.T) {
	root := t.TempDir()
	setIsolatedHome(t, root)

	writeTestFile(t, root, ".gemini/commands/test.md", "A test command without frontmatter.")

	skills, err := skilldb.DiscoverSkills(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)

	assert.Equal(t, "test", skills[0].Name)
	assert.Equal(t, "gemini-cli", skills[0].SourceAgent)
	assert.Equal(t, "command", skills[0].Kind)
	assert.Equal(t, filepath.Join(".gemini", "commands", "test.md"), skills[0].Path)
}

func TestDiscoverSkills_CodexSkills(t *testing.T) {
	root := t.TempDir()
	setIsolatedHome(t, root)

	writeTestFile(t, root, ".codex/skills/review/SKILL.md", "---\nname: codex-review\n---\nReview carefully.")

	skills, err := skilldb.DiscoverSkills(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)

	assert.Equal(t, "review", skills[0].Name)
	assert.Equal(t, "codex", skills[0].SourceAgent)
	assert.Equal(t, "skill", skills[0].Kind)
	assert.Equal(t, filepath.Join(".codex", "skills", "review", "SKILL.md"), skills[0].Path)
}

func TestDiscoverSkills_AllPatterns(t *testing.T) {
	root := t.TempDir()
	setIsolatedHome(t, root)

	writeTestFile(t, root, ".claude/skills/e2e/SKILL.md", "# E2E")
	writeTestFile(t, root, ".claude/commands/dev.md", "Dev command")
	writeTestFile(t, root, ".codex/skills/review/SKILL.md", "# Review")
	writeTestFile(t, root, ".gemini/agents/coder.md", "---\nname: coder-agent\n---\nCodes things.")
	writeTestFile(t, root, ".gemini/commands/build.md", "Build command")

	skills, err := skilldb.DiscoverSkills(root)
	require.NoError(t, err)
	require.Len(t, skills, 5)

	// Sorted by source_agent then name: claude-code first, then codex, then gemini-cli
	assert.Equal(t, "claude-code", skills[0].SourceAgent)
	assert.Equal(t, "dev", skills[0].Name)
	assert.Equal(t, "command", skills[0].Kind)

	assert.Equal(t, "claude-code", skills[1].SourceAgent)
	assert.Equal(t, "e2e", skills[1].Name)
	assert.Equal(t, "skill", skills[1].Kind)

	assert.Equal(t, "codex", skills[2].SourceAgent)
	assert.Equal(t, "review", skills[2].Name)
	assert.Equal(t, "skill", skills[2].Kind)

	assert.Equal(t, "gemini-cli", skills[3].SourceAgent)
	assert.Equal(t, "build", skills[3].Name)
	assert.Equal(t, "command", skills[3].Kind)

	assert.Equal(t, "gemini-cli", skills[4].SourceAgent)
	assert.Equal(t, "coder-agent", skills[4].Name)
	assert.Equal(t, "agent-def", skills[4].Kind)
}

func TestDiscoverSkills_EmptyRepo(t *testing.T) {
	root := t.TempDir()
	setIsolatedHome(t, root)

	skills, err := skilldb.DiscoverSkills(root)
	require.NoError(t, err)
	assert.Empty(t, skills)
}

func TestDiscoverSkills_YAMLNameExtraction(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantName string
	}{
		{
			name:     "valid frontmatter with name",
			content:  "---\nname: my-skill\n---\nBody text.",
			wantName: "my-skill",
		},
		{
			name:     "frontmatter with extra fields",
			content:  "---\ndescription: some desc\nname: extracted\nversion: 1\n---\nBody.",
			wantName: "extracted",
		},
		{
			name:     "no frontmatter",
			content:  "Just plain markdown.",
			wantName: "fallback",
		},
		{
			name:     "empty frontmatter",
			content:  "---\n---\nBody.",
			wantName: "fallback",
		},
		{
			name:     "frontmatter without name field",
			content:  "---\ndescription: hello\n---\nBody.",
			wantName: "fallback",
		},
		{
			name:     "name with spaces",
			content:  "---\nname:   spaced name  \n---\n",
			wantName: "spaced name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			setIsolatedHome(t, root)

			writeTestFile(t, root, ".gemini/agents/fallback.md", tt.content)

			skills, err := skilldb.DiscoverSkills(root)
			require.NoError(t, err)
			require.Len(t, skills, 1)

			assert.Equal(t, tt.wantName, skills[0].Name)
		})
	}
}
