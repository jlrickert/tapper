package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type agentListInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace whose agents to list; omit for every namespace you belong to"`
}

type agentReadInput struct {
	Agent string `json:"agent" jsonschema:"agent reference (@namespace/name)"`
}

type agentCreateInput struct {
	Agent        string   `json:"agent" jsonschema:"agent reference (@namespace/name); the name is permanent"`
	Title        string   `json:"title,omitempty" jsonschema:"agent title"`
	Description  string   `json:"description,omitempty" jsonschema:"what the agent is for, in a sentence; a coordinator reads it to pick an agent"`
	Instructions string   `json:"instructions,omitempty" jsonschema:"markdown system prompt; the flight's orientation is added after it"`
	Model        string   `json:"model,omitempty" jsonschema:"Hub model id (relay/provider/model or @namespace/model)"`
	Tools        []string `json:"tools,omitempty" jsonschema:"tool group ids (e.g. keg:read) and single tool names; omit or [] for every tool"`
	Flight       string   `json:"flight,omitempty" jsonschema:"the agent's memory flight (@namespace/+slug); omit for none, which leaves the agent without KEG access"`
}

type agentEditInput struct {
	Agent        string    `json:"agent" jsonschema:"agent reference (@namespace/name)"`
	Title        *string   `json:"title,omitempty" jsonschema:"new title; omit to keep"`
	Description  *string   `json:"description,omitempty" jsonschema:"new description; omit to keep"`
	Instructions *string   `json:"instructions,omitempty" jsonschema:"new markdown instructions; omit to keep"`
	Model        *string   `json:"model,omitempty" jsonschema:"new Hub model id; omit to keep"`
	Tools        *[]string `json:"tools,omitempty" jsonschema:"replacement tool groups and names; omit to keep, [] for every tool"`
	Flight       *string   `json:"flight,omitempty" jsonschema:"new memory flight (@namespace/+slug); omit to keep, \"\" to clear"`
}

type agentDeleteInput struct {
	Agent string `json:"agent" jsonschema:"agent reference (@namespace/name)"`
}

// registerAgentTools exposes Hub agents over MCP at parity with
// `tap agent list/read/create/edit/delete`. Agents belong to namespaces:
// members read them, owners and admins change them. An agent is the unit of
// authority: its tools say what it may do and its flight is its memory.
func registerAgentTools(srv *sdkmcp.Server, defaults KegDefaults, agents AgentProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "agent_list",
		Description: "List Hub agents (model, description, tool allowlist, flight) in a namespace, or in every namespace you belong to",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in agentListInput) (*sdkmcp.CallToolResult, any, error) {
		rows, err := agents.ListAgents(ctx, in.Namespace)
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(rows))
		for _, a := range rows {
			lines = append(lines, strings.Join([]string{a.Ref, tsvField(a.Title), a.Model, tsvField(a.Description)}, "\t"))
		}
		res := linesResult(lines)
		res.StructuredContent = map[string]any{"agents": publicAgents(rows)}
		return res, nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "agent_read",
		Description: "Read one Hub agent: model, description, instructions, tool allowlist (groups and names, plus effective_tools expanded), and its memory flight",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in agentReadInput) (*sdkmcp.CallToolResult, any, error) {
		agent, err := agents.GetAgent(ctx, in.Agent)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return agentResult(agent), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "agent_create",
		Description: "Create a Hub agent in a namespace you own or administer. Its tools say what it may do; its flight is the memory it works in.",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in agentCreateInput) (*sdkmcp.CallToolResult, any, error) {
		if err := defaults.gate.authorizeMutation(ctx); err != nil {
			return errorResult(err), nil, nil
		}
		agent, err := agents.CreateAgent(ctx, tapper.CreateAgentOptions{
			Ref: in.Agent, Title: in.Title, Description: in.Description,
			Instructions: in.Instructions, Model: in.Model, Tools: in.Tools, Flight: in.Flight,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return agentResult(agent), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "agent_edit",
		Description: "Edit a Hub agent in a namespace you own or administer; omitted fields keep their current values. An edit applies to open sessions running the agent on their next call.",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in agentEditInput) (*sdkmcp.CallToolResult, any, error) {
		if err := defaults.gate.authorizeMutation(ctx); err != nil {
			return errorResult(err), nil, nil
		}
		agent, err := agents.EditAgent(ctx, tapper.EditAgentOptions{
			Ref: in.Agent, Title: in.Title, Description: in.Description,
			Instructions: in.Instructions, Model: in.Model, Tools: in.Tools, Flight: in.Flight,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return agentResult(agent), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "agent_delete",
		Description: "Delete a Hub agent in a namespace you own or administer. Apps and tokens bound to it lose access.",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(true)},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in agentDeleteInput) (*sdkmcp.CallToolResult, any, error) {
		if err := defaults.gate.authorizeMutation(ctx); err != nil {
			return errorResult(err), nil, nil
		}
		if err := agents.DeleteAgent(ctx, in.Agent); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult("deleted " + in.Agent), nil, nil
	})
}

// publicAgent is the MCP wire shape of an agent. Every slice is non-nil so
// clients see [] rather than null.
type publicAgent struct {
	Ref            string   `json:"ref"`
	Namespace      string   `json:"namespace"`
	Name           string   `json:"name"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Instructions   string   `json:"instructions,omitempty"`
	Model          string   `json:"model"`
	Tools          []string `json:"tools"`
	EffectiveTools []string `json:"effective_tools"`
	Flight         string   `json:"flight"`
}

func toPublicAgent(a tapper.HubAgent, instructions bool) publicAgent {
	out := publicAgent{
		Ref: a.Ref, Namespace: a.Namespace, Name: a.Name, Title: a.Title, Description: a.Description,
		Model: a.Model, Tools: nonNil(a.Tools), EffectiveTools: nonNil(a.EffectiveTools), Flight: a.Flight,
	}
	if instructions {
		out.Instructions = a.Instructions
	}
	return out
}

func publicAgents(rows []tapper.HubAgent) []publicAgent {
	out := make([]publicAgent, 0, len(rows))
	for _, a := range rows {
		out = append(out, toPublicAgent(a, false))
	}
	return out
}

func agentResult(a *tapper.HubAgent) *sdkmcp.CallToolResult {
	var b strings.Builder
	fmt.Fprintf(&b, "agent: %s\n", a.Ref)
	if a.Title != "" {
		fmt.Fprintf(&b, "title: %s\n", a.Title)
	}
	if a.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", a.Description)
	}
	fmt.Fprintf(&b, "model: %s\n", a.Model)
	tools := "all"
	if len(a.Tools) > 0 {
		tools = strings.Join(a.Tools, ", ")
	}
	fmt.Fprintf(&b, "tools: %s\n", tools)
	flight := "(none; no KEG access)"
	if a.Flight != "" {
		flight = a.Flight
	}
	fmt.Fprintf(&b, "flight: %s\n", flight)
	if a.Instructions != "" {
		fmt.Fprintf(&b, "\n%s\n", a.Instructions)
	}
	res := textResult(b.String())
	res.StructuredContent = toPublicAgent(*a, true)
	return res
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
