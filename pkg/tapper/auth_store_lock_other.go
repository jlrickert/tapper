//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly || windows)

package tapper

// Platforms without a supported advisory lock serialize refreshes within the
// process only.
func tryLockFile(lockFile) (bool, error) { return true, nil }

func unlockFile(lockFile) error { return nil }
