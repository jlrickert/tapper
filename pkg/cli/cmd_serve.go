package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewServeCmd returns the `serve` cobra command.
//
// Usage examples:
//
//	tap serve
//	tap serve --port 8080
//	tap serve --keg dev --title "Dev KEG" --base-url /keg/dev/
//	tap serve --host 0.0.0.0 --port 3000
func NewServeCmd(deps *Deps) *cobra.Command {
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
