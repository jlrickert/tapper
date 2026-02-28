package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewGraphCmd returns the `graph` cobra command.
//
// Usage examples:
//
//	tap graph
//	tap graph --keg pub --output graph.html
func NewGraphCmd(deps *Deps) *cobra.Command {
	var (
		opts       tapper.GraphOptions
		outputPath string
	)

	cmd := &cobra.Command{
		Use:   "graph",
		Short: "render an interactive keg graph as self-contained HTML",
		Long: `Render KEG nodes and relationships as a standalone HTML page.

The output includes both forward links and backlinks, and can be sent to stdout
or written to a file with --output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.BundleJS = graphBundle

			html, err := deps.Tap.Graph(cmd.Context(), opts)
			if err != nil {
				return err
			}

			if strings.TrimSpace(outputPath) == "" {
				_, err = fmt.Fprint(cmd.OutOrStdout(), html)
				return err
			}

			path := toolkit.ExpandEnv(deps.Runtime, outputPath)
			path, err = toolkit.ExpandPath(deps.Runtime, path)
			if err != nil {
				return fmt.Errorf("unable to resolve output path %q: %w", outputPath, err)
			}
			dir := filepath.Dir(path)
			if err := deps.Runtime.Mkdir(dir, 0o755, true); err != nil {
				return fmt.Errorf("unable to create output directory %q: %w", dir, err)
			}
			if err := deps.Runtime.AtomicWriteFile(path, []byte(html), 0o644); err != nil {
				return fmt.Errorf("unable to write output file %q: %w", path, err)
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "graph written to %s\n", path)
			return err
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "write graph HTML to file (default: stdout)")
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to visualize")

	return cmd
}
