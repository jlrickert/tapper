package tapper

import (
	"cmp"
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/internal/relay"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// DefaultRelayMaxConcurrent bounds in-flight requests when neither the flag
// nor configuration says otherwise.
const DefaultRelayMaxConcurrent = 4

// RelayOptions configures `tap relay`.
type RelayOptions struct {
	// Hub is an explicit hub URL or configured hub name.
	Hub string
	// Name overrides relay.name from configuration.
	Name string
	// MaxConcurrent overrides the default concurrency limit when positive.
	MaxConcurrent int
	// Version is the tap version reported at registration.
	Version string
	// OnRegistered observes each successful registration.
	OnRegistered func(hubURL string, reg relaycontract.Registered)
}

// ErrRelayNotConfigured means the user config has no relay providers.
var ErrRelayNotConfigured = errors.New("no relay providers configured; add a relay.providers block to your user config")

// Relay connects this machine's configured providers to Hub and serves
// inference requests until ctx ends or Hub rejects the relay permanently.
func (t *Tap) Relay(ctx context.Context, opts RelayOptions) error {
	cfg, err := t.ConfigService.Config()
	if err != nil {
		return err
	}
	rc := cfg.Relay()
	if rc == nil || len(rc.Providers) == 0 {
		return ErrRelayNotConfigured
	}
	providers, err := t.relayProviders(rc)
	if err != nil {
		return err
	}
	hubURL, err := ResolveLoginHubURL(cfg, opts.Hub)
	if err != nil {
		return err
	}
	hubURL = strings.TrimRight(hubURLWithScheme(hubURL), "/")

	name := cmp.Or(opts.Name, rc.Name)
	if name == "" {
		name = relayNameFromHost(t.runtimeHostname())
	}
	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultRelayMaxConcurrent
	}

	var onRegistered func(relaycontract.Registered)
	if opts.OnRegistered != nil {
		onRegistered = func(reg relaycontract.Registered) { opts.OnRegistered(hubURL, reg) }
	}
	client, err := relay.NewClient(relay.Options{
		HubURL:        hubURL,
		Token:         func(context.Context) (string, error) { return t.relayToken(cfg, hubURL), nil },
		Name:          name,
		Version:       opts.Version,
		MaxConcurrent: maxConcurrent,
		Providers:     providers,
		Logger:        t.Runtime.Logger(),
		OnRegistered:  onRegistered,
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
			Name:      n,
			Kind:      p.Kind,
			BaseURL:   p.BaseURL,
			Auth:      p.Auth,
			APIKeyEnv: p.APIKeyEnv,
			Allow:     p.Models.Allow,
			Deny:      p.Models.Deny,
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
