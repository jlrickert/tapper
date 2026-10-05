package tapper

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/jlrickert/tapper/internal/relay"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// RelayOptions configures `tap relay`.
type RelayOptions struct {
	// Hub is an explicit hub URL or configured hub name. It narrows the
	// relay to that one hub, overriding relay.hubs.
	Hub string
	// Name overrides relay.name from configuration.
	Name string
	// Version is the tap version reported at registration.
	Version string
	// OnRegistered observes each successful registration.
	OnRegistered func(hubURL string, reg relaycontract.Registered)
}

// ErrRelayNotConfigured means the user config has nothing for the relay to
// offer: no providers and no MCP servers.
var ErrRelayNotConfigured = errors.New("nothing to relay; add a relay.providers or relay.mcp block to your user config")

// ErrRelayDisabled means the user config turns the relay off.
var ErrRelayDisabled = errors.New("the relay is disabled in your user config (relay.enabled: false)")

// Relay connects this machine's configured providers and MCP servers to Hub
// and serves inference and tool calls until ctx ends or Hub rejects the relay
// permanently.
func (t *Tap) Relay(ctx context.Context, opts RelayOptions) error {
	cfg, err := t.ConfigService.Config()
	if err != nil {
		return err
	}
	rc := cfg.Relay()
	if !rc.IsEnabled() {
		return ErrRelayDisabled
	}
	if rc == nil || (len(rc.Providers) == 0 && len(rc.MCP) == 0) {
		return ErrRelayNotConfigured
	}
	providers, err := t.relayProviders(rc)
	if err != nil {
		return err
	}
	toolServers, err := t.relayToolServers(rc)
	if err != nil {
		return err
	}
	if len(providers) == 0 && len(toolServers) == 0 {
		return ErrRelayNotConfigured
	}
	hubURLs, err := relayHubURLs(cfg, rc, opts.Hub)
	if err != nil {
		return err
	}
	hubs := make([]relay.Hub, 0, len(hubURLs))
	for _, hubURL := range hubURLs {
		hubs = append(hubs, relay.Hub{
			URL:   hubURL,
			Token: func(context.Context) (string, error) { return t.relayToken(cfg, hubURL), nil },
		})
	}

	name := cmp.Or(opts.Name, rc.Name)
	if name == "" {
		name = relayNameFromHost(t.runtimeHostname())
	}

	client, err := relay.NewClient(relay.Options{
		Hubs:         hubs,
		Name:         name,
		Version:      opts.Version,
		Providers:    providers,
		ToolServers:  toolServers,
		Logger:       t.Runtime.Logger(),
		OnRegistered: opts.OnRegistered,
	})
	if err != nil {
		return err
	}
	return client.Run(ctx)
}

func (t *Tap) relayProviders(rc *RelayConfig) ([]*relay.Provider, error) {
	names := make([]string, 0, len(rc.Providers))
	for n := range rc.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*relay.Provider, 0, len(names))
	for _, n := range names {
		p := rc.Providers[n]
		provider, err := relay.NewProvider(relay.ProviderConfig{
			Name:          n,
			Kind:          p.Kind,
			BaseURL:       p.BaseURL,
			Auth:          p.Auth,
			APIKeyEnv:     p.APIKeyEnv,
			Allow:         p.Models.Allow,
			Deny:          p.Models.Deny,
			Transcription: p.Models.Transcription,
			Embeddings:    p.Models.Embeddings,
			MaxConcurrent: p.MaxConcurrent,
			Priority:      p.Priority,
			Metadata:      relayMetadata(p.Models.Metadata),
			Variants:      relayVariants(p.Models.Variants),
		}, t.Runtime.Get, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, provider)
	}
	return out, nil
}

