package geminicli_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/geminicli"
)

var _ agent.HeadlessLauncher = (*geminicli.GeminiCLIAgent)(nil)

// LaunchHeadlessCmd must be infallible at construction time even when
// the gemini binary isn't on PATH — missing-binary errors surface at
// cmd.Run() instead, so unit tests and CI runners without gemini
// installed still verify the argv contract. Regression: an earlier
// implementation called exec.LookPath up front, breaking CI.
func TestGeminiCLIAgent_LaunchHeadlessCmd_NoBinaryOnPath(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	if _, err := os.Stat("/nonexistent/gemini"); err == nil {
		t.Skip("PATH-scrub didn't take effect on this runner")
	}

	a := &geminicli.GeminiCLIAgent{}
	cmd, err := a.LaunchHeadlessCmd(context.Background(), "x")
	if err != nil {
		t.Fatalf("LaunchHeadlessCmd must not return an error when binary is missing; got %v", err)
	}
	if cmd == nil {
		t.Fatal("returned nil cmd")
	}
}

func TestGeminiCLIAgent_LaunchHeadlessCmd(t *testing.T) {
	t.Parallel()
	a := &geminicli.GeminiCLIAgent{}
	cmd, err := a.LaunchHeadlessCmd(context.Background(), "review please")
	if err != nil {
		t.Fatalf("LaunchHeadlessCmd: %v", err)
	}
	if cmd == nil {
		t.Fatal("returned nil cmd")
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "-p") {
		t.Errorf("expected '-p' flag in args; got %q", joined)
	}
	// The -p flag carries a single-space placeholder; the real prompt is
	// piped via stdin. Verify the placeholder is present as its own argv
	// entry so the flag parses correctly in headless mode.
	hasSpacePlaceholder := false
	for i, a := range cmd.Args {
		if a == "-p" && i+1 < len(cmd.Args) && cmd.Args[i+1] == " " {
			hasSpacePlaceholder = true
			break
		}
	}
	if !hasSpacePlaceholder {
		t.Errorf("expected '-p' followed by single-space placeholder; got %q", joined)
	}
	// Prompt must NOT appear in argv — it travels via stdin.
	if strings.Contains(joined, "review please") {
		t.Errorf("prompt should not appear in argv (piped via stdin); got %q", joined)
	}
	// Stdin MUST be wired (the prompt pipe); Stdout and Stderr stay nil
	// for the caller to assign.
	if cmd.Stdin == nil {
		t.Error("expected Stdin to be wired with the prompt reader")
	}
	if cmd.Stdout != nil || cmd.Stderr != nil {
		t.Errorf("Stdout/Stderr should be left nil for caller; got stdout=%v stderr=%v",
			cmd.Stdout, cmd.Stderr)
	}
}
