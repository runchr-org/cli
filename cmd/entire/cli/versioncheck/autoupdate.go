package versioncheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/charmbracelet/huh"

	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/logging"
)

// envKillSwitch disables the interactive update prompt regardless of TTY.
const envKillSwitch = "ENTIRE_NO_AUTO_UPDATE"

// Test seams.
var (
	runInstaller  = realRunInstaller
	confirmUpdate = realConfirmUpdate
	isTerminalOut = interactive.IsTerminalWriter
)

// MaybeAutoUpdate offers an interactive upgrade after the standard
// "version available" notification has been printed. Silent on every
// failure path — it must never interrupt the CLI.
//
// If the installer command fails, a hint with the exact command is
// printed so the user can retry manually. The 24h version-check cache
// is not invalidated on failure: we don't want to re-prompt on every
// invocation while an upstream issue (network, auth, repo outage) is
// still in place.
//
// When the prompt cannot be shown (kill switch set, or non-interactive
// environment like CI / agent subprocess / no TTY) the installer
// command is printed so the user still learns what to run manually.
func MaybeAutoUpdate(ctx context.Context, w io.Writer, currentVersion string) {
	// Windows + unknown install manager: the POSIX curl-pipe-bash fallback
	// would error if auto-run, and there's no safe native equivalent. Point
	// the user at the releases page so they can download manually.
	if !canAutoInstall() {
		fmt.Fprintf(w, "To update, download the latest release from:\n  %s\n", downloadsURL)
		return
	}
	if os.Getenv(envKillSwitch) != "" || !interactive.CanPromptInteractively() || !isTerminalOut(w) {
		fmt.Fprintf(w, "To update, run:\n  %s\n", updateCommand(currentVersion))
		return
	}

	confirmed, err := confirmUpdate()
	if err != nil {
		logging.Debug(ctx, "auto-update: prompt failed", "error", err.Error())
		return
	}
	if !confirmed {
		return
	}

	cmdStr := updateCommand(currentVersion)
	fmt.Fprintf(w, "\nUpdating Entire CLI: %s\n", cmdStr)
	if err := runInstaller(ctx, cmdStr); err != nil {
		fmt.Fprintf(w, "Update failed: %v\nTry again later running:\n  %s\n", err, cmdStr)
		return
	}
	fmt.Fprintln(w, "Update complete. Re-run entire to use the new version.")
}

func realConfirmUpdate() (bool, error) {
	// Pre-select "Yes" so pressing Enter accepts — matches the (Y/n) UX.
	confirmed := true
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Install the new version now?").
				Affirmative("Yes").
				Negative("No").
				Value(&confirmed),
		),
	).WithTheme(huh.ThemeDracula())
	if os.Getenv("ACCESSIBLE") != "" {
		form = form.WithAccessible(true)
	}
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, huh.ErrTimeout) {
			return false, nil
		}
		return false, fmt.Errorf("confirm form: %w", err)
	}
	return confirmed, nil
}

// realRunInstaller shells out to the installer command, streaming stdin/stdout/stderr
// so password prompts and progress output reach the user.
func realRunInstaller(ctx context.Context, cmdStr string) error {
	var c *exec.Cmd
	if runtime.GOOS == goosWindows {
		c = exec.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("installer exited: %w", err)
	}
	return nil
}
