package tapper

import (
	"github.com/jlrickert/tapper/pkg/keg"
)

// KegBackendLabel returns a stable, path-free identifier for a keg target
// suitable for user-facing output. It is used by surfaces that must describe
// "what kind of keg is this" without leaking the underlying filesystem path,
// remote URL, or other location-revealing details.
//
// Mapping by scheme:
//
//   - file-backed:  "file-backed"
//   - hub:          "hub:<hub>/@<namespace>/<kegName>"
//   - http(s):      "http" or "https"
//   - in-memory:    "in-memory"
//   - other/unknown: the scheme string, or "" when target is nil
//
// The hub label re-applies the "@" sigil so the rendered string round-trips
// with the canonical hub shorthand the user originally typed. File-backed
// kegs intentionally collapse to a single token: the alias is the user-
// visible handle, and the path lives only behind `tap info`.
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
	case keg.SchemeHub:
		return target.Hub + ":@" + target.Namespace + "/" + target.KegName
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
