package tapper

import (
	"context"
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

func (t *Tap) Create(ctx context.Context, opts CreateOptions) (keg.NodeId, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to determine default keg: %w", err)
	}
	ctx = keg.WithDefaultValidationActor(ctx, keg.ValidationActorHuman)

	if opts.Stream != nil && opts.Stream.IsPiped {
		b, _ := io.ReadAll(opts.Stream.In)
		node, createErr := t.createNodeFromRaw(ctx, k, b, opts)
		if createErr != nil {
			return keg.NodeId{}, createErr
		}
		t.warnSchemaIssues(ctx, k, node, opts.Stream)
		return node, nil
	}

	if shouldUseLiveEditorOnCreate(opts) {
		// Create the node first (single allocation), then open the editor
		// on the already-persisted node. This avoids the double-allocation
		// bug where Next() was called for the editor scaffold and then
		// Create() called Next() again internally.
		//
		// The scaffold is deliberately created WITHOUT opts.Schema. An empty
		// node already typed `task` would fail that schema's required fields
		// and, on a strict keg, be rejected before the editor ever opened.
		// The schema instead rides editWithTempFileSchema, which prefills
		// `type:` in the editor frontmatter and enforces the schema on save —
		// where the user's content actually exists.
		created, createErr := k.Create(ctx, &keg.CreateOptions{})
		if createErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to create node: %w", createErr)
		}
		if editErr := t.editWithTempFileSchema(ctx, k, created.ID, opts.Schema); editErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to edit new node: %w", editErr)
		}
		// Re-validate from storage rather than reporting created.Validation,
		// which describes the empty pre-edit scaffold: for a schema with
		// required fields that result is always invalid, so reporting it warns
		// about a node the user already filled in. This also still warns when
		// the editor is closed without saving.
		t.warnSchemaIssues(ctx, k, created.ID, opts.Stream)
		return created.ID, nil
	}

	created, err := k.Create(ctx, &keg.CreateOptions{Schema: opts.Schema})
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to create node: %w", err)
	}
	t.warnSchemaValidation(created.Validation, created.ID, opts.Stream)
	return created.ID, nil
}

// shouldUseLiveEditorOnCreate reports whether `tap create` should open an
// editor: an interactive terminal with nothing piped in. --schema no longer
// suppresses it — the schema is applied in the editor, not instead of it.
func shouldUseLiveEditorOnCreate(opts CreateOptions) bool {
	if opts.Stream == nil {
		return false
	}
	return !opts.Stream.IsPiped && opts.Stream.IsTTY
}

func (t *Tap) createNodeFromRaw(ctx context.Context, k keg.Keg, raw []byte, defaults CreateOptions) (keg.NodeId, error) {
	createOpts := &keg.CreateOptions{Schema: defaults.Schema}

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
			return keg.NodeId{}, err
		}
		if hasFrontmatter {
			if _, err := keg.ParseMeta(ctx, frontmatterRaw); err != nil {
				return keg.NodeId{}, fmt.Errorf("invalid frontmatter metadata: %w", err)
			}
			createOpts.Meta = frontmatterRaw
		}
		createOpts.Body = bodyRaw
	}

	created, err := k.Create(ctx, createOpts)
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to create node: %w", err)
	}

	return created.ID, nil
}
