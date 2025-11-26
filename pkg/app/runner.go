// Package app provides the Runner used to execute commands and to manage
// project-scoped resources used by the CLI and other application code.
package app

import (
	"context"
	"fmt"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tap"
)

// Runner holds configuration and cached resources used to drive the
// application. The Runner caches a *tap.TapProject so multiple invocations
// can reuse the same project instance.
type Runner struct {
	Root string

	project *tap.TapContext
}

func NewRunnerFromWd(ctx context.Context) (*Runner, error) {
	env := toolkit.EnvFromContext(ctx)
	wd, err := env.Getwd()
	if err != nil {
		return nil, err
	}
	return &Runner{Root: wd}, nil
}

// GetTapContext returns the cached *TapProject when available. If no project is
// cached this constructs a new TapProject using the Runner Root as the
// project root, caches it on the Runner, and returns it. Any error creating
// the project is wrapped to provide context to callers.
func (r *Runner) GetTapContext(ctx context.Context) (*tap.TapContext, error) {
	if r.project != nil {
		return r.project, nil
	}
	project, err := tap.NewTapContext(ctx, r.Root)
	if err != nil {
		return nil, fmt.Errorf("unable to create project: %w", err)
	}
	r.project = project
	return r.project, nil
}
