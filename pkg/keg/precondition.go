package keg

import (
	"crypto/sha256"
	"fmt"
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
