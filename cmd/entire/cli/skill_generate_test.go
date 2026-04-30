package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
)

func TestNewSkillGenerateCmd(t *testing.T) {
	t.Parallel()

	cmd := newSkillGenerateCmd()
	if cmd.Name() != "generate" {
		t.Fatalf("command name = %q, want generate", cmd.Name())
	}
	for _, flag := range []string{"session", "output", "force"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Fatalf("expected --%s flag to exist", flag)
		}
	}
}

func TestSkillGenerationProviderPromptCopy(t *testing.T) {
	t.Parallel()

	if skillGenerationProviderPrompt.Title != "Choose a skill generation provider" {
		t.Fatalf("skill provider prompt title = %q", skillGenerationProviderPrompt.Title)
	}
	if skillGenerationProviderPrompt.GenerationLabel != "skill" {
		t.Fatalf("skill provider generation label = %q", skillGenerationProviderPrompt.GenerationLabel)
	}
}

func TestRootCmd_HasSkillGroup(t *testing.T) {
	t.Parallel()

	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"skill", "generate"})
	if err != nil {
		t.Fatalf("finding skill generate command: %v", err)
	}
	if cmd == nil || cmd.Name() != "generate" {
		t.Fatalf("expected skill generate command, got %#v", cmd)
	}
}

func TestBuildSkillGenerationPrompt(t *testing.T) {
	t.Parallel()

	prompt := buildSkillGenerationPrompt(&skillSource{
		SessionID:    "session-123",
		Description:  "Create release workflow",
		AgentType:    types.AgentType("Codex"),
		Model:        "gpt-test",
		FilesTouched: []string{"cmd/release.go", "docs/release.md"},
	}, "[User] automate releases")

	for _, want := range []string{
		"Generate a Codex-compatible SKILL.md",
		"Session ID: session-123",
		"Description: Create release workflow",
		"Files touched: cmd/release.go, docs/release.md",
		"[User] automate releases",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestValidateGeneratedSkillMarkdown(t *testing.T) {
	t.Parallel()

	valid := "---\nname: release-workflow\ndescription: Use when preparing releases.\n---\n# Release Workflow\n"
	if err := validateGeneratedSkillMarkdown(valid); err != nil {
		t.Fatalf("valid skill markdown rejected: %v", err)
	}

	invalid := "# Missing Frontmatter\n"
	if err := validateGeneratedSkillMarkdown(invalid); err == nil {
		t.Fatal("expected invalid skill markdown to be rejected")
	}
}

func TestStripMarkdownFence(t *testing.T) {
	t.Parallel()

	got := stripMarkdownFence("```markdown\n---\nname: x\ndescription: y\n---\n# X\n```")
	if strings.HasPrefix(got, "```") || !strings.Contains(got, "name: x") {
		t.Fatalf("fence not stripped correctly: %q", got)
	}
}

func TestDefaultSkillOutputDir(t *testing.T) {
	t.Parallel()

	got := defaultSkillOutputDir(&skillSource{Description: "Fix Auth: Login Flow!"})
	if got != "fix-auth-login-flow-skill" {
		t.Fatalf("defaultSkillOutputDir = %q", got)
	}

	got = defaultSkillOutputDir(&skillSource{SessionID: "2026-session", Description: strategy.NoDescription})
	if got != "2026-session-skill" {
		t.Fatalf("fallback defaultSkillOutputDir = %q", got)
	}
}

func TestWriteGeneratedSkill(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "generated-skill")
	content := "---\nname: generated\ndescription: Generated skill.\n---\n# Generated\n"
	if err := writeGeneratedSkill(dir, content, false); err != nil {
		t.Fatalf("writeGeneratedSkill() error = %v", err)
	}

	written, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("reading SKILL.md: %v", err)
	}
	if string(written) != content {
		t.Fatalf("SKILL.md content mismatch:\n%s", written)
	}

	if err := writeGeneratedSkill(dir, content, false); err == nil {
		t.Fatal("expected second write without force to fail")
	}

	if err := writeGeneratedSkill(dir, content, true); err != nil {
		t.Fatalf("force writeGeneratedSkill() error = %v", err)
	}
}
