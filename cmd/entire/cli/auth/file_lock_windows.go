//go:build windows

package auth

import "os"

// acquireExclusiveLock is a no-op on Windows. Concurrent CLI invocations
// against the same ENTIRE_SECRETS_PATH file are uncommon on Windows (users
// who need persistent CLI auth typically use Credential Manager via the
// keyring backend). If concurrent file access becomes a problem on Windows,
// switch this to LockFileEx via golang.org/x/sys/windows.
func acquireExclusiveLock(_ *os.File) (func(), error) {
	return func() {}, nil
}
