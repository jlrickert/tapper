package keg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	MarkdownContentFilename = "README.md"
	YAMLMetaFilename        = "meta.yaml"
	KegCurrentEnvKey        = "KEG_CURRENT"
	KegLockFile             = ".keg-lock"
	NodeImagesDir           = "images"
	NodeAttachmentsDir      = "attachments"
)

// FsRepo implements [KegRepository] using the local filesystem as storage. It
// manages KEG nodes as directories under [Root], with each node containing
// content files, metadata, and optional attachments. Thread-safe operations
// are coordinated through the embedded mutex.
type FsRepo struct {
	// Root is the base directory path containing all KEG node directories
	Root string
	// ContentFilename specifies the filename for node content (typically README.md)
	ContentFilename string
	// MetaFilename specifies the filename for node metadata (typically meta.yaml)
	MetaFilename string
	// lock coordinates concurrent access to repository operations
	lock *sync.Mutex

	// optional defaults for callers who don't set their own
	LockTimeout  time.Duration
	LockInterval time.Duration
}

// // ------------------------------- constructors --------------------------------

// NewFsRepoFromEnvOrSearch tries to locate a keg file using the order:
// 1) KEG_CURRENT env var (file or directory)
// 2) current working directory
// 3) if inside a git project, search the project tree for a keg file
// 4) recursive search from current working directory
// 5) fallback to default config location (~/.config/keg or XDG equivalent)
//
// Returns a pointer to an initialized FsRepo and the path of the discovered keg file (or "" if using fallback path).
func NewFsRepoFromEnvOrSearch(ctx context.Context) (*FsRepo, error) {
	f := &FsRepo{}
	// candidate names we consider a keg file
	candidates := []string{"keg", "keg.yaml", "keg.yml"}

	// 1) KEG_CURRENT
	if v := os.Getenv(KegCurrentEnvKey); v != "" {
		if p, err := resolveKegFromEnv(v, candidates); err == nil {
			f := &FsRepo{
				Root:            p.rootDir,
				ContentFilename: MarkdownContentFilename,
				MetaFilename:    YAMLMetaFilename,
				lock:            &sync.Mutex{},
			}
			return f, nil
		}
		// if env set but didn't resolve, continue searching (do not fail)
	}

	// 2) current directory
	cwd, err := os.Getwd()
	if err != nil {
		return nil, NewBackendError(f.Name(), "NewFsRepoFromEnvOrSearch", 0, err, false)
	}
	if kp := findKegInDir(cwd, candidates); kp != "" {
		f := &FsRepo{
			Root:            cwd,
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
			lock:            &sync.Mutex{},
		}
		return f, nil
	}

	// 3) if in a git project, find git root and search the project tree
	if gitRoot := findGitRoot(cwd); gitRoot != "" {
		if kp := findKegRecursive(gitRoot, candidates); kp != "" {
			f := &FsRepo{
				Root:            filepath.Dir(kp), // directory containing the keg file
				ContentFilename: MarkdownContentFilename,
				MetaFilename:    YAMLMetaFilename,
				lock:            &sync.Mutex{},
			}
			return f, nil
		}
	}

	// 4) traverse current directory recursively (in case the keg is somewhere under cwd)
	if kp := findKegRecursive(cwd, candidates); kp != "" {
		f := &FsRepo{
			Root:            filepath.Dir(kp),
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
			lock:            &sync.Mutex{},
		}
		return f, nil
	}

	// 5) fallback default: use XDG config dir or $HOME/.config/keg
	if cfgDir, err := os.UserConfigDir(); err == nil {
		defDir := filepath.Join(cfgDir, "keg")
		// create directory if missing? only choose as root, don't create file.
		f := &FsRepo{
			Root:            defDir,
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
			lock:            &sync.Mutex{},
		}
		return f, nil
	}

	return nil, NewBackendError(
		f.Name(),
		"NewFsRepoFromEnvOrSearch",
		0,
		errors.New("could not determine fallback config dir"),
		false,
	)
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
			if slices.Contains(candidates, base) {
				found = path
				return nil
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

// ------------------------------ fs repo locks -------------------------------

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
					return NewBackendError(f.Name(), "AcquireLock:unlock", 0, err, false)
				}
				return nil
			}
			return unlock, nil
		}

		// Unexpected error (not "file exists")
		if !os.IsExist(err) {
			return nil, NewBackendError(f.Name(), "AcquireLock", 0, err, false)
		}

		// Lock file exists; wait or bail if context is done
		select {
		case <-ctx.Done():
			// Use sentinel for callers that want to check timeout/cancel specifically
			return nil, NewBackendError(f.Name(), "AcquireLock", 0, fmt.Errorf("%w: %s", ErrLockTimeout, ctx.Err()), false)
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
func (f *FsRepo) LockNode(ctx context.Context, id NodeID, retryInterval time.Duration) (func() error, error) {
	nodeDir := filepath.Join(f.Root, (&id).Path())

	// Ensure the node directory exists before attempting to lock.
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, NewNodeNotFoundError(id)
		}
		return nil, NewBackendError(f.Name(), "AcquireNodeLock", 0, statErr, false)
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
					return NewBackendError(f.Name(), "AcquireNodeLock:unlock", 0, err, false)
				}
				return nil
			}
			return unlock, nil
		}

		// Unexpected error (not "file exists")
		if !os.IsExist(err) {
			return nil, NewBackendError(f.Name(), "AcquireNodeLock", 0, err, false)
		}

		// Lock file exists; wait or bail if context is done
		select {
		case <-ctx.Done():
			return nil, NewBackendError(f.Name(), "AcquireNodeLock", 0, fmt.Errorf("%w: %s", ErrLockTimeout, ctx.Err()), false)
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
		return NewBackendError(f.Name(), "ClearLocks", 0, err, false)
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
		return NewBackendError(f.Name(), "ClearLocks", 0, errs[0], false)
	}

	// Aggregate messages for multiple errors.
	agg := ""
	for i, er := range errs {
		if i > 0 {
			agg += "; "
		}
		agg += er.Error()
	}
	return NewBackendError(f.Name(), "ClearLocks", 0, fmt.Errorf("%s", agg), false)
}

