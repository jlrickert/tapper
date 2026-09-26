package tapper

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// HubAgent is a Hub agent: a namespace-owned model, instructions, tool
// allowlist, and flight. The agent is the unit of authority: its tools say
// what it may do, and its flight is the memory it works in. A flight carries
// no tooling of its own.
type HubAgent struct {
	Ref          string `json:"ref"`
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	// Model is a Hub catalog id; empty leaves the choice to the launcher.
	Model string `json:"model"`
	// Tools is the stored MCP tool allowlist: tool group ids (such as
	// "keg:read") and single tool names. Empty means every tool.
	Tools []string `json:"tools"`
	// EffectiveTools is Tools with its groups expanded to tool names by the
	// hub, for filtering a tool list by name. Empty means every tool.
	EffectiveTools []string `json:"effective_tools,omitempty"`
	// Flight is the agent's memory flight (@ns/+slug); empty means the agent
	// has no KEG access.
	Flight string `json:"flight"`
}

// ToolNames returns the tool names the agent may use: EffectiveTools when the
// hub sent them, else Tools (an older hub stores only names).
func (a HubAgent) ToolNames() []string {
	if len(a.EffectiveTools) > 0 {
		return a.EffectiveTools
	}
	return a.Tools
}

// HubAgentWrite is the body of an agent create or full replace. Tools holds
// tool group ids and single tool names; empty means every tool.
type HubAgentWrite struct {
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions"`
	Model        string   `json:"model"`
	Tools        []string `json:"tools"`
	// Flight is the agent's memory flight (@ns/+slug); empty clears it.
	Flight string `json:"flight"`
}

func hubAgentsPath(namespace string) string {
	return fmt.Sprintf("/api/v1/@%s/agents", namespace)
}

// ListHubAgents returns a namespace's agents, or with an empty namespace the
// agents of every namespace the caller belongs to.
func ListHubAgents(ctx context.Context, hubURL, token, namespace string) ([]HubAgent, error) {
	path := "/api/v1/agents"
	if namespace != "" {
		path = hubAgentsPath(namespace)
	}
	var out []HubAgent
	if err := doHubFlightJSON(ctx, "GET", hubURL, token, path, "", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateHubAgent creates an agent. The caller must be a namespace owner or
// admin.
func CreateHubAgent(ctx context.Context, hubURL, token, namespace string, in HubAgentWrite) (*HubAgent, error) {
	var out HubAgent
	if err := doHubFlightJSON(ctx, "POST", hubURL, token, hubAgentsPath(namespace), "", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateHubAgent replaces an agent's fields. The caller must be a namespace
// owner or admin.
func UpdateHubAgent(ctx context.Context, hubURL, token, namespace, name string, in HubAgentWrite) (*HubAgent, error) {
	var out HubAgent
	path := hubAgentsPath(namespace) + "/" + url.PathEscape(name)
	if err := doHubFlightJSON(ctx, "PUT", hubURL, token, path, "", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteHubAgent removes an agent. The caller must be a namespace owner or
// admin.
func DeleteHubAgent(ctx context.Context, hubURL, token, namespace, name string) error {
	path := hubAgentsPath(namespace) + "/" + url.PathEscape(name)
	return doHubFlightJSON(ctx, "DELETE", hubURL, token, path, "", nil, nil)
}

// IsHubAgentRef reports whether s names a Hub agent (@namespace/name) rather
// than an entry in the local agents config.
func IsHubAgentRef(s string) bool {
	_, _, err := ParseHubAgentRef(s)
	return err == nil
}

// ParseHubAgentRef splits "@namespace/name".
func ParseHubAgentRef(s string) (namespace, name string, err error) {
	s = strings.TrimSpace(s)
	rest, ok := strings.CutPrefix(s, "@")
	if !ok {
		return "", "", fmt.Errorf("hub agent reference %q must be @namespace/name", s)
	}
	namespace, name, ok = strings.Cut(rest, "/")
	if !ok || namespace == "" || name == "" || strings.ContainsAny(name, "/+") {
		return "", "", fmt.Errorf("hub agent reference %q must be @namespace/name", s)
	}
	if err := ValidateNamespace(namespace); err != nil {
		return "", "", fmt.Errorf("hub agent reference %q: %w", s, err)
	}
	return namespace, name, nil
}

// GetHubAgent fetches one agent. The caller must belong to its namespace.
func GetHubAgent(ctx context.Context, hubURL, token, namespace, name string) (*HubAgent, error) {
	var out HubAgent
	path := fmt.Sprintf("/api/v1/@%s/agents/%s", namespace, url.PathEscape(name))
	if err := doHubFlightJSON(ctx, "GET", hubURL, token, path, "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HubAgent fetches a Hub agent (@namespace/name) from the selected hub.
func (t *Tap) HubAgent(ctx context.Context, ref string) (*HubAgent, error) {
	ns, name, err := ParseHubAgentRef(ref)
	if err != nil {
		return nil, err
	}
	hubName, entry, err := t.ConfigService.SelectedHub("")
	if err != nil {
		return nil, err
	}
	token := t.hubToken(entry)
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("hub %q: not signed in; run `tap auth login`", hubName)
	}
	return GetHubAgent(ctx, strings.TrimRight(hubURLWithScheme(entry.URL), "/"), token, ns, name)
}
