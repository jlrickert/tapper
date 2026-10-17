// Package mcptest holds test helpers for MCP servers built on package mcp.
package mcptest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ValidatingClient returns an MCP client that checks every tool result the
// way a strict client does, and fails t when one would be rejected. The
// TypeScript SDK, for one, validates a result's structuredContent against the
// tool's declared outputSchema whether or not isError is set, and refuses a
// successful result that declares an outputSchema but carries no structured
// content; either one reaches the caller as a JSON-RPC error rather than the
// tool's answer.
//
// Schemas come from the session's own tool listing, fetched on the first call
// to a tool the client has not seen listed, so the client checks whatever
// server it connects to.
func ValidatingClient(t testing.TB, impl *sdkmcp.Implementation, opts *sdkmcp.ClientOptions) *sdkmcp.Client {
	t.Helper()
	client := sdkmcp.NewClient(impl, opts)
	v := &validator{t: t, schemas: map[*sdkmcp.ClientSession]map[string]*jsonschema.Resolved{}}
	client.AddSendingMiddleware(v.middleware)
	return client
}

type validator struct {
	t       testing.TB
	mu      sync.Mutex
	schemas map[*sdkmcp.ClientSession]map[string]*jsonschema.Resolved
}

func (v *validator) middleware(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		result, err := next(ctx, method, req)
		if err != nil {
			return result, err
		}
		session, _ := req.GetSession().(*sdkmcp.ClientSession)
		switch method {
		case "tools/list":
			if listed, ok := result.(*sdkmcp.ListToolsResult); ok && listed != nil {
				v.learn(session, listed.Tools)
			}
		case "tools/call":
			params, _ := req.GetParams().(*sdkmcp.CallToolParams)
			call, ok := result.(*sdkmcp.CallToolResult)
			if ok && call != nil && params != nil {
				v.check(ctx, session, params.Name, call)
			}
		}
		return result, err
	}
}

// check fails t when a strict client would reject call.
func (v *validator) check(ctx context.Context, session *sdkmcp.ClientSession, tool string, call *sdkmcp.CallToolResult) {
	schema := v.schema(ctx, session, tool)
	if schema == nil {
		return
	}
	if call.StructuredContent == nil {
		if !call.IsError {
			v.t.Errorf("%s declares an outputSchema but returned no structured content", tool)
		}
		return
	}
	if err := validate(schema, call.StructuredContent); err != nil {
		v.t.Errorf("%s result (isError=%t) does not match its outputSchema: %v\nstructuredContent: %v", tool, call.IsError, err, call.StructuredContent)
	}
}

// schema returns tool's resolved outputSchema, listing the session's tools
// when it has not seen tool yet. A tool without an outputSchema, or one the
// listing does not offer, has none.
func (v *validator) schema(ctx context.Context, session *sdkmcp.ClientSession, tool string) *jsonschema.Resolved {
	if session == nil {
		return nil
	}
	v.mu.Lock()
	known, seen := v.schemas[session][tool]
	v.mu.Unlock()
	if seen {
		return known
	}
	var tools []*sdkmcp.Tool
	for listed, err := range session.Tools(ctx, nil) {
		if err != nil {
			v.t.Errorf("list tools to validate %s: %v", tool, err)
			return nil
		}
		tools = append(tools, listed)
	}
	v.learn(session, tools)
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.schemas[session][tool]
}

// learn records the outputSchemas of listed tools for session.
func (v *validator) learn(session *sdkmcp.ClientSession, tools []*sdkmcp.Tool) {
	if session == nil {
		return
	}
	resolved := make(map[string]*jsonschema.Resolved, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		schema, err := resolve(tool.OutputSchema)
		if err != nil {
			v.t.Errorf("%s outputSchema: %v", tool.Name, err)
			continue
		}
		resolved[tool.Name] = schema
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.schemas[session] == nil {
		v.schemas[session] = map[string]*jsonschema.Resolved{}
	}
	for name, schema := range resolved {
		v.schemas[session][name] = schema
	}
}

// resolve parses an outputSchema as a client receives it; nil means the tool
// declares none.
func resolve(outputSchema any) (*jsonschema.Resolved, error) {
	if outputSchema == nil {
		return nil, nil
	}
	raw, err := json.Marshal(outputSchema)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return schema.Resolve(nil)
}

// validate checks structured content as a client decodes it off the wire.
func validate(schema *jsonschema.Resolved, structured any) error {
	raw, err := json.Marshal(structured)
	if err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return err
	}
	return schema.Validate(instance)
}
