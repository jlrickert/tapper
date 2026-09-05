package cli_test

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	tu "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/internal/testkegrepo"
	"github.com/jlrickert/tapper/pkg/cli"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// NOTE: Production code should call streams.IsStdoutTTY() (method) instead of
// performing raw terminal detection. Tests can override IsStdoutTTYFn to
// simulate TTY or non-TTY environments.
//
// testdata is an optional embedded data FS for fixtures. Previously an embed
// pattern attempted to include empty directories which caused an embed error.
//
//go:embed all:data/**
var testdata embed.FS

func strPtr(value string) *string { return &value }

func NewSandbox(t *testing.T, opts ...tu.Option) *tu.Sandbox {
	sb := tu.NewSandbox(t, &tu.Options{
		Data: testdata,
		Home: "/home/testuser",
		User: "testuser",
	}, opts...)
	normalizeFixtureConfig(t, sb.Runtime())
	return sb
}

func normalizeFixtureConfig(t *testing.T, rt *toolkit.Runtime) {
	t.Helper()
	home, err := rt.GetHome()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".config", "tapper", "config.yaml")
	raw, err := rt.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "kind: local") {
		return
	}
	body := strings.ReplaceAll(string(raw), "kind: local", "kind: remote")
	body = strings.ReplaceAll(body, "    basePath: ~/kegs", "    url: https://fixture.invalid\n    token: test-token")
	if !strings.Contains(body, "fallbackHub:") {
		body += "fallbackHub: home\n"
	}
	if !strings.Contains(body, "namespaces:") {
		body += "namespaces:\n  local:\n    hub: home\n"
	}
	if err := rt.AtomicWriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("normalize fixture config: %v", err)
	}
}

func NewRemoteKegListSandbox(t *testing.T, kegs []tapper.HubKeg) *tu.Sandbox {
	t.Helper()
	flights := []tapper.HubFlight{
		{Namespace: "team", Slug: "backend", Title: "Backend", Visibility: "private", Instructions: "Backend instructions"},
		{Namespace: "team", Slug: "baseline", Title: "Baseline", Visibility: "private", Instructions: "Baseline instructions"},
		{Namespace: "team", Slug: "project", Title: "Project", Visibility: "private", Instructions: "Project instructions"},
		{Namespace: "team", Slug: "environment", Title: "Environment", Visibility: "private", Instructions: "Environment instructions"},
		{Namespace: "team", Slug: "explicit", Title: "Explicit", Visibility: "private", Instructions: "Explicit instructions"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/kegs":
			_ = json.NewEncoder(w).Encode(kegs)
		case r.URL.Path == "/api/v1/flights":
			_ = json.NewEncoder(w).Encode(flights)
		case strings.HasPrefix(r.URL.Path, "/api/v1/@team/+"):
			slug := strings.TrimPrefix(r.URL.Path, "/api/v1/@team/+")
			for _, flight := range flights {
				if flight.Slug == slug {
					_ = json.NewEncoder(w).Encode(flight)
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	sb := NewSandbox(t)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(fmt.Sprintf(`fallbackHub: test
fallbackNamespace: team
hubs:
  test:
    kind: remote
    url: %s
    token: test-token
`, srv.URL)), 0o644)
	return sb
}

func DisableStrictSchemaPolicy(t *testing.T, ctx context.Context, k *keg.LocalKeg) {
	t.Helper()
	if err := k.UpdateSettings(ctx, func(cfg *keg.Settings) {
		if cfg.SchemaPolicy == nil {
			cfg.SchemaPolicy = &keg.SchemaPolicy{}
		}
		cfg.SchemaPolicy.Strict = false
	}); err != nil {
		t.Fatalf("disable strict schema policy: %v", err)
	}
}

func NewCliRunner(t *testing.T) *tu.Process {
	return nil
}

func NewProcess(t *testing.T, isTTY bool, args ...string) *tu.Process {
	var mu sync.Mutex
	var currentRuntime *toolkit.Runtime
	var factory func(tapper.TapOptions) (*tapper.Tap, error)
	return tu.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		mu.Lock()
		if currentRuntime != rt {
			currentRuntime = rt
			factory = newFixtureTapFactory(t, ctx, rt)
		}
		activeFactory := factory
		mu.Unlock()
		ctx = cli.WithTestDepsHook(ctx, func(deps *cli.Deps) { deps.TapFactory = activeFactory })
		return cli.Run(ctx, rt, args)
	}, isTTY)
}

// NewCreateProcess builds a `tap create` process whose node content is piped
// on stdin. Content is the only way to give a new node a title now that
// --title, --lead, --tags and --attrs are gone, so tests that merely need a
// node with a known title go through here instead of repeating the heredoc.
//
// meta, when non-empty, is emitted as the content's YAML frontmatter — the
// documented CLI channel for metadata on a piped create.
func NewCreateProcess(t *testing.T, isTTY bool, title, meta string, extraArgs ...string) *tu.Process {
	proc := NewProcess(t, isTTY, append([]string{"create"}, extraArgs...)...)
	content := "# " + title + "\n"
	if meta != "" {
		content = "---\n" + meta + "---\n" + content
	}
	proc.SetStdin(strings.NewReader(content))
	return proc
}

func NewCompletionProcess(t *testing.T, isTTY bool, pos int, words ...string) *tu.Process {
	_ = pos
	var mu sync.Mutex
	var currentRuntime *toolkit.Runtime
	var factory func(tapper.TapOptions) (*tapper.Tap, error)
	return tu.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		mu.Lock()
		if currentRuntime != rt {
			currentRuntime = rt
			factory = newFixtureTapFactory(t, ctx, rt)
		}
		activeFactory := factory
		mu.Unlock()
		ctx = cli.WithTestDepsHook(ctx, func(deps *cli.Deps) { deps.TapFactory = activeFactory })
		return cli.RunCompletion(ctx, rt, words)
	}, isTTY)
}

