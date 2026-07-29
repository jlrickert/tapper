package tapper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type Tap struct {
	Root string
	// Runtime carries process-level dependencies.
	Runtime *toolkit.Runtime

	PathService   *PathService
	ConfigService *ConfigService
	KegService    *KegService
	FlightService *FlightService

	// AuthValidateFn is the seam AuthStatus uses for its live whoami probe.
	// Defaulted to ValidateToken in NewTap; tests override it (or point the
	// hub at an httptest server) to avoid real network I/O. Both the CLI and
	// MCP surfaces share the same *Tap, so overriding it covers both.
	AuthValidateFn func(ctx context.Context, rt *toolkit.Runtime, hubURL, token string) (*WhoAmI, error)

	// KegResolver, when non-nil, overrides config-driven keg resolution. It is
	// the seam tapper-hub uses to serve a per-user MCP connector: the hub injects
	// a resolver that opens kegs from its own Postgres catalog and enforces the
	// caller's role, bypassing the on-disk config cascade and AuthStore entirely
	// (which, in the hub server's process, hold no login). role is the access
	// level the calling operation needs — FlightRoleViewer for reads and
	// FlightRoleEditor for writes — so the resolver can map it to a catalog role
	// check. Admin-class flight operations may use a different flight cap while
	// retaining an editor identity requirement. Every node read/write op funnels
	// through resolveKegForRole, so a
	// single resolver covers the whole surface. Left nil for the CLI, which keeps
	// the standard config-driven resolution.
	KegResolver func(ctx context.Context, opts KegTargetOptions, role FlightRole) (keg.Keg, error)

	// OrientationDetailsResolver is the hosted-MCP batch seam. Tapper Hub
	// injects a catalog-backed implementation so minimal keg_settings requests
	// can authorize and load several selected KEGs without loopback HTTP or
	// opening each Keg independently. Local and ordinary remote clients leave
	// it nil and use the standard config/hub resolution path.
	OrientationDetailsResolver func(ctx context.Context, refs []string) ([]HubOrientationDetail, error)
}

type TapOptions struct {
	Root       string
	ConfigPath string
	Runtime    *toolkit.Runtime
}

func NewTap(opts TapOptions) (*Tap, error) {
	rt := opts.Runtime
	if rt == nil {
		var err error
		rt, err = toolkit.NewRuntime()
		if err != nil {
			return nil, fmt.Errorf("unable to create runtime: %w", err)
		}
	}
	if err := rt.Validate(); err != nil {
		return nil, fmt.Errorf("invalid runtime: %w", err)
	}

	if opts.Root == "" {
		wd, err := rt.Getwd()
		if err != nil {
			return nil, fmt.Errorf("unable to determine working directory: %w", err)
		}
		opts.Root = wd
	}
	pathService, err := NewPathService(rt, opts.Root)
	if err != nil {
		return nil, fmt.Errorf("unable to create path service: %w", err)
	}
	configService := &ConfigService{
		Runtime:     rt,
		PathService: pathService,
		ConfigPath:  opts.ConfigPath,
	}
	kegService := &KegService{
		Runtime:       rt,
		ConfigService: configService,
	}
	flightService := &FlightService{
		Runtime:       rt,
		ConfigService: configService,
		KegService:    kegService,
	}
	return &Tap{
		Runtime:        rt,
		Root:           opts.Root,
		PathService:    pathService,
		ConfigService:  configService,
		KegService:     kegService,
		FlightService:  flightService,
		AuthValidateFn: ValidateToken,
	}, nil
}

// KegTargetOptions describes how a command should resolve a keg target.
type KegTargetOptions struct {
	// Keg is the keg selector: a bare name or an @namespace/keg reference.
	Keg string

	// Namespace overrides the namespace the keg resolves in when Keg is a bare
	// name (it loses to an @namespace/ already present in Keg). Empty means use
	// the configured defaultNamespace/fallbackNamespace chain.
	Namespace string

	// Hub pins the hub the keg resolves on, overriding namespace→hub resolution.
	// Empty means resolve the hub from the namespace as usual.
	Hub string

	// Project resolves using project-local keg discovery. Not exposed as a tap
	// flag; retained for the pruned `keg` binary (ForceProjectResolution) and
	// the keg-create destination flags.
	Project bool

	// Cwd resolves project keg at the current working directory instead of git root.
	// Works standalone or combined with Project.
	Cwd bool

	// Path is an explicit local project path used for project keg discovery.
	Path string

	// Flight is optional task context that can restrict which kegs are available
	// and injects agent instructions. It composes with the single-keg selectors
	// (Keg/Namespace/Hub): the selector picks a keg and the flight gates it unless
	// BypassFlightRestrictions is true.
	Flight string

	// FlightContext is the immutable, already-resolved flight snapshot used by
	// an MCP session. When set it is authoritative over Flight and avoids
	// consulting the process-wide flight cache while a tool call is running.
	// Direct CLI callers leave this nil.
	FlightContext *Flight

	// BypassFlightRestrictions skips flight cover and role-cap checks while
	// preserving Flight for callers that still need the flight context, such as
	// orient. Leave false for MCP and other agent-facing surfaces.
	BypassFlightRestrictions bool

	// RequireBootstrap makes config-driven resolution fail with
	// ErrNotBootstrapped when no user config exists. Set by the full `tap`
	// surface and the MCP server; left false by the pruned `keg` binary and by
	// direct Tap API callers (e.g. tests).
	RequireBootstrap bool
}

