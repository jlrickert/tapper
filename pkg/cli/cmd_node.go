package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewNodeCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "manage node history",
	}

	cmd.AddCommand(
		NewNodeHistoryCmd(deps),
		NewNodeSnapshotCmd(deps),
		NewNodeRestoreCmd(deps),
	)
	return cmd
}

func NewNodeHistoryCmd(deps *Deps) *cobra.Command {
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

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg containing the node")
	return cmd
}

func NewNodeSnapshotCmd(deps *Deps) *cobra.Command {
	var opts tapper.NodeSnapshotOptions

	cmd := &cobra.Command{
		Use:   "snapshot NODE_ID",
		Short: "append a snapshot for the current node state",
		Args:  cobra.ExactArgs(1),
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
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg containing the node")
	return cmd
}

func NewNodeRestoreCmd(deps *Deps) *cobra.Command {
	var (
		opts tapper.NodeRestoreOptions
		yes  bool
	)

	cmd := &cobra.Command{
		Use:   "restore NODE_ID REV",
		Short: "restore a node to a prior snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.NodeID = args[0]
			opts.Rev = args[1]

			if !yes {
				if !deps.Runtime.Stream().IsTTY {
					return fmt.Errorf("restore requires confirmation; rerun with --yes")
				}
				ok, err := confirmNodeRestore(cmd, opts.NodeID, opts.Rev)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("restore canceled")
				}
			}

			return deps.Tap.NodeRestore(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg containing the node")
	return cmd
}

func confirmNodeRestore(cmd *cobra.Command, nodeID string, rev string) (bool, error) {
	_, err := fmt.Fprintf(cmd.ErrOrStderr(), "Restore node %s to revision %s? [y/N]: ", nodeID, rev)
	if err != nil {
		return false, err
	}

	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func shortHash(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}
