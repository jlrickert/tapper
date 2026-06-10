package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewSnapshotCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "manage node snapshots",
		Long: `Manage snapshot history for a node.

Use "snapshot create" to capture the current node state, "snapshot history" to
list revisions, "snapshot view" to read a prior revision, and "snapshot restore"
to recover the current node from a prior revision.`,
		Example: strings.TrimSpace(`
tap snapshot create 12 --keg personal -m "before refactor"
tap snapshot history 12 --keg personal
tap snapshot view 12 1 --keg personal
tap snapshot restore 12 1 --keg personal
`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		NewSnapshotCreateCmd(deps),
		NewSnapshotHistoryCmd(deps),
		NewSnapshotViewCmd(deps),
		NewSnapshotRestoreCmd(deps),
	)
	return cmd
}

func NewSnapshotCreateCmd(deps *Deps) *cobra.Command {
	var opts tapper.NodeSnapshotOptions

	cmd := &cobra.Command{
		Use:   "create NODE_ID",
		Short: "create a snapshot for the current node state",
		Example: strings.TrimSpace(`
tap snapshot create 12 --keg personal -m "before refactor"
keg snapshot create 12 -m "before refactor"
`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.NodeID = args[0]
			snap, err := deps.Tap.NodeSnapshot(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%d\n", snap.ID)
			return err
		},
	}

	cmd.Flags().StringVarP(&opts.Message, "message", "m", "", "snapshot message")
	return cmd
}

func NewSnapshotHistoryCmd(deps *Deps) *cobra.Command {
	var opts tapper.NodeHistoryOptions

	cmd := &cobra.Command{
		Use:   "history NODE_ID",
		Short: "list snapshots for a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.NodeID = args[0]

			history, err := deps.Tap.NodeHistory(cmd.Context(), opts)
			if err != nil {
				return err
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "REV\tCREATED\tHASH\tMESSAGE")
			for _, snap := range history {
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n",
					snap.ID,
					snap.CreatedAt.Format("2006-01-02 15:04:05"),
					shortHash(snap.ContentHash),
					snap.Message,
				)
			}
			return tw.Flush()
		},
	}
	return cmd
}

func NewSnapshotViewCmd(deps *Deps) *cobra.Command {
	var opts tapper.NodeSnapshotViewOptions

	cmd := &cobra.Command{
		Use:   "view NODE_ID REV",
		Short: "view a read-only snapshot revision",
		Example: strings.TrimSpace(`
tap snapshot view 12 1 --keg personal
keg snapshot view 12 1
`),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.NodeID = args[0]
			opts.Rev = args[1]

			content, err := deps.Tap.NodeSnapshotView(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(content)
			return err
		},
	}
	return cmd
}

func NewSnapshotRestoreCmd(deps *Deps) *cobra.Command {
	var opts tapper.NodeRestoreOptions

	cmd := &cobra.Command{
		Use:   "restore NODE_ID REV",
		Short: "recover the current node from a prior snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.NodeID = args[0]
			opts.Rev = args[1]

			return deps.Tap.NodeRestore(cmd.Context(), opts)
		},
	}
	return cmd
}

func shortHash(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}