func (t *Tap) LookupKeg(ctx context.Context, kegAlias string) (keg.Keg, error) {
	k, err := t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:    t.Root,
		Keg:     kegAlias,
		NoCache: false,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	return k, nil
}

func (t *Tap) resolveKeg(ctx context.Context, opts KegTargetOptions) (keg.Keg, error) {
	return t.resolveKegForRole(ctx, opts, FlightRoleViewer)
}

func (t *Tap) resolveKegForRole(ctx context.Context, opts KegTargetOptions, role FlightRole) (keg.Keg, error) {
	return t.resolveKegForRoles(ctx, opts, role, role)
}

// resolveKegForRoles resolves a keg with independent identity and flight
// requirements. Most operations use matching roles through resolveKegForRole;
// admin-class agent operations can require a stronger flight cap without
// expanding the authenticated identity's underlying KEG access.
func (t *Tap) resolveKegForRoles(ctx context.Context, opts KegTargetOptions, identityRole, flightRole FlightRole) (keg.Keg, error) {
	// A hub-injected resolver owns resolution and authorization end-to-end (it
	// opens a catalog-backed keg.Keg and applies its own role check), so the
	// config cascade and flight gating below are bypassed when one is set.
	if t.KegResolver != nil {
		k, err := t.KegResolver(ctx, opts, identityRole)
		if err != nil {
			return nil, err
		}
		if !opts.BypassFlightRestrictions && opts.FlightContext != nil {
			if err := t.enforceFlightSnapshot(opts.FlightContext, k, flightRole); err != nil {
				return nil, err
			}
		}
		return k, nil
	}
	k, err := t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:             t.Root,
		Keg:              opts.Keg,
		Namespace:        opts.Namespace,
		Hub:              opts.Hub,
		Project:          opts.Project,
		Cwd:              opts.Cwd,
		Path:             opts.Path,
		RequireBootstrap: opts.RequireBootstrap,
		NoCache:          false,
	})
	if err != nil {
		return nil, err
	}
	// An active flight restricts which kegs are available unless the caller is a
	// direct CLI surface that keeps Flight only for context/instructions.
	if !opts.BypassFlightRestrictions {
		if opts.FlightContext != nil {
			err = t.enforceFlightSnapshot(opts.FlightContext, k, flightRole)
		} else {
			err = t.enforceFlight(ctx, opts.Flight, k, flightRole)
		}
		if err != nil {
			return nil, err
		}
	}
	return k, nil
}

func newEditorTempFilePath(rt *toolkit.Runtime, prefix string, suffix string) (string, error) {
	base := ""
	if strings.TrimSpace(rt.GetJail()) != "" {
		if home, err := rt.GetHome(); err == nil && strings.TrimSpace(home) != "" {
			base = filepath.Join(home, ".cache", "tapper", "tmp")
		} else {
			base = "/tmp"
		}
	} else {
		base = strings.TrimSpace(rt.GetTempDir())
		if base == "" {
			base = os.TempDir()
		}
	}

	expanded := toolkit.ExpandEnv(rt, base)
	if p, err := toolkit.ExpandPath(rt, expanded); err == nil {
		expanded = p
	}

	if err := rt.Mkdir(expanded, 0o755, true); err != nil {
		return "", err
	}

	for i := 0; i < 64; i++ {
		path := filepath.Join(expanded,
			fmt.Sprintf("%s%d-%02d%s", prefix, rt.Clock().Now().UnixNano(), i, suffix))
		if _, err := rt.Stat(path, false); err == nil {
			continue
		} else if os.IsNotExist(err) {
			return path, nil
		} else {
			return "", err
		}
	}
	return "", fmt.Errorf("unable to allocate temp file path")
}
