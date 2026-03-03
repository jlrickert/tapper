package cli

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/docs"
	"github.com/spf13/cobra"
)

func NewDocsCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs [DOC]",
		Short: "display embedded documentation",
		Long: `Display built-in documentation topics.

Run without arguments to list available documents.
Provide a topic name to display its content.

Examples:
  tap docs                          # list available docs
  tap docs query-expressions        # display query syntax doc
  tap docs configuration/user-config  # display nested doc`,
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			names, _ := listDocNames()
			prefix := strings.ToLower(toComplete)
			out := make([]string, 0, len(names))
			for _, n := range names {
				if strings.HasPrefix(strings.ToLower(n), prefix) {
					out = append(out, n)
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				names, err := listDocNames()
				if err != nil {
					return err
				}
				for _, name := range names {
					fmt.Fprintln(cmd.OutOrStdout(), name)
				}
				return nil
			}

			path := args[0] + ".md"
			data, err := docs.Content.ReadFile(path)
			if err != nil {
				return fmt.Errorf("unknown doc %q (use `tap docs` to list available topics)", args[0])
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), string(data))
			return err
		},
	}
	return cmd
}

func listDocNames() ([]string, error) {
	var names []string
	err := fs.WalkDir(docs.Content, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		name := strings.TrimSuffix(path, ".md")
		names = append(names, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}
