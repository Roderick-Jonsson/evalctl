//go:build !windows

package cli

// One lock, held for the length of a run. Its key is the profile the run measures, not the tree:
// two checkouts measuring the same profile share the same four sandboxes, so they collide even when
// their trees differ.

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive, non-blocking lock on f.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFile releases it.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
