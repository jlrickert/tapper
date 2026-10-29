package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewVideoCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "video",
		Short: "manage videos for a node",
	}

	cmd.AddCommand(
		newVideoLsCmd(deps),
		newVideoUploadCmd(deps),
		newVideoDownloadCmd(deps),
		newVideoRmCmd(deps),
	)

	return cmd
}

// listVideoNames completes a node's video names.
func listVideoNames(deps *Deps) func(ctx context.Context, nodeID string, kegOpts tapper.KegTargetOptions) ([]string, error) {
	return func(ctx context.Context, nodeID string, kegOpts tapper.KegTargetOptions) ([]string, error) {
		return deps.Tap.ListVideos(ctx, tapper.ListVideosOptions{
			KegTargetOptions: kegOpts,
			NodeID:           nodeID,
		})
	}
}

func newVideoLsCmd(deps *Deps) *cobra.Command {
	var opts tapper.ListVideosOptions

	cmd := &cobra.Command{
		Use:               "list NODE_ID",
		Short:             "list videos for a node",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: nodeIDCompletionFunc(deps, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			names, err := deps.Tap.ListVideos(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if len(names) > 0 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.Join(names, "\n"))
			}
			return err
		},
	}
	return cmd
}

func newVideoUploadCmd(deps *Deps) *cobra.Command {
	var opts tapper.UploadVideoOptions

	cmd := &cobra.Command{
		Use:               "upload NODE_ID LOCAL_PATH",
		Short:             "upload a video (MP4, WebM, or Ogg) to a node",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: nodeIDWithLocalFileCompletionFunc(deps),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.FilePath = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			name, err := deps.Tap.UploadVideo(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), name)
			return err
		},
	}

	cmd.Flags().StringVar(&opts.Name, "name", "", "stored filename (default: basename of LOCAL_PATH)")
	return cmd
}

func newVideoDownloadCmd(deps *Deps) *cobra.Command {
	var opts tapper.DownloadVideoOptions

	cmd := &cobra.Command{
		Use:               "download NODE_ID NAME [DEST]",
		Short:             "download a video from a node",
		Args:              cobra.RangeArgs(2, 3),
		ValidArgsFunction: nodeAndNameCompletionFunc(deps, listVideoNames(deps), true),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.Name = args[1]
			if len(args) == 3 {
				opts.Dest = args[2]
			}
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			dest, err := deps.Tap.DownloadVideo(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if dest != "" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), dest)
			}
			return err
		},
	}
	return cmd
}

func newVideoRmCmd(deps *Deps) *cobra.Command {
	var opts tapper.DeleteVideoOptions

	cmd := &cobra.Command{
		Use:               "delete NODE_ID NAME",
		Short:             "remove a video from a node",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: nodeAndNameCompletionFunc(deps, listVideoNames(deps), false),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.Name = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.DeleteVideo(cmd.Context(), opts)
		},
	}
	return cmd
}
