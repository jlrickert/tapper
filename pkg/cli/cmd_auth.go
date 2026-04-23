package cli

// `tap auth` — hub authentication commands. Provides `login`, `logout`,
// and `status` subcommands. The `login` and `logout` paths are CLI-layer
// helpers that read/write the AuthStore directly; `status` delegates to
// Tap.AuthStatus so the MCP tool can share the exact pre-formatted output.

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
	cmd.AddCommand(newAuthLogoutCmd(deps))
	cmd.AddCommand(newAuthStatusCmd(deps))
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

			entry, err := deps.AuthLoginFn(ctx, rt, tapper.AuthLoginOptions{
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

// newAuthLogoutCmd wires `tap auth logout`. Business logic lives in
// Tap.AuthLogout; this command is a thin shell that routes the result's
// Formatted line to stdout (on removal) or stderr (on soft-success).
// Logout is exposed only via CLI — intentionally excluded from MCP so
// agents cannot revoke a user's hub credentials (see the godoc on
// Tap.AuthLogout for the full rationale).
func newAuthLogoutCmd(deps *Deps) *cobra.Command {
	var hubURL string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove a stored hub login from the local auth store",
		Long: `Delete the cached credentials for a tapper hub. With --hub, removes
that specific hub's entry (canonicalized before lookup). With no --hub,
removes the only stored entry when exactly one is present; errors when
multiple hubs are stored (the caller must disambiguate).

"No login stored for <hub>" is a soft success, not an error — logout
is idempotent by design so cleanup scripts can re-run without special
casing the already-logged-out state.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt := deps.Runtime

			result, err := deps.Tap.AuthLogout(ctx, tapper.AuthLogoutOptions{Hub: hubURL})
			if err != nil {
				return err
			}
			// Stream routing: removal → stdout (the action happened),
			// soft-success → stderr (so stdout stays clean for scripts
			// that pipe output unconditionally).
			stream := rt.Stream().Out
			if !result.Removed {
				stream = rt.Stream().Err
			}
			_, _ = fmt.Fprint(stream, result.Formatted)
			return nil
		},
	}

	cmd.Flags().StringVar(&hubURL, "hub", "", "Hub base URL to log out of (required when multiple hubs are stored)")
	mustRegisterFlagCompletion(cmd, "hub", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// newAuthStatusCmd wires `tap auth status`. This is the only `auth`
// subcommand that goes through *Tap — status reporting has a natural
// MCP exposure (agents should be able to check whether they can
// authenticate before attempting a remote call) and the pre-formatted
// Result.Formatted field lets both surfaces emit byte-identical output.
func newAuthStatusCmd(deps *Deps) *cobra.Command {
	var hubURL string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the login status for a stored tapper hub",
		Long: `Report whether a hub has a cached login, the token type, scope,
and expiry. With --hub, reports on that specific hub; with no --hub
and exactly one stored entry, auto-resolves to it. The access token
itself is never printed — only the last 4 characters as a suffix.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt := deps.Runtime
			result, err := deps.Tap.AuthStatus(ctx, tapper.AuthStatusOptions{Hub: hubURL})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(rt.Stream().Out, result.Formatted)
			return nil
		},
	}

	cmd.Flags().StringVar(&hubURL, "hub", "", "Hub base URL to query (optional when exactly one hub is stored)")
	mustRegisterFlagCompletion(cmd, "hub", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
