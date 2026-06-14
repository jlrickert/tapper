package tapper

import (
	"context"
	"fmt"
	"strings"
)

// Keg administration (hub-side ACLs + visibility). Listing kegs and creating
// kegs live elsewhere: `tap keg list` maps to Tap.HubListKegs (tap_hub.go) and
// `tap keg create` maps to Tap.InitKeg (tap_init.go). These methods cover the
// per-keg admin surface the hub exposes under /api/v1/@{namespace}/kegs/{keg}.

// KegGrantsOptions selects the keg whose grants to list.
type KegGrantsOptions struct {
	Keg string
}

// KegGrantOptions upserts a grant on a keg.
type KegGrantOptions struct {
	Keg  string
	User string
	Role string // viewer|editor|admin
}

// KegRevokeOptions revokes a user's grant on a keg.
type KegRevokeOptions struct {
	Keg  string
	User string
}

// KegVisibilityOptions sets a keg's visibility.
type KegVisibilityOptions struct {
	Keg        string
	Visibility string // public|private
}

// kegGrantRoles is the role set accepted for keg grants (distinct from
// namespace membership roles).
var kegGrantRoles = map[string]bool{"viewer": true, "editor": true, "admin": true}

var kegVisibilities = map[string]bool{"public": true, "private": true}

// KegGrants lists the per-(user, role) grants on a keg.
func (t *Tap) KegGrants(ctx context.Context, opts KegGrantsOptions) ([]HubGrant, error) {
	ns, alias, hubURL, token, err := t.resolveKegAdminRef(opts.Keg)
	if err != nil {
		return nil, err
	}
	return ListGrants(ctx, hubURL, token, ns, alias)
}

// KegGrant upserts a grant (user → role) on a keg.
func (t *Tap) KegGrant(ctx context.Context, opts KegGrantOptions) error {
	role := strings.TrimSpace(opts.Role)
	if !kegGrantRoles[role] {
		return fmt.Errorf("invalid role %q: expected viewer, editor, or admin", opts.Role)
	}
	user := strings.TrimPrefix(strings.TrimSpace(opts.User), "@")
	if user == "" {
		return fmt.Errorf("a username is required")
	}
	ns, alias, hubURL, token, err := t.resolveKegAdminRef(opts.Keg)
	if err != nil {
		return err
	}
	return CreateGrant(ctx, hubURL, token, ns, alias, user, role)
}

// KegRevoke removes a user's grant on a keg.
func (t *Tap) KegRevoke(ctx context.Context, opts KegRevokeOptions) error {
	user := strings.TrimPrefix(strings.TrimSpace(opts.User), "@")
	if user == "" {
		return fmt.Errorf("a username is required")
	}
	ns, alias, hubURL, token, err := t.resolveKegAdminRef(opts.Keg)
	if err != nil {
		return err
	}
	return RevokeGrant(ctx, hubURL, token, ns, alias, user)
}

// KegVisibility sets a keg's visibility to public or private.
func (t *Tap) KegVisibility(ctx context.Context, opts KegVisibilityOptions) error {
	vis := strings.TrimSpace(opts.Visibility)
	if !kegVisibilities[vis] {
		return fmt.Errorf("invalid visibility %q: expected public or private", opts.Visibility)
	}
	ns, alias, hubURL, token, err := t.resolveKegAdminRef(opts.Keg)
	if err != nil {
		return err
	}
	return SetKegVisibility(ctx, hubURL, token, ns, alias, vis)
}

// resolveKegAdminRef parses a keg reference (@namespace/keg, or a bare name that
// resolves the default namespace), resolves the remote hub backing that
// namespace, and returns the namespace, keg alias, hub URL, and bearer token.
// Keg administration requires a remote hub-backed namespace with a token.
func (t *Tap) resolveKegAdminRef(raw string) (namespace, alias, hubURL, token string, err error) {
	cfg, cErr := t.ConfigService.Config(true)
	if cErr != nil {
		return "", "", "", "", cErr
	}
	if strings.TrimSpace(raw) == "" {
		return "", "", "", "", fmt.Errorf("a keg reference is required (e.g. @namespace/keg)")
	}
	ref := parseKegRef(raw)
	if ref.Path != "" || ref.Name == "" {
		return "", "", "", "", fmt.Errorf("keg administration requires a hub-backed keg reference like @namespace/keg")
	}
	ns := strings.TrimSpace(ref.Namespace)
	if ns == "" {
		ns = strings.TrimSpace(cfg.resolveNamespaceForName())
	}
	if ns == "" {
		return "", "", "", "", fmt.Errorf("could not determine a namespace for %q; qualify it as @namespace/%s", raw, ref.Name)
	}
	hubName := strings.TrimSpace(ref.Hub)
	if hubName == "" {
		hubName = cfg.resolveHubForNamespace(ns)
	}
	entry, ok := cfg.Hub(hubName)
	if !ok {
		return "", "", "", "", fmt.Errorf("hub %q is not configured", hubName)
	}
	if strings.TrimSpace(entry.Kind) == HubKindLocal {
		return "", "", "", "", fmt.Errorf("keg administration requires a remote hub-backed namespace")
	}
	url := strings.TrimSpace(entry.URL)
	if url == "" {
		return "", "", "", "", fmt.Errorf("hub %q has no url configured", hubName)
	}
	tok := t.hubToken(entry)
	if tok == "" {
		return "", "", "", "", fmt.Errorf("hub %q has no auth token (run `tap auth login --hub %s`)", hubName, url)
	}
	return ns, ref.Name, url, tok, nil
}
