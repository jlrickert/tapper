// Package schemasfs embeds the published JSON Schemas so they ship inside the
// tap binary and can be materialized onto disk at runtime.
//
// The declaration lives here instead of pkg/schemas because the //go:embed
// directive can only reach files rooted at the declaring package's directory,
// and the schemas live at the repository root under schemas/. pkg/schemas
// re-exports FS and owns everything else, so consumers see a single import.
// Same arrangement as integrations/embed.go.
package schemasfs

import "embed"

// FS is the embedded tree rooted at the schemas/ directory. Entries are the
// bare file names, e.g. "tap-config.json".
//
//go:embed *.json
var FS embed.FS
