package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/relaycontract"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewRelayCmd builds the `tap relay` command, which contributes this
// machine's model providers to Hub's catalog and serves the inference Hub
// routes to them.
func NewRelayCmd(deps *Deps) *cobra.Command {
	var opts tapper.RelayOptions

	cmd := &cobra.Command{
		Use:   "relay",
		Short: "offer this machine's models to Hub (experimental)",
		Long: `Connect to Hub and offer the models of your configured providers to your
account's model catalog. The relay dials out to Hub and stays connected, so no
inbound port is needed. Provider credentials stay on this machine.

Providers are configured in your user config only; project config cannot set
them:

  relay:
    name: laptop            # optional; defaults to the hostname
    providers:
      ollama:
        kind: ollama        # baseUrl defaults to http://127.0.0.1:11434/v1
        models:
          allow: ["qwen3*"]
      openrouter:
        kind: openrouter
        auth: apiKey
        apiKeyEnv: OPENROUTER_API_KEY   # the variable's name, never the key

The relay authenticates with the token from 'tap auth login'. It runs in the
foreground until interrupted, reconnecting if the connection drops. Disconnecting
it from Hub's Account → Relay page stops it for good; run it again to reconnect.

Experimental and unstable: expect this to change.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.Hub = deps.KegTargetOptions.Hub
			opts.Version = Version
			out := cmd.ErrOrStderr()
			opts.OnRegistered = func(hubURL string, reg relaycontract.Registered) {
				fmt.Fprintf(out, "relay connected to %s with %d model(s)\n", hubURL, len(reg.Models))
				for _, m := range reg.Models {
					fmt.Fprintf(out, "  %s\n", m.CatalogID)
				}
			}
			return deps.Tap.Relay(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "relay name shown in Hub (overrides relay.name)")
	cmd.Flags().IntVar(&opts.MaxConcurrent, "max-concurrent", tapper.DefaultRelayMaxConcurrent,
		fmt.Sprintf("maximum in-flight requests (1-%d)", relaycontract.MaxConcurrentCap))
	return cmd
}
