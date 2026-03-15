package cli

import (
	"fmt"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewSiteCmd returns the `site` parent cobra command with `build` and `serve`
// subcommands.
//
// Usage examples:
//
//	tap site build
//	tap site build --output ./public
//	tap site build --keg dev --title "Dev KEG" --base-url https://keg.example.com
//	tap site build --no-search
//	tap site serve
//	tap site serve --port 8080
//	tap site serve --keg dev --host 0.0.0.0
func NewSiteCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "static site generation and serving",
		Long: `Commands for generating and serving a KEG as a website.

Subcommands:
  build   Generate a complete static HTML website from the resolved keg.
  serve   Start an HTTP server that renders KEG pages dynamically.`,
	}

	cmd.AddCommand(
		newSiteBuildCmd(deps),
		newSiteServeCmd(deps),
	)

	return cmd
}

// newSiteBuildCmd returns the `site build` subcommand (formerly `tap site`).
func newSiteBuildCmd(deps *Deps) *cobra.Command {
	var opts tapper.SiteOptions

	cmd := &cobra.Command{
		Use:   "build",
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

// newSiteServeCmd returns the `site serve` subcommand (formerly `tap serve`).
func newSiteServeCmd(deps *Deps) *cobra.Command {
	var opts tapper.ServeOptions

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "start an HTTP server that renders KEG pages dynamically",
		Long: `Start an embedded HTTP server that renders KEG pages dynamically
on each request. Edits to the keg are immediately visible on browser
refresh without a rebuild step.

By default the server binds to 127.0.0.1 on a random available port.
Use --port to specify an explicit port and --host to change the bind
address.

Routes:
  /              landing page (node list sorted by last modified)
  /{id}/         rendered node page
  /{id}/README.md   raw markdown content
  /{id}/meta.yaml   raw metadata
  /{id}/meta.json   metadata as JSON
  /{id}/stats.json  raw stats
  /{id}/stats.yaml  stats as YAML
  /{id}/{asset}     images and file attachments
  /tags/            tag index
  /tags/{tag}/      individual tag page
  /changes/         changes page

Press Ctrl+C to stop the server.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			_, err := deps.Tap.Serve(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&opts.Port, "port", "p", 0, "port to listen on (default: random available port)")
	cmd.Flags().StringVar(&opts.Host, "host", "127.0.0.1", "bind address")
	cmd.Flags().StringVar(&opts.Title, "title", "", "override site title")
	cmd.Flags().StringVar(&opts.BaseURL, "base-url", "", "base URL for links (default: /)")

	// Register flag completions.
	_ = cmd.RegisterFlagCompletionFunc("port", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"8080", "3000", "9090"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("host", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"127.0.0.1", "0.0.0.0", "localhost"}, cobra.ShellCompDirectiveNoFileComp
	})

	// Suppress usage output on error -- the help text is long.
	cmd.SilenceUsage = true

	_ = cmd.Flags().SetAnnotation("port", cobra.BashCompCustom, []string{fmt.Sprintf("__tap_no_file_comp")})

	return cmd
}
