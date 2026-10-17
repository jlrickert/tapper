package mcp

import (
	"context"
	"fmt"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerLockTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerLockAcquire(srv, tap, defaults)
	registerLockRelease(srv, tap, defaults)
	registerLockStatus(srv, tap, defaults)
	registerLockForceRelease(srv, tap, defaults)
}

// --- lock_acquire ---

type lockAcquireInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to lock."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerLockAcquire(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "lock_acquire",
		Description:  "Memory: Acquire a Hub-held lock on a node and return its token.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in lockAcquireInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.LockOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		token, err := tap.Lock(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(token)), nil, nil
	})
}

// --- lock_release ---

type lockReleaseInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to unlock."`
	Token  string `json:"token" jsonschema:"Lock token returned by lock_acquire."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerLockRelease(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "lock_release",
		Description:  "Memory: Release a lock on a node.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in lockReleaseInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.UnlockOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Token:            in.Token,
		}
		if err := tap.Unlock(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("node %s unlocked", in.NodeID)), nil, nil
	})
}

// --- lock_status ---

type lockStatusInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to check."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerLockStatus(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "lock_status",
		Description:  "Memory: Check a node's lock state.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in lockStatusInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.LockStatusOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		info, err := tap.LockStatus(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if info.Token == "" {
			return textResult("unlocked"), nil, nil
		}
		return textResult(fmt.Sprintf(
			"locked\ntoken: %s\nholder: %s\nacquired: %s\nttl: %ds",
			info.Token, info.Holder,
			info.AcquiredAt.Format(time.RFC3339),
			info.TTLSeconds,
		)), nil, nil
	})
}

// --- lock_force_release ---

type lockForceReleaseInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to force-unlock."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerLockForceRelease(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "lock_force_release",
		Description:  "Manage: Remove a lock on a node unconditionally.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in lockForceReleaseInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ForceUnlockOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		if err := tap.ForceUnlock(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("node %s force-unlocked", in.NodeID)), nil, nil
	})
}
