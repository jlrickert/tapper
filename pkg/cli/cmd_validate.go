package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewValidateCmd(deps *Deps) *cobra.Command {
	var opts tapper.ValidateOptions
	cmd := &cobra.Command{
		Use:               "validate [NODE_ID...]",
		Short:             "validate nodes against keg schemas",
		ValidArgsFunction: nodeIDCompletionFunc(deps, 0),
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.NodeIDs = args
			results, err := deps.Tap.Validate(cmd.Context(), opts)
			if err != nil {
				return err
			}
			invalid := 0
			out := cmd.OutOrStdout()
			for _, result := range results {
				if result.Valid {
					fmt.Fprintf(out, "ok: node %s", result.NodeID)
					if result.Type != "" {
						fmt.Fprintf(out, " type=%s", result.Type)
					}
					fmt.Fprintln(out)
					continue
				}
				invalid++
				for _, issue := range result.Issues {
					field := issue.Field
					if field == "" {
						field = "schema"
					}
					fmt.Fprintf(out, "error: node %s %s: %s\n", result.NodeID, field, issue.Message)
				}
			}
			if invalid > 0 {
				return fmt.Errorf("%d invalid node(s)", invalid)
			}
			return nil
		},
	}
	return cmd
}
