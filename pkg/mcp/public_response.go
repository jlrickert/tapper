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
		// A relayed tool's result is its own server's, passed through as sent.
		if params, _ := req.GetParams().(*sdkmcp.CallToolParamsRaw); params != nil && IsRelayedToolName(params.Name) {
			return result, nil
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
// invent a completion claim from rendered line counts (node_grep spans many lines).
// A nonempty bounded page supplies a safe next request; an empty page ends it.
func pagedLinesResult(lines []string, offset, limit int) *sdkmcp.CallToolResult {
	result := linesResult(lines)
	out := pagedLinesOutput{
		Lines: append([]string{}, lines...), Offset: offset, Limit: limit,
		HasMore:    new(bool),
		Pagination: "Repeat the same query with next_offset as offset until next_offset is null. A final nonempty page may be followed by an empty page. Pages are live, not a stable snapshot; reverse reverses each page only.",
	}
	if limit > 0 && len(lines) > 0 {
		next := offset + limit
		out.NextOffset, out.HasMore = &next, nil
	}
	result.StructuredContent = out
	return result
}

// pagedLinesOutput is the structured content of the offset-paged listing
// tools: node_list, node_grep, tag_list, node_backlinks, and node_links.
type pagedLinesOutput struct {
	Lines      []string `json:"lines" jsonschema:"The page's lines, one per node rendered by format. tag_list without a query lists tags; node_grep without format follows each node with its matched lines."`
	Offset     int      `json:"offset" jsonschema:"The offset this page starts at."`
	Limit      int      `json:"limit" jsonschema:"The page size applied; 0 means unlimited."`
	NextOffset *int     `json:"next_offset" jsonschema:"The offset of the next page, or null when no page follows."`
	HasMore    *bool    `json:"has_more" jsonschema:"False when no page follows; null when one may."`
	Pagination string   `json:"pagination" jsonschema:"How to page this listing."`
}

// errorResultMiddleware sends a failed tool result without structured
// content. A tool's outputSchema describes its success, and clients that
// validate results (the TypeScript SDK among them) check structuredContent
// against it even when isError is set, so an error payload there turns a
// readable tool error into a JSON-RPC -32602. The payload travels as the
// result's leading JSON text block instead, which publicToolResponse
// already renders; a relayed failure that carries structured content but no
// JSON text gets one rendered from it.
func errorResultMiddleware(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		result, err := next(ctx, method, req)
		if err != nil || method != "tools/call" {
			return result, err
		}
		call, ok := result.(*sdkmcp.CallToolResult)
		if !ok || call == nil || !call.IsError || call.StructuredContent == nil {
			return result, nil
		}
		out := *call
		if !leadsWithJSONObject(call.Content) {
			raw, err := json.Marshal(call.StructuredContent)
			if err != nil {
				return nil, fmt.Errorf("render MCP error payload: %w", err)
			}
			out.Content = append([]sdkmcp.Content{&sdkmcp.TextContent{Text: string(raw)}}, call.Content...)
		}
		out.StructuredContent = nil
		return &out, nil
	}
}

// leadsWithJSONObject reports whether content opens with a text block that
// holds a JSON object.
func leadsWithJSONObject(content []sdkmcp.Content) bool {
	if len(content) == 0 {
		return false
	}
	text, ok := content[0].(*sdkmcp.TextContent)
	if !ok {
		return false
	}
	trimmed := strings.TrimSpace(text.Text)
	return strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed))
}
