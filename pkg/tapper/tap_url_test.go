package tapper

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func newWebURLTap(t *testing.T, config string) *Tap {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	rt := sb.Runtime()
	tap, err := NewTap(TapOptions{Runtime: rt, Root: "/workspace"})
	require.NoError(t, err)
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0o644))
	tap.ConfigService.Reload()
	return tap
}

func TestWebURL_EveryRefForm(t *testing.T) {
	t.Parallel()
	tap := newWebURLTap(t, "hub: cloud\nkeg: '@team/notes'\nhubs:\n  cloud: {url: 'https://hub.example/', token: tok}\n")
	ctx := context.Background()

	cases := []struct {
		ref, url, kind, canonical string
	}{
		{"", "https://hub.example/@team/notes", WebURLKindKeg, "keg:@team/notes"},
		{"42", "https://hub.example/@team/notes/42", WebURLKindNode, "keg:@team/notes/42"},
		{"42-1234", "https://hub.example/@team/notes/42", WebURLKindNode, "keg:@team/notes/42"},
		{"keg:@ops/runbooks", "https://hub.example/@ops/runbooks", WebURLKindKeg, "keg:@ops/runbooks"},
		{"@ops/runbooks", "https://hub.example/@ops/runbooks", WebURLKindKeg, "keg:@ops/runbooks"},
		{"keg:@ops/runbooks/7", "https://hub.example/@ops/runbooks/7", WebURLKindNode, "keg:@ops/runbooks/7"},
		{"@ops/runbooks/7", "https://hub.example/@ops/runbooks/7", WebURLKindNode, "keg:@ops/runbooks/7"},
		{"+dev", "https://hub.example/@team/+dev", WebURLKindFlight, "@team/+dev"},
		{"@ops/+oncall", "https://hub.example/@ops/+oncall", WebURLKindFlight, "@ops/+oncall"},
		{"@ops", "https://hub.example/@ops", WebURLKindNamespace, "@ops"},
		{"https://hub.example/@ops/runbooks/7", "https://hub.example/@ops/runbooks/7", WebURLKindNode, "keg:@ops/runbooks/7"},
		{"https://hub.example/@ops/+oncall", "https://hub.example/@ops/+oncall", WebURLKindFlight, "@ops/+oncall"},
		{"https://hub.example/@ops", "https://hub.example/@ops", WebURLKindNamespace, "@ops"},
		{"https://hub.example/@ops/runbooks/", "https://hub.example/@ops/runbooks/", WebURLKindKeg, "keg:@ops/runbooks"},
		{"https://hub.example/@ops/runbooks/007", "https://hub.example/@ops/runbooks/007", WebURLKindNode, "keg:@ops/runbooks/7"},
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			got, err := tap.WebURL(ctx, WebURLOptions{Ref: tc.ref})
			require.NoError(t, err)
			require.Equal(t, WebURL{URL: tc.url, Kind: tc.kind, Ref: tc.canonical}, got)
		})
	}
}

func TestWebURL_KegTargetOptionsSelectTheCurrentKeg(t *testing.T) {
	t.Parallel()
	tap := newWebURLTap(t, "hub: cloud\nkeg: '@team/notes'\nhubs:\n  cloud: {url: 'https://hub.example', token: tok}\n")
	ctx := context.Background()

	got, err := tap.WebURL(ctx, WebURLOptions{Ref: "5", KegTargetOptions: KegTargetOptions{Keg: "@ops/runbooks"}})
	require.NoError(t, err)
	require.Equal(t, "https://hub.example/@ops/runbooks/5", got.URL)

	got, err = tap.WebURL(ctx, WebURLOptions{KegTargetOptions: KegTargetOptions{Keg: "journal", Namespace: "ops"}})
	require.NoError(t, err)
	require.Equal(t, "https://hub.example/@ops/journal", got.URL)

	// A bare alias resolves in --namespace, as it does for every command.
	for ref, want := range map[string]string{
		"journal":       "https://hub.example/@ops/journal",
		"keg:journal":   "https://hub.example/@ops/journal",
		"keg:journal/3": "https://hub.example/@ops/journal/3",
	} {
		got, err = tap.WebURL(ctx, WebURLOptions{Ref: ref, KegTargetOptions: KegTargetOptions{Namespace: "ops"}})
		require.NoError(t, err, ref)
		require.Equal(t, want, got.URL, ref)
	}
	_, err = tap.WebURL(ctx, WebURLOptions{Ref: "journal"})
	require.ErrorContains(t, err, "namespace is required")

	// A qualified reference names its own namespace; --namespace only fills a
	// bare one.
	got, err = tap.WebURL(ctx, WebURLOptions{Ref: "@team/notes/1", KegTargetOptions: KegTargetOptions{Namespace: "ops"}})
	require.NoError(t, err)
	require.Equal(t, "https://hub.example/@team/notes/1", got.URL)
}

