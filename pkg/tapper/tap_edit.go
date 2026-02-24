package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// Edit opens a node in an editor using a temporary markdown file.
//
// The temp file format is:
//
//	---
//	<meta yaml>
//	---
//	<markdown body>
//
// If stdin is piped, it seeds the temp file content. On save, frontmatter is
// written to meta.yaml and the body is written to the node content file.
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

	originalRaw := composeEditNodeFile(meta, content)
	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			return t.applyEditedNodeRaw(ctx, k, id, pipedRaw)
		}
	}
	initialRaw := originalRaw

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

	if err := editWithLiveSaves(ctx, t.Runtime, tempPath, func(editedRaw []byte) error {
		return t.applyEditedNodeRaw(ctx, k, id, editedRaw)
	}); err != nil {
		return fmt.Errorf("unable to edit node: %w", err)
	}
	return nil
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
