package tapper

import (
	"context"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// InitOptions configures creation of a KEG on a configured remote hub.
type InitOptions struct {
	Hub        string
	Title      string
	Keg        string
	Visibility string

	// RequireBootstrap rejects config-driven creation until user setup exists.
	RequireBootstrap bool
}

// CreateKegOptions is the agent-facing KEG creation request.
type CreateKegOptions struct {
	Keg        string
	Title      string
	Visibility string
}

// InitKeg creates a KEG through the configured hub creation endpoint.
func (t *Tap) InitKeg(ctx context.Context, options InitOptions) (*keg.Target, error) {
	namespace, name, err := ParseKegCreationRef(options.Keg)
	if err != nil {
		return nil, err
	}
	if options.RequireBootstrap && !t.ConfigService.UserConfigExists() {
		return nil, ErrNotBootstrapped
	}

	cfg, err := t.ConfigService.Config()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	hubName, _, err := t.ConfigService.SelectedHub(options.Hub)
	if err != nil {
		return nil, err
	}
	namespace, hubName, _, err = cfg.resolveNamespaceHub(namespace, hubName)
	if err != nil {
		return nil, fmt.Errorf("cannot create %q: %w", name, err)
	}
	target, err := cfg.ResolveRef(t.Runtime, KegRef{Hub: hubName, Namespace: namespace, Name: name})
	if err != nil {
		return nil, fmt.Errorf("resolve create destination: %w", err)
	}
	return t.initRemoteKeg(ctx, options, target, hubName, namespace, name)
}

func (t *Tap) initRemoteKeg(ctx context.Context, options InitOptions, target *keg.Target, hubName, namespace, name string) (*keg.Target, error) {
	hubURL := strings.TrimSpace(target.HubURL)
	if hubURL == "" {
		hubURL = strings.TrimSpace(target.Url)
	}
	if hubURL == "" {
		return nil, fmt.Errorf("remote create requires a hub URL; none resolved for hub %q", hubName)
	}
	token := t.hubTokenForTarget(target)
	if token == "" {
		return nil, fmt.Errorf("not logged in to hub %q (run `tap auth login --hub %s`)", hubName, hubURL)
	}
	if err := CreateKeg(ctx, hubURL, token, namespace, name, options.Title, options.Visibility); err != nil {
		return nil, err
	}
	return target, nil
}

// ParseCanonicalKegRef splits an explicit @namespace/keg reference into its two
// validated segments. Every surface that accepts a canonical reference parses
// through here, so the reference rule has exactly one implementation.
func ParseCanonicalKegRef(raw string) (string, string, error) {
	ref := strings.TrimSpace(raw)
	if !strings.HasPrefix(ref, "@") {
		return "", "", fmt.Errorf("keg reference %q must start with @: %w", raw, keg.ErrInvalid)
	}
	namespace, name, ok := strings.Cut(strings.TrimPrefix(ref, "@"), "/")
	if !ok {
		return "", "", fmt.Errorf("keg reference %q must be @namespace/keg: %w", raw, keg.ErrInvalid)
	}
	if err := ValidateNamespace(namespace); err != nil {
		return "", "", err
	}
	if err := ValidateKegAlias(name); err != nil {
		return "", "", err
	}
	return namespace, name, nil
}

// ParseKegCreationRef validates the single creation destination before any I/O.
// The message names the expected shape rather than a command, because callers
// include hosted MCP clients that have no CLI to run.
func ParseKegCreationRef(raw string) (string, string, error) {
	namespace, name, err := ParseCanonicalKegRef(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid KEG creation reference %q: expected @namespace/keg: %w", raw, err)
	}
	return namespace, name, nil
}
