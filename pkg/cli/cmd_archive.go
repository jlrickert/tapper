package cli

import (
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewArchiveCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "export keg archives",
		Long:  `Export nodes to a tar archive.`,
	}

	cmd.AddCommand(
		NewArchiveExportCmd(deps),
	)
	return cmd
}

func NewArchiveExportCmd(deps *Deps) *cobra.Command {
	var opts tapper.ExportOptions
	var rawNodes string
	var noHistory bool

	opts.WithHistory = true

	cmd := &cobra.Command{
		Use:   "export",
		Short: "export nodes to a keg archive",
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.NodeIDs = splitArchiveNodeList(rawNodes)
			opts.WithHistory = !noHistory
			path, err := deps.Tap.Export(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), path)
			return err
		},
	}

	cmd.Flags().StringVar(&rawNodes, "nodes", "", "comma-separated node IDs to export (default all nodes)")
	cmd.Flags().BoolVar(&noHistory, "no-history", false, "omit snapshot history from the archive")
	cmd.Flags().StringVarP(&opts.OutputPath, "output", "o", "", "archive output path")
	_ = cmd.MarkFlagRequired("output")
	_ = cmd.MarkFlagFilename("output", "tar", "tar.gz", "tgz", "gz")
	return cmd
}

func splitArchiveNodeList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
