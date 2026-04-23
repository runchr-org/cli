package claudecode_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/claudecode"
)

// Compile-time pin: ClaudeCodeAgent must satisfy SkillDiscoverer.
var _ agent.SkillDiscoverer = (*claudecode.ClaudeCodeAgent)(nil)

func withFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestDiscoverReviewSkills_NoPluginsDirReturnsNilNil(t *testing.T) {
	// Cannot t.Parallel — uses t.Setenv.
	withFakeHome(t)

	a := &claudecode.ClaudeCodeAgent{}
	skills, err := a.DiscoverReviewSkills(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skills != nil {
		t.Errorf("skills = %v, want nil", skills)
	}
}

func TestDiscoverReviewSkills_FindsPluginReviewSkill(t *testing.T) {
	home := withFakeHome(t)
	skillDir := filepath.Join(home, ".claude", "plugins", "cache",
		"fake-market", "pr-review-toolkit", "0.1.0", "skills", "review-pr")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: review-pr
description: Full PR review
---

Review the PR.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &claudecode.ClaudeCodeAgent{}
	skills, err := a.DiscoverReviewSkills(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills count = %d, want 1", len(skills))
	}
	if skills[0].Name != "/pr-review-toolkit:review-pr" {
		t.Errorf("skills[0].Name = %q, want /pr-review-toolkit:review-pr", skills[0].Name)
	}
	if skills[0].Description != "Full PR review" {
		t.Errorf("skills[0].Description = %q", skills[0].Description)
	}
}

func TestDiscoverReviewSkills_SkipsNonReviewSkill(t *testing.T) {
	home := withFakeHome(t)
	skillDir := filepath.Join(home, ".claude", "plugins", "cache",
		"fake-market", "unrelated-plugin", "1.0.0", "skills", "format-code")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: format-code
description: Reformat code according to project style
---
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &claudecode.ClaudeCodeAgent{}
	skills, err := a.DiscoverReviewSkills(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("non-review skill should be skipped, got %+v", skills)
	}
}

func TestDiscoverReviewSkills_MalformedSkillSkipped(t *testing.T) {
	home := withFakeHome(t)
	goodDir := filepath.Join(home, ".claude", "plugins", "cache",
		"fake-market", "good-plugin", "1.0.0", "skills", "review-pr")
	badDir := filepath.Join(home, ".claude", "plugins", "cache",
		"fake-market", "bad-plugin", "1.0.0", "skills", "audit")
	if err := os.MkdirAll(goodDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "SKILL.md"),
		[]byte("---\nname: review-pr\ndescription: PR review\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Malformed: no closing frontmatter delimiter.
	if err := os.WriteFile(filepath.Join(badDir, "SKILL.md"),
		[]byte("---\nname: audit\ndescription: uh oh"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &claudecode.ClaudeCodeAgent{}
	skills, err := a.DiscoverReviewSkills(context.Background())
	if err != nil {
		t.Fatalf("malformed SKILL.md should not abort discovery; got err=%v", err)
	}
	if len(skills) != 1 {
		t.Errorf("good skill should still appear, got %+v", skills)
	}
}

func TestDiscoverReviewSkills_UserSkillsDir(t *testing.T) {
	home := withFakeHome(t)
	userSkillDir := filepath.Join(home, ".claude", "skills", "my-review")
	if err := os.MkdirAll(userSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userSkillDir, "SKILL.md"),
		[]byte("---\nname: my-review\ndescription: personal review skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &claudecode.ClaudeCodeAgent{}
	skills, err := a.DiscoverReviewSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 user skill, got %d", len(skills))
	}
	if skills[0].Name != "/my-review" {
		t.Errorf("user skill name = %q, want /my-review", skills[0].Name)
	}
}
