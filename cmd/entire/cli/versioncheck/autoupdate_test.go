package versioncheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// autoUpdateFixture wires the test seams for MaybeAutoUpdate.
type autoUpdateFixture struct {
	installCalls int
	installErr   error
	lastCommand  string
	confirmValue bool
	confirmErr   error
}

func newAutoUpdateFixture(t *testing.T) *autoUpdateFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(envKillSwitch, "")
	// Force interactive mode on by default; individual tests can opt out.
	t.Setenv("ENTIRE_TEST_TTY", "1")

	f := &autoUpdateFixture{confirmValue: true}

	origRun := runInstaller
	runInstaller = func(_ context.Context, cmd string) error {
		f.installCalls++
		f.lastCommand = cmd
		return f.installErr
	}
	origConfirm := confirmUpdate
	confirmUpdate = func() (bool, error) { return f.confirmValue, f.confirmErr }
	origIsTerminalOut := isTerminalOut
	isTerminalOut = func(_ io.Writer) bool { return true }

	t.Cleanup(func() {
		runInstaller = origRun
		confirmUpdate = origConfirm
		isTerminalOut = origIsTerminalOut
	})
	return f
}

// useBrewExecutable points the install-manager detector at a brew cellar path.
func useBrewExecutable(t *testing.T) {
	t.Helper()
	orig := executablePath
	executablePath = func() (string, error) {
		return "/opt/homebrew/Cellar/entire/1.0.0/bin/entire", nil
	}
	t.Cleanup(func() { executablePath = orig })
}

// assertManualHint checks that the "To update entire run:\n  <cmd>" hint
// was printed when the prompt couldn't be shown.
func assertManualHint(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "To update, run:") {
		t.Errorf("missing manual-update hint: %q", out)
	}
	if !strings.Contains(out, "brew upgrade --cask entire") {
		t.Errorf("manual hint missing installer command: %q", out)
	}
}

func TestMaybeAutoUpdate_KillSwitch(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	t.Setenv(envKillSwitch, "1")

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer called with kill-switch set")
	}
	assertManualHint(t, buf.String())
}

func TestMaybeAutoUpdate_NoTTY(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	// No TTY → MaybeAutoUpdate must print the manual hint instead of prompting.
	t.Setenv("ENTIRE_TEST_TTY", "0")

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer called without TTY")
	}
	assertManualHint(t, buf.String())
}

func TestMaybeAutoUpdate_CIEnv(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	// Clear the test override so the real CanPromptInteractively path runs.
	t.Setenv("ENTIRE_TEST_TTY", "")
	t.Setenv("CI", "true")

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer called on CI (CI=true)")
	}
	assertManualHint(t, buf.String())
}

func TestMaybeAutoUpdate_NonTerminalWriter(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	isTerminalOut = func(_ io.Writer) bool { return false }

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer called with non-terminal output writer")
	}
	assertManualHint(t, buf.String())
}

// TestMaybeAutoUpdate_WindowsUnknownInstallerNoAutoRun verifies that on
// Windows without a detected install manager we never execute the POSIX
// curl-pipe-bash fallback (which would error from cmd.exe). Instead the
// user is pointed at the releases download page.
func TestMaybeAutoUpdate_WindowsUnknownInstallerNoAutoRun(t *testing.T) {
	f := newAutoUpdateFixture(t)
	// Force unknown install manager: point executablePath at a plain
	// Program Files path that matches none of the known prefixes.
	orig := executablePath
	executablePath = func() (string, error) {
		return `C:\Program Files\Entire\entire.exe`, nil
	}
	t.Cleanup(func() { executablePath = orig })

	origGOOS := goos
	goos = goosWindows
	t.Cleanup(func() { goos = origGOOS })

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer was auto-run on Windows + unknown install manager")
	}
	out := buf.String()
	if !strings.Contains(out, "download the latest release") ||
		!strings.Contains(out, "github.com/entireio/cli/releases") {
		t.Errorf("expected download-page hint, got: %q", out)
	}
	if strings.Contains(out, "curl -fsSL") {
		t.Errorf("Windows fallback must not show POSIX curl command: %q", out)
	}
}

// TestMaybeAutoUpdate_WindowsScoopStillAutoRuns verifies that a Windows
// scoop install still takes the interactive path — only unknown install
// managers are blocked on Windows.
func TestMaybeAutoUpdate_WindowsScoopStillAutoRuns(t *testing.T) {
	f := newAutoUpdateFixture(t)
	orig := executablePath
	executablePath = func() (string, error) {
		return `C:\Users\test\scoop\apps\cli\current\entire.exe`, nil
	}
	t.Cleanup(func() { executablePath = orig })

	origGOOS := goos
	goos = goosWindows
	t.Cleanup(func() { goos = origGOOS })

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 1 {
		t.Fatalf("scoop install should auto-run on Windows; calls=%d", f.installCalls)
	}
	if f.lastCommand != "scoop update entire/cli" {
		t.Errorf("got %q, want scoop update entire/cli", f.lastCommand)
	}
}

func TestMaybeAutoUpdate_UserDeclines(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	f.confirmValue = false

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer called after user declined")
	}
}

func TestMaybeAutoUpdate_HappyPath(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 1 {
		t.Fatalf("installer called %d times, want 1", f.installCalls)
	}
	if f.lastCommand != "brew upgrade --cask entire" {
		t.Errorf("installer got %q, want brew upgrade --cask entire", f.lastCommand)
	}
	if !strings.Contains(buf.String(), "Update complete") {
		t.Errorf("missing success message: %q", buf.String())
	}
}

func TestMaybeAutoUpdate_InstallerFailurePrintedToUser(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	f.installErr = errors.New("boom")

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 1 {
		t.Fatalf("installer called %d times, want 1", f.installCalls)
	}
	out := buf.String()
	if !strings.Contains(out, "Update failed") {
		t.Errorf("missing failure message: %q", out)
	}
	// Failure message should include a manual-retry hint with the exact command.
	if !strings.Contains(out, "Try again later running:") {
		t.Errorf("missing retry hint: %q", out)
	}
	if !strings.Contains(out, "brew upgrade --cask entire") {
		t.Errorf("retry hint missing installer command: %q", out)
	}
}
