package docs

import "embed"

//go:embed *.md **/*.md
var Content embed.FS
