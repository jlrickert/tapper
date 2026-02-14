package keg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	appCtx "github.com/jlrickert/cli-toolkit/appctx"
	"github.com/jlrickert/cli-toolkit/toolkit"
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

// FsRepo implements [Repository] using the local filesystem as storage. It
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
}

// ------------------------------- constructors --------------------------------

// NewFsRepoFromEnvOrSearch tries to locate a keg file using the order:
// 1) KEG_CURRENT env var (file or directory)
// 2) current working directory
// 3) if inside a git project, search the project tree for a keg file
// 4) recursive search from current working directory
// 5) fallback to default config location (~/.config/keg or XDG equivalent)
//
// Returns a pointer to an initialized FsRepo and the path of the discovered keg
// file (or "" if using fallback path).
func NewFsRepoFromEnvOrSearch(ctx context.Context) (*FsRepo, error) {
	env := toolkit.EnvFromContext(ctx)
	f := &FsRepo{}
	// candidate names we consider a keg file
	candidates := []string{"keg", "keg.yaml", "keg.yml"}

	// 1) KEG_CURRENT
	if v := env.Get(KegCurrentEnvKey); v != "" {
		if p, err := resolveKegFromEnv(ctx, v, candidates); err == nil {
			f := &FsRepo{
				Root:            p.rootDir,
				ContentFilename: MarkdownContentFilename,
				MetaFilename:    YAMLMetaFilename,
			}
			return f, nil
		}
		// if env set but didn't resolve, continue searching (do not fail)
	}

	// 2) current directory
	cwd, err := env.Getwd()
	if err != nil {
		return nil, NewBackendError(f.Name(),
			"NewFsRepoFromEnvOrSearch", 0, err, false)
	}
	if kp := findKegInDir(ctx, cwd, candidates); kp != "" {
		f := &FsRepo{
			Root:            cwd,
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
		}
		return f, nil
	}

	// 3) if in a git project, find git root and search the project tree
	if gitRoot := appCtx.FindGitRoot(ctx, cwd); gitRoot != "" {
		if kp := findKegRecursive(gitRoot, candidates); kp != "" {
			f := &FsRepo{
				Root:            filepath.Dir(kp), // directory containing the keg file
				ContentFilename: MarkdownContentFilename,
				MetaFilename:    YAMLMetaFilename,
			}
			return f, nil
		}
	}

	// 4) traverse current directory recursively (in case the keg is somewhere
	// under cwd)
	if kp := findKegRecursive(cwd, candidates); kp != "" {
		f := &FsRepo{
			Root:            filepath.Dir(kp),
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
		}
		return f, nil
	}

	// 5) fallback default: use XDG config dir or $HOME/.config/keg
	if cfgDir, err := toolkit.UserConfigPath(ctx); err == nil {
		defDir := filepath.Join(cfgDir, "keg")
		// create directory if missing? only choose as root, don't create file.
		f := &FsRepo{
			Root:            defDir,
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
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
//   - absolute path to a file (keg file) -> use its directory as root
//   - directory path -> check for a keg file inside that directory -> if found
//     use that
//
// if nothing matches, returns error.
//
// This refactor uses std helpers to expand env vars and tildes. ctx may be nil.
func resolveKegFromEnv(ctx context.Context, v string, candidates []string) (envResolveResult, error) {

	// Expand env vars first, then attempt path expansion.
	v = toolkit.ExpandEnv(ctx, v)
	if expanded, err := toolkit.ExpandPath(ctx, v); err == nil {
		v = expanded
	}
	info, err := toolkit.Stat(ctx, v, false)
	if err == nil && info.Mode().IsRegular() {
		// env pointed to a file; verify its name is a candidate
		base := filepath.Base(v)
		if slices.Contains(candidates, base) {
			return envResolveResult{rootDir: filepath.Dir(v), kegPath: v}, nil
		}
		return envResolveResult{}, NewBackendError("fs",
			"resolveKegFromEnv", 0,
			errors.New("KEG_CURRENT pointed to a file that is not a known keg filename"),
			false)
	}
	if err == nil && info.IsDir() {
		// env pointed to a directory: check for candidate file inside
		for _, c := range candidates {
			p := filepath.Join(v, c)
			if fi, statErr := toolkit.Stat(ctx, p, false); statErr == nil && fi.Mode().IsRegular() {
				return envResolveResult{rootDir: v, kegPath: p}, nil
			}
		}
		// directory but no keg file found — treat as valid root only if caller
		// expects that. For our purposes require the keg file to exist; return
		// error to let caller continue search.
		return envResolveResult{}, NewBackendError("fs",
			"resolveKegFromEnv", 0,
			errors.New("KEG_CURRENT directory does not contain a keg file"),
			false)
	}
	// path doesn't exist or stat failed — treat as error
	return envResolveResult{}, NewBackendError("fs",
		"resolveKegFromEnv", 0, err, false)
}

// findKegInDir checks if any candidate keg filename exists directly in dir.
// returns full path or "".
func findKegInDir(ctx context.Context, dir string, candidates []string) string {
	for _, c := range candidates {
		p := filepath.Join(dir, c)
		if fi, err := toolkit.Stat(ctx, p, false); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// findKegRecursive walks root and returns the first matched keg file path, or
// "" if none.
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

// ------------------ Repository interface implementation ------------------

func (f *FsRepo) Name() string {
	return "fs"
}

// WithNodeLock executes fn while holding an exclusive lock for node id.
func (f *FsRepo) WithNodeLock(ctx context.Context, id NodeId, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	if contextHasNodeLock(ctx, id) {
		return fn(ctx)
	}

	nodeDir := filepath.Join(f.Root, id.Path())
	if err := toolkit.Mkdir(ctx, nodeDir, 0o755, true); err != nil {
		return errors.Join(ErrLock, NewBackendError(f.Name(), "WithNodeLock", 0, err, false))
	}

	lockPath := filepath.Join(nodeDir, KegLockFile)
	for {
		err := toolkit.Mkdir(ctx, lockPath, 0o700, false)
		if err == nil {
			break
		}
		if os.IsExist(err) {
			select {
			case <-ctx.Done():
				return fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		return errors.Join(ErrLock, NewBackendError(f.Name(), "WithNodeLock", 0, err, false))
	}

	lockedCtx := contextWithNodeLock(ctx, id)
	runErr := fn(lockedCtx)

	unlockCtx := context.WithoutCancel(lockedCtx)
	unlockErr := toolkit.Remove(unlockCtx, lockPath, true)
	if unlockErr != nil && !os.IsNotExist(unlockErr) {
		unlockErr = errors.Join(ErrLock, NewBackendError(f.Name(), "WithNodeLockUnlock", 0, unlockErr, false))
	} else {
		unlockErr = nil
	}

	return errors.Join(runErr, unlockErr)
}

func (f *FsRepo) Next(ctx context.Context) (NodeId, error) {
	// Ensure repo root exists (if not, create it)
	if _, statErr := toolkit.Stat(ctx, f.Root, false); statErr != nil {
		return NodeId{}, NewBackendError(f.Name(), "Next", 0, statErr, false)
	}

	entries, err := toolkit.ReadDir(ctx, f.Root)
	if err != nil {
		return NodeId{}, NewBackendError(f.Name(), "Next", 0, err, false)
	}

	maxID := -1
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Accept directory names that parse as valid NodeId ids, e.g. "42" or "42-0001".
		if n, perr := ParseNode(e.Name()); perr == nil && n != nil {
			if n.ID > maxID {
				maxID = n.ID
			}
		}
	}

	next := maxID + 1
	return NodeId{ID: next}, nil
}

// ReadContent implements Repository.
func (f *FsRepo) ReadContent(ctx context.Context, id NodeId) ([]byte, error) {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := toolkit.Stat(ctx, nodeDir, false); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, ErrNotExist
		} else if os.IsPermission(statErr) {
			return nil, ErrPermission
		}
		return nil, NewBackendError(f.Name(), "ReadContent", 0, statErr, false)
	}
	contentPath := filepath.Join(nodeDir, f.ContentFilename)
	b, err := toolkit.ReadFile(ctx, contentPath)
	if err != nil {
		if os.IsNotExist(err) {
			// node exists but no content
			return []byte(nil), nil
		}
		return nil, NewBackendError(f.Name(), "ReadContent", 0, err, false)
	}
	return b, nil
}

// ReadMeta implements Repository.
func (f *FsRepo) ReadMeta(ctx context.Context, id NodeId) ([]byte, error) {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := toolkit.Stat(ctx, nodeDir, false); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, ErrNotExist
		}
		return nil, NewBackendError(f.Name(), "ReadMeta", 0, statErr, false)
	}
	metaPath := filepath.Join(nodeDir, f.MetaFilename)
	b, err := toolkit.ReadFile(ctx, metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []byte(nil), nil
		}
		return nil, NewBackendError(f.Name(), "ReadMeta", 0, err, false)
	}
	return b, nil
}

// ReadStats implements Repository.
func (f *FsRepo) ReadStats(ctx context.Context, id NodeId) (*NodeStats, error) {
	raw, err := f.ReadMeta(ctx, id)
	if err != nil {
		return nil, err
	}
	stats, err := ParseStats(ctx, raw)
	if err != nil {
		return nil, NewBackendError(f.Name(), "ReadStats", 0, err, false)
	}
	return stats, nil
}

func (f *FsRepo) ListNodes(ctx context.Context) ([]NodeId, error) {
	entries, err := toolkit.ReadDir(ctx, f.Root)
	if err != nil {
		return nil, NewBackendError(f.Name(), "ListNodes", 0, err, false)
	}
	var ids []NodeId
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Only include directory names that parse as valid NodeId identifiers.
		if n, perr := ParseNode(e.Name()); perr == nil && n != nil && n.Valid() {
			ids = append(ids, *n)
		}
	}
	// sort ascending using NodeId.Compare for deterministic ordering
	for i := 0; i < len(ids); i++ {
		min := i
		for j := i + 1; j < len(ids); j++ {
			if ids[j].Compare(ids[min]) < 0 {
				min = j
			}
		}
		if min != i {
			ids[i], ids[min] = ids[min], ids[i]
		}
	}
	return ids, nil
}

