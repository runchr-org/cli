//go:build !windows

package auth

import (
	"fmt"
	"os"
	"syscall"
)

// acquireExclusiveLock takes a blocking flock(2) exclusive lock on f. The
// returned release function unlocks the file; the kernel also releases the
// lock when the file is closed or the process exits.
func acquireExclusiveLock(f *os.File) (func(), error) {
	fd := int(f.Fd()) //nolint:gosec // file descriptors fit in int on every supported platform.
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("flock token file: %w", err)
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN) //nolint:errcheck // best-effort unlock; kernel releases on close/exit.
	}, nil
}