type fixtureTapState struct {
	mu   sync.Mutex
	kegs map[string]keg.Keg
}

var fixtureTapStates sync.Map

func fixtureStateKey(rt *toolkit.Runtime) string {
	if jail := rt.GetJail(); jail != "" {
		if resolved, err := filepath.EvalSymlinks(jail); err == nil {
			jail = resolved
		}
		return jail
	}
	return fmt.Sprintf("runtime:%p", rt)
}

func fixtureKeg(t *testing.T, rt *toolkit.Runtime, alias string) keg.Keg {
	t.Helper()
	stored, ok := fixtureTapStates.Load(fixtureStateKey(rt))
	if !ok {
		_ = newFixtureTapFactory(t, context.Background(), rt)
		stored, ok = fixtureTapStates.Load(fixtureStateKey(rt))
		if !ok {
			t.Fatalf("fixture state not initialized")
		}
	}
	state := stored.(*fixtureTapState)
	state.mu.Lock()
	defer state.mu.Unlock()
	opened := state.kegs["@local/"+alias]
	if opened == nil {
		t.Fatalf("fixture keg @local/%s not found", alias)
	}
	return opened
}

func fixtureContent(t *testing.T, rt *toolkit.Runtime, alias, id string) string {
	t.Helper()
	nodeID, err := keg.ParseNode(id)
	if err != nil || nodeID == nil {
		t.Fatalf("parse fixture node %q: %v", id, err)
	}
	raw, err := fixtureKeg(t, rt, alias).GetContent(context.Background(), *nodeID)
	if err != nil {
		t.Fatalf("read fixture content @local/%s/%s: %v", alias, id, err)
	}
	return string(raw)
}

func fixtureSetContent(t *testing.T, rt *toolkit.Runtime, alias, id, content string) {
	t.Helper()
	nodeID, err := keg.ParseNode(id)
	if err != nil || nodeID == nil {
		t.Fatalf("parse fixture node %q: %v", id, err)
	}
	if err := fixtureKeg(t, rt, alias).SetContent(context.Background(), *nodeID, []byte(content)); err != nil {
		t.Fatalf("write fixture content @local/%s/%s: %v", alias, id, err)
	}
}

func fixtureMeta(t *testing.T, rt *toolkit.Runtime, alias, id string) string {
	t.Helper()
	nodeID, err := keg.ParseNode(id)
	if err != nil || nodeID == nil {
		t.Fatalf("parse fixture node %q: %v", id, err)
	}
	raw, err := fixtureKeg(t, rt, alias).GetMetaRaw(context.Background(), *nodeID)
	if err != nil {
		t.Fatalf("read fixture metadata @local/%s/%s: %v", alias, id, err)
	}
	return string(raw)
}

