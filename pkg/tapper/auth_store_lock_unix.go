//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package tapper

import (
	"errors"
	"syscall"
)

// tryLockFile takes an exclusive flock on f without blocking, reporting false
// when another open file holds it.
func tryLockFile(f lockFile) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f lockFile) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
