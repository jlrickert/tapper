package keg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	lockTokenFile    = "lock.json"
	KegCrossLockFile = ".keg-cross-lock"
)

func (f *FsRepo) crossLockPath(id NodeId) string {
	return filepath.Join(f.Root, id.Path(), KegCrossLockFile)
}

func (f *FsRepo) crossLockTokenPath(id NodeId) string {
	return filepath.Join(f.crossLockPath(id), lockTokenFile)
}

// AcquireLock implements RepositoryLock.
func (f *FsRepo) AcquireLock(ctx context.Context, id NodeId) (LockToken, error) {
	nodeDir := filepath.Join(f.Root, id.Path())
	if err := f.runtime.Mkdir(nodeDir, 0o755, true); err != nil {
		return "", errors.Join(ErrLock, NewBackendError(f.Name(), "AcquireLock", 0, err, false))
	}

	lockPath := f.crossLockPath(id)
	for {
		err := f.runtime.Mkdir(lockPath, 0o700, false)
		if err == nil {
			// Lock directory created — write token metadata.
			token := generateLockToken()
			info := LockInfo{
				Token:      token,
				AcquiredAt: f.runtime.Clock().Now(),
				TTLSeconds: int(DefaultLockTTL / time.Second),
				Holder:     f.lockHolder(),
			}
			if writeErr := f.writeCrossLockInfo(id, info); writeErr != nil {
				// Clean up the lock directory on write failure.
				_ = f.runtime.Remove(lockPath, true)
				return "", errors.Join(ErrLock, NewBackendError(f.Name(), "AcquireLock", 0, writeErr, false))
			}
			return token, nil
		}
		if os.IsExist(err) {
			// Lock directory exists — check if it's stale.
			info, readErr := f.readCrossLockInfo(id)
			if readErr != nil || info.IsStale(f.runtime.Clock().Now()) {
				// Stale or unreadable — remove and retry.
				_ = f.runtime.Remove(lockPath, true)
				continue
			}
			// Active lock held by someone else — wait or bail.
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		return "", errors.Join(ErrLock, NewBackendError(f.Name(), "AcquireLock", 0, err, false))
	}
}

// ReleaseLock implements RepositoryLock.
func (f *FsRepo) ReleaseLock(ctx context.Context, id NodeId, token LockToken) error {
	info, err := f.readCrossLockInfo(id)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotLocked
		}
		return errors.Join(ErrLock, NewBackendError(f.Name(), "ReleaseLock", 0, err, false))
	}
	if info.Token == "" {
		return ErrNotLocked
	}
	if info.Token != token {
		return fmt.Errorf("%w: lock held by %q", ErrLockTokenMismatch, info.Holder)
	}
	lockPath := f.crossLockPath(id)
	if rmErr := f.runtime.Remove(lockPath, true); rmErr != nil && !os.IsNotExist(rmErr) {
		return errors.Join(ErrLock, NewBackendError(f.Name(), "ReleaseLock", 0, rmErr, false))
	}
	return nil
}

// LockStatus implements RepositoryLock.
func (f *FsRepo) LockStatus(ctx context.Context, id NodeId) (LockInfo, error) {
	info, err := f.readCrossLockInfo(id)
	if err != nil {
		if os.IsNotExist(err) {
			return LockInfo{}, nil
		}
		return LockInfo{}, errors.Join(ErrLock, NewBackendError(f.Name(), "LockStatus", 0, err, false))
	}
	if info.IsStale(f.runtime.Clock().Now()) {
		return LockInfo{}, nil
	}
	return info, nil
}

// ForceReleaseLock implements RepositoryLock.
func (f *FsRepo) ForceReleaseLock(ctx context.Context, id NodeId) error {
	lockPath := f.crossLockPath(id)
	if err := f.runtime.Remove(lockPath, true); err != nil && !os.IsNotExist(err) {
		return errors.Join(ErrLock, NewBackendError(f.Name(), "ForceReleaseLock", 0, err, false))
	}
	return nil
}

func (f *FsRepo) readCrossLockInfo(id NodeId) (LockInfo, error) {
	tokenPath := f.crossLockTokenPath(id)
	data, err := f.runtime.ReadFile(tokenPath)
	if err != nil {
		return LockInfo{}, err
	}
	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return LockInfo{}, err
	}
	return info, nil
}

func (f *FsRepo) writeCrossLockInfo(id NodeId, info LockInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return f.runtime.WriteFile(f.crossLockTokenPath(id), data, 0o644)
}

func (f *FsRepo) lockHolder() string {
	if pi := f.runtime.Process(); pi != nil {
		return fmt.Sprintf("pid:%d@%s", pi.PID, pi.Hostname)
	}
	return "tap-cli"
}

var _ RepositoryLock = (*FsRepo)(nil)
