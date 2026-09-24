package schemas_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/schemas"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func newSandbox(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	return sandbox.NewSandbox(t, &sandbox.Options{
		Home: filepath.FromSlash("/home/testuser"),
		User: "testuser",
	})
}

func TestMaterialize(t *testing.T) {
	t.Parallel()

	t.Run("writes every embedded schema", func(t *testing.T) {
		t.Parallel()
		sb := newSandbox(t)
		rt := sb.Runtime()

		dir, err := schemas.Materialize(rt)
		require.NoError(t, err)

		want, err := schemas.Dir(rt)
		require.NoError(t, err)
		require.Equal(t, want, dir)

		for _, name := range schemas.Names() {
			embedded, err := schemas.Read(name)
			require.NoError(t, err)

			onDisk, err := rt.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err, "schema %s should have been written", name)
			require.Equal(t, embedded, onDisk, "schema %s should match the embedded copy", name)
		}
	})

	t.Run("is idempotent", func(t *testing.T) {
		t.Parallel()
		sb := newSandbox(t)
		rt := sb.Runtime()

		first, err := schemas.Materialize(rt)
		require.NoError(t, err)
		second, err := schemas.Materialize(rt)
		require.NoError(t, err)
		require.Equal(t, first, second)

		embedded, err := schemas.Read(schemas.TapConfig)
		require.NoError(t, err)
		onDisk, err := rt.ReadFile(filepath.Join(second, schemas.TapConfig))
		require.NoError(t, err)
		require.Equal(t, embedded, onDisk)
	})

	t.Run("restores a schema whose content drifted", func(t *testing.T) {
		t.Parallel()
		sb := newSandbox(t)
		rt := sb.Runtime()

		dir, err := schemas.Materialize(rt)
		require.NoError(t, err)

		// Simulate an older build's copy: same path, stale bytes. Comparing
		// content (not a version stamp) is what makes this recoverable.
		target := filepath.Join(dir, schemas.FlightManifest)
		require.NoError(t, rt.AtomicWriteFile(target, []byte(`{"stale": true}`), 0o644))

		_, err = schemas.Materialize(rt)
		require.NoError(t, err)

		embedded, err := schemas.Read(schemas.FlightManifest)
		require.NoError(t, err)
		onDisk, err := rt.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, embedded, onDisk)
	})
}

func TestModelineURI(t *testing.T) {
	t.Parallel()

	t.Run("points at the materialized copy", func(t *testing.T) {
		t.Parallel()
		sb := newSandbox(t)
		rt := sb.Runtime()

		dir, err := schemas.Dir(rt)
		require.NoError(t, err)

		uri := schemas.ModelineURI(rt, schemas.TapConfig)
		require.Equal(t, schemas.FileURI(filepath.Join(dir, schemas.TapConfig)), uri)

		// Resolving the modeline is the whole point — the file must be there.
		_, err = rt.ReadFile(filepath.Join(dir, schemas.TapConfig))
		require.NoError(t, err)
	})

	t.Run("falls back to the published URL when materialization fails", func(t *testing.T) {
		t.Parallel()
		sb := newSandbox(t)
		rt := sb.Runtime()

		// A plain file where the schema directory belongs: Mkdir cannot
		// proceed, so ModelineURI has to degrade instead of failing.
		dir, err := schemas.Dir(rt)
		require.NoError(t, err)
		require.NoError(t, rt.Mkdir(filepath.Dir(dir), 0o755, true))
		require.NoError(t, rt.AtomicWriteFile(dir, []byte("not a directory"), 0o644))

		_, err = schemas.Materialize(rt)
		require.Error(t, err)

		require.Equal(t, schemas.TapConfigURL, schemas.ModelineURI(rt, schemas.TapConfig))
	})
}

