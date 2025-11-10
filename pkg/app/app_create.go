package app

import (
	"context"
	"fmt"
	"io"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

// CreateOptions configures behavior for Runner.Create.
type CreateOptions struct {
	Title  string
	Lead   string
	Tags   []string
	Attrs  map[string]string
	Stream *toolkit.Stream
}

// Create creates a new node in the project's default keg.
//
// It resolves the TapProject (via r.getProject), determines the project's
// default keg target, constructs a Keg service for that target, and delegates
// to keg.Keg.Create to allocate and persist the new node.
//
// Errors are wrapped with contextual messages to aid callers.
func (r *Runner) Create(ctx context.Context, opts CreateOptions) (keg.Node, error) {
	proj, err := r.getProject(ctx)
	if err != nil {
		return keg.Node{}, fmt.Errorf("unable to create node: %w", err)
	}

	target, err := proj.DefaultKeg(ctx)
	if err != nil {
		return keg.Node{}, fmt.Errorf("unable to determine default keg: %w", err)
	}
	if target == nil {
		return keg.Node{}, fmt.Errorf("no default keg configured: %w", keg.ErrInvalid)
	}

	k, err := keg.NewKegFromTarget(ctx, *target)
	if err != nil {
		return keg.Node{}, fmt.Errorf("unable to open keg: %w", err)
	}

	body := []byte{}
	attrs := make(map[string]any, len(opts.Attrs))
	if opts.Stream != nil && opts.Stream.IsPiped {
		b, _ := io.ReadAll(opts.Stream.In)
		body = b
	} else {
		// Convert map[string]string to map[string]any
		for k, v := range opts.Attrs {
			attrs[k] = v
		}
	}

	node, err := k.Create(ctx, &keg.KegCreateOptions{
		Title: opts.Title,
		Lead:  opts.Lead,
		Tags:  opts.Tags,
		Body:  body,
		Attrs: attrs,
	})
	if err != nil {
		return keg.Node{}, fmt.Errorf("unable to create node: %w", err)
	}

	return node, nil
}
