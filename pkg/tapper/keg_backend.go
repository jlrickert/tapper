package tapper

import (
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// KegLocation renders where a keg lives for user-facing output after
// `tap keg create`: "at <path>" for a file-backed keg, "on <hubURL>" for a
// remote keg, or "" when no concrete location is known (in-memory / nil
// target). Unlike KegBackendLabel it intentionally reveals the path/URL so a
// fresh create says exactly where the keg landed.
func KegLocation(target *keg.Target) string {
	if target == nil {
		return ""
	}
	if p := strings.TrimSpace(target.File); p != "" {
		return "at " + p
	}
	if hub := strings.TrimSpace(target.HubURL); hub != "" {
		return "on " + hub
	}
	if u := strings.TrimSpace(target.Url); u != "" {
		return "on " + u
	}
	return ""
}

// KegBackendLabel returns a stable, path-free identifier for a keg target
// suitable for user-facing output. It is used by surfaces that must describe
// "what kind of keg is this" without leaking the underlying filesystem path,
// remote URL, or other location-revealing details.
//
// Mapping by scheme:
//
//   - file-backed:  "file-backed"
//   - hub:          "keg:@<namespace>/<kegName>"
//   - http(s):      "http" or "https"
//   - in-memory:    "in-memory"
//   - other/unknown: the scheme string, or "" when target is nil
//
// The hub label is the canonical keg reference (Target.String): the real "keg"
// scheme with the hub resolved from the namespace, never encoded in the string.
// File-backed kegs intentionally collapse to a single token: the alias is the
// user-visible handle, and the path lives only behind `tap info`.
func KegBackendLabel(target *keg.Target) string {
	if target == nil {
		return ""
	}
	// Memory targets do not surface through Scheme() because NewMemory
	// leaves every string field blank — Scheme() falls through to
	// SchemeFile in that case. Check the explicit Memory flag first so
	// in-memory kegs render correctly even before any persistence work.
	if target.Memory {
		return "in-memory"
	}
	switch target.Scheme() {
	case keg.SchemeFile:
		return "file-backed"
	case keg.SchemeAlias:
		// A keg reference renders with the real "keg" scheme; the hub is
		// resolution metadata, not part of the reference. Mirror Target.String().
		return target.String()
	case keg.SchemeMemory:
		return "in-memory"
	case keg.SchemeHTTP:
		return "http"
	case keg.SchemeHTTPs:
		return "https"
	default:
		return target.Scheme()
	}
}