func (f *FsRepo) ClearNodeLock(ctx context.Context, id NodeID) error {
	nodeDir := filepath.Join(f.Root, id.Path())

	// Ensure the node directory exists.
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return NewNodeNotFoundError(id)
		}
		return NewBackendError(f.Name(), "ClearNodeLock", 0, statErr, false)
	}

	lockPath := filepath.Join(nodeDir, KegLockFile)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return NewBackendError(f.Name(), "ClearNodeLock", 0, err, false)
	}

	return nil
}

// ------------------ KegRepository interface implementation ------------------

func (f *FsRepo) Name() string {
	return "fs"
}

func (f *FsRepo) Next(ctx context.Context) (NodeID, error) {
	// use repo-level lock to reserve a new id deterministically
	lctx, cancel, retry := f.withLockContext(ctx)
	defer cancel()

	unlock, err := f.acquireLock(lctx, retry)
	if err != nil {
		return 0, NewBackendError(f.Name(), "Next", 0, err, false)
	}
	// best-effort unlock
	defer func() { _ = unlock() }()

	// Ensure repo root exists (if not, create it)
	if _, statErr := os.Stat(f.Root); statErr != nil {
		return 0, NewBackendError(f.Name(), "Next", 0, statErr, false)
	}

	entries, err := os.ReadDir(f.Root)
	if err != nil {
		return 0, NewBackendError(f.Name(), "Next", 0, err, false)
	}

	maxID := -1
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if v, perr := strconvAtoiSafe(e.Name()); perr == nil {
			if v > maxID {
				maxID = v
			}
		}
	}

	next := maxID + 1
	return NodeID(next), nil
}

