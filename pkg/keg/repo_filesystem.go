package keg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	MarkdownContentFilename = "README.md"
	YAMLMetaFilename        = "meta.yaml"
	KegCurrentEnvKey        = "KEG_CURRENT"
	KegLockFile             = ".keg-lock"
)

// FsRepo implements [KegRepository] using the local filesystem as storage. It
// manages KEG nodes as directories under [Root], with each node containing
// content files, metadata, and optional attachments. Thread-safe operations
// are coordinated through the embedded mutex.
type FsRepo struct {
	// Root is the base directory path containing all KEG node directories
	Root string
	// ContentFileName specifies the filename for node content (typically README.md)
	ContentFileName string
	// MetaFilename specifies the filename for node metadata (typically meta.yaml)
	MetaFilename string
	// lock coordinates concurrent access to repository operations
	lock *sync.Mutex

	// optional defaults for callers who don't set their own
	LockTimeout  time.Duration
	LockInterval time.Duration
}

// NewFsRepoFromEnvOrSearch tries to locate a keg file using the order:
// 1) KEG_CURRENT env var (file or directory)
// 2) current working directory
// 3) if inside a git project, search the project tree for a keg file
// 4) recursive search from current working directory
// 5) fallback to default config location (~/.config/keg or XDG equivalent)
//
// Returns a pointer to an initialized FsRepo and the path of the discovered keg file (or "" if using fallback path).
func NewFsRepoFromEnvOrSearch(ctx context.Context) (*FsRepo, string, error) {
	// candidate names we consider a keg file
	candidates := []string{"keg", "keg.yaml", "keg.yml"}

	// 1) KEG_CURRENT
	if v := os.Getenv(KegCurrentEnvKey); v != "" {
		if p, err := resolveKegFromEnv(v, candidates); err == nil {
			f := &FsRepo{
				Root:            p.rootDir,
				ContentFileName: MarkdownContentFilename,
				MetaFilename:    YAMLMetaFilename,
				lock:            &sync.Mutex{},
			}
			return f, p.kegPath, nil
		}
		// if env set but didn't resolve, continue searching (do not fail)
	}

	// 2) current directory
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", NewBackendError("fs", "NewFsRepoFromEnvOrSearch", 0, err, false)
	}
	if kp := findKegInDir(cwd, candidates); kp != "" {
		f := &FsRepo{
			Root:            cwd,
			ContentFileName: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
			lock:            &sync.Mutex{},
		}
		return f, kp, nil
	}

	// 3) if in a git project, find git root and search the project tree
	if gitRoot := findGitRoot(cwd); gitRoot != "" {
		if kp := findKegRecursive(gitRoot, candidates); kp != "" {
			f := &FsRepo{
				Root:            filepath.Dir(kp), // directory containing the keg file
				ContentFileName: MarkdownContentFilename,
				MetaFilename:    YAMLMetaFilename,
				lock:            &sync.Mutex{},
			}
			return f, kp, nil
		}
	}

	// 4) traverse current directory recursively (in case the keg is somewhere under cwd)
	if kp := findKegRecursive(cwd, candidates); kp != "" {
		f := &FsRepo{
			Root:            filepath.Dir(kp),
			ContentFileName: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
			lock:            &sync.Mutex{},
		}
		return f, kp, nil
	}

	// 5) fallback default: use XDG config dir or $HOME/.config/keg
	if cfgDir, err := os.UserConfigDir(); err == nil {
		defDir := filepath.Join(cfgDir, "keg")
		// create directory if missing? only choose as root, don't create file.
		f := &FsRepo{
			Root:            defDir,
			ContentFileName: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
			lock:            &sync.Mutex{},
		}
		return f, "", nil
	}

	// as last last resort fall back to $HOME/.keg
	home, hErr := os.UserHomeDir()
	if hErr == nil {
		defDir := filepath.Join(home, ".keg")
		f := &FsRepo{
			Root:            defDir,
			ContentFileName: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
			lock:            &sync.Mutex{},
		}
		return f, "", nil
	}

	return nil, "", NewBackendError("fs", "NewFsRepoFromEnvOrSearch", 0, errors.New("could not determine fallback config dir"), false)
}

// helper types for env resolution
type envResolveResult struct {
	rootDir string // directory for the repo root that will contain keg file
	kegPath string // full path to the keg file (may be empty if not present)
}

