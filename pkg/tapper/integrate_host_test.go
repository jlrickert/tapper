package tapper

import "testing"

// Hooks are per-plugin, so the PATH check that guarantees a `tap hook`-capable
// binary has to follow the selection. Codex keeps its SessionStart orientation
// hook in the baseline plugin and always needs one; Claude ships hooks only in
// tapper-guard, so a --no-safety install there has nothing to verify.
func TestRequiresHookSupport_FollowsSelectedPlugins(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		host    integrationHost
		plugins []string
		want    bool
	}{
		{"claude with guard", claudeIntegrationHost(), []string{"tapper", "tapper-guard"}, true},
		{"claude without guard", claudeIntegrationHost(), []string{"tapper", "tapper-dev"}, false},
		{"codex with guard", codexIntegrationHost(), []string{"tapper", "tapper-guard"}, true},
		{"codex without guard", codexIntegrationHost(), []string{"tapper"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.host.RequiresHookSupport(tt.plugins); got != tt.want {
				t.Fatalf("RequiresHookSupport(%v) = %t, want %t", tt.plugins, got, tt.want)
			}
		})
	}
}
