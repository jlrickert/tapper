package cli

import (
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewImageCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "manage images for a node",
	}

	cmd.AddCommand(
		newImageLsCmd(deps),
		newImageUploadCmd(deps),
		newImageDownloadCmd(deps),
		newImageRmCmd(deps),
	)

	return cmd
}

func newImageLsCmd(deps *Deps) *cobra.Command {
	var opts tapper.ListImagesOptions

	cmd := &cobra.Command{
		Use:     "ls NODE_ID",
		Short:   "list images for a node",
		Aliases: []string{"list"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			names, err := deps.Tap.ListImages(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if len(names) > 0 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.Join(names, "\n"))
			}
			return err
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg")
	return cmd
}

func newImageUploadCmd(deps *Deps) *cobra.Command {
	var opts tapper.UploadImageOptions

	cmd := &cobra.Command{
		Use:   "upload NODE_ID LOCAL_PATH",
		Short: "upload an image to a node",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.FilePath = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			name, err := deps.Tap.UploadImage(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), name)
			return err
		},
	}

	cmd.Flags().StringVar(&opts.Name, "name", "", "stored filename (default: basename of LOCAL_PATH)")
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg")
	return cmd
}

func newImageDownloadCmd(deps *Deps) *cobra.Command {
	var opts tapper.DownloadImageOptions

	cmd := &cobra.Command{
		Use:   "download NODE_ID NAME",
		Short: "download an image from a node",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.Name = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			dest, err := deps.Tap.DownloadImage(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), dest)
			return err
		},
	}

	cmd.Flags().StringVar(&opts.Dest, "dest", "", "destination path (default: ./<NAME>)")
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg")
	return cmd
}

func newImageRmCmd(deps *Deps) *cobra.Command {
	var opts tapper.DeleteImageOptions

	cmd := &cobra.Command{
		Use:     "rm NODE_ID NAME",
		Short:   "remove an image from a node",
		Aliases: []string{"remove"},
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.Name = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.DeleteImage(cmd.Context(), opts)
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg")
	return cmd
}
