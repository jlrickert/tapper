package tapper

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// InitOptions configures creation of a KEG on a configured remote hub.
type InitOptions struct {
	Namespace  string
	Hub        string
	Title      string
	Keg        string
	Visibility string

	// RequireBootstrap rejects config-driven creation until user setup exists.
	RequireBootstrap bool
}

// CreateKegOptions is the agent-facing KEG creation request.
type CreateKegOptions struct {
	Namespace  string
	Keg        string
	Title      string
	Visibility string
}

// InitKeg creates a KEG through the configured hub creation endpoint.
func (t *Tap) InitKeg(ctx context.Context, options InitOptions) (*keg.Target, error) {
	name := strings.TrimSpace(options.Keg)
	if err := ValidateKegAlias(name); err != nil {
		return nil, err
	}
	if options.RequireBootstrap && !t.ConfigService.UserConfigExists() {
		return nil, ErrNotBootstrapped
	}

	cfg, err := t.ConfigService.Config()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	namespace, hubName, entry, err := cfg.resolveNamespaceHub(options.Namespace, options.Hub)
	if err != nil {
		return nil, fmt.Errorf("cannot create %q: %w", name, err)
	}
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}
	if kind != HubKindRemote {
		return nil, fmt.Errorf("hub %q kind %q does not support KEG creation: %w", hubName, kind, keg.ErrNotSupported)
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
	if err := t.recordInitKeg(hubName, namespace); err != nil {
		return nil, err
	}
	return target, nil
}

func (t *Tap) recordInitKeg(hubName, namespace string) error {
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(hubName) == "" {
		return nil
	}
	userConfig, err := t.ConfigService.ReadUserConfigFile()
	if err != nil {
		if !errors.Is(err, keg.ErrNotExist) {
			return err
		}
		userConfig = &Config{data: &configDTO{}}
	}
	if err := userConfig.SetNamespace(namespace, NamespaceRef{Hub: hubName}); err != nil {
		return err
	}
	if err := userConfig.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
		return err
	}
	t.ConfigService.Reload()
	return nil
}
