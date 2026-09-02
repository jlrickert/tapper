package tapper

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Namespace administration: listing namespaces, managing membership roles, and
// creating org namespaces. Backs `tap namespace`. The hub endpoints live under
// /api/v1/namespaces and /api/v1/@{namespace}/members.

// NamespaceListOptions selects which hub to query for namespaces. An empty Hub
// aggregates every configured hub the user can reach.
type NamespaceListOptions struct {
	Hub string
}

// NamespaceListResult carries the aggregated rows plus a note for each hub that
// could not be reached. Warnings are non-fatal by design: one unreachable hub
// must not blank out the namespaces the others returned.
type NamespaceListResult struct {
	Namespaces []HubNamespace
	Warnings   []string
}

// NamespaceMembersOptions selects the namespace whose members to list. Namespace
// is the selector; empty resolves the default namespace. Hub pins the hub.
type NamespaceMembersOptions struct {
	Namespace string
	Hub       string
}

// NamespaceAddMemberOptions upserts a member into a namespace.
type NamespaceAddMemberOptions struct {
	Namespace string
	Hub       string
	User      string
	Role      string // owner|admin|member
}

// NamespaceSetRoleOptions changes an existing member's role.
type NamespaceSetRoleOptions struct {
	Namespace string
	Hub       string
	User      string
	Role      string // owner|admin|member
}

// NamespaceRemoveMemberOptions removes a member from a namespace.
type NamespaceRemoveMemberOptions struct {
	Namespace string
	Hub       string
	User      string
}

// NamespaceCreateOptions creates an org namespace.
type NamespaceCreateOptions struct {
	Name string
	Hub  string
}

// NamespaceCreateResult tells the caller where to create the namespace in the
// hub UI. Namespace creation is a paid hub feature, so the CLI/MCP surface
// hands the user to the browser instead of calling the API.
type NamespaceCreateResult struct {
	Name string
	Hub  string
	URL  string
}

// namespaceMemberRoles is the role set accepted for namespace membership
// (distinct from the keg grant roles).
var namespaceMemberRoles = map[string]bool{"owner": true, "admin": true, "member": true}

// NamespaceList returns the namespaces the caller belongs to. With no --hub it
// aggregates across every configured hub rather than only the selected one,
// which previously hid memberships on every other hub and gave no way to tell
// which hub a row came from (tapper#73). Every row carries its source hub, so
// the same namespace name on two hubs stays two distinct rows.
//
// With an explicit --hub only that hub is queried and its errors surface
// directly. In aggregate mode an unreachable or unauthenticated hub is recorded
// as a warning and skipped. This mirrors HubListKegs.
//
// Local namespaces are not represented. Tapper has no local hub kind: config
// admits only remote and readonly, and every resolver rejects anything else.
func (t *Tap) NamespaceList(ctx context.Context, opts NamespaceListOptions) (NamespaceListResult, error) {
	explicit := strings.TrimSpace(opts.Hub)
	if explicit != "" {
		hubURL, token, err := t.resolveHubEndpoint(explicit)
		if err != nil {
			return NamespaceListResult{}, err
		}
		nss, err := ListNamespaces(ctx, hubURL, token)
		if err != nil {
			return NamespaceListResult{}, err
		}
		return NamespaceListResult{Namespaces: tagNamespaceHub(nss, explicit)}, nil
	}

	cfg, err := t.ConfigService.Config()
	if err != nil {
		return NamespaceListResult{}, err
	}

	var result NamespaceListResult
	for _, name := range t.allHubNames(cfg) {
		entry, ok := cfg.Hub(name)
		if !ok {
			continue
		}
		if kind := hubKindOrDefault(entry.Kind); kind != HubKindRemote && kind != HubKindReadonly {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("hub %q: unsupported kind %q", name, kind))
			continue
		}
		hubURL, token, hErr := remoteHubEndpoint(t, name, entry)
		if hErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("hub %q: %v", name, hErr))
			continue
		}
		nss, lErr := ListNamespaces(ctx, hubURL, token)
		if lErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("hub %q: %v", name, lErr))
			if lg := t.Runtime.Logger(); lg != nil {
				lg.Warn("namespace list: skipping hub", "hub", name, "err", lErr)
			}
			continue
		}
		result.Namespaces = append(result.Namespaces, tagNamespaceHub(nss, name)...)
	}

	sort.Slice(result.Namespaces, func(i, j int) bool {
		a, b := result.Namespaces[i], result.Namespaces[j]
		if a.Hub != b.Hub {
			return a.Hub < b.Hub
		}
		return a.Name < b.Name
	})
	return result, nil
}

// tagNamespaceHub stamps each row with the hub it came from.
func tagNamespaceHub(nss []HubNamespace, hub string) []HubNamespace {
	out := make([]HubNamespace, 0, len(nss))
	for _, ns := range nss {
		ns.Hub = hub
		out = append(out, ns)
	}
	return out
}

// NamespaceMembers returns the member roster of a namespace.
func (t *Tap) NamespaceMembers(ctx context.Context, opts NamespaceMembersOptions) ([]HubMember, error) {
	ns, hubURL, token, err := t.resolveNamespaceHub(opts.Namespace, opts.Hub)
	if err != nil {
		return nil, err
	}
	return ListNamespaceMembers(ctx, hubURL, token, ns)
}

// NamespaceAddMember upserts a member (user → role) into a namespace.
func (t *Tap) NamespaceAddMember(ctx context.Context, opts NamespaceAddMemberOptions) error {
	role := strings.TrimSpace(opts.Role)
	if !namespaceMemberRoles[role] {
		return fmt.Errorf("invalid role %q: expected owner, admin, or member", opts.Role)
	}
	user := strings.TrimPrefix(strings.TrimSpace(opts.User), "@")
	if user == "" {
		return fmt.Errorf("a username is required")
	}
	ns, hubURL, token, err := t.resolveNamespaceHub(opts.Namespace, opts.Hub)
	if err != nil {
		return err
	}
	return AddNamespaceMember(ctx, hubURL, token, ns, user, role)
}

