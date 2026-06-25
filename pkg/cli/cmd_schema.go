package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewSchemaCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "manage keg note schemas",
	}

	cmd.AddCommand(
		newSchemaListCmd(deps),
		newSchemaGetCmd(deps),
		newSchemaCreateCmd(deps),
		newSchemaRmCmd(deps),
	)
	return cmd
}

func newSchemaListCmd(deps *Deps) *cobra.Command {
	var opts tapper.SchemaOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list schema types",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			names, err := deps.Tap.ListSchemas(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, name := range names {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	}
	return cmd
}

func newSchemaGetCmd(deps *Deps) *cobra.Command {
	var opts tapper.SchemaOptions
	cmd := &cobra.Command{
		Use:   "get TYPE",
		Short: "print a schema",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.Type = args[0]
			data, err := deps.Tap.ReadSchema(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	return cmd
}

func newSchemaCreateCmd(deps *Deps) *cobra.Command {
	var opts tapper.SchemaOptions
	cmd := &cobra.Command{
		Use:   "create FILE|-",
		Short: "create a schema",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			data, err := readSchemaCommandInput(deps, args[0])
			if err != nil {
				return err
			}
			opts.Data = data
			return deps.Tap.CreateSchema(cmd.Context(), opts)
		},
	}
	return cmd
}

func newSchemaRmCmd(deps *Deps) *cobra.Command {
	var opts tapper.SchemaOptions
	cmd := &cobra.Command{
		Use:     "rm TYPE",
		Aliases: []string{"remove", "delete"},
		Short:   "delete a schema",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.Type = args[0]
			return deps.Tap.DeleteSchema(cmd.Context(), opts)
		},
	}
	return cmd
}

func readSchemaCommandInput(deps *Deps, path string) ([]byte, error) {
	if strings.TrimSpace(path) == "-" {
		return io.ReadAll(deps.Runtime.Stream().In)
	}
	return deps.Runtime.ReadFile(path)
}
