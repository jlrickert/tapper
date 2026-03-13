package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewLockCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "manage cross-process node locks",
		Long: `Manage cross-process exclusive locks on nodes.

Use "lock acquire" to lock a node and print a token, "lock release" to release
it, "lock status" to inspect a lock, and "lock force-release" to break a stuck lock.`,
		Example: strings.TrimSpace(`
TOKEN=$(tap lock acquire 42)
tap lock status 42
tap lock release 42 --token "$TOKEN"
tap lock force-release 42
`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newLockAcquireCmd(deps),
		newLockReleaseCmd(deps),
		newLockStatusCmd(deps),
		newLockForceReleaseCmd(deps),
	)
	return cmd
}

func newLockAcquireCmd(deps *Deps) *cobra.Command {
	var opts tapper.LockOptions

	cmd := &cobra.Command{
		Use:               "acquire NODE_ID",
		Short:             "acquire a cross-process lock on a node",
		Long:              "Acquire a cross-process lock and print the token to stdout.",
		ValidArgsFunction: nodeIDCompletionFunc(deps, 1),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			token, err := deps.Tap.Lock(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), token)
			return err
		},
	}
	return cmd
}

func newLockReleaseCmd(deps *Deps) *cobra.Command {
	var opts tapper.UnlockOptions

	cmd := &cobra.Command{
		Use:               "release NODE_ID",
		Short:             "release a cross-process lock on a node",
		ValidArgsFunction: nodeIDCompletionFunc(deps, 1),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.Unlock(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Token, "token", "", "lock token returned by acquire (required)")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

func newLockStatusCmd(deps *Deps) *cobra.Command {
	var opts tapper.LockStatusOptions

	cmd := &cobra.Command{
		Use:               "status NODE_ID",
		Short:             "show lock state for a node",
		ValidArgsFunction: nodeIDCompletionFunc(deps, 1),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			info, err := deps.Tap.LockStatus(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if info.Token == "" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "unlocked")
				return err
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "Token:\t%s\n", info.Token)
			fmt.Fprintf(tw, "Holder:\t%s\n", info.Holder)
			fmt.Fprintf(tw, "Acquired:\t%s\n", info.AcquiredAt.Format(time.RFC3339))
			fmt.Fprintf(tw, "TTL:\t%ds\n", info.TTLSeconds)
			return tw.Flush()
		},
	}
	return cmd
}

func newLockForceReleaseCmd(deps *Deps) *cobra.Command {
	var opts tapper.ForceUnlockOptions

	cmd := &cobra.Command{
		Use:               "force-release NODE_ID",
		Short:             "unconditionally remove a lock on a node",
		ValidArgsFunction: nodeIDCompletionFunc(deps, 1),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.ForceUnlock(cmd.Context(), opts)
		},
	}
	return cmd
}
