// Package renderdata embeds canonical host-specific source bytes that the
// render-integrations command overlays onto the canonical content tree.
//
// This package exists to keep these host-specific source bytes (today: the
// host plugin hooks) out of cmd/tap
// binaries. Only cmd/render-integrations imports this package; the rendered
// output of those bytes ships in the user binaries via integrations/embed.go,
// not via this embed FS.
//
// The "all:" prefix is required so dot-directories under these roots (none
// today, but reserved) are included; without it Go's embed machinery silently
// skips names starting with "." or "_".
package renderdata

import "embed"

// FS exposes the embedded canonical-source tree. Paths inside the FS are
// relative to this package's directory — for example
// "codex/hooks/hooks.json" (the baseline orientation hook) or
// "guard/hooks.json" (the PreToolUse guard both marketplace hosts ship in the
// separate tapper-guard plugin).
//
//go:embed all:codex all:developer all:guard
var FS embed.FS
