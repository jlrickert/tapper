package cli

import _ "embed"

// graphBundle is the compiled self-contained graph renderer.
//
//go:embed assets/graph.bundle.js
var graphBundle []byte
