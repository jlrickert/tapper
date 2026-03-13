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
	NodeID string `json:"node_id" jsonschema:"node ID to lock"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerLockAcquire(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "lock_acquire",
		Description: "Acquire a cross-process lock on a node and return the token",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in lockAcquireInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.LockOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
	NodeID string `json:"node_id" jsonschema:"node ID to unlock"`
	Token  string `json:"token" jsonschema:"lock token returned by lock_acquire"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerLockRelease(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "lock_release",
		Description: "Release a cross-process lock on a node",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in lockReleaseInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.UnlockOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
	NodeID string `json:"node_id" jsonschema:"node ID to check"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerLockStatus(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "lock_status",
		Description: "Check the lock state of a node",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in lockStatusInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.LockStatusOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
	NodeID string `json:"node_id" jsonschema:"node ID to force-unlock"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerLockForceRelease(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "lock_force_release",
		Description: "Unconditionally remove a lock on a node",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in lockForceReleaseInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ForceUnlockOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		if err := tap.ForceUnlock(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("node %s force-unlocked", in.NodeID)), nil, nil
	})
}