func TestWebURL_Errors(t *testing.T) {
	t.Parallel()
	tap := newWebURLTap(t, "hub: cloud\nkeg: '@team/notes'\nhubs:\n  cloud: {url: 'https://hub.example', token: tok}\n")
	ctx := context.Background()

	cases := []struct {
		name string
		opts WebURLOptions
		want string
	}{
		{"raw endpoint keg", WebURLOptions{KegTargetOptions: KegTargetOptions{Keg: "https://raw.example/keg"}}, "has no Hub web page"},
		{"malformed ref", WebURLOptions{Ref: "Not A Keg!"}, "unrecognized reference"},
		{"malformed namespace", WebURLOptions{Ref: "@Bad"}, "invalid namespace"},
		{"url on another host", WebURLOptions{Ref: "https://elsewhere.example/@team/notes"}, "is not a page on the Hub"},
		{"url that is not a page", WebURLOptions{Ref: "https://hub.example/settings"}, "is not a namespace, KEG, note, or flight page"},
		{"unknown hub", WebURLOptions{Ref: "@ops", KegTargetOptions: KegTargetOptions{Hub: "missing"}}, "missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tap.WebURL(ctx, tc.opts)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestWebURL_FlightNeedsANamespace(t *testing.T) {
	t.Parallel()
	tap := newWebURLTap(t, "hub: cloud\nkeg: notes\nhubs:\n  cloud: {url: 'https://hub.example', token: tok}\n")
	_, err := tap.WebURL(context.Background(), WebURLOptions{Ref: "+dev"})
	require.ErrorContains(t, err, "needs a namespace")
}

// A keg-local ~alias is the one form that reads the network: the current
// keg's settings name the keg it points at.
func TestWebURL_SettingsAliasReadsCurrentKegSettings(t *testing.T) {
	t.Parallel()
	hub := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/@team/kegs/notes/settings" {
			_, _ = w.Write([]byte("kegv: 2025-07\naliases:\n  - alias: lib\n    url: keg:@ops/library\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer hub.Close()
	tap := newWebURLTap(t, "hub: cloud\nkeg: '@team/notes'\nhubs:\n  cloud: {url: '"+hub.URL+"', token: tok}\n")
	ctx := context.Background()

	got, err := tap.WebURL(ctx, WebURLOptions{Ref: "keg:~lib/12"})
	require.NoError(t, err)
	require.Equal(t, WebURL{URL: hub.URL + "/@ops/library/12", Kind: WebURLKindNode, Ref: "keg:@ops/library/12"}, got)

	got, err = tap.WebURL(ctx, WebURLOptions{Ref: "keg:~lib"})
	require.NoError(t, err)
	require.Equal(t, WebURL{URL: hub.URL + "/@ops/library", Kind: WebURLKindKeg, Ref: "keg:@ops/library"}, got)

	_, err = tap.WebURL(ctx, WebURLOptions{Ref: "keg:~missing/1"})
	require.ErrorIs(t, err, keg.ErrNotExist)
}

// A Hub serving its own MCP resolves kegs through its injected resolver and
// links to its own web address.
func TestWebURL_HostedUsesTheServingHub(t *testing.T) {
	t.Parallel()
	tap := newWebURLTap(t, "")
	tap.KegResolver = func(_ context.Context, opts KegTargetOptions, _ FlightRole) (keg.Keg, error) {
		ns, name := opts.Namespace, opts.Keg
		if head, tail, ok := strings.Cut(strings.TrimPrefix(opts.Keg, "@"), "/"); ok && strings.HasPrefix(opts.Keg, "@") {
			ns, name = head, tail
		}
		if name == "" {
			name = "notes"
		}
		k := keg.NewLocalKeg(nil, tap.Runtime)
		k.SetTarget(&keg.Target{Namespace: ns, KegName: name})
		return k, nil
	}
	ctx := context.Background()
	opts := KegTargetOptions{Namespace: "jared"}

	_, err := tap.WebURL(ctx, WebURLOptions{Ref: "@ops", KegTargetOptions: opts})
	require.ErrorContains(t, err, "no public web address")

	tap.WebBaseURL = "https://atlas.example/"
	for ref, want := range map[string]string{
		"":                                    "https://atlas.example/@jared/notes",
		"4":                                   "https://atlas.example/@jared/notes/4",
		"journal":                             "https://atlas.example/@jared/journal",
		"@ops/runbooks/7":                     "https://atlas.example/@ops/runbooks/7",
		"+dev":                                "https://atlas.example/@jared/+dev",
		"@ops":                                "https://atlas.example/@ops",
		"https://atlas.example/@ops/runbooks": "https://atlas.example/@ops/runbooks",
	} {
		got, err := tap.WebURL(ctx, WebURLOptions{Ref: ref, KegTargetOptions: opts})
		require.NoError(t, err, ref)
		require.Equal(t, want, got.URL, ref)
	}
}
