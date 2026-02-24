package cli

import (
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewTagsCmd(deps *Deps) *cobra.Command {
	var opts tapper.TagsOptions

	cmd := &cobra.Command{
		Use:   "tags [EXPR]",
		Short: "list tags or query nodes by tag expression",
		Long: `List all tags when no expression is provided.

When EXPR is provided, return nodes matching a boolean tag expression.

Expression language:
  - Literals: fire, project_x, "and"
  - Operators: and, or, not
  - Symbol operators: &&, ||, !
  - Grouping: parentheses ()
  - Precedence: not > and > or

Examples:
  tap tags
  tap tags fire
  tap tags "fire and (project or guide)"
  tap tags "fire and not archived" --id-only
  tap tags "client && !draft" --format "%i|%t"`,
		Example: `  tap tags
  tap tags fire
  tap tags "fire and (project or guide)"
  tap tags "fire and not archived" --id-only
  tap tags "client && !draft" --format "%i|%t"`,
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 || deps.Tap == nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}

			co := tapper.TagsOptions{
				KegTargetOptions: opts.KegTargetOptions,
			}
			applyKegTargetProfile(deps, &co.KegTargetOptions)
			tags, err := deps.Tap.Tags(cmd.Context(), co)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}

			prefix := strings.ToLower(strings.TrimSpace(toComplete))
			if prefix == "" {
				return tags, cobra.ShellCompDirectiveNoFileComp
			}

			out := make([]string, 0, len(tags))
			for _, tag := range tags {
				if strings.HasPrefix(strings.ToLower(tag), prefix) {
					out = append(out, tag)
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Tag = args[0]
			}
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			items, err := deps.Tap.Tags(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, item := range items {
				fmt.Fprintln(cmd.OutOrStdout(), item)
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&opts.IdOnly, "id-only", "", false, "show only ids when TAG is provided")
	cmd.Flags().BoolVar(&opts.Reverse, "reverse", false, "list in reverse order")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "output format when TAG is provided")
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to read from")

	return cmd
}
