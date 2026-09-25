package tapper

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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

// ErrRelayNotConfigured means the user config has no relay providers.
var ErrRelayNotConfigured = errors.New("no relay providers configured; add a relay.providers block to your user config")

// ErrRelayDisabled means the user config turns the relay off.
var ErrRelayDisabled = errors.New("the relay is disabled in your user config (relay.enabled: false)")

// Relay connects this machine's configured providers to Hub and serves
// inference requests until ctx ends or Hub rejects the relay permanently.
func (t *Tap) Relay(ctx context.Context, opts RelayOptions) error {
	cfg, err := t.ConfigService.Config()
	if err != nil {
		return err
	}
	rc := cfg.Relay()
	if !rc.IsEnabled() {
		return ErrRelayDisabled
	}
	if rc == nil || len(rc.Providers) == 0 {
		return ErrRelayNotConfigured
	}
	providers, err := t.relayProviders(rc)
	if err != nil {
		return err
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
			MaxConcurrent: p.MaxConcurrent,
			Priority:      p.Priority,
		}, t.Runtime.Get, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, provider)
	}
	return out, nil
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
