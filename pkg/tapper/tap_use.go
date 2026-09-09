package tapper

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// UseOptions configures Tap.Use, the `tap use` setter that records which keg
// (and flight) a project resolves, or a user-wide user KEG.
//
// Scope picks the keg slot: the default (project) scope writes keg; the
// user scope (User=true) writes keg. Flight is project-scoped only.
type UseOptions struct {
	// Keg is the keg reference to record (a bare name or @namespace/keg). Empty
	// leaves the keg slot untouched (unless Clear is set).
	Keg string
	// Flight is a flight reference to record for the project (@namespace/+slug,
	// +slug, or a bare slug). Empty leaves the flight untouched.
	Flight string
	// User writes the user config's keg instead of the project config's
	// keg.
	User bool
	// ConfigPath, when set, writes that explicit config file instead of the
	// user/project file. The slot still follows User.
	ConfigPath string
	// Clear unsets the slot(s) for the chosen scope (keg + flight for the
	// project scope; keg for the user scope).
	Clear bool
}

// Use records the project's keg + flight, or the user-wide user KEG, in the
// appropriate config file. It mirrors the resolution convention: project and user both write keg in their own scope, with flight project-scoped.
func (t *Tap) Use(ctx context.Context, opts UseOptions) error {
	kegRef := strings.TrimSpace(opts.Keg)
	flight := strings.TrimSpace(opts.Flight)
	if flight != "" && opts.User {
		return fmt.Errorf("flight is project-scoped; drop --user to set a project flight")
	}
	if !opts.Clear && kegRef == "" && flight == "" {
		return fmt.Errorf("nothing to set: pass a keg reference, --flight, or --clear")
	}

	// Normalize the flight reference to its canonical +slug form, preserving an
	// explicit namespace but leaving a bare slug namespace-free for read-time
	// resolution.
	var flightVal string
	if flight != "" {
		ref, err := ParseFlightRef(flight, "")
		if err != nil {
			return err
		}
		flightVal = ref.Canonical()
	}

	path := t.PathService.ProjectConfig()
	if cp := strings.TrimSpace(opts.ConfigPath); cp != "" {
		path = cp
	} else if opts.User {
		path = t.PathService.UserConfig()
	}

	return t.mutateConfigFile(path, func(c *Config) error {
		if opts.Clear {
			if opts.User {
				_ = c.SetKeg("")
			} else {
				_ = c.SetKeg("")
				_ = c.SetFlight("")
			}
		}
		if kegRef != "" {
			_ = c.SetKeg(kegRef)
		}
		if flightVal != "" {
			_ = c.SetFlight(flightVal)
		}
		c.Touch(t.Runtime)
		return nil
	})
}

// UseStatus returns a YAML summary of the resolved keg/flight context plus the
// configured keg slots and the scope that set each, for `tap use` with no args.
func (t *Tap) UseStatus(_ context.Context, opts KegTargetOptions) (string, error) {
	type slot struct {
		Value string `yaml:"value,omitempty"`
		Scope string `yaml:"scope,omitempty"`
	}
	type status struct {
		Resolved resolvedIdentity `yaml:"resolved"`
		Keg      slot             `yaml:"keg"`
		Flight   slot             `yaml:"flight"`
	}

	out := status{Resolved: t.resolveIdentity(opts)}
	if cfg, err := t.ConfigService.Config(); err == nil && cfg != nil {
		out.Keg = slot{Value: cfg.Keg(), Scope: t.configFieldScope("keg")}
		if _, ok := cfg.LookupMapping(t.Runtime, t.ConfigService.PathService.Root); ok && t.Runtime.Get("TAP_KEG") == "" {
			out.Keg.Scope = "kegMap overrides top-level keg"
		}
		out.Flight = slot{Value: cfg.Flight(), Scope: t.configFieldScope("flight")}
	}

	b, err := yaml.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("unable to marshal use status: %w", err)
	}
	return string(b), nil
}
