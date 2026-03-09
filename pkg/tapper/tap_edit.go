package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

type EditOptions struct {
	// NodeID is the node identifier to edit (e.g., "0", "42")
	NodeID string

	KegTargetOptions

	// Stream carries stdin piping information.
	Stream *toolkit.Stream
}

// MetaOptions configures behavior for Tap.Meta.
type MetaOptions struct {
	// NodeID is the node identifier to inspect (e.g., "0", "42")
	NodeID string

	KegTargetOptions

	// Edit opens metadata in the editor.
	Edit bool

	// Stream carries stdin piping information.
	Stream *toolkit.Stream
}

// Cat reads and displays node(s) content with metadata as frontmatter.
//

func (t *Tap) Meta(ctx context.Context, opts MetaOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	id := keg.NodeId{ID: node.ID, Code: node.Code}
	exists, err := k.Repo.HasNode(ctx, id)
	if err != nil {
		return "", fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("node %s not found", id.Path())
	}

	if opts.Edit {
		if err := t.editMeta(ctx, k, id, opts.Stream); err != nil {
			return "", err
		}
		return "", nil
	}

	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return "", fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			metaNode, parseErr := keg.ParseMeta(ctx, pipedRaw)
			if parseErr != nil {
				return "", fmt.Errorf("metadata from stdin is invalid: %w", parseErr)
			}
			if err := k.SetMeta(ctx, id, metaNode); err != nil {
				return "", fmt.Errorf("unable to save node metadata: %w", err)
			}
			return "", nil
		}
	}

	raw, err := k.Repo.ReadMeta(ctx, id)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return "", fmt.Errorf("unable to read node metadata: %w", err)
	}
	metaNode, err := keg.ParseMeta(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("node metadata is invalid: %w", err)
	}
	return strings.TrimRight(metaNode.ToYAML(), "\n"), nil
}

// Edit opens a node in an editor. When the repository is an FsRepo, the real
// README.md is opened directly for in-place editing. Otherwise a temporary
// file with frontmatter is used and changes are split back on save.
//
// The temp file format (non-FsRepo) is:
//
//	---
//	<meta yaml>
//	---
//	<markdown body>
//
// If stdin is piped, it seeds the content directly without opening an editor.
func (t *Tap) Edit(ctx context.Context, opts EditOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	id := keg.NodeId{ID: node.ID, Code: node.Code}
	exists, err := k.Repo.HasNode(ctx, id)
	if err != nil {
		return fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return fmt.Errorf("node %s not found", id.Path())
	}

	// Handle piped stdin: apply content directly without opening an editor.
	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			return t.applyEditedNodeRaw(ctx, k, id, pipedRaw)
		}
	}

	return t.editWithTempFile(ctx, k, id)
}

// editWithTempFile is the editing flow that composes frontmatter + body into
// a temporary file. When the repository is an FsRepo, a reverse sync watcher
// monitors the real node files (README.md, meta.yaml) and re-composes the
// temp file when external changes are detected, so the editor can reload
// with :e! to pick up changes from other tap instances.
func (t *Tap) editWithTempFile(ctx context.Context, k *keg.Keg, id keg.NodeId) error {
	content, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return fmt.Errorf("unable to read node content: %w", err)
	}
	meta, err := k.Repo.ReadMeta(ctx, id)
	if err != nil {
		if !errors.Is(err, keg.ErrNotExist) {
			return fmt.Errorf("unable to read node metadata: %w", err)
		}
		meta = nil
	}

	initialRaw := composeEditNodeFile(meta, content)

	tempPath, err := newEditorTempFilePath(t.Runtime, "tap-edit-"+id.String()+"-", ".md")
	if err != nil {
		return fmt.Errorf("unable to create temp file path: %w", err)
	}
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return fmt.Errorf("unable to write temp edit file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	// Resolve the real temp file path for os.WriteFile in the sync goroutine.
	resolvedTempPath := tempPath
	if rp, resolveErr := t.Runtime.ResolvePath(tempPath, false); resolveErr == nil {
		resolvedTempPath = rp
	}
	if jail := strings.TrimSpace(t.Runtime.GetJail()); jail != "" {
		trimmed := strings.TrimPrefix(resolvedTempPath, string(filepath.Separator))
		resolvedTempPath = filepath.Join(jail, trimmed)
	}

	editCtx, editCancel := context.WithCancel(ctx)
	defer editCancel()

	// Start reverse sync: watch real node files and update temp file.
	if fsRepo, ok := k.Repo.(*keg.FsRepo); ok {
		w, watchErr := fsRepo.WatchEvents()
		if watchErr == nil {
			ch, chErr := w.Watch(editCtx, id)
			if chErr == nil {
				go reverseSync(editCtx, k, id, resolvedTempPath, ch)
			} else {
				_ = w.Close()
			}
			// Close watcher when edit context is done.
			go func() {
				<-editCtx.Done()
				_ = w.Close()
			}()
		}
	}

	if err := editWithLiveSaves(editCtx, t.Runtime, tempPath, func(editedRaw []byte) error {
		return t.applyEditedNodeRaw(ctx, k, id, editedRaw)
	}); err != nil {
		return fmt.Errorf("unable to edit node: %w", err)
	}
	return nil
}

