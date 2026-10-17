package mcp

import (
	"context"
	"encoding/json"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// Some arguments were dropped from the tool schemas because another argument
// already says the same thing. The schemas forbid properties they do not
// list, so a client written against an older schema would fail validation the
// moment it sent one. legacyArgumentsMiddleware rewrites them into their
// replacement before the SDK validates the call, so those clients keep
// working without the old names reappearing in any schema an agent reads.
//
//   - id_only on node_list, node_grep, tag_list, node_backlinks, and
//     node_links is format "%i", which prints the same lines. A true id_only
//     wins over any format sent with it, as it always did.
//   - keg on node_search named the one KEG to search. kegs now names any
//     number of them, so a legacy keg is searched as if it were the only
//     entry in kegs (see legacySearchKeg).

// idOnlyTools are the listing tools that accepted id_only.
var idOnlyTools = map[string]bool{
	"node_list": true, "node_grep": true, "tag_list": true, "node_backlinks": true, "node_links": true,
}

type legacySearchKegKey struct{}

// legacySearchKeg returns the keg a pre-kegs node_search call named, or "".
// It travels outside the arguments because a bare name ("notes", meaning the
// caller's default namespace) is not a valid kegs entry; node_search resolves
// it the way the keg argument always was.
func legacySearchKeg(ctx context.Context) string {
	keg, _ := ctx.Value(legacySearchKegKey{}).(string)
	return keg
}

func legacyArgumentsMiddleware(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		params, _ := req.GetParams().(*sdkmcp.CallToolParamsRaw)
		if params == nil || len(params.Arguments) == 0 || IsRelayedToolName(params.Name) {
			return next(ctx, method, req)
		}
		switch {
		case idOnlyTools[params.Name]:
			rewriteArguments(params, func(args map[string]json.RawMessage) {
				raw, ok := args["id_only"]
				if !ok {
					return
				}
				delete(args, "id_only")
				var idOnly bool
				if json.Unmarshal(raw, &idOnly) == nil && idOnly {
					args["format"], _ = json.Marshal(tapper.IDOnlyFormat)
				}
			})
		case params.Name == "node_search":
			rewriteArguments(params, func(args map[string]json.RawMessage) {
				raw, ok := args["keg"]
				if !ok {
					return
				}
				delete(args, "keg")
				var keg string
				if json.Unmarshal(raw, &keg) == nil && strings.TrimSpace(keg) != "" {
					ctx = context.WithValue(ctx, legacySearchKegKey{}, strings.TrimSpace(keg))
				}
			})
		}
		return next(ctx, method, req)
	}
}

// rewriteArguments applies edit to the call's arguments when they decode as
// an object, and leaves anything else for the SDK to refuse.
func rewriteArguments(params *sdkmcp.CallToolParamsRaw, edit func(map[string]json.RawMessage)) {
	var args map[string]json.RawMessage
	if json.Unmarshal(params.Arguments, &args) != nil || args == nil {
		return
	}
	edit(args)
	if raw, err := json.Marshal(args); err == nil {
		params.Arguments = raw
	}
}
