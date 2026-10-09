package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewSearchCmd returns `tap node search`, the peer of the node_search tool.
func NewSearchCmd(deps *Deps) *cobra.Command {
	var (
		opts   tapper.SearchOptions
		idOnly bool
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "search QUERY...",
		Short: "find the nodes most relevant to a query",
		Long: `Rank nodes by relevance to a query across every KEG you can read on the
hub, one KEG (--keg), or the listed KEGs (--kegs). The query takes plain
terms, "quoted phrases", and -exclusions; the words of a multi-word query
are joined with spaces. Quote the query, or put it after --, when it starts
an exclusion, so the shell and tap pass it through intact.

Each hit prints its reference, score, title, and a snippet with matches in
**bold** (or highlighted on a terminal), tab-separated. When more hits
remain, the cursor for the next page is printed to stderr; pass it back
with --cursor and the same query. Use tap node grep for exact regex
matching.`,
		Example: strings.TrimSpace(`
tap node search release plan
tap node search '"billing migration" -draft' --keg @acme/engineering
tap node search deploy --kegs @acme/ops,@acme/eng --limit 5
tap node search deploy --cursor "$NEXT"
tap node search -- deploy -staging
`),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Query = strings.Join(args, " ")
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			resp, err := deps.Tap.Search(cmd.Context(), opts)
			if err != nil {
				return err
			}
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(resp)
			}
			for _, warning := range resp.Warnings {
				fmt.Fprintln(errOut, "warning: "+warning)
			}
			if idOnly {
				for _, hit := range resp.Hits {
					fmt.Fprintln(out, hit.Ref)
				}
			} else {
				open, close := searchHighlightMarkers(deps)
				for _, line := range tapper.SearchHitLines(resp.Hits, open, close) {
					fmt.Fprintln(out, line)
				}
			}
			if resp.NextCursor != "" {
				fmt.Fprintln(errOut, tapper.SearchNextCursorLine(resp.NextCursor))
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&opts.Kegs, "kegs", nil, "narrow the search to these @namespace/keg references, at most 50 (comma separated or repeated)")
	mustRegisterFlagCompletion(cmd, "kegs", kegListFlagCompletionFunc(deps))
	cmd.Flags().StringVar(&opts.Cursor, "cursor", "", "continue from a previous page's next cursor")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "n", 0, "maximum hits to return (0 lets the hub choose)")
	cmd.Flags().BoolVar(&idOnly, "id-only", false, "print only node references")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the hub's ranked response as JSON")
	cmd.MarkFlagsMutuallyExclusive("id-only", "json")
	return cmd
}

// searchHighlightMarkers bolds matches on a terminal and marks them
// **like this** elsewhere, matching node_search's rendering.
func searchHighlightMarkers(deps *Deps) (string, string) {
	if deps != nil && deps.Runtime != nil && deps.Runtime.Stream().IsTTY {
		return "\x1b[1m", "\x1b[0m"
	}
	return "**", "**"
}

// kegListFlagCompletionFunc completes the last item of a comma-separated KEG
// list, keeping the items already typed.
func kegListFlagCompletionFunc(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		cut := strings.LastIndex(toComplete, ",") + 1
		done, last := toComplete[:cut], toComplete[cut:]
		candidates := kegFlagCompletions(cmd.Context(), deps, last)
		for i, candidate := range candidates {
			candidates[i] = done + candidate
		}
		return candidates, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
}
