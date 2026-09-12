package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type CreateOptions struct {
	KegTargetOptions

	Schema string
	Stream *toolkit.Stream
}

// ErrNoCreateContent is returned when there is nothing to build a node from:
// no piped stdin and no terminal to open an editor on. A node's title is its
// content's H1 and there is no other source, so an empty create has nothing to
// title the node with. Creating one anyway is what produced the opaque
// "node title is required" rejection from the hub.
var ErrNoCreateContent = errors.New("no content to create a node from: pipe the node on stdin, or run in a terminal to open an editor")

func (t *Tap) Create(ctx context.Context, opts CreateOptions) (keg.NodeId, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to determine default keg: %w", err)
	}
	ctx = keg.WithDefaultValidationActor(ctx, keg.ValidationActorHuman)

	// An attached but empty pipe is not content. tap edit and tap config edit
	// both read the pipe and fall through to the editor when nothing came
	// through it; create used to branch on the attachment alone, so
	// `tap create < /dev/null` on a terminal tried to build a node out of
	// nothing instead of opening an editor — and its own help already promised
	// the opposite ("if stdin is piped with non-empty content").
	var piped []byte
	if opts.Stream != nil && opts.Stream.IsPiped {
		b, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to read piped input: %w", readErr)
		}
		piped = b
	}
	if len(bytes.TrimSpace(piped)) > 0 {
		node, _, createErr := t.createNodeFromRaw(ctx, k, piped, opts)
		if createErr != nil {
			return keg.NodeId{}, createErr
		}
		t.warnSchemaIssues(ctx, k, node, opts.Stream)
		return node, nil
	}

	if shouldUseLiveEditorOnCreate(opts) {
		return t.createWithEditor(ctx, k, opts)
	}

	return keg.NodeId{}, ErrNoCreateContent
}

// createWithEditor opens an editor on a scaffold buffer that has no node behind
// it, and creates the node from the first save.
//
// The node deliberately does not exist when the editor opens. Creating an empty
// scaffold first — which is what this used to do — cannot work against a hub:
// its POST /nodes refuses a node with no title, so the create failed before the
// editor ever opened and the user was told to supply a title they had no way to
// type. Creating from the buffer also means abandoning the editor leaves no
// empty node behind and burns no node id.
//
// Nothing here calls Repo.Next: the buffer is a temp file, and the id it is
// named after is only cosmetic. The double-allocation problem that motivated
// the scaffold-first order therefore cannot recur.
func (t *Tap) createWithEditor(ctx context.Context, k keg.Keg, opts CreateOptions) (keg.NodeId, error) {
	initialRaw, err := composeEditNodeFileWithSchema(ctx, nil, nil, opts.Schema)
	if err != nil {
		return keg.NodeId{}, err
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, editorTempFilePrefix(k, keg.NodeId{}, "create"), ".md")
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to create temp file path: %w", err)
	}
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to write temp create file: %w", err)
	}

	var (
		created  keg.NodeId
		hasNode  bool
		savedRaw []byte
		revision = &editRevision{}
	)

	// The first successful save creates the node; every later save updates it,
	// guarded by the hash the previous write returned — the same discipline
	// editWithTempFileLockedSchema applies to an existing node. A save that
	// fails is reported as a warning and the session continues (see
	// liveSaveState.process), so a first save with no H1 tells the author in
	// the editor and they can add a title and save again.
	editErr := editWithLiveSaves(ctx, t.Runtime, tempPath, nil, func(editedRaw []byte) error {
		if !hasNode {
			id, hash, createErr := t.createNodeFromRaw(ctx, k, editedRaw, opts)
			if createErr != nil {
				return createErr
			}
			created, hasNode = id, true
			savedRaw = bytes.Clone(editedRaw)
			revision.set(hash)
			return nil
		}
		newHash, updateErr := t.applyEditedNodeRawExpectedSchema(ctx, k, created, editedRaw, "", revision.get(), opts.Schema)
		if updateErr == nil {
			revision.set(newHash)
			savedRaw = bytes.Clone(editedRaw)
		}
		return updateErr
	})

	// The buffer is only safe to discard once its content is in the keg.
	// Removing it after a failed create would throw away everything the author
	// wrote, so on that path it is kept and named instead.
	draftPending := true
	if hasNode {
		raw, readErr := t.Runtime.ReadFile(tempPath)
		draftPending = readErr != nil || !bytes.Equal(raw, savedRaw)
		if !draftPending {
			_ = t.Runtime.Remove(tempPath, false)
		}
	}

	if editErr != nil {
		if hasNode {
			if draftPending {
				return keg.NodeId{}, fmt.Errorf("unable to edit new node: %w; your draft is kept at %s", editErr, tempPath)
			}
			return keg.NodeId{}, fmt.Errorf("unable to edit new node: %w", editErr)
		}
		return keg.NodeId{}, fmt.Errorf("unable to create node: %w; your draft is kept at %s", editErr, tempPath)
	}
	if !hasNode {
		// The editor exited without ever writing the file. Nothing was created,
		// and the scaffold holds nothing worth keeping.
		_ = t.Runtime.Remove(tempPath, false)
		return keg.NodeId{}, errors.New("no node created: the editor exited without saving")
	}

	if draftPending {
		_, _ = fmt.Fprintf(t.Runtime.Stream().Err, "Warning: your draft is kept at %s; the node contains the last accepted save.\n", tempPath)
	}

	// Validate from storage rather than from the create result: the author has
	// since saved over it, possibly more than once.
	t.warnSchemaIssues(ctx, k, created, opts.Stream)
	return created, nil
}

