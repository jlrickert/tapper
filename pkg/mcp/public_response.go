package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// publicResponseMiddleware renders text from the public structured payload.
// Existing keys are preserved. Legacy prose (including diagnostics and next
// steps) lives in message, so clients never need to join two representations.
// Binary attachments stay in their MCP content blocks, outside the JSON.
func publicResponseMiddleware(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		result, err := next(ctx, method, req)
		if err != nil || method != "tools/call" {
			return result, err
		}
		call, ok := result.(*sdkmcp.CallToolResult)
		if !ok || call == nil {
			return result, nil
		}
		out, err := publicToolResponse(call)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}

func publicToolResponse(call *sdkmcp.CallToolResult) (*sdkmcp.CallToolResult, error) {
	payload := map[string]any{}
	if call.StructuredContent != nil {
		raw, err := json.Marshal(call.StructuredContent)
		if err != nil {
			return nil, fmt.Errorf("encode public MCP response: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			return nil, fmt.Errorf("decode public MCP response: %w", err)
		}
	}
	var messages []string
	attachments := []sdkmcp.Content{}
	for _, block := range call.Content {
		if text, ok := block.(*sdkmcp.TextContent); ok {
			messages = append(messages, text.Text)
		} else {
			attachments = append(attachments, block)
		}
	}
	if _, exists := payload["message"]; !exists && len(messages) > 0 {
		payload["message"] = strings.Join(messages, "\n")
	}
	if call.IsError && call.StructuredContent == nil {
		payload["code"] = "INTERNAL"
		payload["operationPerformed"] = nil
		payload["action"] = "Inspect the current resource before retrying; the outcome is unknown."
		if strings.HasPrefix(strings.Join(messages, "\n"), "validating ") {
			payload["code"] = "INVALID_ARGUMENT"
			payload["operationPerformed"] = false
			payload["action"] = "Correct the arguments named in message using this tool's input schema, then retry."
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("render public MCP response: %w", err)
	}
	out := *call
	out.StructuredContent = payload
	out.Content = append([]sdkmcp.Content{&sdkmcp.TextContent{Text: string(raw)}}, attachments...)
	return &out, nil
}

// Offset listings have no stable cursor or total in the service result. Do not
// invent a completion claim from rendered line counts (grep spans many lines).
// A nonempty bounded page supplies a safe next request; an empty page ends it.
func pagedLinesResult(lines []string, offset, limit int) *sdkmcp.CallToolResult {
	result := linesResult(lines)
	var next any
	var more any = false
	if limit > 0 && len(lines) > 0 {
		next = offset + limit
		more = nil
	}
	result.StructuredContent = map[string]any{
		"lines": append([]string{}, lines...), "offset": offset, "limit": limit,
		"next_offset": next, "has_more": more,
		"pagination": "Repeat the same query with next_offset as offset until next_offset is null. A final nonempty page may be followed by an empty page. Pages are live, not a stable snapshot; reverse reverses each page only.",
	}
	return result
}
