package cli

import (
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewFileCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "manage file attachments for a node",
	}

	cmd.AddCommand(
		newFileLsCmd(deps),
		newFileUploadCmd(deps),
		newFileDownloadCmd(deps),
		newFileRmCmd(deps),
	)

	return cmd
}

func newFileLsCmd(deps *Deps) *cobra.Command {
	var opts tapper.ListFilesOptions

	cmd := &cobra.Command{
		Use:     "ls NODE_ID",
		Short:   "list file attachments for a node",
		Aliases: []string{"list"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			names, err := deps.Tap.ListFiles(cmd.Context(), opts)
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

func newFileUploadCmd(deps *Deps) *cobra.Command {
	var opts tapper.UploadFileOptions

	cmd := &cobra.Command{
		Use:   "upload NODE_ID LOCAL_PATH",
		Short: "upload a file attachment to a node",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.FilePath = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			name, err := deps.Tap.UploadFile(cmd.Context(), opts)
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

func newFileDownloadCmd(deps *Deps) *cobra.Command {
	var opts tapper.DownloadFileOptions

	cmd := &cobra.Command{
		Use:   "download NODE_ID NAME",
		Short: "download a file attachment from a node",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.Name = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			dest, err := deps.Tap.DownloadFile(cmd.Context(), opts)
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

func newFileRmCmd(deps *Deps) *cobra.Command {
	var opts tapper.DeleteFileOptions

	cmd := &cobra.Command{
		Use:     "rm NODE_ID NAME",
		Short:   "remove a file attachment from a node",
		Aliases: []string{"remove"},
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.Name = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.DeleteFile(cmd.Context(), opts)
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg")
	return cmd
}
