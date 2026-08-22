package tapper

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

type CreateOptions struct {
	KegTargetOptions

	Schema string
	Title  string
	Lead   string
	Tags   []string
	Attrs  map[string]string
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
		attrs := createAttrsFromStrings(opts.Attrs)
		created, createErr := k.Create(ctx, &keg.CreateOptions{
			Schema: opts.Schema,
			Title:  opts.Title,
			Lead:   opts.Lead,
			Tags:   opts.Tags,
			Attrs:  attrs,
		})
		if createErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to create node: %w", createErr)
		}
		if editErr := t.editWithTempFileSchema(ctx, k, created.ID, opts.Schema); editErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to edit new node: %w", editErr)
		}
		t.warnSchemaValidation(created.Validation, created.ID, opts.Stream)
		return created.ID, nil
	}

	attrs := createAttrsFromStrings(opts.Attrs)
	created, err := k.Create(ctx, &keg.CreateOptions{
		Schema: opts.Schema,
		Title:  opts.Title,
		Lead:   opts.Lead,
		Tags:   opts.Tags,
		Attrs:  attrs,
	})
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to create node: %w", err)
	}
	t.warnSchemaValidation(created.Validation, created.ID, opts.Stream)
	return created.ID, nil
}

func createAttrsFromStrings(attrs map[string]string) map[string]any {
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

func shouldUseLiveEditorOnCreate(opts CreateOptions) bool {
	if opts.Stream == nil {
		return false
	}
	if opts.Stream.IsPiped || !opts.Stream.IsTTY {
		return false
	}
	if strings.TrimSpace(opts.Schema) != "" || strings.TrimSpace(opts.Title) != "" || strings.TrimSpace(opts.Lead) != "" {
		return false
	}
	if len(opts.Tags) > 0 || len(opts.Attrs) > 0 {
		return false
	}
	return true
}

func (t *Tap) createNodeFromRaw(ctx context.Context, k keg.Keg, raw []byte, defaults CreateOptions) (keg.NodeId, error) {
	createOpts := &keg.CreateOptions{
		Schema: defaults.Schema,
		Title:  defaults.Title,
		Lead:   defaults.Lead,
		Tags:   defaults.Tags,
		Attrs:  createAttrsFromStrings(defaults.Attrs),
	}

	hasFrontmatter := false
	var frontmatterRaw []byte
	if len(raw) > 0 {
		originalRaw := raw
		var err error
		hasFrontmatter, frontmatterRaw, _, err = splitEditNodeFile(raw)
		if err != nil {
			return keg.NodeId{}, err
		}
		createOpts.Body = originalRaw
	}
	if hasFrontmatter {
		var attrs map[string]any
		if err := yaml.Unmarshal(frontmatterRaw, &attrs); err != nil {
			return keg.NodeId{}, fmt.Errorf("invalid frontmatter metadata: %w", err)
		}
		// Keep frontmatter in Body so the Keg layer can detect a conflicting
		// type from --attrs or --schema before applying either source.
	}

	created, err := k.Create(ctx, createOpts)
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to create node: %w", err)
	}

	return created.ID, nil
}