func fixtureSettings(t *testing.T, rt *toolkit.Runtime, alias string) string {
	t.Helper()
	settings, err := fixtureKeg(t, rt, alias).Settings(context.Background())
	if err != nil {
		t.Fatalf("read fixture settings @local/%s: %v", alias, err)
	}
	return string(settings.Raw())
}

func fixtureStats(t *testing.T, rt *toolkit.Runtime, alias, id string) *keg.NodeStats {
	t.Helper()
	nodeID, err := keg.ParseNode(id)
	if err != nil || nodeID == nil {
		t.Fatalf("parse fixture node %q: %v", id, err)
	}
	stats, err := fixtureKeg(t, rt, alias).GetStats(context.Background(), *nodeID)
	if err != nil {
		t.Fatalf("read fixture stats @local/%s/%s: %v", alias, id, err)
	}
	return stats
}

func fixtureStatsJSON(t *testing.T, rt *toolkit.Runtime, alias, id string) string {
	t.Helper()
	raw, err := fixtureStats(t, rt, alias, id).ToJSON()
	if err != nil {
		t.Fatalf("encode fixture stats @local/%s/%s: %v", alias, id, err)
	}
	return string(raw)
}

func fixtureNodeExists(t *testing.T, rt *toolkit.Runtime, alias, id string) bool {
	t.Helper()
	nodeID, err := keg.ParseNode(id)
	if err != nil || nodeID == nil {
		t.Fatalf("parse fixture node %q: %v", id, err)
	}
	exists, err := fixtureKeg(t, rt, alias).NodeExists(context.Background(), *nodeID)
	if err != nil {
		t.Fatalf("check fixture node @local/%s/%s: %v", alias, id, err)
	}
	return exists
}

func fixtureFile(t *testing.T, rt *toolkit.Runtime, alias, id, name string, image bool) []byte {
	t.Helper()
	nodeID, err := keg.ParseNode(id)
	if err != nil || nodeID == nil {
		t.Fatalf("parse fixture node %q: %v", id, err)
	}
	var raw []byte
	if image {
		raw, err = fixtureKeg(t, rt, alias).ReadImage(context.Background(), *nodeID, name)
	} else {
		raw, err = fixtureKeg(t, rt, alias).ReadFile(context.Background(), *nodeID, name)
	}
	if err != nil {
		t.Fatalf("read fixture attachment @local/%s/%s/%s: %v", alias, id, name, err)
	}
	return raw
}