// ListAssets implements Repository.
func (f *FsRepo) ListAssets(ctx context.Context, id NodeId, kind AssetKind) ([]string, error) {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := toolkit.Stat(ctx, nodeDir, false); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, fmt.Errorf("node %s does not exist: %w", nodeDir, ErrNotExist)
		}
		return nil, NewBackendError(f.Name(), "ListAssets", 0, statErr, false)
	}

	var dir string
	switch kind {
	case AssetKindImage:
		dir = filepath.Join(nodeDir, NodeImagesDir)
	case AssetKindItem:
		dir = filepath.Join(nodeDir, NodeAttachmentsDir)
	default:
		return nil, fmt.Errorf("unknown asset kind %q", kind)
	}

	entries, err := toolkit.ReadDir(ctx, dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, NewBackendError(f.Name(), "ListAssets", 0, err, false)
	}

	var names []string
	for _, e := range entries {
		if kind == AssetKindImage && e.Name() == ".meta" {
			continue
		}
		names = append(names, e.Name())
	}
	sortStrings(names)
	return names, nil
}

// WriteContent implements Repository.
func (f *FsRepo) WriteContent(ctx context.Context, id NodeId, data []byte) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	contentPath := filepath.Join(nodeDir, f.ContentFilename)

	// Create parent directory if it doesn't exist
	dir := filepath.Dir(contentPath)
	if err := toolkit.Mkdir(ctx, dir, 0o755, true); err != nil {
		return NewBackendError(f.Name(), "WriteContent", 0, err, false)
	}

	err := toolkit.AtomicWriteFile(ctx, contentPath, data, 0o644)
	if err != nil {
		return NewBackendError(f.Name(), "WriteContent", 0, err, false)
	}
	return nil
}