// relayToolServers builds the enabled MCP servers, in name order. Secrets are
// read here, from the variables the config names, and stay in this process.
func (t *Tap) relayToolServers(rc *RelayConfig) ([]*relay.ToolServer, error) {
	names := make([]string, 0, len(rc.MCP))
	for n, s := range rc.MCP {
		if s.IsEnabled() {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	out := make([]*relay.ToolServer, 0, len(names))
	for _, n := range names {
		s := rc.MCP[n]
		var timeout time.Duration
		if s.Timeout != "" {
			d, err := time.ParseDuration(s.Timeout)
			if err != nil || d <= 0 {
				return nil, fmt.Errorf("relay.mcp.%s.timeout %q must be a positive duration such as 30s or 5m", n, s.Timeout)
			}
			timeout = d
		}
		headers := make(map[string]string, len(s.Headers)+len(s.HeadersFromEnv))
		maps.Copy(headers, s.Headers)
		for header, name := range s.HeadersFromEnv {
			v := t.Runtime.Get(name)
			if v == "" {
				return nil, fmt.Errorf("relay.mcp.%s.headersFromEnv: %s is not set", n, name)
			}
			headers[header] = v
		}
		cfg := relay.ToolServerConfig{
			Name:          n,
			Title:         s.Title,
			URL:           s.URL,
			Headers:       headers,
			Allow:         s.Tools.Allow,
			Deny:          s.Tools.Deny,
			MaxConcurrent: s.MaxConcurrent,
			Timeout:       timeout,
		}
		if s.Command != "" {
			cfg.Command = s.Command
			cfg.Args = s.Args
			cfg.Dir = s.Cwd
			cfg.Env = relay.ToolEnv(t.Runtime.Environ(), s.InheritEnv, s.EnvFrom, s.Env)
		}
		server, err := relay.NewToolServer(cfg)
		if err != nil {
			return nil, err
		}
		out = append(out, server)
	}
	return out, nil
}

func relayMetadata(in map[string]RelayModelMeta) map[string]relay.ModelMeta {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]relay.ModelMeta, len(in))
	for id, m := range in {
		out[id] = relay.ModelMeta(m)
	}
	return out
}

func relayVariants(in map[string]RelayVariant) map[string]relay.Variant {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]relay.Variant, len(in))
	for id, v := range in {
		out[id] = relay.Variant{From: v.From, ModelMeta: relay.ModelMeta(v.RelayModelMeta)}
	}
	return out
}

// relayToken resolves the bearer token for hubURL the same way keg access
// does: a configured hub entry's tokenEnv or token, then the `tap auth login`
// store, refreshing an expiring OAuth token first.
// relayHubURLs picks the hubs the relay serves: the explicit hub alone when
// one is given, else every hub named in relay.hubs, else the one hub login
// resolution picks. URLs are canonical and listed once each.
func relayHubURLs(cfg *Config, rc *RelayConfig, explicit string) ([]string, error) {
	names := rc.Hubs
	if strings.TrimSpace(explicit) != "" || len(names) == 0 {
		names = []string{explicit}
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if strings.TrimSpace(explicit) == "" && len(rc.Hubs) > 0 {
			if _, ok := cfg.Hub(name); !ok {
				return nil, fmt.Errorf("relay.hubs: hub %q is not defined in hubs", name)
			}
		}
		hubURL, err := ResolveLoginHubURL(cfg, name)
		if err != nil {
			return nil, err
		}
		hubURL = strings.TrimRight(hubURLWithScheme(hubURL), "/")
		key := CanonicalHubURL(hubURL)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, hubURL)
	}
	return out, nil
}

func (t *Tap) relayToken(cfg *Config, hubURL string) string {
	canonical := CanonicalHubURL(hubURL)
	for _, entry := range cfg.Hubs() {
		if entry.URL != "" && CanonicalHubURL(hubURLWithScheme(entry.URL)) == canonical {
			return t.hubToken(entry)
		}
	}
	return t.hubTokenForTarget(&keg.Target{Url: hubURL, HubURL: hubURL})
}

// runtimeHostname reports the host name captured by the runtime, or "" when
// the runtime has no process information.
func (t *Tap) runtimeHostname() string {
	if t.Runtime == nil {
		return ""
	}
	if proc := t.Runtime.Process(); proc != nil {
		return proc.Hostname
	}
	return ""
}

// relayNameFromHost derives a protocol-safe relay name from host.
func relayNameFromHost(host string) string {
	host, _, _ = strings.Cut(host, ".")
	var b strings.Builder
	for _, r := range strings.ToLower(host) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > relaycontract.MaxNameLength {
		name = name[:relaycontract.MaxNameLength]
	}
	if name == "" {
		return "relay"
	}
	return name
}
