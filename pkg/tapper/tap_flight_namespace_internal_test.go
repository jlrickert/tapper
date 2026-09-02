package tapper

import (
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
)

// newFlightNamespaceTap builds a Tap over a sandboxed filesystem holding the
// given user config. In-package so the unexported namespace resolvers are
// reachable directly; the exported CreateFlight path would need a live hub.
func newFlightNamespaceTap(t *testing.T, userConfig string) *Tap {
	t.Helper()
	fx := sandbox.NewSandbox(t, &sandbox.Options{
		Home: filepath.FromSlash("/home/testuser"),
		User: "testuser",
	})
	if err := fx.Setwd("/home/testuser"); err != nil {
		t.Fatalf("setwd: %v", err)
	}
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	if err != nil {
		t.Fatalf("new tap: %v", err)
	}
	if err := fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return tap
}

// TestDefaultFlightNamespace covers tapper#74: an unqualified flight name used
// to resolve to the personal namespace even when the active KEG was in an org
// namespace, silently creating the flight in the wrong place.
func TestDefaultFlightNamespace(t *testing.T) {
	const hubs = "hubs:\n  atlas:\n    kind: remote\n    url: https://atlas.foldwise.ai\n    token: tok\n"

	tests := []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "active org keg wins over personal default namespace",
			config: hubs + "defaultNamespace: jlrickert\ndefaultKeg: \"@foldwise/notes\"\n",
			want:   "foldwise",
		},
		{
			name:   "active personal keg resolves to the personal namespace",
			config: hubs + "defaultNamespace: jlrickert\ndefaultKeg: \"@jlrickert/notes\"\n",
			want:   "jlrickert",
		},
		{
			name:   "fallback keg supplies the namespace when no default keg is set",
			config: hubs + "defaultNamespace: jlrickert\nfallbackKeg: \"@foldwise/notes\"\n",
			want:   "foldwise",
		},
		{
			name:   "default keg outranks fallback keg",
			config: hubs + "defaultKeg: \"@foldwise/notes\"\nfallbackKeg: \"@other/notes\"\n",
			want:   "foldwise",
		},
		{
			name:   "no active keg falls back to the configured default namespace",
			config: hubs + "defaultNamespace: jlrickert\n",
			want:   "jlrickert",
		},
		{
			name:   "no active keg and no default namespace falls back to the hub default",
			config: "hubs:\n  atlas:\n    kind: remote\n    url: https://atlas.foldwise.ai\n    token: tok\n    defaultNamespace: hubns\n",
			want:   "hubns",
		},
		{
			name: "a bare keg name states no namespace and does not shadow the default",
			// The selector names no namespace, so the active-KEG step must
			// decline rather than reporting the personal namespace as though
			// the KEG had named it.
			config: hubs + "defaultNamespace: jlrickert\ndefaultKeg: notes\n",
			want:   "jlrickert",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tap := newFlightNamespaceTap(t, tc.config)
			cfg, err := tap.ConfigService.Config()
			if err != nil {
				t.Fatalf("config: %v", err)
			}
			if got := tap.defaultFlightNamespace(cfg); got != tc.want {
				t.Fatalf("defaultFlightNamespace = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveWriteFlightRef_QualifiedRefIsPreserved confirms an explicit
// @namespace/+slug is never rewritten by the defaulting above.
func TestResolveWriteFlightRef_QualifiedRefIsPreserved(t *testing.T) {
	tap := newFlightNamespaceTap(t,
		"hubs:\n  atlas:\n    kind: remote\n    url: https://atlas.foldwise.ai\n    token: tok\n"+
			"defaultNamespace: jlrickert\ndefaultKeg: \"@foldwise/notes\"\n")

	ref, _, _, err := tap.resolveWriteFlightRef("@other/+plan")
	if err != nil {
		t.Fatalf("resolveWriteFlightRef: %v", err)
	}
	if ref.Namespace != "other" {
		t.Fatalf("namespace = %q, want %q", ref.Namespace, "other")
	}

	// And the unqualified form picks up the active KEG's namespace.
	ref, _, _, err = tap.resolveWriteFlightRef("plan")
	if err != nil {
		t.Fatalf("resolveWriteFlightRef unqualified: %v", err)
	}
	if ref.Namespace != "foldwise" {
		t.Fatalf("unqualified namespace = %q, want %q", ref.Namespace, "foldwise")
	}
}
