package tapper

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type CreateOptions struct {
	KegTargetOptions

	Title  string
	Lead   string
	Tags   []string
	Attrs  map[string]string
	Stream *toolkit.Stream
}

func (t *Tap) Create(ctx context.Context, opts CreateOptions) (keg.NodeId, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to determine default keg: %w", err)
	}

	if opts.Stream != nil && opts.Stream.IsPiped {
		b, _ := io.ReadAll(opts.Stream.In)
		node, createErr := t.createNodeFromRaw(ctx, k, b, opts)
		if createErr != nil {
			return keg.NodeId{}, createErr
		}
		return node, nil
	}

	if shouldUseLiveEditorOnCreate(opts) {
		nextID, peekErr := k.Next(ctx)
		if peekErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to peek next node id: %w", peekErr)
		}
		initialRaw := buildCreateEditorInitialRaw(ctx, t.Runtime, opts, nextID)
		tempPath, pathErr := newEditorTempFilePath(t.Runtime, "tap-create-", ".md")
		if pathErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to create temp file path: %w", pathErr)
		}
		if writeErr := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); writeErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to write temp create file: %w", writeErr)
		}
		defer func() {
			_ = t.Runtime.Remove(tempPath, false)
		}()

		var (
			created   bool
			createdID keg.NodeId
		)
		if editErr := editWithLiveSaves(ctx, t.Runtime, tempPath, func(editedRaw []byte) error {
			if !created {
				id, err := t.createNodeFromRaw(ctx, k, editedRaw, opts)
				if err != nil {
					return err
				}
				createdID = id
				created = true
				return nil
			}
			return t.applyEditedNodeRaw(ctx, k, createdID, editedRaw)
		}); editErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to create node: %w", editErr)
		}
		if created {
			return createdID, nil
		}

		finalRaw, readErr := t.Runtime.ReadFile(tempPath)
		if readErr != nil {
			return keg.NodeId{}, fmt.Errorf("unable to read temp create file: %w", readErr)
		}
		node, createErr := t.createNodeFromRaw(ctx, k, finalRaw, opts)
		if createErr != nil {
			return keg.NodeId{}, createErr
		}
		return node, nil
	}

	attrs := createAttrsFromStrings(opts.Attrs)
	node, err := k.Create(ctx, &keg.CreateOptions{
		Title: opts.Title,
		Lead:  opts.Lead,
		Tags:  opts.Tags,
		Attrs: attrs,
	})
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to create node: %w", err)
	}
	return node, nil
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
	if strings.TrimSpace(opts.Title) != "" || strings.TrimSpace(opts.Lead) != "" {
		return false
	}
	if len(opts.Tags) > 0 || len(opts.Attrs) > 0 {
		return false
	}
	return true
}

func buildCreateEditorInitialRaw(ctx context.Context, rt *toolkit.Runtime, opts CreateOptions, nextID keg.NodeId) []byte {
	meta := keg.NewMeta(ctx, rt.Clock().Now())
	if len(opts.Tags) > 0 {
		meta.SetTags(opts.Tags)
	}
	if len(opts.Attrs) > 0 {
		meta.SetAttrs(ctx, createAttrsFromStrings(opts.Attrs))
	}

	var body strings.Builder
	if strings.TrimSpace(opts.Title) != "" {
		body.WriteString(fmt.Sprintf("# %s\n", opts.Title))
	} else {
		body.WriteString(fmt.Sprintf("# %s\n", nextID.Path()))
	}
	if strings.TrimSpace(opts.Lead) != "" {
		body.WriteString("\n")
		body.WriteString(opts.Lead)
		body.WriteString("\n")
	}

	return composeEditNodeFile([]byte(meta.ToYAML()), []byte(body.String()))
}

func (t *Tap) createNodeFromRaw(ctx context.Context, k *keg.Keg, raw []byte, defaults CreateOptions) (keg.NodeId, error) {
	createOpts := &keg.CreateOptions{
		Title: defaults.Title,
		Lead:  defaults.Lead,
		Tags:  defaults.Tags,
		Attrs: createAttrsFromStrings(defaults.Attrs),
	}

	hasFrontmatter := false
	var frontmatterRaw []byte
	if len(raw) > 0 {
		var err error
		hasFrontmatter, frontmatterRaw, raw, err = splitEditNodeFile(raw)
		if err != nil {
			return keg.NodeId{}, err
		}
		createOpts.Body = raw
	}

	node, err := k.Create(ctx, createOpts)
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to create node: %w", err)
	}

	if hasFrontmatter {
		metaNode, err := keg.ParseMeta(ctx, frontmatterRaw)
		if err != nil {
			return keg.NodeId{}, fmt.Errorf("invalid frontmatter metadata: %w", err)
		}
		if err := k.SetMeta(ctx, node, metaNode); err != nil {
			return keg.NodeId{}, fmt.Errorf("unable to save node metadata: %w", err)
		}
	}

	return node, nil
}