// shouldUseLiveEditorOnCreate reports whether `tap create` should open an
// editor. Callers reach it only once piped content has been ruled out, so a
// terminal is the whole condition — an attached but empty pipe still gets the
// editor. --schema does not suppress it either: the schema is applied in the
// editor, not instead of it.
func shouldUseLiveEditorOnCreate(opts CreateOptions) bool {
	if opts.Stream == nil {
		return false
	}
	return opts.Stream.IsTTY
}

// createNodeFromRaw creates one node from an edit-file buffer, returning its id
// and the hash of the write. The hash lets a caller that keeps editing guard
// its next write; the piped path has no next write and ignores it.
func (t *Tap) createNodeFromRaw(ctx context.Context, k keg.Keg, raw []byte, defaults CreateOptions) (keg.NodeId, string, error) {
	node := keg.NodeCreate{Key: "node", Schema: defaults.Schema}

	if len(raw) > 0 {
		// The editor buffer presents metadata as frontmatter above the body
		// because that reads well to a human, but content and meta are
		// separate inputs to the keg, which rejects content opening with a
		// `---` block. Split the buffer into the two fields — exactly what tap
		// edit already does for UpdateNode — so create and edit accept the
		// same thing. A type declared up there still reaches schema selection,
		// now as metadata rather than as frontmatter.
		hasFrontmatter, frontmatterRaw, bodyRaw, err := splitEditNodeFile(raw)
		if err != nil {
			return keg.NodeId{}, "", err
		}
		if hasFrontmatter {
			if _, err := keg.ParseMeta(ctx, frontmatterRaw); err != nil {
				return keg.NodeId{}, "", fmt.Errorf("invalid frontmatter metadata: %w", err)
			}
			node.Meta = frontmatterRaw
		}
		node.Body = bodyRaw
	}

	// CreateNodes rather than Create: only its result carries the hash, and a
	// single create routes through the same batch endpoint either way.
	results, err := k.CreateNodes(ctx, []keg.NodeCreate{node})
	if err != nil {
		return keg.NodeId{}, "", fmt.Errorf("unable to create node: %w", err)
	}
	if len(results) == 0 {
		return keg.NodeId{}, "", errors.New("unable to create node: the keg returned no result")
	}

	return results[0].ID, results[0].Hash, nil
}
