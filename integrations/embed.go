// Package integrationsfs embeds the canonical integration content and the
// adapter-rendered host trees so they ship inside the tap binary.
//
// The declaration lives here instead of pkg/integrations because the
// //go:embed directive can only reach files rooted at the declaring
// package's directory, and the canonical and rendered trees live at the
// repository root under integrations/. pkg/integrations re-exports FS as
// integrations.IntegrationsFS so consumers see a single import.
package integrationsfs

import "embed"

// FS is the embedded tree rooted at the integrations/ directory. Paths
// inside the FS are relative to this directory — canonical content appears
// at "content/<file>" and host-rendered outputs at "rendered/<host>/...".
//
// The "all:" prefix is required so dot-directories such as
// "rendered/claude/.claude-plugin" are included; without it Go's embed
// machinery silently skips names starting with "." or "_".
//
//go:embed all:content all:rendered
var FS embed.FS