// WriteMeta implements Repository.
func (f *FsRepo) WriteMeta(ctx context.Context, id NodeId, data []byte) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	metaPath := filepath.Join(nodeDir, f.MetaFilename)

	// Create parent directory if it doesn't exist
	dir := filepath.Dir(metaPath)
	if err := toolkit.Mkdir(ctx, dir, 0o755, true); err != nil {
		return NewBackendError(f.Name(), "WriteMeta", 0, err, false)
	}

	err := toolkit.AtomicWriteFile(ctx, metaPath, data, 0o644)
	if err != nil {
		return NewBackendError(f.Name(), "WriteMeta", 0, err, false)
	}
	return nil
}

// WriteStats implements Repository.
func (f *FsRepo) WriteStats(ctx context.Context, id NodeId, stats *NodeStats) error {
	if stats == nil {
		stats = &NodeStats{}
	}

	raw, err := f.ReadMeta(ctx, id)
	if err != nil && !errors.Is(err, ErrNotExist) {
		return err
	}
	if errors.Is(err, ErrNotExist) {
		raw = nil
	}

	meta, perr := ParseMeta(ctx, raw)
	if perr != nil {
		return NewBackendError(f.Name(), "WriteStats", 0, perr, false)
	}
	return f.WriteMeta(ctx, id, []byte(meta.ToYAMLWithStats(stats)))
}

