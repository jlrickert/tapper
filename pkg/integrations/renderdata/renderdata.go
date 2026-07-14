// Package renderdata embeds canonical host-specific source bytes that the
// render-integrations command overlays onto the canonical content tree.
//
// This package exists to keep these host-specific source bytes (today: the
// host plugin hooks) out of the cmd/tap and cmd/keg
// binaries. Only cmd/render-integrations imports this package; the rendered
// output of those bytes ships in the user binaries via integrations/embed.go,
// not via this embed FS.
//
// The "all:" prefix is required so dot-directories under claude/ (none today,
// but reserved) are included; without it Go's embed machinery silently skips
// names starting with "." or "_".
package renderdata

import "embed"

// FS exposes the embedded canonical-source tree. Paths inside the FS are
// relative to this package's directory — for example
// "claude/hooks/block-tap-cli.py" or "codex/hooks/hooks.json".
//
//go:embed all:claude all:codex all:developer
var FS embed.FS
