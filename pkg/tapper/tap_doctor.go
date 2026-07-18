package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

// DoctorOptions configures behavior for Tap.Doctor.
type DoctorOptions struct {
	KegTargetOptions
}

// Issue represents a single problem found during a doctor check.
type Issue = keg.DoctorIssue

// DoctorConfig validates the tapper configuration (not keg-level) and returns
// issues. This does not require a keg to be resolved.
func (t *Tap) DoctorConfig() []Issue {
	var issues []Issue

	// Report any config load warnings.
	for _, w := range t.ConfigService.LoadWarnings {
		issues = append(issues, Issue{
			Level:   "warning",
			Kind:    "config-load",
			Message: w.Message,
		})
	}

	// Run semantic validation on the merged config.
	cfg, err := t.ConfigService.Config(true)
	if err != nil {
		issues = append(issues, Issue{
			Level:   "error",
			Kind:    "config",
			Message: fmt.Sprintf("unable to load merged config: %v", err),
		})
		return issues
	}

	for _, w := range ValidateConfig(cfg) {
		issues = append(issues, Issue{
			Level:   "warning",
			Kind:    "config-validate",
			Message: fmt.Sprintf("%s: %s", w.Field, w.Message),
		})
	}

	return issues
}

// Doctor scans the resolved keg and reports health issues.
func (t *Tap) Doctor(ctx context.Context, opts DoctorOptions) ([]Issue, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	issues, err := k.Doctor(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to inspect keg: %w", err)
	}
	return issues, nil
}
