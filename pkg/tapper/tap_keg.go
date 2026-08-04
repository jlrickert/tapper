package tapper

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// Keg administration (hub-side ACLs + visibility). Listing kegs and creating
// kegs live elsewhere: `tap keg list` maps to Tap.HubListKegs (tap_hub.go) and
// `tap keg create` maps to Tap.InitKeg (tap_init.go). These methods cover the
// per-keg admin surface the hub exposes under /api/v1/@{namespace}/kegs/{keg}.

// KegGrantsOptions selects the keg whose grants to list. Keg is the selector
// (a bare name or @namespace/keg); empty resolves the default keg. Namespace and
// Hub override the resolved reference's components.
type KegGrantsOptions struct {
	Keg       string
	Namespace string
	Hub       string
}

// KegGrantOptions upserts a grant on a keg.
type KegGrantOptions struct {
	Keg       string
	Namespace string
	Hub       string
	User      string
	Role      string // viewer|editor|admin
}

// KegRevokeOptions revokes a user's grant on a keg.
type KegRevokeOptions struct {
	Keg       string
	Namespace string
	Hub       string
	User      string
}

// KegVisibilityOptions sets a keg's visibility.
type KegVisibilityOptions struct {
	Keg        string
	Namespace  string
	Hub        string
	Visibility string // public|private
}

// KegRenameOptions renames a hub-backed keg alias within one namespace.
type KegRenameOptions struct {
	Old       string
	New       string
	Namespace string
	Hub       string
}

// kegGrantRoles is the role set accepted for keg grants (distinct from
// namespace membership roles).
var kegGrantRoles = map[string]bool{"viewer": true, "editor": true, "admin": true}

var kegVisibilities = map[string]bool{"public": true, "private": true}

// hubKegAliasPattern mirrors tapper-hub's catalog alias regex. It is stricter
// than local keg aliases: hub aliases are lowercase alphanumeric plus hyphen,
// 1-64 chars, and must start with an alphanumeric.
var hubKegAliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// KegGrants lists the per-(user, role) grants on a keg.
func (t *Tap) KegGrants(ctx context.Context, opts KegGrantsOptions) ([]HubGrant, error) {
	ns, alias, hubURL, token, err := t.resolveKegAdminRef(opts.Keg, opts.Namespace, opts.Hub)
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
	ns, alias, hubURL, token, err := t.resolveKegAdminRef(opts.Keg, opts.Namespace, opts.Hub)
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
	ns, alias, hubURL, token, err := t.resolveKegAdminRef(opts.Keg, opts.Namespace, opts.Hub)
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
	ns, alias, hubURL, token, err := t.resolveKegAdminRef(opts.Keg, opts.Namespace, opts.Hub)
	if err != nil {
		return err
	}
	return SetKegVisibility(ctx, hubURL, token, ns, alias, vis)
}

// KegRename renames a hub-backed keg alias within its namespace.
func (t *Tap) KegRename(ctx context.Context, opts KegRenameOptions) error {
	oldSelector := strings.TrimSpace(opts.Old)
	if oldSelector == "" {
		return fmt.Errorf("old keg alias is required")
	}
	newAlias := strings.TrimSpace(opts.New)
	if strings.HasPrefix(newAlias, "@") || strings.Contains(newAlias, "/") || strings.Contains(newAlias, "://") || strings.HasPrefix(newAlias, keg.SchemeAlias+":") {
		return fmt.Errorf("new alias must be a bare alias in the same namespace")
	}
	if !hubKegAliasPattern.MatchString(newAlias) {
		return fmt.Errorf("invalid alias %q: must be 1-64 lowercase alphanumeric characters or hyphens, starting with alphanumeric", opts.New)
	}

	ns, oldAlias, hubURL, token, err := t.resolveKegAdminRef(oldSelector, opts.Namespace, opts.Hub)
	if err != nil {
		return err
	}
	if newAlias == oldAlias {
		return fmt.Errorf("new alias is unchanged")
	}
	return RenameKeg(ctx, hubURL, token, ns, oldAlias, newAlias)
}

// resolveKegAdminRef resolves the keg an admin command targets. keg is the
// selector from --keg (a bare name or @namespace/keg); when empty it falls back
// to the configured defaultKeg then fallbackKeg. nsOverride/hubOverride apply
// the --namespace/--hub flags. It resolves the remote hub backing that
// namespace and returns the namespace, keg alias, hub URL, and bearer token.
// Keg administration requires a remote hub-backed namespace with a token.
func (t *Tap) resolveKegAdminRef(keg, nsOverride, hubOverride string) (namespace, alias, hubURL, token string, err error) {
	cfg, cErr := t.ConfigService.Config()
	if cErr != nil {
		return "", "", "", "", cErr
	}
	raw := strings.TrimSpace(keg)
	if raw == "" {
		raw = strings.TrimSpace(cfg.DefaultKeg())
	}
	if raw == "" {
		raw = strings.TrimSpace(cfg.FallbackKeg())
	}
	if raw == "" {
		return "", "", "", "", fmt.Errorf("no keg specified and no default keg configured; pass --keg @namespace/keg or set one with `tap use`")
	}
	ref, oErr := applyRefOverrides(parseKegRef(raw), nsOverride, hubOverride, raw)
	if oErr != nil {
		return "", "", "", "", oErr
	}
	if ref.Path != "" || ref.Name == "" {
		return "", "", "", "", fmt.Errorf("keg administration requires a hub-backed keg reference like @namespace/keg")
	}
	// Infer namespace + hub through the shared chain (same as ResolveRef), so a
	// bare name picks up its per-hub/default namespace instead of erroring.
	ns, hubName, entry, rErr := cfg.resolveNamespaceHub(ref.Namespace, ref.Hub)
	if rErr != nil {
		return "", "", "", "", fmt.Errorf("keg %q: %w", raw, rErr)
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
