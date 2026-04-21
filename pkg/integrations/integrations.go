package integrations

import (
	"embed"

	integrationsfs "github.com/jlrickert/tapper/integrations"
)

// IntegrationsFS is the embedded canonical + rendered integration tree.
// The //go:embed directive lives in package integrationsfs at the
// repository-root integrations/ directory because Go's embed machinery
// can only reach files rooted at the declaring package; this package
// re-exports the FS so callers keep a single import path.
//
// Paths inside the FS are relative to the integrations/ directory:
// canonical content at "content/<file>" and adapter output at
// "rendered/<host>/...".
var IntegrationsFS embed.FS = integrationsfs.FS