// resolveKegFromEnv accepts KEG_CURRENT value which can be:
// - absolute path to a file (keg file) -> use its directory as root
// - directory path -> check for a keg file inside that directory -> if found use that
// if nothing matches, returns error.
func resolveKegFromEnv(v string, candidates []string) (envResolveResult, error) {
	// expand ~ if present (simple)
	if after, ok := strings.CutPrefix(v, "~"); ok {
		if hd, err := os.UserHomeDir(); err == nil {
			v = filepath.Join(hd, after)
		}
	}
	info, err := os.Stat(v)
	if err == nil && info.Mode().IsRegular() {
		// env pointed to a file; verify its name is a candidate
		base := filepath.Base(v)
		if slices.Contains(candidates, base) {
			return envResolveResult{rootDir: filepath.Dir(v), kegPath: v}, nil
		}
		return envResolveResult{}, NewBackendError("fs", "resolveKegFromEnv", 0, errors.New("KEG_CURRENT pointed to a file that is not a known keg filename"), false)
	}
	if err == nil && info.IsDir() {
		// env pointed to a directory: check for candidate file inside
		for _, c := range candidates {
			p := filepath.Join(v, c)
			if fi, statErr := os.Stat(p); statErr == nil && fi.Mode().IsRegular() {
				return envResolveResult{rootDir: v, kegPath: p}, nil
			}
		}
		// directory but no keg file found — treat as valid root only if caller expects that.
		// For our purposes require the keg file to exist; return error to let caller continue search.
		return envResolveResult{}, NewBackendError("fs", "resolveKegFromEnv", 0, errors.New("KEG_CURRENT directory does not contain a keg file"), false)
	}
	// path doesn't exist — treat as error
	return envResolveResult{}, NewBackendError("fs", "resolveKegFromEnv", 0, err, false)
}

// findKegInDir checks if any candidate keg filename exists directly in dir.
// returns full path or "".
func findKegInDir(dir string, candidates []string) string {
	for _, c := range candidates {
		p := filepath.Join(dir, c)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// findKegRecursive walks root and returns the first matched keg file path, or "" if none.
func findKegRecursive(root string, candidates []string) string {
	// use WalkDir for efficiency; stop early on first found.
	var found string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			// skip on error or already found
			return nil
		}
		if d.Type().IsRegular() {
			base := filepath.Base(path)
			for _, c := range candidates {
				if base == c {
					found = path
					return nil
				}
			}
		}
		return nil
	})
	return found
}

// findGitRoot attempts to use the git CLI to determine the repository top-level
// directory starting from 'start'. If that fails (git not available, not a git
// worktree, or command error), it falls back to the original upward filesystem
// search for a .git entry.
//
// Note: this implementation uses os/exec and strings; ensure those packages are
// imported in the file: "os/exec" and "strings".
func findGitRoot(start string) string {
	// Normalize start to a directory (in case a file path was passed).
	if fi, err := os.Stat(start); err == nil && !fi.IsDir() {
		start = filepath.Dir(start)
	}

	// First, try using git itself to find the top-level directory. Using `-C`
	// makes git operate relative to the provided path.
	if out, err := exec.Command("git", "-C", start, "rev-parse", "--show-toplevel").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return p
		}
	}

	// Fallback: walk upwards looking for a .git entry (dir or file).
	p := start
	for {
		gitPath := filepath.Join(p, ".git")
		if fi, err := os.Stat(gitPath); err == nil {
			// .git can be a dir (normal repo) or a file (worktree / submodule).
			if fi.IsDir() || fi.Mode().IsRegular() {
				return p
			}
		}
		parent := filepath.Dir(p)
		if parent == p {
			// reached filesystem root
			break
		}
		p = parent
	}
	return ""
}

// lockParams returns configured timeouts, falling back to safe defaults.
func (f *FsRepo) lockParams() (timeout time.Duration, retryInterval time.Duration) {
	timeout = f.LockTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	retryInterval = f.LockInterval
	if retryInterval == 0 {
		retryInterval = 100 * time.Millisecond
	}
	return
}

// withLockContext derives a context with the repo's configured lock timeout.
// If parent is nil, context.Background() is used. It returns ctx, cancel, retryInterval.
func (f *FsRepo) withLockContext(parent context.Context) (context.Context, context.CancelFunc, time.Duration) {
	if parent == nil {
		parent = context.Background()
	}
	timeout, retry := f.lockParams()
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, cancel, retry
}