func newFixtureTapFactory(t *testing.T, ctx context.Context, rt *toolkit.Runtime) func(tapper.TapOptions) (*tapper.Tap, error) {
	t.Helper()
	stateKey := fixtureStateKey(rt)
	candidate := &fixtureTapState{kegs: loadFixtureKegs(t, ctx, rt)}
	stored, loaded := fixtureTapStates.LoadOrStore(stateKey, candidate)
	state := stored.(*fixtureTapState)
	if !loaded {
		t.Cleanup(func() { fixtureTapStates.Delete(stateKey) })
	}
	return func(opts tapper.TapOptions) (*tapper.Tap, error) {
		tap, err := tapper.NewTap(opts)
		if err != nil {
			return nil, err
		}
		tap.KegResolver = func(_ context.Context, target tapper.KegTargetOptions, _ tapper.FlightRole) (keg.Keg, error) {
			alias, namespace := strings.TrimSpace(target.Keg), strings.TrimPrefix(strings.TrimSpace(target.Namespace), "@")
			if strings.HasPrefix(alias, "@") {
				head, tail, ok := strings.Cut(strings.TrimPrefix(alias, "@"), "/")
				if ok {
					if namespace != "" && namespace != head {
						return nil, fmt.Errorf("keg reference namespace %q conflicts with the namespace %q", head, namespace)
					}
					namespace, alias = head, tail
				}
			}
			if alias == "" {
				if cfg, cfgErr := tap.ConfigService.Config(); cfgErr == nil && cfg != nil {
					alias = strings.TrimSpace(cfg.DefaultKeg())
					if alias == "" {
						alias = strings.TrimSpace(cfg.LookupAlias(rt, tap.Root))
					}
					if namespace == "" {
						namespace = strings.TrimPrefix(strings.TrimSpace(cfg.DefaultNamespace()), "@")
						if namespace == "" {
							namespace = strings.TrimPrefix(strings.TrimSpace(cfg.FallbackNamespace()), "@")
						}
					}
				}
			}
			if namespace == "" {
				namespace = "local"
			}
			if alias == "" {
				return nil, tapper.ErrNotBootstrapped
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if opened := state.kegs["@"+namespace+"/"+alias]; opened != nil {
				return opened, nil
			}
			return nil, fmt.Errorf("keg not initialized: @%s/%s: %w", namespace, alias, keg.ErrNotExist)
		}
		return tap, nil
	}
}

func loadFixtureKegs(t *testing.T, ctx context.Context, rt *toolkit.Runtime) map[string]keg.Keg {
	t.Helper()
	home, err := rt.GetHome()
	if err != nil {
		t.Fatalf("fixture home: %v", err)
	}
	settingsPaths, err := rt.Glob(filepath.Join(home, "kegs", "@*", "*", "keg"))
	if err != nil {
		t.Fatalf("find fixture kegs: %v", err)
	}
	sort.Strings(settingsPaths)
	out := make(map[string]keg.Keg, len(settingsPaths))
	for _, settingsPath := range settingsPaths {
		base := filepath.Dir(settingsPath)
		alias := filepath.Base(base)
		namespace := strings.TrimPrefix(filepath.Base(filepath.Dir(base)), "@")
		repo := testkegrepo.NewMemoryRepository(rt)
		rawSettings, readErr := rt.ReadFile(settingsPath)
		if readErr != nil {
			t.Fatalf("read %s: %v", settingsPath, readErr)
		}
		if writeErr := repo.WriteSettingsDocument(ctx, rawSettings); writeErr != nil {
			t.Fatalf("load %s settings: %v", settingsPath, writeErr)
		}
		entries, readErr := rt.ReadDir(base)
		if readErr != nil {
			t.Fatalf("read %s: %v", base, readErr)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			id, parseErr := keg.ParseNode(entry.Name())
			if parseErr != nil || id == nil {
				continue
			}
			nodeDir := filepath.Join(base, entry.Name())
			content, contentErr := rt.ReadFile(filepath.Join(nodeDir, keg.MarkdownContentFilename))
			if contentErr != nil {
				continue
			}
			if writeErr := repo.WriteContent(ctx, *id, content); writeErr != nil {
				t.Fatalf("load %s content: %v", nodeDir, writeErr)
			}
			if rawMeta, metaErr := rt.ReadFile(filepath.Join(nodeDir, "meta.yaml")); metaErr == nil {
				if writeErr := repo.WriteMeta(ctx, *id, rawMeta); writeErr != nil {
					t.Fatalf("load %s metadata: %v", nodeDir, writeErr)
				}
			}
			stats := keg.NewStats(rt.Clock().Now())
			if rawStats, statsErr := rt.ReadFile(filepath.Join(nodeDir, "stats.json")); statsErr == nil {
				stats, statsErr = keg.ParseStats(ctx, rawStats)
				if statsErr != nil {
					t.Fatalf("parse %s stats: %v", nodeDir, statsErr)
				}
			}
			if writeErr := repo.WriteStats(ctx, *id, stats); writeErr != nil {
				t.Fatalf("load %s stats: %v", nodeDir, writeErr)
			}
		}
		local := keg.NewLocalKeg(repo, rt)
		target := keg.NewApi("fixture", namespace, alias, keg.WithHubURL("https://fixture.invalid"))
		local.SetTarget(&target)
		if dexEntries, dexErr := rt.ReadDir(filepath.Join(base, "dex")); dexErr == nil {
			for _, dexEntry := range dexEntries {
				if dexEntry.IsDir() {
					continue
				}
				rawIndex, readErr := rt.ReadFile(filepath.Join(base, "dex", dexEntry.Name()))
				if readErr != nil {
					t.Fatalf("read fixture index %s: %v", dexEntry.Name(), readErr)
				}
				if writeErr := repo.WriteIndex(ctx, dexEntry.Name(), rawIndex); writeErr != nil {
					t.Fatalf("load fixture index %s: %v", dexEntry.Name(), writeErr)
				}
			}
		}
		out["@"+namespace+"/"+alias] = local
	}
	return out
}

// parseCompletionSuggestions parses the raw output of a cobra __complete
// invocation and returns the suggestion strings, stopping at the directive line.
func parseCompletionSuggestions(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			break
		}
		if strings.Contains(line, "\t") {
			line = strings.SplitN(line, "\t", 2)[0]
		}
		out = append(out, line)
	}
	return out
}
