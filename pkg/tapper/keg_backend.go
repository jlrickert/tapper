package tapper

import (
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// KegLocation renders where a remotely hosted keg lives for user-facing output
// after `tap keg create`: "on <hubURL>", or "" when no concrete location is
// known. Unlike KegBackendLabel it intentionally reveals the URL so a
// fresh create says exactly where the keg landed.
func KegLocation(target *keg.Target) string {
	if target == nil {
		return ""
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
// "what kind of keg is this" without leaking the remote URL or other
// location-revealing details.
//
// Mapping by scheme:
//
//   - hub:          "keg:@<namespace>/<kegName>"
//   - http(s):      "http" or "https"
//   - other/unknown: the scheme string, or "" when target is nil
//
// The hub label is the canonical keg reference (Target.String): the real "keg"
// scheme with the hub resolved from the namespace, never encoded in the string.
func KegBackendLabel(target *keg.Target) string {
	if target == nil {
		return ""
	}
	switch target.Scheme() {
	case keg.SchemeAlias:
		// A keg reference renders with the real "keg" scheme; the hub is
		// resolution metadata, not part of the reference. Mirror Target.String().
		return target.String()
	case keg.SchemeHTTP:
		return "http"
	case keg.SchemeHTTPs:
		return "https"
	default:
		return target.Scheme()
	}
}