// AcquireLock tries to acquire a simple file-based lock at the repository root
// and returns an unlock function that releases the lock. The unlock function
// returns an error if the release failed.
//
// It attempts to create the lock file atomically using O_EXCL. On success it
// writes pid/timestamp for diagnostics and returns the unlock closure that
// removes the lock file (best-effort).
//
// The operation respects ctx for cancellation/deadline and retries at the
// provided retryInterval while waiting to acquire the lock. Filesystem errors
// are wrapped with NewBackendError("fs", "AcquireLock", ...).
func (f *FsRepo) acquireLock(ctx context.Context, retryInterval time.Duration) (func() error, error) {
	lockPath := filepath.Join(f.Root, KegLockFile)

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// best-effort diagnostics
			_, _ = fmt.Fprintf(lf, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			_ = lf.Close()

			unlock := func() error {
				if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
					return NewBackendError("fs", "AcquireLock:unlock", 0, err, false)
				}
				return nil
			}
			return unlock, nil
		}

		// Unexpected error (not "file exists")
		if !os.IsExist(err) {
			return nil, NewBackendError("fs", "AcquireLock", 0, err, false)
		}

		// Lock file exists; wait or bail if context is done
		select {
		case <-ctx.Done():
			// Use sentinel for callers that want to check timeout/cancel specifically
			return nil, NewBackendError("fs", "AcquireLock", 0, fmt.Errorf("%w: %s", ErrLockTimeout, ctx.Err()), false)
		case <-ticker.C:
			// retry
		}
	}
}

// AcquireNodeLock attempts to acquire a per-node lock placed inside the node's
// directory (root/<node-id>/.keg-lock). It behaves like AcquireLock but scoped
// to a specific node. If the node directory does not exist, it returns a
// NewNodeNotFoundError.
//
// The function respects ctx for cancellation/deadline and will retry every
// retryInterval until the lock can be created or ctx is done. Filesystem errors
// are wrapped with NewBackendError("fs", "AcquireNodeLock", ...).
func (f *FsRepo) acquireNodeLock(ctx context.Context, id NodeID, retryInterval time.Duration) (func() error, error) {
	nodeDir := filepath.Join(f.Root, (&id).Path())

	// Ensure the node directory exists before attempting to lock.
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, NewNodeNotFoundError(id)
		}
		return nil, NewBackendError("fs", "AcquireNodeLock", 0, statErr, false)
	}

	lockPath := filepath.Join(nodeDir, KegLockFile)

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// best-effort diagnostics: pid, node id, timestamp
			_, _ = fmt.Fprintf(lf, "%d %s %s\n", os.Getpid(), (&id).Path(), time.Now().UTC().Format(time.RFC3339))
			_ = lf.Close()

			unlock := func() error {
				if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
					return NewBackendError("fs", "AcquireNodeLock:unlock", 0, err, false)
				}
				return nil
			}
			return unlock, nil
		}

		// Unexpected error (not "file exists")
		if !os.IsExist(err) {
			return nil, NewBackendError("fs", "AcquireNodeLock", 0, err, false)
		}

		// Lock file exists; wait or bail if context is done
		select {
		case <-ctx.Done():
			return nil, NewBackendError("fs", "AcquireNodeLock", 0, fmt.Errorf("%w: %s", ErrLockTimeout, ctx.Err()), false)
		case <-ticker.C:
			// retry
		}
	}
}

// ClearLocks removes orphaned repository and per-node lock files (.keg-lock).
// It attempts to remove the root-level lock and any per-node lock files found
// in immediate subdirectories. Non-existence is ignored. If filesystem errors
// occur they are wrapped in a BackendError; multiple errors are combined into
// a single BackendError with an aggregated message.
func (f *FsRepo) ClearLocks() error {
	// Remove repo-level lock first.
	var errs []error

	rootLock := filepath.Join(f.Root, KegLockFile)
	if err := os.Remove(rootLock); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("remove root lock: %w", err))
	}

	// List immediate entries in repo root and attempt to remove per-node locks.
	entries, err := os.ReadDir(f.Root)
	if err != nil {
		return NewBackendError("fs", "ClearLocks", 0, err, false)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Only consider direct child directories as node dirs. We don't enforce
		// numeric names here; absence of a lock file is normal and ignored.
		nodeLock := filepath.Join(f.Root, e.Name(), KegLockFile)
		if err := os.Remove(nodeLock); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove lock %s: %w", nodeLock, err))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return NewBackendError("fs", "ClearLocks", 0, errs[0], false)
	}

	// Aggregate messages for multiple errors.
	agg := ""
	for i, er := range errs {
		if i > 0 {
			agg += "; "
		}
		agg += er.Error()
	}
	return NewBackendError("fs", "ClearLocks", 0, fmt.Errorf("%s", agg), false)
}

