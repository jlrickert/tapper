package tapper

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Hub agents: a namespace-owned model, instructions, tool allowlist, and
// memory flight. Backs `tap agent` and the agent_*
// MCP tools. Members of a namespace read its agents; owners and admins
// change them.

// ListAgentsOptions selects whose agents to list. An empty Namespace lists the
// agents of every namespace the caller belongs to. Hub pins the hub.
type ListAgentsOptions struct {
	Namespace string
	Hub       string
}

// GetAgentOptions names one agent (@namespace/name).
type GetAgentOptions struct {
	Ref string
	Hub string
}

// CreateAgentOptions creates an agent at Ref (@namespace/name). Tools holds
// tool group ids and single tool names; empty means every tool. Flight is the
// agent's memory flight (@ns/+slug); empty leaves it without KEG access.
type CreateAgentOptions struct {
	Ref          string
	Hub          string
	Title        string
	Description  string
	Instructions string
	Model        string
	Tools        []string
	Flight       string
}

// EditAgentOptions changes an agent. A nil field keeps its current value;
// Tools set to an empty slice clears the allowlist (every tool); Flight set
// to "" clears the agent's flight.
type EditAgentOptions struct {
	Ref          string
	Hub          string
	Title        *string
	Description  *string
	Instructions *string
	Model        *string
	Tools        *[]string
	Flight       *string
}

// DeleteAgentOptions removes the agent at Ref.
type DeleteAgentOptions struct {
	Ref string
	Hub string
}

// ListAgents returns agents from the hub, sorted by ref.
func (t *Tap) ListAgents(ctx context.Context, opts ListAgentsOptions) ([]HubAgent, error) {
	namespace := strings.TrimPrefix(strings.TrimSpace(opts.Namespace), "@")
	var hubURL, token string
	var err error
	if namespace == "" {
		hubURL, token, err = t.resolveHubEndpoint(opts.Hub)
	} else {
		namespace, hubURL, token, err = t.resolveNamespaceHub(namespace, opts.Hub)
	}
	if err != nil {
		return nil, err
	}
	agents, err := ListHubAgents(ctx, hubURL, token, namespace)
	if err != nil {
		return nil, err
	}
	sortHubAgents(agents)
	return agents, nil
}

// GetAgent returns one agent. The caller must belong to its namespace.
func (t *Tap) GetAgent(ctx context.Context, opts GetAgentOptions) (*HubAgent, error) {
	ns, name, hubURL, token, err := t.resolveAgentRef(opts.Ref, opts.Hub)
	if err != nil {
		return nil, err
	}
	return GetHubAgent(ctx, hubURL, token, ns, name)
}

// CreateAgent creates an agent. The caller must be a namespace owner or admin.
func (t *Tap) CreateAgent(ctx context.Context, opts CreateAgentOptions) (*HubAgent, error) {
	ns, name, hubURL, token, err := t.resolveAgentRef(opts.Ref, opts.Hub)
	if err != nil {
		return nil, err
	}
	return CreateHubAgent(ctx, hubURL, token, ns, HubAgentWrite{
		Name: name, Title: opts.Title, Description: opts.Description,
		Instructions: opts.Instructions, Model: opts.Model, Tools: nonNilStrings(opts.Tools),
		Flight: strings.TrimSpace(opts.Flight),
	})
}

// EditAgent reads the agent, applies the set fields, and writes it back. The
// caller must be a namespace owner or admin.
func (t *Tap) EditAgent(ctx context.Context, opts EditAgentOptions) (*HubAgent, error) {
	ns, name, hubURL, token, err := t.resolveAgentRef(opts.Ref, opts.Hub)
	if err != nil {
		return nil, err
	}
	current, err := GetHubAgent(ctx, hubURL, token, ns, name)
	if err != nil {
		return nil, err
	}
	in := HubAgentWrite{
		Name: name, Title: current.Title, Description: current.Description,
		Instructions: current.Instructions, Model: current.Model, Tools: nonNilStrings(current.Tools),
		Flight: current.Flight,
	}
	if opts.Title != nil {
		in.Title = *opts.Title
	}
	if opts.Description != nil {
		in.Description = *opts.Description
	}
	if opts.Instructions != nil {
		in.Instructions = *opts.Instructions
	}
	if opts.Model != nil {
		in.Model = *opts.Model
	}
	if opts.Tools != nil {
		in.Tools = nonNilStrings(*opts.Tools)
	}
	if opts.Flight != nil {
		in.Flight = strings.TrimSpace(*opts.Flight)
	}
	return UpdateHubAgent(ctx, hubURL, token, ns, name, in)
}

// DeleteAgent removes an agent. The caller must be a namespace owner or admin.
func (t *Tap) DeleteAgent(ctx context.Context, opts DeleteAgentOptions) error {
	ns, name, hubURL, token, err := t.resolveAgentRef(opts.Ref, opts.Hub)
	if err != nil {
		return err
	}
	return DeleteHubAgent(ctx, hubURL, token, ns, name)
}

// resolveAgentRef splits an @namespace/name ref and resolves its hub.
func (t *Tap) resolveAgentRef(ref, hub string) (ns, name, hubURL, token string, err error) {
	ns, name, err = ParseHubAgentRef(ref)
	if err != nil {
		return "", "", "", "", err
	}
	ns, hubURL, token, err = t.resolveNamespaceHub(ns, hub)
	if err != nil {
		return "", "", "", "", fmt.Errorf("agent %s: %w", ref, err)
	}
	return ns, name, hubURL, token, nil
}

func sortHubAgents(agents []HubAgent) {
	sort.Slice(agents, func(i, j int) bool { return agents[i].Ref < agents[j].Ref })
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