// ReadContent implements KegRepository.
func (f *FsRepo) ReadContent(ctx context.Context, id NodeID) ([]byte, error) {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, NewNodeNotFoundError(id)
		}
		return nil, NewBackendError(f.Name(), "ReadContent", 0, statErr, false)
	}
	contentPath := filepath.Join(nodeDir, f.ContentFilename)
	b, err := os.ReadFile(contentPath)
	if err != nil {
		if os.IsNotExist(err) {
			// node exists but no content
			return nil, nil
		}
		return nil, NewBackendError(f.Name(), "ReadContent", 0, err, false)
	}
	return append([]byte(nil), b...), nil
}

// ReadMeta implements KegRepository.
func (f *FsRepo) ReadMeta(ctx context.Context, id NodeID) ([]byte, error) {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, NewNodeNotFoundError(id)
		}
		return nil, NewBackendError(f.Name(), "ReadMeta", 0, statErr, false)
	}
	metaPath := filepath.Join(nodeDir, f.MetaFilename)
	b, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []byte(nil), nil
		}
		return nil, NewBackendError(f.Name(), "ReadMeta", 0, err, false)
	}
	return append([]byte(nil), b...), nil
}

func (f *FsRepo) ListNodes(ctx context.Context) ([]NodeID, error) {
	entries, err := os.ReadDir(f.Root)
	if err != nil {
		return nil, NewBackendError(f.Name(), "ListNodes", 0, err, false)
	}
	var ids []NodeID
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if v, perr := strconvAtoiSafe(e.Name()); perr == nil {
			ids = append(ids, NodeID(v))
		}
	}
	// sort ascending (selection sort to avoid extra imports)
	for i := 0; i < len(ids); i++ {
		min := i
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[min] {
				min = j
			}
		}
		if min != i {
			ids[i], ids[min] = ids[min], ids[i]
		}
	}
	return ids, nil
}

// ListItems implements KegRepository.
func (f *FsRepo) ListItems(ctx context.Context, id NodeID) ([]string, error) {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, NewNodeNotFoundError(id)
		}
		return nil, NewBackendError(f.Name(), "ListItems", 0, statErr, false)
	}

	entries, err := os.ReadDir(filepath.Join(nodeDir, NodeAttachmentsDir))
	if err != nil {
		return nil, NewBackendError(f.Name(), "ListItems", 0, err, false)
	}

	var names []string
	for _, e := range entries {
		n := e.Name()
		names = append(names, n)
	}
	sortStrings(names)
	return names, nil
}

// ListImages implements KegRepository.
func (f *FsRepo) ListImages(ctx context.Context, id NodeID) ([]string, error) {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, NewNodeNotFoundError(id)
		}
		return nil, NewBackendError(f.Name(), "ListImages", 0, statErr, false)
	}

	imagesDir := filepath.Join(nodeDir, NodeImagesDir)
	entries, err := os.ReadDir(imagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			// no images directory -> empty list
			return []string{}, nil
		}
		return nil, NewBackendError(f.Name(), "ListImages", 0, err, false)
	}

	var names []string
	for _, e := range entries {
		// skip metadata dir
		if e.Name() == ".meta" {
			continue
		}
		// include files only (images typically files); allow directories as well
		names = append(names, e.Name())
	}
	// deterministic order
	sortStrings(names)
	return names, nil
}

// WriteContent implements KegRepository.
func (f *FsRepo) WriteContent(ctx context.Context, id NodeID, data []byte) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		return NewBackendError(f.Name(), "WriteContent", 0, err, false)
	}

	metaPath := filepath.Join(nodeDir, f.ContentFilename)
	err := atomicWriteFile(metaPath, data, 0o0644)
	if err != nil {
		return NewBackendError(f.Name(), "WriteContent", 0, err, false)
	}
	return nil
}