// ClearDex removes all entries (files and subdirectories) contained in the
// repository's "dex" directory. It intentionally does not remove the "dex"
// directory itself — only its contents are deleted.
//
// It acquires a simple file-based lock at the repo root (KegLockFile) for the
// duration of the operation. The lock acquisition is attempted for up to
// lockTimeout and retries every lockInterval.
func (f *FsRepo) ClearDex() (err error) {
	dexDir := filepath.Join(f.Root, "dex")

	const (
		lockTimeout  = 5 * time.Second
		lockInterval = 100 * time.Millisecond
	)

	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	unlock, err := f.acquireLock(ctx, lockInterval)
	if err != nil {
		// AcquireLock returns a BackendError on filesystem issues already,
		// but wrap here to retain the ClearDex semantic in the op name.
		return NewBackendError("fs", "ClearDex", 0, err, false)
	}

	// Ensure we always remove the lock when done and return any unlock error.
	defer func() {
		if uerr := unlock(); uerr != nil {
			if err == nil {
				err = NewBackendError("fs", "ClearDex", 0, uerr, false)
			} else {
				// Combine both errors: prefer to keep original err and
				// annotate unlock failure.
				err = fmt.Errorf("%w; unlock error: %v", err, uerr)
			}
		}
	}()

	// If dex directory doesn't exist, nothing to clear.
	if _, statErr := os.Stat(dexDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return NewBackendError("fs", "ClearDex", 0, statErr, false)
	}

	entries, readErr := os.ReadDir(dexDir)
	if readErr != nil {
		return NewBackendError("fs", "ClearDex", 0, readErr, false)
	}

	for _, e := range entries {
		path := filepath.Join(dexDir, e.Name())
		if rmErr := os.RemoveAll(path); rmErr != nil {
			return NewBackendError("fs", "ClearDex", 0, rmErr, false)
		}
	}

	return nil
}

// DeleteImage implements KegRepository.
func (f *FsRepo) DeleteImage(id NodeID, name string) error {
	panic("unimplemented")
}

// DeleteItem removes a named ancillary item (file or directory) from the
// node's directory (root/<id>/<name>). Behavior:
//   - If the node directory does not exist, return a typed NodeNotFoundError.
//   - If the named item does not exist, return the sentinel ErrMetaNotFound.
//   - Any unexpected filesystem errors are wrapped in a BackendError.
//   - Removal is performed with os.RemoveAll so both files and directories are
//     supported.
func (f *FsRepo) DeleteItem(id NodeID, name string) error {
	// Disallow removing the primary content or meta files.
	if name == f.ContentFileName || name == f.MetaFilename {
		return ErrPermissionDenied
	}

	const (
		lockTimeout  = 5 * time.Second
		lockInterval = 100 * time.Millisecond
	)

	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	// Acquire a per-node lock to avoid races with concurrent node operations.
	unlock, err := f.acquireNodeLock(ctx, id, lockInterval)
	if err != nil {
		// acquireNodeLock already returns typed errors (NewNodeNotFoundError
		// or BackendError), so return as-is to preserve matchability by
		// callers.
		return err
	}
	// Best-effort unlock; don't override a more specific error returned below.
	defer func() { _ = unlock() }()

	nodeDir := filepath.Join(f.Root, id.Path())
	itemPath := filepath.Join(nodeDir, name)

	// Verify the target exists so we can return a meaningful sentinel when
	// missing.
	if _, statErr := os.Stat(itemPath); statErr != nil {
		if os.IsNotExist(statErr) {
			// Item absent — treat as missing metadata/item.
			return ErrMetaNotFound
		}
		return NewBackendError("fs", "DeleteItem", 0, statErr, false)
	}

	// Remove the item (file or directory). Use RemoveAll to handle both files
	// and directories; wrap any error for callers to inspect/decide about
	// retry.
	if err := os.RemoveAll(itemPath); err != nil {
		return NewBackendError("fs", "DeleteItem", 0, err, false)
	}

	return nil
}

