package cli

// `tap auth` — hub authentication commands. Provides `login`, `logout`,
// and `status` subcommands. The `login` and `logout` paths are CLI-layer
// helpers that read/write the AuthStore directly; `status` delegates to
// Tap.AuthStatus so the MCP tool can share the exact pre-formatted output.

import (
	"context"
	"fmt"
	"io"
	"strings"
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

// authLoginParams carries the resolved inputs runAuthLogin needs. It is the
// shared contract between `tap auth login` and `tap bootstrap`, both of which
// drive the same resolve → flow-select → persist sequence. The interactive
// hub/method selection happens in the command layer (newAuthLoginCmd); by the
// time params reach here the hub and method are already decided.
type authLoginParams struct {
	HubURL   string        // explicit/selected hub URL, or "" to resolve via the config chain
	ClientID string        // "" defaults to "tapper-cli"
	Scope    string        // space-separated OAuth2 scopes
	Timeout  time.Duration // zero lets the per-flow default win
	Method   loginMethod   // browser (device flow) or token (paste)
	Token    string        // the pasted token, for methodToken
}

// runAuthLogin resolves the hub, obtains a credential via the selected method
// through the deps seams, and persists the result to the AuthStore. It returns
// the canonical hub URL the token was stored under so callers can print their
// own success line without re-resolving. `tap auth login` and `tap bootstrap`
// share it so the device/persist plumbing lives in exactly one place.
func runAuthLogin(ctx context.Context, deps *Deps, p authLoginParams) (string, error) {
	rt := deps.Runtime

	// Resolve hub via the five-step chain (decision keg-dev/1035) so an
	// unflagged login lands on the configured default — or the compiled-in
	// DefaultHubURL — without forcing every invocation to repeat --hub. An
	// explicit/selected URL still wins.
	cfg, err := deps.Tap.ConfigService.Config(true)
	if err != nil {
		return "", err
	}
	hubURL, err := tapper.ResolveLoginHubURL(cfg, p.HubURL)
	if err != nil {
		return "", err
	}

	clientID := p.ClientID
	if clientID == "" {
		// Production default: the stock public client ID. Users running
		// against a hub that issues per-org client IDs pass --client-id.
		clientID = "tapper-cli"
	}

	var entry *tapper.AuthEntry
	switch p.Method {
	case methodToken:
		// Validate the pasted token against the hub before storing it, so a
		// typo or a revoked token fails here rather than on the first call.
		if _, verr := deps.AuthValidateTokenFn(ctx, rt, hubURL, p.Token); verr != nil {
			return "", verr
		}
		entry = &tapper.AuthEntry{AccessToken: strings.TrimSpace(p.Token), TokenType: "Bearer"}
	default: // methodBrowser — RFC 8628 device flow with the gh-style open prompt
		entry, err = deps.AuthLoginDeviceFn(ctx, rt, tapper.AuthLoginDeviceOptions{
			HubURL:     hubURL,
			ClientID:   clientID,
			Scope:      p.Scope,
			Timeout:    p.Timeout,
			OnUserCode: deviceUserCodeHandler(deps),
		})
		if err != nil {
			return "", err
		}
	}

	// Persist via AuthStore. The Tap is already constructed by
	// PersistentPreRunE, so PathService is available for the store location —
	// no second resolution pass needed.
	storePath := deps.Tap.PathService.AuthStorePath()
	store, err := tapper.LoadAuthStore(ctx, rt, storePath)
	if err != nil {
		return "", err
	}
	store.Set(tapper.CanonicalHubURL(hubURL), *entry)
	if err := store.Save(ctx, rt, storePath); err != nil {
		return "", err
	}
	return hubURL, nil
}

// deviceUserCodeHandler returns the AuthLoginDeviceOptions.OnUserCode callback
// that drives the browser step gh-style: print the one-time code, offer to open
// the browser on Enter (or let the user copy the URL), then hand back so the
// flow polls for approval. On a non-TTY it skips the prompt and just prints the
// URL, preserving the old --device behavior for containers and remote shells.
func deviceUserCodeHandler(deps *Deps) func(context.Context, tapper.DeviceUserPrompt) error {
	return func(ctx context.Context, p tapper.DeviceUserPrompt) error {
		rt := deps.Runtime
		out := rt.Stream().Err
		// Always carries the user_code, so opening or pasting the link
		// pre-fills the code (works even against hubs that omit
		// verification_uri_complete).
		verifyURL := p.VerificationURL()

		// Show the code and the full code-bearing URL up front so it is
		// visible whether the user opens the browser or copies the link.
		_, _ = fmt.Fprintf(out, "\n! First copy your one-time code: %s\n", p.UserCode)
		_, _ = fmt.Fprintf(out, "  Then open this URL to continue:\n    %s\n\n", verifyURL)

		if rt.Stream().IsTTY {
			open, err := deps.AuthPrompter.ConfirmOpenBrowser(hostOf(p.VerificationURI))
			if err != nil {
				return err
			}
			if open {
				// OpenBrowser prints the URL itself on a launch failure, so an
				// error here is not fatal — the user can still complete in any
				// browser using the URL shown above.
				_ = tapper.OpenBrowser(ctx, rt, verifyURL)
			}
		}

		if p.ExpiresIn > 0 {
			_, _ = fmt.Fprintf(out, "  (Code expires in %d minutes.)\n", p.ExpiresIn/60)
		}
		_, _ = fmt.Fprintln(out, "\nWaiting for approval...")
		return nil
	}
}

// newAuthLoginCmd wires the `tap auth login` child. On a TTY with no --hub it
// walks an interactive picker (hub → method), modeled on `gh auth login`:
// choose a hub, then either open a browser (RFC 8628 device flow) or paste an
// API token. The flags keep the command scriptable; --with-token reads a token
// from stdin for CI. The resolve → credential → persist sequence lives in the
// shared runAuthLogin so bootstrap reuses it.
func newAuthLoginCmd(deps *Deps) *cobra.Command {
	var (
		hubURL    string
		clientID  string
		scope     string
		timeout   time.Duration
		withToken bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a tapper hub",
		Long: `Authenticate with a tapper hub.

On a terminal, login is interactive: pick a hub (atlas.foldwise.ai, another
hub from your config, or a new endpoint), then choose how to authenticate:

  • Login with a web browser — Tap shows a one-time code and opens your
    browser (RFC 8628 device flow). Press Enter to open it, or copy the URL
    and visit it yourself; Tap polls the hub until you approve.
  • Paste an authentication token — paste an API token created on the hub's
    account page. Tap validates it against the hub before saving.

The token is stored at ~/.local/state/tapper/auth.yaml (location varies by
platform). For scripts, pass --hub and pipe a token to --with-token:

  echo "$TOKEN" | tap auth login --hub https://atlas.foldwise.ai --with-token`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt := deps.Runtime
			isTTY := rt.Stream().IsTTY

			// Pass the user-supplied timeout only when explicitly set, so the
			// device-flow default (10m, which budgets for the user switching
			// to a browser) wins when the user didn't pick one.
			var passedTimeout time.Duration
			if cmd.Flags().Changed("timeout") {
				passedTimeout = timeout
			}

			method := methodBrowser
			var token string

			// --with-token: non-interactive token paste from stdin (CI).
			if withToken {
				method = methodToken
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("auth login: read token from stdin: %w", err)
				}
				token = strings.TrimSpace(string(raw))
				if token == "" {
					return fmt.Errorf("auth login: --with-token was set but no token was provided on stdin")
				}
			}

			// Resolve the hub. An explicit --hub always wins. Otherwise, on a
			// TTY (and when not scripting a token), show the interactive
			// picker; if still empty, runAuthLogin resolves via the config
			// chain / compiled-in default.
			selectedHub := hubURL
			if selectedHub == "" && isTTY && !withToken {
				cfg, err := deps.Tap.ConfigService.Config(true)
				if err != nil {
					return err
				}
				choice, err := deps.AuthPrompter.SelectHub(buildHubChoices(cfg))
				if err != nil {
					return err
				}
				if choice.Other {
					endpoint, err := deps.AuthPrompter.PromptEndpointURL()
					if err != nil {
						return err
					}
					selectedHub = ensureScheme(endpoint)
				} else {
					selectedHub = choice.URL
				}
			}

			// Choose the method interactively unless --with-token forced it.
			if !withToken && isTTY {
				m, err := deps.AuthPrompter.SelectMethod()
				if err != nil {
					return err
				}
				method = m
				if method == methodToken {
					t, err := deps.AuthPrompter.PromptToken()
					if err != nil {
						return err
					}
					token = t
				}
			}

			resolvedHub, err := runAuthLogin(ctx, deps, authLoginParams{
				HubURL:   selectedHub,
				ClientID: clientID,
				Scope:    scope,
				Timeout:  passedTimeout,
				Method:   method,
				Token:    token,
			})
			if err != nil {
				return err
			}

			// Intentionally minimal success output: never echo the token
			// on stdout, even at debug level. The store file itself is
			// the source of truth.
			_, _ = fmt.Fprintf(rt.Stream().Out, "Logged in to %s\n", resolvedHub)
			return nil
		},
	}

	cmd.Flags().StringVar(&hubURL, "hub", "", "Hub base URL (skips the interactive picker; defaults via config when omitted)")
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth2 client ID (default: tapper-cli)")
	cmd.Flags().StringVar(&scope, "scope", "", "Requested OAuth2 scopes (space-separated)")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Timeout for browser approval")
	cmd.Flags().BoolVar(&withToken, "with-token", false, "Read an authentication token from stdin instead of opening a browser")

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