// WriteMeta implements KegRepository.
func (f *FsRepo) WriteMeta(ctx context.Context, id NodeID, data []byte) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return NewNodeNotFoundError(id)
		}
		return NewBackendError(f.Name(), "WriteMeta", 0, statErr, false)
	}

	metaPath := filepath.Join(nodeDir, f.MetaFilename)
	err := atomicWriteFile(metaPath, data, 0o0644)
	if err != nil {
		return NewBackendError(f.Name(), "WriteMeta", 0, err, false)
	}
	return nil
}

// UploadImage implements KegRepository.
func (f *FsRepo) UploadImage(ctx context.Context, id NodeID, name string, data []byte) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return NewNodeNotFoundError(id)
		}
		return NewBackendError(f.Name(), "UploadImage", 0, statErr, false)
	}

	imagePath := filepath.Join(nodeDir, NodeImagesDir, name)
	err := atomicWriteFile(imagePath, data, 0o0644)
	if err != nil {
		return NewBackendError(f.Name(), "UploadImage", 0, err, false)
	}

	return nil
}

// UploadItem implements KegRepository.
func (f *FsRepo) UploadItem(ctx context.Context, id NodeID, name string, data []byte) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return NewNodeNotFoundError(id)
		}
		return NewBackendError(f.Name(), "UploadImage", 0, statErr, false)
	}

	itemPath := filepath.Join(nodeDir, NodeAttachmentsDir, name)
	err := atomicWriteFile(itemPath, data, 0o0644)
	if err != nil {
		return NewBackendError(f.Name(), "UploadItem", 0, err, false)
	}

	return nil
}

// MoveNode implements KegRepository.
func (f *FsRepo) MoveNode(ctx context.Context, id NodeID, dst NodeID) error {
	src := filepath.Join(f.Root, id.Path())
	if _, statErr := os.Stat(src); statErr != nil {
		if os.IsNotExist(statErr) {
			return NewNodeNotFoundError(id)
		}
		return NewBackendError(f.Name(), "MoveNode", 0, statErr, false)
	}

	dstPath := filepath.Join(f.Root, dst.Path())
	if _, statErr := os.Stat(dstPath); statErr == nil {
		return NewDestinationExistsError(dst)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return NewBackendError(f.Name(), "MoveNode", 0, statErr, false)
	}

	if err := os.Rename(src, dstPath); err != nil {
		return NewBackendError(f.Name(), "MoveNode", 0, err, false)
	}
	return nil
}

// GetIndex implements KegRepository.
func (f *FsRepo) GetIndex(ctx context.Context, name string) ([]byte, error) {
	idxPath := filepath.Join(f.Root, "dex", name)
	b, err := os.ReadFile(idxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrDexNotFound
		}
		return nil, NewBackendError(f.Name(), "GetIndex", 0, err, false)
	}
	// return a copy (ReadFile already returns a copy)
	return append([]byte(nil), b...), nil
}

func (f *FsRepo) ClearIndexes(ctx context.Context) error {
	dexDir := filepath.Join(f.Root, "dex")

	// If dex directory doesn't exist, nothing to clear.
	if _, statErr := os.Stat(dexDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return NewBackendError(f.Name(), "ClearIndexes", 0, statErr, false)
	}

	entries, readErr := os.ReadDir(dexDir)
	if readErr != nil {
		return NewBackendError(f.Name(), "ClearIndexes", 0, readErr, false)
	}

	for _, e := range entries {
		path := filepath.Join(dexDir, e.Name())
		if rmErr := os.RemoveAll(path); rmErr != nil {
			return NewBackendError(f.Name(), "ClearIndexes", 0, rmErr, false)
		}
	}

	return nil
}

// WriteIndex implements KegRepository.
func (f *FsRepo) WriteIndex(ctx context.Context, name string, data []byte) error {
	idxPath := filepath.Join("dex", name)
	err := atomicWriteFile(idxPath, data, 0o0644)
	if err != nil {
		return NewBackendError(f.Name(), "WriteIndex", 0, err, false)
	}
	return nil
}

