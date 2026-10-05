package keg

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// DocumentHash returns the precondition token for a whole-document keg
// resource — a schema definition or the settings file. A caller echoes the
// token it read back on its next write, so a write is rejected when the
// document changed in between rather than silently overwriting the change.
//
// Nodes have their own token (NodeView.Hash) derived from content and
// metadata together; this is the equivalent for resources that are a single
// opaque YAML document.
//
// SHA-256 is deliberately fixed here rather than supplied by Runtime. These
// tokens cross local, browser, REST, and remote-client boundaries, so the same
// document must have the same token in every process.
func DocumentHash(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func requireExpectedHash(resource, expected string) error {
	if expected != "" {
		return nil
	}
	return fmt.Errorf("%s: %w", resource, ErrPreconditionRequired)
}

func checkExpectedHash(resource, expected, current string, content []byte) error {
	if err := requireExpectedHash(resource, expected); err != nil {
		return err
	}
	if expected == current {
		return nil
	}
	return &PreconditionConflictError{
		Resource:       resource,
		CurrentHash:    current,
		CurrentContent: append([]byte(nil), content...),
	}
}

func nodeRecoveryContent(view *NodeView) []byte {
	if view == nil || len(view.Meta) == 0 {
		if view == nil {
			return nil
		}
		return append([]byte(nil), view.Content...)
	}
	out := make([]byte, 0, len(view.Meta)+len(view.Content)+10)
	out = append(out, "---\n"...)
	out = append(out, view.Meta...)
	if out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, "---\n"...)
	out = append(out, view.Content...)
	return out
}

// checkNodeUpdatePrecondition guards a node update against the state it was
// read at.
//
// When the caller supplies a per-part token (ExpectedContentHash or
// ExpectedMetaHash), each part being written is checked against its own token
// and the combined ExpectedHash is ignored: a part written without its token
// is ErrPreconditionRequired, a stale token is a PreconditionConflictError.
// Otherwise the combined ExpectedHash is checked exactly as before, which is
// what callers that hold NodeView.Hash rely on.
func checkNodeUpdatePrecondition(ctx context.Context, rt *toolkit.Runtime, opts NodeUpdateOptions, existing *NodeView) error {
	resource := "node " + opts.ID.Path()
	currentContentHash, currentMetaHash := existing.ContentHash(), existing.MetaHash()
	if currentContentHash == "" {
		currentContentHash = nodeContentHash(rt, existing.Content)
	}
	if currentMetaHash == "" {
		currentMetaHash = nodeRawMetaHash(ctx, rt, existing.Meta)
	}
	conflict := func() error {
		return &PreconditionConflictError{
			Resource:           resource,
			CurrentHash:        existing.Hash(),
			CurrentContentHash: currentContentHash,
			CurrentMetaHash:    currentMetaHash,
			CurrentContent:     nodeRecoveryContent(existing),
		}
	}
	if opts.ExpectedContentHash == "" && opts.ExpectedMetaHash == "" {
		err := checkExpectedHash(resource, opts.ExpectedHash, existing.Hash(), nodeRecoveryContent(existing))
		var pc *PreconditionConflictError
		if errors.As(err, &pc) {
			pc.CurrentContentHash = currentContentHash
			pc.CurrentMetaHash = currentMetaHash
		}
		return err
	}
	if opts.HasContent {
		if opts.ExpectedContentHash == "" {
			return fmt.Errorf("%s content (expected content hash): %w", resource, ErrPreconditionRequired)
		}
		if opts.ExpectedContentHash != currentContentHash {
			return conflict()
		}
	}
	if opts.HasMeta {
		if opts.ExpectedMetaHash == "" {
			return fmt.Errorf("%s metadata (expected meta hash): %w", resource, ErrPreconditionRequired)
		}
		if opts.ExpectedMetaHash != currentMetaHash {
			return conflict()
		}
	}
	return nil
}
