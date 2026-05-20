// Package testutil holds shared test helpers for the agent package and
// its sub-packages. Not for production use.
package testutil

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// FakeStreamCmd returns a CommandRunner factory whose *exec.Cmd, when
// Start()'d and Wait()'d, produces stdout/stderr/exit-code as configured.
// Implementation assumes a POSIX shell (`sh -c`) to write fixture data;
// it is not usable from a Windows runner. The streaming agents these
// helpers test are not currently exercised by the Windows E2E workflow
// (e2e-windows.yml), so a POSIX-only fake is sufficient.
func FakeStreamCmd(stdout, stderr string, exitCode int) func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		script := BuildFakeShellScript(stdout, stderr, exitCode)
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
}

// BuildFakeShellScript renders a shell snippet that emits the given stdout,
// stderr, and exit code. Exported so callers that need a customized fake
// (e.g. multi-stage stdout) can compose against it.
func BuildFakeShellScript(stdout, stderr string, exitCode int) string {
	var sb strings.Builder
	if stdout != "" {
		sb.WriteString("cat <<'__EOF__'\n")
		sb.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("__EOF__\n")
	}
	if stderr != "" {
		sb.WriteString("cat <<'__EOF__' 1>&2\n")
		sb.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("__EOF__\n")
	}
	if exitCode != 0 {
		sb.WriteString("exit ")
		sb.WriteString(strconv.Itoa(exitCode))
		sb.WriteString("\n")
	}
	return sb.String()
}
