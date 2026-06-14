// Package tapper — hub namespace-administration client calls.
//
// These talk to a remote hub's namespace endpoints under /api/v1/namespaces
// and /api/v1/@{namespace}/members: listing/creating namespaces and managing
// membership roles. Slim, dependency-free functions over http.DefaultClient
// (via doHubJSON) so tests can point hubURL at an httptest.Server.
package tapper

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// HubMember is one namespace membership row. Mirrors the hub's
// handler.memberWire JSON body — keep the two in sync.
type HubMember struct {
	Username string `json:"username"`
	Role     string `json:"role"` // owner|admin|member
}

// HubNamespace is one namespace the caller belongs to (or just created).
// Mirrors the hub's handler.namespaceWire JSON body — keep the two in sync.
type HubNamespace struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"` // user|org
	Role string `json:"role,omitempty"` // caller's role
}

func namespaceMembersPath(namespace string) string {
	return fmt.Sprintf("/api/v1/@%s/members", namespace)
}

// ListNamespaceMembers returns the member roster of a namespace via
// GET /api/v1/@{namespace}/members.
func ListNamespaceMembers(ctx context.Context, hubURL, token, namespace string) ([]HubMember, error) {
	var out []HubMember
	if err := doHubJSON(ctx, http.MethodGet, hubURL, token, namespaceMembersPath(namespace), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AddNamespaceMember upserts a member via POST /api/v1/@{namespace}/members.
// role is owner|admin|member.
func AddNamespaceMember(ctx context.Context, hubURL, token, namespace, username, role string) error {
	payload := map[string]string{"username": strings.TrimPrefix(strings.TrimSpace(username), "@"), "role": role}
	return doHubJSON(ctx, http.MethodPost, hubURL, token, namespaceMembersPath(namespace), payload, nil)
}

// SetNamespaceMemberRole changes a member's role via
// PATCH /api/v1/@{namespace}/members/@{username}.
func SetNamespaceMemberRole(ctx context.Context, hubURL, token, namespace, username, role string) error {
	path := namespaceMembersPath(namespace) + "/@" + strings.TrimPrefix(strings.TrimSpace(username), "@")
	return doHubJSON(ctx, http.MethodPatch, hubURL, token, path, map[string]string{"role": role}, nil)
}

// RemoveNamespaceMember removes a member via
// DELETE /api/v1/@{namespace}/members/@{username}.
func RemoveNamespaceMember(ctx context.Context, hubURL, token, namespace, username string) error {
	path := namespaceMembersPath(namespace) + "/@" + strings.TrimPrefix(strings.TrimSpace(username), "@")
	return doHubJSON(ctx, http.MethodDelete, hubURL, token, path, nil, nil)
}

// ListNamespaces returns the namespaces the caller belongs to via
// GET /api/v1/namespaces.
func ListNamespaces(ctx context.Context, hubURL, token string) ([]HubNamespace, error) {
	var out []HubNamespace
	if err := doHubJSON(ctx, http.MethodGet, hubURL, token, "/api/v1/namespaces", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateNamespace creates an org namespace via POST /api/v1/namespaces.
func CreateNamespace(ctx context.Context, hubURL, token, name string) (*HubNamespace, error) {
	var out HubNamespace
	payload := map[string]string{"name": strings.TrimPrefix(strings.TrimSpace(name), "@")}
	if err := doHubJSON(ctx, http.MethodPost, hubURL, token, "/api/v1/namespaces", payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