// WriteAsset implements Repository.
func (f *FsRepo) WriteAsset(ctx context.Context, id NodeId, kind AssetKind, name string, data []byte) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := toolkit.Stat(ctx, nodeDir, false); statErr != nil {
		if os.IsNotExist(statErr) {
			return ErrNotExist
		}
		return NewBackendError(f.Name(), "WriteAsset", 0, statErr, false)
	}

	var assetPath string
	switch kind {
	case AssetKindImage:
		assetPath = filepath.Join(nodeDir, NodeImagesDir, name)
	case AssetKindItem:
		assetPath = filepath.Join(nodeDir, NodeAttachmentsDir, name)
	default:
		return fmt.Errorf("unknown asset kind %q", kind)
	}

	// Create parent directory if it doesn't exist
	dir := filepath.Dir(assetPath)
	if err := toolkit.Mkdir(ctx, dir, 0o755, true); err != nil {
		return NewBackendError(f.Name(), "WriteAsset", 0, err, false)
	}

	err := toolkit.AtomicWriteFile(ctx, assetPath, data, 0o0644)
	if err != nil {
		return NewBackendError(f.Name(), "WriteAsset", 0, err, false)
	}

	return nil
}

// MoveNode implements Repository.
func (f *FsRepo) MoveNode(ctx context.Context, id NodeId, dst NodeId) error {
	src := filepath.Join(f.Root, id.Path())
	if _, statErr := toolkit.Stat(ctx, src, false); statErr != nil {
		if os.IsNotExist(statErr) {
			return ErrNotExist
		}
		return NewBackendError(f.Name(), "MoveNode", 0, statErr, false)
	}

	dstPath := filepath.Join(f.Root, dst.Path())
	if _, statErr := toolkit.Stat(ctx, dstPath, false); statErr == nil {
		return ErrDestinationExists
	} else if !os.IsNotExist(statErr) {
		return NewBackendError(f.Name(), "MoveNode", 0, statErr, false)
	}

	if err := toolkit.Rename(ctx, src, dstPath); err != nil {
		return NewBackendError(f.Name(), "MoveNode", 0, err, false)
	}
	return nil
}

// GetIndex implements Repository.
func (f *FsRepo) GetIndex(ctx context.Context, name string) ([]byte, error) {
	idxPath := filepath.Join(f.Root, "dex", name)
	b, err := toolkit.ReadFile(ctx, idxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotExist
		}
		return nil, NewBackendError(f.Name(), "GetIndex", 0, err, false)
	}
	// return a copy (ReadFile already returns a copy)
	return append([]byte(nil), b...), nil
}

func (f *FsRepo) ClearIndexes(ctx context.Context) error {
	dexDir := filepath.Join(f.Root, "dex")

	// If dex directory doesn't exist, nothing to clear.
	if _, statErr := toolkit.Stat(ctx, dexDir, false); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return NewBackendError(f.Name(), "ClearIndexes", 0, statErr, false)
	}

	entries, readErr := toolkit.ReadDir(ctx, dexDir)
	if readErr != nil {
		return NewBackendError(f.Name(), "ClearIndexes", 0, readErr, false)
	}

	for _, e := range entries {
		path := filepath.Join(dexDir, e.Name())
		if rmErr := toolkit.Remove(ctx, path, true); rmErr != nil {
			return NewBackendError(f.Name(), "ClearIndexes", 0, rmErr, false)
		}
	}

	return nil
}

// WriteIndex implements Repository.
func (f *FsRepo) WriteIndex(ctx context.Context, name string, data []byte) error {
	idxPath := filepath.Join(f.Root, "dex", name)
	err := toolkit.AtomicWriteFile(ctx, idxPath, data, 0o0644)
	if err != nil {
		return NewBackendError(f.Name(), "WriteIndex", 0, err, false)
	}
	return nil
}

