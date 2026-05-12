//go:build windows

package execx

import (
	"os/exec"
	"syscall"
	"time"
)

// KillOnCancel configures cmd so that when its context is cancelled the
// process is terminated and any inherited stdio pipes are forcibly closed
// after waitDelay. On Windows, CREATE_NEW_PROCESS_GROUP isolates the child
// from the parent's console signals; cmd.Process.Kill terminates the child,
// and WaitDelay covers stray descendants that may keep pipes open.
func KillOnCancel(cmd *exec.Cmd, waitDelay time.Duration) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = waitDelay
}
