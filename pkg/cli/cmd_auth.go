package cli

// `tap auth` — hub authentication commands. Ships `login` for OAuth2
// PKCE-based hub authentication; additional subcommands may be added
// here as new child constructors.

import (
	"fmt"
	"time"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewAuthCmd builds the `auth` parent. Parent with no RunE would show
// "unknown command" noise; returning cmd.Help() is what every other
// two-level command tree in this repo does (see cmd_repo.go /
// cmd_config.go).
func NewAuthCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication with tapper hubs",
		Long: `Authenticate tapper with a hub so that CLI commands can publish,
subscribe, and sync across distributed kegs. Credentials are stored at
$XDG_STATE_HOME/tapper/auth.yaml with owner-only permissions.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAuthLoginCmd(deps))
	return cmd
}

// newAuthLoginCmd wires the `tap auth login` child. The command is a
// thin shell over tapper.AuthLogin + the AuthStore — keeping the flow
// inside pkg/tapper lets MCP and any future surface share the same
// implementation without duplicating the listener/PKCE plumbing.
func newAuthLoginCmd(deps *Deps) *cobra.Command {
	var (
		hubURL   string
		clientID string
		scope    string
		timeout  time.Duration
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a tapper hub via browser-based OAuth2 PKCE",
		Long: `Open a browser window to complete an OAuth2 authorization_code
grant with PKCE against the specified hub. A loopback listener on
127.0.0.1 receives the redirect, the resulting code is exchanged for an
access token, and the token is stored at ~/.local/state/tapper/auth.yaml
(location varies by platform). The flow aborts if the browser cannot be
opened or the user does not complete the handshake within --timeout.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if hubURL == "" {
				return fmt.Errorf("--hub is required")
			}
			if clientID == "" {
				// Production default: the stock public client ID. Users
				// running against a hub that issues per-org client IDs
				// pass --client-id explicitly.
				clientID = "tapper-cli"
			}

			ctx := cmd.Context()
			rt := deps.Runtime

			entry, err := authLoginFn(ctx, rt, tapper.AuthLoginOptions{
				HubURL:   hubURL,
				ClientID: clientID,
				Scope:    scope,
				Timeout:  timeout,
			})
			if err != nil {
				return err
			}

			// Persist via AuthStore. The Tap is already constructed by
			// PersistentPreRunE, so PathService is available for the
			// store location — no second resolution pass needed.
			storePath := deps.Tap.PathService.AuthStorePath()
			store, err := tapper.LoadAuthStore(ctx, rt, storePath)
			if err != nil {
				return err
			}
			store.Set(tapper.CanonicalHubURL(hubURL), *entry)
			if err := store.Save(ctx, rt, storePath); err != nil {
				return err
			}

			// Intentionally minimal success output: never echo the token
			// on stdout, even at debug level. The store file itself is
			// the source of truth.
			_, _ = fmt.Fprintf(rt.Stream().Out, "Logged in to %s\n", hubURL)
			return nil
		},
	}

	cmd.Flags().StringVar(&hubURL, "hub", "", "Hub base URL (required)")
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth2 client ID (default: tapper-cli)")
	cmd.Flags().StringVar(&scope, "scope", "", "Requested OAuth2 scopes (space-separated)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for the browser flow")

	// --hub is an arbitrary URL; file completion would be misleading.
	mustRegisterFlagCompletion(cmd, "hub", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
	mustRegisterFlagCompletion(cmd, "client-id", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
	mustRegisterFlagCompletion(cmd, "scope", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
	mustRegisterFlagCompletion(cmd, "timeout", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// authLoginFn is the seam tests swap to drive the PKCE flow without a
// real browser + loopback handshake. Production code uses the real
// AuthLogin verbatim; tests restore this after overriding.
var authLoginFn = tapper.AuthLogin