// NamespaceSetRole changes an existing member's role.
func (t *Tap) NamespaceSetRole(ctx context.Context, opts NamespaceSetRoleOptions) error {
	role := strings.TrimSpace(opts.Role)
	if !namespaceMemberRoles[role] {
		return fmt.Errorf("invalid role %q: expected owner, admin, or member", opts.Role)
	}
	user := strings.TrimPrefix(strings.TrimSpace(opts.User), "@")
	if user == "" {
		return fmt.Errorf("a username is required")
	}
	ns, hubURL, token, err := t.resolveNamespaceHub(opts.Namespace, opts.Hub)
	if err != nil {
		return err
	}
	return SetNamespaceMemberRole(ctx, hubURL, token, ns, user, role)
}

// NamespaceRemoveMember removes a member from a namespace.
func (t *Tap) NamespaceRemoveMember(ctx context.Context, opts NamespaceRemoveMemberOptions) error {
	user := strings.TrimPrefix(strings.TrimSpace(opts.User), "@")
	if user == "" {
		return fmt.Errorf("a username is required")
	}
	ns, hubURL, token, err := t.resolveNamespaceHub(opts.Namespace, opts.Hub)
	if err != nil {
		return err
	}
	return RemoveNamespaceMember(ctx, hubURL, token, ns, user)
}

// NamespaceCreate returns the hub UI URL for creating an org namespace. It does
// not call the namespace creation API.
func (t *Tap) NamespaceCreate(ctx context.Context, opts NamespaceCreateOptions) (*NamespaceCreateResult, error) {
	name := strings.TrimPrefix(strings.TrimSpace(opts.Name), "@")
	if name == "" {
		return nil, fmt.Errorf("a namespace name is required")
	}
	if err := ValidateNamespace(name); err != nil {
		return nil, err
	}
	hubName, hubURL, err := t.resolveHubUIEndpoint(opts.Hub)
	if err != nil {
		return nil, err
	}
	_ = ctx
	return &NamespaceCreateResult{
		Name: name,
		Hub:  hubName,
		URL:  hubURL + "/namespaces/new?name=" + url.QueryEscape(name),
	}, nil
}

// resolveNamespaceHub resolves the remote hub + token backing a namespace for
// namespace-scoped admin ops. An empty namespace resolves the default.
func (t *Tap) resolveNamespaceHub(namespace, hubOverride string) (ns, hubURL, token string, err error) {
	cfg, cErr := t.ConfigService.Config()
	if cErr != nil {
		return "", "", "", cErr
	}
	ns = strings.TrimPrefix(strings.TrimSpace(namespace), "@")
	if ns == "" {
		ns = strings.TrimSpace(cfg.resolveNamespaceForName())
	}
	if ns == "" {
		return "", "", "", fmt.Errorf("a namespace is required")
	}
	hubName := strings.TrimSpace(hubOverride)
	if hubName == "" {
		hubName = cfg.resolveHubForNamespace(ns)
	}
	entry, ok := cfg.Hub(hubName)
	if !ok {
		return "", "", "", fmt.Errorf("hub %q is not configured", hubName)
	}
	url, tok, err := remoteHubEndpoint(t, hubName, entry)
	if err != nil {
		return "", "", "", err
	}
	return ns, url, tok, nil
}

// resolveHubEndpoint resolves a hub (explicit name or the default) to a URL +
// token for hub-level namespace ops (list/create).
func (t *Tap) resolveHubEndpoint(hubOverride string) (hubURL, token string, err error) {
	cfg, cErr := t.ConfigService.Config()
	if cErr != nil {
		return "", "", cErr
	}
	hubName := strings.TrimSpace(hubOverride)
	if hubName == "" {
		hubName = cfg.resolveHubName()
	}
	entry, ok := cfg.Hub(hubName)
	if !ok {
		return "", "", fmt.Errorf("hub %q is not configured", hubName)
	}
	return remoteHubEndpoint(t, hubName, entry)
}

// resolveHubUIEndpoint resolves a hub to a browser base URL without requiring
// a token. It is used for UI handoffs such as namespace creation.
func (t *Tap) resolveHubUIEndpoint(hubOverride string) (hubName, hubURL string, err error) {
	cfg, cErr := t.ConfigService.Config()
	if cErr != nil {
		return "", "", cErr
	}
	hubName = strings.TrimSpace(hubOverride)
	if hubName == "" {
		hubName = cfg.resolveHubName()
	}
	entry, ok := cfg.Hub(hubName)
	if !ok {
		return "", "", fmt.Errorf("hub %q is not configured", hubName)
	}
	raw := strings.TrimSpace(entry.URL)
	if raw == "" {
		return "", "", fmt.Errorf("hub %q has no url configured", hubName)
	}
	base, err := normalizeHubURL(hubURLWithScheme(raw))
	if err != nil {
		return "", "", err
	}
	return hubName, base, nil
}

// remoteHubEndpoint validates a hub entry as a usable remote endpoint and
// returns its URL + resolved bearer token.
func remoteHubEndpoint(t *Tap, hubName string, entry HubEntry) (hubURL, token string, err error) {
	url := strings.TrimSpace(entry.URL)
	if url == "" {
		return "", "", fmt.Errorf("hub %q has no url configured", hubName)
	}
	tok := t.hubToken(entry)
	if tok == "" {
		return "", "", fmt.Errorf("hub %q has no auth token (run `tap auth login --hub %s`)", hubName, url)
	}
	return url, tok, nil
}
