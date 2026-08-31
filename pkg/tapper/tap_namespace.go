package tapper

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Namespace administration: listing namespaces, managing membership roles, and
// creating org namespaces. Backs `tap namespace`. The hub endpoints live under
// /api/v1/namespaces and /api/v1/@{namespace}/members.

// NamespaceListOptions selects which hub to query for namespaces. Empty uses
// the resolved default hub.
type NamespaceListOptions struct {
	Hub string
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

// NamespaceList returns the namespaces the caller belongs to on a hub.
func (t *Tap) NamespaceList(ctx context.Context, opts NamespaceListOptions) ([]HubNamespace, error) {
	hubURL, token, err := t.resolveHubEndpoint(opts.Hub)
	if err != nil {
		return nil, err
	}
	return ListNamespaces(ctx, hubURL, token)
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
