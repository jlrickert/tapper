package cli

import (
	"encoding/json"
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewURLCmd returns the `url` command: the Hub web link for a KEG, note,
// flight, or namespace.
//
// Usage examples:
//
//	tap url
//	tap url 42
//	tap url @me/notes/42 --open
//	tap url +work --json
func NewURLCmd(deps *Deps) *cobra.Command {
	var (
		open   bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "url [REF]",
		Short: "print the Hub web link for a keg, note, flight, or namespace",
		Long: `Print the Hub web link for REF, or for the current keg when REF is omitted.

REF is a node id in the current keg (42), a keg (keg:alias, keg:~alias,
@ns/keg), a note in another keg (keg:alias/42, keg:~alias/42, @ns/keg/42), a
flight (+slug, @ns/+slug), a namespace (@ns), or a Hub page URL. The link is
built from configuration; only a keg-local ~alias reads the current keg's
settings. --open also opens the link in the browser.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{localOnlyAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			var opts tapper.WebURLOptions
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			if len(args) == 1 {
				opts.Ref = args[0]
			}
			ctx := cmd.Context()
			link, err := deps.Tap.WebURL(ctx, opts)
			if err != nil {
				return err
			}
			if asJSON {
				out, err := json.MarshalIndent(link, "", "  ")
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", out)
				if err != nil {
					return err
				}
			} else if _, err := fmt.Fprintln(cmd.OutOrStdout(), link.URL); err != nil {
				return err
			}
			if open {
				return deps.OpenBrowserFn(ctx, deps.Runtime, link.URL)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&open, "open", false, "also open the link in the browser")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the url, kind, and canonical ref as JSON")
	nodeIDs := nodeIDCompletionFunc(deps, 1)
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := useArgCompletions(cmd.Context(), deps, toComplete)
		ids, _ := nodeIDs(cmd, args, toComplete)
		return append(out, ids...), cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}
