package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewRepoConfigCmd returns the `repo config` cobra command.
//
// Usage examples:
//
//	tap repo config
//	tap repo config --project
//	tap repo config --user
//	tap repo config --explain defaultKeg
//	tap repo config --show-sources
//	tap repo config template user
//	tap repo config template project
//	tap repo config edit
//	tap repo config edit --project
func NewRepoConfigCmd(deps *Deps) *cobra.Command {
	var opts tapper.ConfigOptions
	var explainField string
	var showSources bool

	cmd := &cobra.Command{
		Use:   "config",
		Short: "display tap configuration",
		Long: `Display the merged tap configuration (user + project).

Use 'tap repo config edit' to modify configuration files.
Use '--project' to view only project configuration.
Use '--explain FIELD' to show which source set a field value.
Use '--show-sources' to show all fields with their source.
Use 'tap repo config template {user|project}' to print starter config.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --explain and --show-sources take precedence over normal display.
			if showSources || explainField != "" {
				explainOpts := tapper.ConfigExplainOptions{
					Field: explainField,
				}
				results, err := deps.Tap.ConfigExplain(cmd.Context(), explainOpts)
				if err != nil {
					return err
				}
				if showSources {
					return printShowSources(cmd, results)
				}
				return printExplain(cmd, results)
			}

			opts.ConfigPath = deps.ConfigPath
			output, err := deps.Tap.Config(opts)
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("no configuration available: %w", err)
			}
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	cmd.Flags().BoolVar(&opts.Project, "project", false, "display project configuration")
	cmd.Flags().BoolVar(&opts.User, "user", false, "display user configuration")
	cmd.Flags().StringVar(&explainField, "explain", "", "show provenance for a specific config field")
	cmd.Flags().BoolVar(&showSources, "show-sources", false, "show all fields with their resolved source")

	mustRegisterFlagCompletion(cmd, "explain", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		var out []string
		for _, f := range tapper.ConfigExplainFields {
			if strings.HasPrefix(f, toComplete) {
				out = append(out, f)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})

	cmd.AddCommand(NewRepoConfigTemplateCmd(deps))
	cmd.AddCommand(NewRepoConfigEditCmd(deps))

	return cmd
}

// printExplain formats --explain output for a single field (or all fields if
// multiple results are returned).
func printExplain(cmd *cobra.Command, results []tapper.ConfigExplainResult) error {
	var sb strings.Builder
	for _, r := range results {
		val := r.Value
		if val == "" {
			val = `""`
		}
		sb.WriteString(fmt.Sprintf("%s = %s\n", r.Field, val))
		sb.WriteString(fmt.Sprintf("  source: %s\n", r.Source))
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), sb.String())
	return err
}

// printShowSources formats --show-sources output: all fields with their source annotation.
func printShowSources(cmd *cobra.Command, results []tapper.ConfigExplainResult) error {
	var sb strings.Builder
	for _, r := range results {
		val := r.Value
		if val == "" {
			val = `""`
		}
		sb.WriteString(fmt.Sprintf("%s = %s  [%s]\n", r.Field, val, r.Source))
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), sb.String())
	return err
}