func TestModelineHelpers(t *testing.T) {
	t.Parallel()

	const modeline = schemas.ModelinePrefix + "file:///tmp/tap-config.json\n"

	t.Run("HasModeline only inspects the leading comment block", func(t *testing.T) {
		t.Parallel()
		require.True(t, schemas.HasModeline([]byte(schemas.ModelinePrefix+"x\ntitle: a\n")))
		require.True(t, schemas.HasModeline([]byte("# header\n"+schemas.ModelinePrefix+"x\ntitle: a\n")))
		require.False(t, schemas.HasModeline([]byte("title: a\n")))
		// Past the first content line it is data, not a directive.
		require.False(t, schemas.HasModeline([]byte("title: a\n"+schemas.ModelinePrefix+"x\n")))
	})

	t.Run("EnsureModeline prepends only when absent", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, []byte(modeline+"title: a\n"),
			schemas.EnsureModeline([]byte("title: a\n"), modeline))

		existing := []byte(schemas.ModelinePrefix + "https://example.test/s.json\ntitle: a\n")
		require.Equal(t, existing, schemas.EnsureModeline(existing, modeline))
	})

	t.Run("StripModeline removes only the directive line", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, []byte("title: a\n"),
			schemas.StripModeline([]byte(modeline+"title: a\n")))
		require.Equal(t, []byte("# header\ntitle: a\n"),
			schemas.StripModeline([]byte("# header\n"+modeline+"title: a\n")))
		// Nothing to strip leaves the bytes untouched.
		require.Equal(t, []byte("title: a\n"), schemas.StripModeline([]byte("title: a\n")))
		// Past the first content line it is data, not a directive.
		body := []byte("title: a\n" + schemas.ModelinePrefix + "x\n")
		require.Equal(t, body, schemas.StripModeline(body))
	})

	t.Run("StripModeline undoes ReplaceModeline", func(t *testing.T) {
		t.Parallel()
		body := []byte("kegv: \"2025-07\"\ntitle: a\n")
		require.Equal(t, body, schemas.StripModeline(schemas.ReplaceModeline(body, modeline)))
	})

	t.Run("ReplaceModeline swaps the URI in place", func(t *testing.T) {
		t.Parallel()
		in := []byte(schemas.ModelinePrefix + schemas.TapConfigURL + "\ntitle: a\n")
		require.Equal(t, []byte(modeline+"title: a\n"), schemas.ReplaceModeline(in, modeline))
	})

	t.Run("ReplaceModeline preserves comments above the modeline", func(t *testing.T) {
		t.Parallel()
		in := []byte("# header\n" + schemas.ModelinePrefix + schemas.TapConfigURL + "\ntitle: a\n")
		require.Equal(t, []byte("# header\n"+modeline+"title: a\n"), schemas.ReplaceModeline(in, modeline))
	})

	t.Run("ReplaceModeline prepends when there is nothing to replace", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, []byte(modeline+"title: a\n"),
			schemas.ReplaceModeline([]byte("title: a\n"), modeline))
	})
}

// TestTapConfigRelay keeps the tap-config schema in step with the relay block
// `tap config edit` validates against, including the enums the relay enforces.
func TestTapConfigRelay(t *testing.T) {
	data, err := schemas.Read(schemas.TapConfig)
	require.NoError(t, err)
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(data, &schema))
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err)

	validate := func(doc string) error {
		var v any
		require.NoError(t, yaml.Unmarshal([]byte(doc), &v))
		return resolved.Validate(v)
	}

	require.NoError(t, validate(`
relay:
  name: laptop
  providers:
    ollama:
      models:
        allow: ["qwen3:8b"]
    openrouter:
      kind: openrouter
      auth: apiKey
      apiKeyEnv: OPENROUTER_API_KEY
    lan:
      kind: openai-compatible
      baseUrl: http://10.0.0.5:8000/v1
      auth: none
`))

	for name, doc := range map[string]string{
		"unknown kind":     "relay: {providers: {x: {kind: anthropic}}}",
		"unknown auth":     "relay: {providers: {x: {auth: keychain}}}",
		"non-http baseUrl": "relay: {providers: {x: {baseUrl: 'ftp://host'}}}",
		"bad relay name":   "relay: {name: 'has space'}",
		"bad provider key": "relay: {providers: {'a/b': {}}}",
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, validate(doc))
		})
	}
}