// DeleteMeta implements KegRepository.
func (f *FsRepo) DeleteMeta(id NodeID) error {
	const (
		lockTimeout  = 5 * time.Second
		lockInterval = 100 * time.Millisecond
	)

	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	// Acquire a per-node lock to avoid races with concurrent node operations.
	unlock, err := f.acquireNodeLock(ctx, id, lockInterval)
	if err != nil {
		// acquireNodeLock already returns typed errors (NewNodeNotFoundError
		// or BackendError), so return as-is to preserve matchability by callers.
		return err
	}
	// Best-effort unlock; don't override a more specific error returned below.
	defer func() { _ = unlock() }()

	nodeDir := filepath.Join(f.Root, id.Path())
	metaPath := filepath.Join(nodeDir, f.MetaFilename)

	// Verify the meta file exists so we can return a meaningful sentinel when missing.
	if _, statErr := os.Stat(metaPath); statErr != nil {
		if os.IsNotExist(statErr) {
			return ErrMetaNotFound
		}
		return NewBackendError("fs", "DeleteMeta", 0, statErr, false)
	}

	// Remove the meta file. Use os.Remove to remove a single file; wrap any error
	// for callers to inspect/decide about retry.
	if err := os.Remove(metaPath); err != nil {
		// If the file vanished between Stat and Remove, treat as missing meta.
		if os.IsNotExist(err) {
			return ErrMetaNotFound
		}
		return NewBackendError("fs", "DeleteMeta", 0, err, false)
	}

	return nil
}

// DeleteNode implements KegRepository.
func (f *FsRepo) DeleteNode(id NodeID) error {
	panic("unimplemented")
}

// GetIndex implements KegRepository.
func (f *FsRepo) GetIndex(name string) ([]byte, error) {
	panic("unimplemented")
}

// ListImages implements KegRepository.
func (f *FsRepo) ListImages(id NodeID) ([]string, error) {
	panic("unimplemented")
}

// ListIndexes implements KegRepository.
func (f *FsRepo) ListIndexes() ([]string, error) {
	panic("unimplemented")
}

// ListItems implements KegRepository.
func (f *FsRepo) ListItems(id NodeID) ([]string, error) {
	panic("unimplemented")
}

// ListNodes implements KegRepository.
func (f *FsRepo) ListNodes() ([]NodeRef, error) {
	panic("unimplemented")
}

// ListNodesID implements KegRepository.
func (f *FsRepo) ListNodesID() ([]NodeID, error) {
	panic("unimplemented")
}

// MoveNode implements KegRepository.
func (f *FsRepo) MoveNode(id NodeID, dst NodeID) error {
	panic("unimplemented")
}

// ReadConfig implements KegRepository.
func (f *FsRepo) ReadConfig() (Config, error) {
	panic("unimplemented")
}

// ReadContent implements KegRepository.
func (f *FsRepo) ReadContent(id NodeID) ([]byte, error) {
	panic("unimplemented")
}

// ReadMeta implements KegRepository.
func (f *FsRepo) ReadMeta(id NodeID) ([]byte, error) {
	panic("unimplemented")
}

// Stats implements KegRepository.
func (f *FsRepo) Stats(id NodeID) (NodeStats, error) {
	panic("unimplemented")
}

// UploadImage implements KegRepository.
func (f *FsRepo) UploadImage(id NodeID, name string, data []byte) error {
	panic("unimplemented")
}

// UploadItem implements KegRepository.
func (f *FsRepo) UploadItem(id NodeID, name string, data []byte) error {
	panic("unimplemented")
}

// WriteConfig implements KegRepository.
func (f *FsRepo) WriteConfig(config Config) error {
	panic("unimplemented")
}

// WriteContent implements KegRepository.
func (f *FsRepo) WriteContent(id NodeID, data []byte) error {
	panic("unimplemented")
}

// WriteIndex implements KegRepository.
func (f *FsRepo) WriteIndex(name string, data []byte) error {
	panic("unimplemented")
}

// WriteMeta implements KegRepository.
func (f *FsRepo) WriteMeta(id NodeID, data []byte) error {
	panic("unimplemented")
}

var _ KegRepository = (*FsRepo)(nil)