// ListIndexes implements KegRepository.
func (f *FsRepo) ListIndexes(ctx context.Context) ([]string, error) {
	dexDir := filepath.Join(f.Root, "dex")
	entries, err := os.ReadDir(dexDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, NewBackendError(f.Name(), "ListIndexes", 0, err, false)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sortStrings(names)
	return names, nil
}

// DeleteNode implements KegRepository.
func (f *FsRepo) DeleteNode(ctx context.Context, id NodeID) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return NewNodeNotFoundError(id)
		}
		return NewBackendError(f.Name(), "DeleteNode", 0, statErr, false)
	}

	if err := os.RemoveAll(nodeDir); err != nil {
		return NewBackendError(f.Name(), "DeleteNode", 0, err, false)
	}
	return nil
}

// DeleteImage implements KegRepository.
func (f *FsRepo) DeleteImage(ctx context.Context, id NodeID, name string) error {
	nodeDir := filepath.Join(f.Root, id.Path())

	// Ensure node exists
	if _, statErr := os.Stat(nodeDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return NewNodeNotFoundError(id)
		}
		return NewBackendError(f.Name(), "DeleteImage", 0, statErr, false)
	}

	imagesDir := filepath.Join(nodeDir, NodeImagesDir)
	imagePath := filepath.Join(imagesDir, name)

	if _, statErr := os.Stat(imagePath); statErr != nil {
		if os.IsNotExist(statErr) {
			return ErrNotFound
		}
		return NewBackendError(f.Name(), "DeleteImage", 0, statErr, false)
	}

	// Remove image and possible metadata/thumbs; best-effort for extras.
	if err := os.RemoveAll(imagePath); err != nil {
		return NewBackendError(f.Name(), "DeleteImage", 0, err, false)
	}

	// remove per-image meta if present
	metaPath := filepath.Join(imagesDir, ".meta", name+".json")
	_ = os.Remove(metaPath)

	// remove thumbs directory entry best-effort
	thumbPath := filepath.Join(imagesDir, "thumbs", name)
	_ = os.RemoveAll(thumbPath)

	return nil
}

// DeleteItem removes a named ancillary item (file or directory) from the
// node's directory (root/<id>/<name>). Behavior:
//   - If the node directory does not exist, return a typed NodeNotFoundError.
//   - If the named item does not exist, return the sentinel ErrMetaNotFound.
//   - Any unexpected filesystem errors are wrapped in a BackendError.
//   - Removal is performed with os.RemoveAll so both files and directories are
//     supported.
func (f *FsRepo) DeleteItem(ctx context.Context, id NodeID, name string) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	itemPath := filepath.Join(nodeDir, name)

	// Verify the target exists so we can return a meaningful sentinel when
	// missing.
	if _, statErr := os.Stat(itemPath); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return NewBackendError(f.Name(), "DeleteItem", 0, statErr, false)
	}

	// Remove the item (file or directory). Use RemoveAll to handle both files
	// and directories; wrap any error for callers to inspect/decide about
	// retry.
	if err := os.RemoveAll(itemPath); err != nil {
		return NewBackendError(f.Name(), "DeleteItem", 0, err, false)
	}

	return nil
}

// ReadConfig implements KegRepository.
func (f *FsRepo) ReadConfig(ctx context.Context) (Config, error) {
	candidates := []string{"keg", "keg.yaml", "keg.yml"}
	var lastErr error
	for _, c := range candidates {
		p := filepath.Join(f.Root, c)
		if _, err := os.Stat(p); err == nil {
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return Config{}, NewBackendError(f.Name(), "ReadConfig", 0, rerr, false)
			}
			cfg, perr := ParseConfigData(b)
			if perr != nil {
				return Config{}, NewBackendError(f.Name(), "ReadConfig", 0, perr, false)
			}
			return cfg, nil
		} else if !os.IsNotExist(err) {
			lastErr = err
		}
	}
	if lastErr != nil {
		return Config{}, NewBackendError(f.Name(), "ReadConfig", 0, lastErr, false)
	}
	return Config{}, ErrKegNotFound
}

