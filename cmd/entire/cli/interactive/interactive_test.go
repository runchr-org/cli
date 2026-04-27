package interactive

import (
	"bytes"
	"os"
	"testing"
)

func TestCanPromptInteractively_ForcedOn(t *testing.T) {
	t.Setenv("ENTIRE_TEST_TTY", "1")
	if !CanPromptInteractively() {
		t.Error("CanPromptInteractively() = false; want true when ENTIRE_TEST_TTY=1")
	}
}

func TestCanPromptInteractively_ForcedOff(t *testing.T) {
	t.Setenv("ENTIRE_TEST_TTY", "0")
	if CanPromptInteractively() {
		t.Error("CanPromptInteractively() = true; want false when ENTIRE_TEST_TTY=0")
	}
}

func TestCanPromptInteractively_AgentEnvGuards(t *testing.T) {
	// Unset ENTIRE_TEST_TTY so agent-env guards run. Force an explicit unset
	// since the top-level check short-circuits on presence, not value.
	t.Setenv("ENTIRE_TEST_TTY", "")
	_ = os.Unsetenv("ENTIRE_TEST_TTY")

	cases := []struct {
		name, key, val string
	}{
		{"gemini", "GEMINI_CLI", "1"},
		{"copilot", "COPILOT_CLI", "1"},
		{"pi", "PI_CODING_AGENT", "true"},
		{"git-terminal-prompt-off", "GIT_TERMINAL_PROMPT", "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(c.key, c.val)
			if CanPromptInteractively() {
				t.Errorf("CanPromptInteractively() = true; want false when %s=%s", c.key, c.val)
			}
		})
	}
}

func TestCanPromptInteractively_CIEnv(t *testing.T) {
	t.Setenv("ENTIRE_TEST_TTY", "")
	_ = os.Unsetenv("ENTIRE_TEST_TTY")
	t.Setenv("CI", "true")
	if CanPromptInteractively() {
		t.Error("CanPromptInteractively() = true; want false when CI=true")
	}
}

// CI=false is the `is-ci` escape hatch: a dev may set it to override an
// inherited CI=true. Verify the CI branch doesn't short-circuit to false,
// using ENTIRE_TEST_TTY=1 to stand in for a real TTY in the test host.
func TestCanPromptInteractively_CIFalseOverride(t *testing.T) {
	t.Setenv("CI", "false")
	t.Setenv("ENTIRE_TEST_TTY", "1")
	if !CanPromptInteractively() {
		t.Error("CanPromptInteractively() = false; want true when CI=false")
	}
}

func TestIsTerminalWriter_NonFile(t *testing.T) {
	t.Parallel()
	if IsTerminalWriter(&bytes.Buffer{}) {
		t.Error("IsTerminalWriter(*bytes.Buffer) = true; want false")
	}
}

func TestIsTerminalWriter_Pipe(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if IsTerminalWriter(w) {
		t.Error("IsTerminalWriter(pipe) = true; want false")
	}
}
