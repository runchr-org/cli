//go:build unix

package remote

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Not parallel: uses t.Setenv. Clearing ENTIRE_CHECKPOINT_TOKEN keeps the test
// hermetic — with a token set, newCommand would resolve "origin" via GetRemoteURL
// and spawn a git subprocess against the ambient repo.
func TestKillProcessGroupOnCancel_SetsSetpgidAndCancel(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "")

	cmd := newCommand(context.Background(), "push", "origin", "main")

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Error("Setpgid = false; want true so the whole process group can be killed")
	}
	if cmd.Cancel == nil {
		t.Fatal("Cancel = nil; want a group-kill handler")
	}
}

// TestTerminateOnCancel_KillsProcessGroup proves the fix end-to-end: a child that
// backgrounds a grandchild which inherits (and holds open) the output pipe must
// still be terminated when the context is cancelled. Without group-kill, the
// backgrounded `sleep` keeps the pipe open and the read blocks for the full 60s;
// with it, the whole group dies and the read returns promptly.
func TestTerminateOnCancel_KillsProcessGroup(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// `sleep 60 &` backgrounds a grandchild that inherits and holds stdout; the
	// "ready" marker is printed only after it is backgrounded, so we can cancel
	// strictly after the pipe-holding grandchild exists. A fixed timeout deadline
	// could instead fire before the shell even spawns the grandchild (e.g. slow
	// process start under load), silently turning this into a no-op pass. `wait`
	// keeps the shell (the group leader) alive so only a group-wide kill ends both.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 60 & echo ready; wait")
	terminateOnCancel(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Block until the grandchild is alive and holding the pipe open.
	br := bufio.NewReader(stdout)
	if line, err := br.ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("did not observe ready marker: line=%q err=%v", line, err)
	}

	done := make(chan error, 1)
	go func() {
		_, _ = io.Copy(io.Discard, br) //nolint:errcheck // draining to block until the pipe closes; copy errors are irrelevant
		done <- cmd.Wait()
	}()

	cancel()

	// Group-kill closes the inherited pipe on cancellation, so the read returns well
	// under the 60s sleep. Without it, the backgrounded grandchild keeps the pipe
	// open and this would block for the full minute.
	//
	// The deadline must stay strictly below killWaitDelay: that WaitDelay backstop
	// would itself force the pipe closed once it elapses, so a deadline >=
	// killWaitDelay would let this test pass even with group-kill removed, silently
	// turning it into a no-op. Halving keeps it comfortably inside that window.
	select {
	case <-done:
	case <-time.After(killWaitDelay / 2):
		t.Fatal("read did not return after cancellation; the pipe-holding grandchild outlived the group-kill")
	}
}

// The Cancel handler must return cleanly and actually terminate the process
// group leader (the running shell), not just succeed silently.
func TestKillProcessGroupOnCancel_TerminatesLeader(t *testing.T) {
	t.Parallel()

	// exec requires CommandContext when Cancel is non-nil; the context stays
	// open and we invoke Cancel directly to exercise the group-kill handler.
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "sleep 60 & wait")
	killProcessGroupOnCancel(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := cmd.Cancel(); err != nil {
		t.Fatalf("Cancel returned %v; want nil", err)
	}

	// The leader was SIGKILLed, so Wait reports a non-nil (signal: killed) error
	// rather than blocking on the 60s sleep. killProcessGroupOnCancel sets no
	// WaitDelay backstop, so bound the wait ourselves: if the group-kill path ever
	// regresses to a no-op, the shell would block on `sleep 60` and hang the suite
	// for a full minute instead of failing fast.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Wait returned nil; the killed shell should report a termination error")
		}
	case <-time.After(killWaitDelay):
		t.Fatal("Wait did not return after Cancel; the group-kill handler may have regressed")
	}
}
