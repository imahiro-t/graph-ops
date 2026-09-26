//go:build unix

package terminal

import (
	"errors"
	"os"
	"syscall"
)

// tryFlock takes an exclusive flock on f without blocking: false (and no
// error) when another open file description holds it. The kernel drops the
// lock when its holder closes the file or exits, so a crashed holder never
// leaves a stale lock behind.
func tryFlock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}