// reverseSync watches for real node file changes and re-composes the temp
// file so the editor can reload with :e! to pick up external modifications.
// It compares the composed content against the current temp file to avoid
// writing when our own saves caused the real file change.
func reverseSync(
	ctx context.Context,
	k *keg.Keg,
	id keg.NodeId,
	tempPath string,
	ch <-chan keg.NodeEvent,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// Only sync on content or meta modifications, not access events.
			if ev.Kind == keg.NodeEventAccessed {
				continue
			}
			if ev.Field != "content" && ev.Field != "meta" {
				continue
			}
			// Re-read the real files and recompose.
			content, err := k.Repo.ReadContent(ctx, id)
			if err != nil {
				continue
			}
			meta, err := k.Repo.ReadMeta(ctx, id)
			if err != nil && !errors.Is(err, keg.ErrNotExist) {
				continue
			}
			composed := composeEditNodeFile(meta, content)

			// Compare with what the temp file already contains. If
			// the content matches, the change was caused by our own
			// save — skip the write to avoid triggering the editor's
			// "file changed" warning.
			if current, readErr := os.ReadFile(tempPath); readErr == nil {
				if bytes.Equal(current, composed) {
					continue
				}
			}

			if writeErr := os.WriteFile(tempPath, composed, 0o600); writeErr != nil {
				_, _ = fmt.Fprintf(os.Stderr,
					"Warning: failed to sync external change to temp file: %v\n", writeErr)
				continue
			}
			_, _ = fmt.Fprintf(os.Stderr,
				"Info: node %s updated externally (%s) — reload with :e! to see changes\n",
				id.Path(), ev.Field)
		}
	}
}

func (t *Tap) applyEditedNodeRaw(ctx context.Context, k *keg.Keg, id keg.NodeId, editedRaw []byte) error {
	hasFrontmatter, frontmatterRaw, bodyRaw, err := splitEditNodeFile(editedRaw)
	if err != nil {
		return err
	}

	if hasFrontmatter {
		metaNode, parseErr := keg.ParseMeta(ctx, frontmatterRaw)
		if parseErr != nil {
			return fmt.Errorf("invalid frontmatter metadata: %w", parseErr)
		}
		if err := k.SetMeta(ctx, id, metaNode); err != nil {
			return fmt.Errorf("unable to save node metadata: %w", err)
		}
	}

	if err := k.SetContent(ctx, id, bodyRaw); err != nil {
		return fmt.Errorf("unable to save node content: %w", err)
	}

	return nil
}

func composeEditNodeFile(meta []byte, content []byte) []byte {
	metaText := strings.TrimRight(string(meta), "\n")
	return []byte(fmt.Sprintf("---\n%s\n---\n%s", metaText, string(content)))
}

func splitEditNodeFile(raw []byte) (bool, []byte, []byte, error) {
	if len(raw) == 0 {
		return false, nil, raw, nil
	}

	trimmed := raw
	if bytes.HasPrefix(trimmed, []byte("\xef\xbb\xbf")) {
		trimmed = trimmed[3:]
	}

	var rest []byte
	switch {
	case bytes.HasPrefix(trimmed, []byte("---\n")):
		rest = trimmed[len([]byte("---\n")):]
	case bytes.HasPrefix(trimmed, []byte("---\r\n")):
		rest = trimmed[len([]byte("---\r\n")):]
	default:
		return false, nil, raw, nil
	}

	choices := [][]byte{
		[]byte("\n---\r\n"),
		[]byte("\n---\n"),
		[]byte("\r\n---\n"),
		[]byte("\n---"),
	}
	endIdx := -1
	endLen := 0
	for _, marker := range choices {
		if idx := bytes.Index(rest, marker); idx >= 0 {
			endIdx = idx
			endLen = len(marker)
			break
		}
	}
	if endIdx < 0 {
		return false, nil, nil, fmt.Errorf("invalid frontmatter: missing closing delimiter")
	}

	frontmatter := bytes.TrimSpace(rest[:endIdx])
	if len(frontmatter) > 0 {
		var check map[string]any
		if err := yaml.Unmarshal(frontmatter, &check); err != nil {
			return false, nil, nil, fmt.Errorf("invalid frontmatter yaml: %w", err)
		}
	}

	body := bytes.TrimLeft(rest[endIdx+endLen:], "\r\n")
	return true, frontmatter, body, nil
}

func (t *Tap) editMeta(ctx context.Context, k *keg.Keg, id keg.NodeId, stream *toolkit.Stream) error {
	raw, err := k.Repo.ReadMeta(ctx, id)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return fmt.Errorf("unable to read node metadata: %w", err)
	}

	metaNode, err := keg.ParseMeta(ctx, raw)
	if err != nil {
		return fmt.Errorf("node metadata is invalid: %w", err)
	}
	initialRaw := []byte(metaNode.ToYAML())
	if stream != nil && stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			initialRaw = pipedRaw
		}
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, "tap-meta-"+id.String()+"-", ".yaml")
	if err != nil {
		return fmt.Errorf("unable to create temp file path: %w", err)
	}
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return fmt.Errorf("unable to write temp metadata file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	if err := editWithLiveSaves(ctx, t.Runtime, tempPath, func(editedRaw []byte) error {
		updatedMeta, err := keg.ParseMeta(ctx, editedRaw)
		if err != nil {
			return fmt.Errorf("node metadata is invalid after editing: %w", err)
		}
		if err := k.SetMeta(ctx, id, updatedMeta); err != nil {
			return fmt.Errorf("unable to save node metadata: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("unable to edit node metadata: %w", err)
	}
	return nil
}
