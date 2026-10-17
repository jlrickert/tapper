package tapper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// authStoreLockPoll is how often a blocked refresher retries the store lock.
const authStoreLockPoll = 20 * time.Millisecond

// authStoreLockWait bounds how long a refresher waits for another process's
// refresh of the same store: that refresh's own timeout plus room to persist.
const authStoreLockWait = authRefreshTimeout + 5*time.Second

// errAuthStoreLockTimeout reports that another process held the store lock for
// longer than the wait allowed.
var errAuthStoreLockTimeout = errors.New("auth store: timed out waiting for another process's refresh")

// lockFile is the part of an opened lock file the platform lock needs.
type lockFile interface {
	io.Closer
	Fd() uintptr
}

// lockAuthStore takes the cross-process lock that serializes refreshes of the
// store at storePath, waiting up to wait. The lock is an advisory OS lock on
// "<storePath>.lock", opened through the runtime so the jail applies; the OS
// drops it if the process dies. When the runtime's filesystem cannot provide
// a lockable file, the returned unlock is a no-op and refreshes are serialized
// within the process only.
func lockAuthStore(ctx context.Context, rt *toolkit.Runtime, storePath string, wait time.Duration) (func(), error) {
	w, err := rt.OpenFile(storePath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("auth store: open lock: %w", err)
	}
	f, ok := w.(lockFile)
	if !ok {
		_ = w.Close()
		return func() {}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		locked, err := tryLockFile(f)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("auth store: lock: %w", err)
		}
		if locked {
			return func() {
				_ = unlockFile(f)
				_ = f.Close()
			}, nil
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, errAuthStoreLockTimeout
			}
			return nil, ctx.Err()
		case <-time.After(authStoreLockPoll):
		}
	}
}
