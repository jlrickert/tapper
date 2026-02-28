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
		Short: "import and export keg archives",
	}

	cmd.AddCommand(
		NewArchiveExportCmd(deps),
		NewArchiveImportCmd(deps),
	)
	return cmd
}

func NewArchiveExportCmd(deps *Deps) *cobra.Command {
	var opts tapper.ExportOptions
	var rawNodes string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "export nodes to a keg archive",
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.NodeIDs = splitArchiveNodeList(rawNodes)
			path, err := deps.Tap.Export(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), path)
			return err
		},
	}

	cmd.Flags().StringVar(&rawNodes, "nodes", "", "comma-separated node IDs to export (default all nodes)")
	cmd.Flags().BoolVar(&opts.WithHistory, "with-history", false, "include snapshot history")
	cmd.Flags().StringVarP(&opts.OutputPath, "output", "o", "", "archive output path")
	_ = cmd.MarkFlagRequired("output")
	_ = cmd.MarkFlagFilename("output", "tar", "tar.gz", "tgz", "gz")
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to export from")
	return cmd
}

func NewArchiveImportCmd(deps *Deps) *cobra.Command {
	var opts tapper.ImportOptions

	cmd := &cobra.Command{
		Use:   "import ARCHIVE",
		Short: "import nodes from a keg archive",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return nil, cobra.ShellCompDirectiveDefault
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.Input = args[0]
			imported, err := deps.Tap.Import(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, id := range imported {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), id.Path()); err != nil {
					return err
				}
			}
			return nil
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to import into")
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
