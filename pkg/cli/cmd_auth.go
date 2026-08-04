package cli

// `tap auth` — hub authentication commands. Provides `login`, `logout`,
// and `status` subcommands. The `login` and `logout` paths are CLI-layer
// helpers that read/write the AuthStore directly; `status` delegates to
// Tap.AuthStatus so the MCP tool can share the exact pre-formatted output.

import (
	"context"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
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

type authLoginResult struct {
	HubURL    string
	Namespace string
}

// runAuthLogin resolves the hub, obtains a credential via the selected method
// through the deps seams, persists the result to the AuthStore, and adopts the
// hub-reported default namespace onto the matching configured hub when possible.
// `tap auth login` and `tap bootstrap` share it so the device/persist plumbing
// lives in exactly one place.
func runAuthLogin(ctx context.Context, deps *Deps, p authLoginParams) (*authLoginResult, error) {
	rt := deps.Runtime

	// Resolve hub via the five-step chain (decision keg-dev/1035) so an
	// unflagged login lands on the configured default — or the compiled-in
	// DefaultHubURL — without forcing every invocation to repeat --hub. An
	// explicit/selected URL still wins.
	cfg, err := deps.Tap.ConfigService.Config()
	if err != nil {
		return nil, err
	}
	hubURL, err := tapper.ResolveLoginHubURL(cfg, p.HubURL)
	if err != nil {
		return nil, err
	}

	clientID := p.ClientID
	if clientID == "" {
		// Production default: the stock public client ID. Users running
		// against a hub that issues per-org client IDs pass --client-id.
		clientID = "tapper-cli"
	}

	var entry *tapper.AuthEntry
	var who *tapper.WhoAmI
	switch p.Method {
	case methodToken:
		// Validate the pasted token against the hub before storing it, so a
		// typo or a revoked token fails here rather than on the first call.
		var verr error
		who, verr = deps.AuthValidateTokenFn(ctx, rt, hubURL, p.Token)
		if verr != nil {
			return nil, verr
		}
		entry = &tapper.AuthEntry{AccessToken: strings.TrimSpace(p.Token), TokenType: "Bearer"}
	default: // methodBrowser — RFC 8628 device flow with the gh-style open prompt
		entry, err = deps.AuthLoginDeviceFn(ctx, rt, tapper.AuthLoginDeviceOptions{
			HubURL:      hubURL,
			ClientID:    clientID,
			Scope:       p.Scope,
			DeviceLabel: authDeviceLabel(rt),
			Timeout:     p.Timeout,
			OnUserCode:  deviceUserCodeHandler(deps),
		})
		if err != nil {
			return nil, err
		}
	}

	// Persist via AuthStore. The Tap is already constructed by
	// PersistentPreRunE, so PathService is available for the store location —
	// no second resolution pass needed.
	storePath := deps.Tap.PathService.AuthStorePath()
	store, err := tapper.LoadAuthStore(ctx, rt, storePath)
	if err != nil {
		return nil, err
	}
	store.Set(tapper.CanonicalHubURL(hubURL), *entry)
	if err := store.Save(ctx, rt, storePath); err != nil {
		return nil, err
	}

	if who == nil && strings.TrimSpace(entry.AccessToken) != "" {
		vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		who, _ = deps.AuthValidateTokenFn(vctx, rt, hubURL, entry.AccessToken)
		cancel()
	}

	namespace := loginNamespaceFromWho(who)
	if namespace != "" {
		if _, err := deps.Tap.SetHubDefaultNamespaceByURL(ctx, hubURL, namespace); err != nil {
			_, _ = fmt.Fprintf(rt.Stream().Err, "warning: could not adopt namespace from hub: %v\n", err)
		}
	}

	return &authLoginResult{HubURL: hubURL, Namespace: namespace}, nil
}

func loginNamespaceFromWho(who *tapper.WhoAmI) string {
	if who == nil {
		return ""
	}
	if ns := strings.TrimSpace(who.DefaultNamespace); ns != "" {
		return ns
	}
	return strings.TrimSpace(who.Username)
}

func authDeviceLabel(rt *toolkit.Runtime) string {
	osLabel := authDeviceOSLabel()
	host := ""
	if rt != nil {
		if proc := rt.Process(); proc != nil {
			host = sanitizeAuthDeviceHost(proc.Hostname)
		}
	}
	if host == "" {
		return fmt.Sprintf("Tapper CLI (%s)", osLabel)
	}
	return fmt.Sprintf("Tapper CLI on %s (%s)", host, osLabel)
}

func authDeviceOSLabel() string {
	switch goruntime.GOOS {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return goruntime.GOOS
	}
}

func sanitizeAuthDeviceHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range host {
		if b.Len() >= 40 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if b.Len() > 0 {
				b.WriteByte('-')
			}
		}
	}
	return strings.Trim(b.String(), "-._")
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
			open, err := deps.AuthPrompter.ConfirmOpenBrowser(ctx, hostOf(p.VerificationURI))
			if err != nil {
				// A cancelled ctx means the device flow already obtained the
				// token and tore this prompt down — the user approved before
				// answering. That's success, not failure: swallow it so login
				// completes cleanly instead of surfacing huh's cancellation.
				if ctx.Err() != nil {
					return nil
				}
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
				cfg, err := deps.Tap.ConfigService.Config()
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

			loginRes, err := runAuthLogin(ctx, deps, authLoginParams{
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
			_, _ = fmt.Fprintf(rt.Stream().Out, "Logged in to %s\n", loginRes.HubURL)
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
	var (
		hubURL  string
		offline bool
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show stored tapper hub login status",
		Long: `Report whether a hub has a cached login and validate the stored
token against the hub. On success it shows the account it resolves to;
a rejected token or an unreachable hub is reported without failing the
command. With --hub, reports on that specific hub; with no --hub, reports
every stored hub login. Pass --offline to skip the network check and report
purely from the local store.

The access token itself is never printed — only a short, non-secret
prefix matching the one shown in the hub's account UI.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt := deps.Runtime
			result, err := deps.Tap.AuthStatus(ctx, tapper.AuthStatusOptions{Hub: hubURL, Offline: offline})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(rt.Stream().Out, result.Formatted)
			return nil
		},
	}

	cmd.Flags().StringVar(&hubURL, "hub", "", "Hub base URL to query (omit to show every stored hub)")
	cmd.Flags().BoolVar(&offline, "offline", false, "Skip the live hub check and report from the local store only")
	mustRegisterFlagCompletion(cmd, "hub", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
