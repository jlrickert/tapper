package cli

import (
	"fmt"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewSiteCmd returns the `site` cobra command.
//
// Usage examples:
//
//	tap site
//	tap site --output ./public
//	tap site --keg dev --title "Dev KEG" --base-url https://keg.example.com
//	tap site --no-search
func NewSiteCmd(deps *Deps) *cobra.Command {
	var opts tapper.SiteOptions

	cmd := &cobra.Command{
		Use:   "site",
		Short: "generate a static HTML website from a keg",
		Long: `Generate a complete static HTML website from the resolved keg.

Each node produces a directory containing index.html (rendered HTML),
README.md, meta.yaml, meta.json, stats.json, and stats.yaml. Tag pages,
a landing page, and a changes page are also generated.

If pagefind is installed, client-side full-text search is added automatically.
Use --no-search to skip search indexing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			// Resolve output path.
			if opts.Output != "" {
				path := toolkit.ExpandEnv(deps.Runtime, opts.Output)
				resolved, err := toolkit.ExpandPath(deps.Runtime, path)
				if err == nil {
					opts.Output = resolved
				}
			}

			result, err := deps.Tap.Site(cmd.Context(), opts)
			if err != nil {
				return err
			}

			absPath, _ := filepath.Abs(result.OutputDir)
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"site generated: %d nodes, %d tags -> %s\n",
				result.NodeCount, result.TagCount, absPath)
			if result.HasSearch {
				fmt.Fprintln(cmd.OutOrStdout(), "search index created with pagefind")
			}
			return err
		},
	}

	cmd.Flags().StringVarP(&opts.Output, "output", "o", "", "output directory (default: ./site)")
	cmd.Flags().StringVar(&opts.Title, "title", "", "override site title")
	cmd.Flags().StringVar(&opts.BaseURL, "base-url", "", "base URL for absolute links (default: /)")
	cmd.Flags().BoolVar(&opts.NoSearch, "no-search", false, "skip pagefind search indexing")

	_ = cmd.MarkFlagDirname("output")

	return cmd
}