// ListIndexes implements Repository.
func (f *FsRepo) ListIndexes(ctx context.Context) ([]string, error) {
	dexDir := filepath.Join(f.Root, "dex")
	entries, err := toolkit.ReadDir(ctx, dexDir)
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

// DeleteNode implements Repository.
func (f *FsRepo) DeleteNode(ctx context.Context, id NodeId) error {
	nodeDir := filepath.Join(f.Root, id.Path())
	if _, statErr := toolkit.Stat(ctx, nodeDir, false); statErr != nil {
		if os.IsNotExist(statErr) {
			return ErrNotExist
		}
		return NewBackendError(f.Name(), "DeleteNode", 0, statErr, false)
	}

	if err := toolkit.Remove(ctx, nodeDir, true); err != nil {
		return NewBackendError(f.Name(), "DeleteNode", 0, err, false)
	}
	return nil
}

// DeleteAsset implements Repository.
func (f *FsRepo) DeleteAsset(ctx context.Context, id NodeId, kind AssetKind, name string) error {
	nodeDir := filepath.Join(f.Root, id.Path())

	// Ensure node exists
	if _, statErr := toolkit.Stat(ctx, nodeDir, false); statErr != nil {
		if os.IsNotExist(statErr) {
			return ErrNotExist
		}
		return NewBackendError(f.Name(), "DeleteAsset", 0, statErr, false)
	}

	switch kind {
	case AssetKindImage:
		imagesDir := filepath.Join(nodeDir, NodeImagesDir)
		imagePath := filepath.Join(imagesDir, name)
		if _, statErr := toolkit.Stat(ctx, imagePath, false); statErr != nil {
			if os.IsNotExist(statErr) {
				return ErrNotExist
			}
			return NewBackendError(f.Name(), "DeleteAsset", 0, statErr, false)
		}
		if err := toolkit.Remove(ctx, imagePath, true); err != nil {
			return NewBackendError(f.Name(), "DeleteAsset", 0, err, false)
		}
		metaPath := filepath.Join(imagesDir, ".meta", name+".json")
		_ = toolkit.Remove(ctx, metaPath, false)
		thumbPath := filepath.Join(imagesDir, "thumbs", name)
		_ = toolkit.Remove(ctx, thumbPath, false)
		return nil
	case AssetKindItem:
		itemPath := filepath.Join(nodeDir, NodeAttachmentsDir, name)
		if _, statErr := toolkit.Stat(ctx, itemPath, false); statErr != nil {
			if os.IsNotExist(statErr) {
				return ErrNotExist
			}
			return NewBackendError(f.Name(), "DeleteAsset", 0, statErr, false)
		}
		if err := toolkit.Remove(ctx, itemPath, true); err != nil {
			return NewBackendError(f.Name(), "DeleteAsset", 0, err, false)
		}
		return nil
	default:
		return fmt.Errorf("unknown asset kind %q", kind)
	}
}

// Compatibility wrappers for pre-assets API callers.
func (f *FsRepo) ListItems(ctx context.Context, id NodeId) ([]string, error) {
	return f.ListAssets(ctx, id, AssetKindItem)
}

func (f *FsRepo) ListImages(ctx context.Context, id NodeId) ([]string, error) {
	return f.ListAssets(ctx, id, AssetKindImage)
}

func (f *FsRepo) UploadImage(ctx context.Context, id NodeId, name string, data []byte) error {
	return f.WriteAsset(ctx, id, AssetKindImage, name, data)
}

func (f *FsRepo) UploadItem(ctx context.Context, id NodeId, name string, data []byte) error {
	return f.WriteAsset(ctx, id, AssetKindItem, name, data)
}

func (f *FsRepo) DeleteImage(ctx context.Context, id NodeId, name string) error {
	return f.DeleteAsset(ctx, id, AssetKindImage, name)
}

func (f *FsRepo) DeleteItem(ctx context.Context, id NodeId, name string) error {
	return f.DeleteAsset(ctx, id, AssetKindItem, name)
}

// ReadConfig implements Repository.
func (f *FsRepo) ReadConfig(ctx context.Context) (*KegConfig, error) {
	candidates := []string{"keg", "keg.yaml", "keg.yml"}
	for _, c := range candidates {
		p := filepath.Join(f.Root, c)
		if _, err := toolkit.Stat(ctx, p, false); err == nil {
			b, rerr := toolkit.ReadFile(ctx, p)
			if rerr != nil {
				return nil, NewBackendError(f.Name(), "ReadConfig", 0, rerr, false)
			}
			cfg, perr := ParseKegConfig(b)
			if perr != nil {
				return nil, NewBackendError(f.Name(), "ReadConfig", 0, perr, false)
			}
			return cfg, nil
		}
	}
	return nil, ErrNotExist
}

// WriteConfig implements Repository.
func (f *FsRepo) WriteConfig(ctx context.Context, config *KegConfig) error {
	// marshal to YAML
	out, err := yaml.Marshal(config)
	if err != nil {
		return NewBackendError(f.Name(), "WriteConfig", 0, err, false)
	}
	target := filepath.Join(f.Root, "keg")

	// Create parent directory if it doesn't exist
	dir := filepath.Dir(target)
	if err := toolkit.Mkdir(ctx, dir, 0o755, true); err != nil {
		return NewBackendError(f.Name(), "WriteConfig", 0, err, false)
	}

	err = toolkit.AtomicWriteFile(ctx, target, out, 0o0644)
	if err != nil {
		return NewBackendError(f.Name(), "WriteConfig", 0, err, false)
	}
	return nil
}

var _ Repository = (*FsRepo)(nil)

// ----------------- small helpers -----------------

func sortStrings(ss []string) {
	if len(ss) <= 1 {
		return
	}
	for i := range ss {
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
