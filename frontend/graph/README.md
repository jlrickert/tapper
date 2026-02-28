# Graph Frontend

Build the bundled graph renderer with:

```bash
bun build frontend/graph/src/main.ts --bundle --minify --outfile pkg/cli/assets/graph.bundle.js
```

The generated bundle is embedded by `pkg/cli/assets.go`.