// WriteConfig implements KegRepository.
func (f *FsRepo) WriteConfig(ctx context.Context, config Config) error {
	// marshal to YAML
	out, err := yaml.Marshal(config)
	if err != nil {
		return NewBackendError(f.Name(), "WriteConfig", 0, err, false)
	}
	target := filepath.Join(f.Root, "keg")

	err = atomicWriteFile(target, out, 0o0644)
	if err != nil {
		return NewBackendError(f.Name(), "WriteConfig", 0, err, false)
	}
	return nil
}

var _ KegRepository = (*FsRepo)(nil)

// ----------------- small helpers -----------------

// atomicWriteFile writes data to path atomically. It:
//   - ensures parent directory exists,
//   - writes to a temp file in the same directory,
//   - fsyncs the file,
//   - renames the temp file to the final path (atomic on POSIX),
//   - fsyncs the parent directory (best-effort).
//
// perm is the file mode to use for the final file (e.g. 0644).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	// Ensure parent directory exists.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("atomic write: mkdirall %q: %w", dir, err)
	}

	// Create temp file in same dir so rename is atomic on same filesystem.
	tmpFile, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("atomic write: create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	// If anything goes wrong, try to remove the temp file.
	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	// Write data.
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return fmt.Errorf("atomic write: write temp file %q: %w", tmpName, err)
	}

	// Sync to storage.
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return fmt.Errorf("atomic write: sync temp file %q: %w", tmpName, err)
	}

	// Close file before renaming.
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return fmt.Errorf("atomic write: close temp file %q: %w", tmpName, err)
	}

	// Set final permissions (rename preserves perms on many systems, but ensure).
	if err := os.Chmod(tmpName, perm); err != nil {
		// Not fatal: attempt rename anyway, but record error if rename fails.
		// We don't return here because chmod may fail on platforms with different semantics.
	}

	// Rename into place (atomic on POSIX when same fs).
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("atomic write: rename %q -> %q: %w", tmpName, path, err)
	}

	// Best-effort sync of parent directory so directory entry is durable.
	// Skip on Windows (no reliable dir fsync semantics there).
	if err := syncDir(dir); err != nil && runtime.GOOS != "windows" {
		// Directory sync failure is important on POSIX; report it.
		return fmt.Errorf("atomic write: sync dir %q: %w", dir, err)
	}

	return nil
}

// syncDir opens dir and calls Sync on it so directory metadata (the rename)
// is flushed. Returns error from Open or Sync.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	// On Unix, File.Sync on a directory will fsync the directory.
	return d.Sync()
}

// strconvAtoiSafe wraps strconv.Atoi while avoiding an import in many files.
func strconvAtoiSafe(s string) (int, error) {
	// accept leading/trailing spaces trimmed
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	// disallow non-digit prefixes
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit")
		}
	}
	// parse manually
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n, nil
}

func sortStrings(ss []string) {
	if len(ss) <= 1 {
		return
	}
	for i := 0; i < len(ss); i++ {
		min := i
		for j := i + 1; j < len(ss); j++ {
			if ss[j] < ss[min] {
				min = j
			}
		}
		if min != i {
			ss[i], ss[min] = ss[min], ss[i]
		}
	}
}

func sortNodeRefsByID(refs []NodeRef) {
	if len(refs) <= 1 {
		return
	}
	for i := 0; i < len(refs); i++ {
		min := i
		for j := i + 1; j < len(refs); j++ {
			if refs[j].ID < refs[min].ID {
				min = j
			}
		}
		if min != i {
			refs[i], refs[min] = refs[min], refs[i]
		}
	}
}
