package tapper

import (
	"context"
	"fmt"
)

// RemoveRepoOptions configures which keg alias to remove.
type RemoveRepoOptions struct {
	// Alias is the keg alias to remove from the user config.
	Alias string

	// Force allows removing an alias that is currently set as the
	// defaultKeg or fallbackKeg in the user config.
	Force bool
}

// RemoveRepo removes a registered keg alias from the user configuration.
//
// Safety checks (bypassed with Force):
//   - Refuses to remove the configured defaultKeg alias without --force.
//   - Refuses to remove the configured fallbackKeg alias without --force.
func (t *Tap) RemoveRepo(ctx context.Context, opts RemoveRepoOptions) error {
	if opts.Alias == "" {
		return fmt.Errorf("alias is required")
	}

	userCfg, err := t.ConfigService.UserConfig(false)
	if err != nil {
		return fmt.Errorf("unable to load user config: %w", err)
	}

	// Safety: block removal of the default keg unless --force is given.
	if !opts.Force {
		if def := userCfg.DefaultKeg(); def == opts.Alias {
			return fmt.Errorf(
				"alias %q is the defaultKeg; use --force to remove it (consider setting a new default first with `tap repo config --default`)",
				opts.Alias,
			)
		}
		if fb := userCfg.FallbackKeg(); fb == opts.Alias {
			return fmt.Errorf(
				"alias %q is the fallbackKeg; use --force to remove it (consider setting a new fallback first with `tap repo config --fallback`)",
				opts.Alias,
			)
		}
	}

	if err := userCfg.RemoveKeg(opts.Alias); err != nil {
		return err
	}

	if err := userCfg.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
		return fmt.Errorf("unable to save user config: %w", err)
	}

	t.ConfigService.ResetCache()
	return nil
}
