package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/cli"
	"github.com/stretchr/testify/require"
)

// newURLSandbox configures a hub whose every request is counted: building a
// link from configuration must not reach it, not even for a credential
// refresh.
func newURLSandbox(t *testing.T) (*testutils.Sandbox, *atomic.Int32, string) {
	t.Helper()
	var calls atomic.Int32
	hub := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(hub.Close)
	sb := NewSandbox(t)
	cfg := fmt.Sprintf("hub: cloud\nkeg: \"@team/notes\"\nhubs:\n  cloud: {url: %s, token: tok}\n", hub.URL)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o600))
	return sb, &calls, hub.URL
}

// withBrowser records what `--open` would launch instead of starting a browser.
func withBrowser(ctx context.Context, opened *[]string) context.Context {
	return cli.WithTestDepsHook(ctx, func(deps *cli.Deps) {
		deps.OpenBrowserFn = func(_ context.Context, _ *toolkit.Runtime, rawURL string) error {
			*opened = append(*opened, rawURL)
			return nil
		}
	})
}

func TestURLCLI_PrintsTheLinkAlone(t *testing.T) {
	t.Parallel()
	sb, calls, base := newURLSandbox(t)
	var opened []string
	ctx := withBrowser(sb.Context(), &opened)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"url"}, "/@team/notes"},
		{[]string{"url", "42"}, "/@team/notes/42"},
		{[]string{"url", "@ops/runbooks/7"}, "/@ops/runbooks/7"},
		{[]string{"url", "keg:@ops/library/3"}, "/@ops/library/3"},
		{[]string{"url", "keg:@ops/runbooks"}, "/@ops/runbooks"},
		{[]string{"url", "journal", "--namespace", "ops"}, "/@ops/journal"},
		{[]string{"url", "5", "--keg", "@ops/runbooks"}, "/@ops/runbooks/5"},
		{[]string{"url", "+dev"}, "/@team/+dev"},
		{[]string{"url", "@ops"}, "/@ops"},
	} {
		res := NewHubProcess(t, false, tc.args...).Run(ctx, sb.Runtime())
		require.NoError(t, res.Err, tc.args)
		require.Equal(t, base+tc.want+"\n", string(res.Stdout), tc.args)
	}
	require.Empty(t, opened, "without --open nothing is launched")
	require.Zero(t, calls.Load(), "a link built from configuration never reaches the hub")
}

func TestURLCLI_JSON(t *testing.T) {
	t.Parallel()
	sb, _, base := newURLSandbox(t)

	res := NewHubProcess(t, false, "url", "42", "--json").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	var got map[string]string
	require.NoError(t, json.Unmarshal(res.Stdout, &got), string(res.Stdout))
	require.Equal(t, map[string]string{"url": base + "/@team/notes/42", "kind": "node", "ref": "keg:@team/notes/42"}, got)
}

func TestURLCLI_OpenLaunchesTheBrowser(t *testing.T) {
	t.Parallel()
	sb, calls, base := newURLSandbox(t)
	var opened []string

	res := NewHubProcess(t, false, "url", "@team/notes/9", "--open").Run(withBrowser(sb.Context(), &opened), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, base+"/@team/notes/9\n", string(res.Stdout))
	require.Equal(t, []string{base + "/@team/notes/9"}, opened)
	require.Zero(t, calls.Load())
}

func TestURLCLI_RejectsAnUnrecognizedRef(t *testing.T) {
	t.Parallel()
	sb, _, _ := newURLSandbox(t)
	var opened []string

	res := NewHubProcess(t, false, "url", "Not A Keg!", "--open").Run(withBrowser(sb.Context(), &opened), sb.Runtime())
	require.ErrorContains(t, res.Err, "unrecognized reference")
	require.Empty(t, res.Stdout)
	require.Empty(t, opened)
}

func TestURLCompletion_SuggestsKegsFlightsAndNodeIDs(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "url", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)
	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@team/personal")
	require.Contains(t, suggestions, "@team/+backend")

	local := NewSandbox(t, testutils.WithFixture("joe", "~"))
	comp = NewCompletionProcess(t, false, 0, "url", "--keg", "personal", "").Run(local.Context(), local.Runtime())
	require.NoError(t, comp.Err)
	suggestions = parseCompletionSuggestions(string(comp.Stdout))
	require.Subset(t, suggestions, []string{"0", "1", "2", "3"})

	comp = NewCompletionProcess(t, false, 0, "url", "--keg", "personal", "1", "").Run(local.Context(), local.Runtime())
	require.NoError(t, comp.Err)
	require.Empty(t, parseCompletionSuggestions(string(comp.Stdout)))
}
