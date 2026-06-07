package cli

// `tap bootstrap` — first-run onboarding. Walks the user through a deployment
// kind (local / cloud / enterprise), writes a usable user config, and
// optionally drives a hub login by reusing runAuthLogin. CLI-only: login and
// the conversational prompt are not agent operations, so there is no MCP
// surface (Tap.Bootstrap is listed in pkg/parity's tapMethodsExcluded).

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewBootstrapCmd returns the `tap bootstrap` cobra command.
//
// Usage examples:
//
//	tap bootstrap                                  # interactive on a TTY
//	tap bootstrap --kind local                     # local filesystem hub only
//	tap bootstrap --kind cloud                     # atlas.foldwise.ai
//	tap bootstrap --kind enterprise --endpoint https://keg.acme.com
func NewBootstrapCmd(deps *Deps) *cobra.Command {
	var (
		kind           string
		endpoint       string
		hubName        string
		login          bool
		noLogin        bool
		nonInteractive bool
	)

	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "set up your user config (and optionally log in)",
		Long: strings.TrimSpace(`
Set up your user-level tapper config so plain commands resolve without
per-invocation flags. Choose where your kegs live:

  local        a filesystem hub on this machine (no account, no login)
  cloud        atlas.foldwise.ai, the hosted hub
  enterprise   a self-hosted hub at a URL you provide

Bootstrap writes the matching fallback hub and an auto-derived fallback
namespace, and ensures the built-in local hub is always available. It is
idempotent: re-running preserves any kegs and keg-map entries you already have.

On a TTY with no flags, bootstrap prompts for the kind (and, for enterprise,
the endpoint), then offers to log in. Pass --non-interactive to rely on flags.
For cloud/enterprise, --login / --no-login control the login step; local never
logs in.
`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt := deps.Runtime
			ctx := cmd.Context()

			reader := bufio.NewReader(cmd.InOrStdin())
			stderr := cmd.ErrOrStderr()
			interactive := rt.Stream().IsTTY && !nonInteractive

			// 1. Resolve the deployment kind.
			if strings.TrimSpace(kind) == "" {
				if interactive {
					ans, err := promptLine(stderr, reader, "Where should your kegs live? [local/cloud/enterprise] (default cloud): ")
					if err != nil {
						return err
					}
					k, err := parseBootstrapKind(ans)
					if err != nil {
						return err
					}
					kind = k
				} else {
					kind = tapper.BootstrapKindCloud
				}
			} else {
				k, err := parseBootstrapKind(kind)
				if err != nil {
					return err
				}
				kind = k
			}

			// 2. Enterprise needs an endpoint; prompt on a TTY, else require the flag.
			if kind == tapper.BootstrapKindEnterprise && strings.TrimSpace(endpoint) == "" {
				if interactive {
					for strings.TrimSpace(endpoint) == "" {
						ans, err := promptLine(stderr, reader, "Enterprise hub endpoint URL: ")
						if err != nil {
							return err
						}
						endpoint = ans
					}
				} else {
					return fmt.Errorf("enterprise bootstrap requires --endpoint")
				}
			}

			// 3. Write the config (returns the hub URL for an optional login).
			res, err := deps.Tap.Bootstrap(ctx, tapper.BootstrapOptions{
				Kind:     kind,
				Endpoint: endpoint,
				HubName:  hubName,
			})
			if err != nil {
				return err
			}

			// 4. Optional login for cloud/enterprise.
			loggedIn := false
			if res.HubURL != "" {
				doLogin := login && !noLogin
				if !cmd.Flags().Changed("login") && !cmd.Flags().Changed("no-login") && interactive {
					ans, perr := promptLine(stderr, reader, fmt.Sprintf("Log in to %s now? [Y/n]: ", hostOf(res.HubURL)))
					if perr != nil {
						return perr
					}
					switch strings.ToLower(strings.TrimSpace(ans)) {
					case "", "y", "yes":
						doLogin = true
					}
				}
				if doLogin {
					resolvedHub, lerr := runAuthLogin(ctx, deps, authLoginParams{HubURL: res.HubURL, Method: methodBrowser})
					if lerr != nil {
						if login {
							return lerr
						}
						_, _ = fmt.Fprintf(stderr, "login failed (continuing): %v\n", lerr)
					} else {
						loggedIn = true
						_, _ = fmt.Fprintf(stderr, "Logged in to %s\n", resolvedHub)
						// Adopt the user's home namespace from the hub so plain
						// references land in the user's own namespace rather than
						// the provisional OS-user guess bootstrap wrote. Best-effort:
						// a probe failure leaves the provisional namespace in place.
						if ns := bootstrapHubNamespace(ctx, deps, resolvedHub); ns != "" {
							if serr := deps.Tap.SetBootstrapNamespace(ctx, res.Hub, ns); serr != nil {
								_, _ = fmt.Fprintf(stderr, "warning: could not adopt namespace from hub: %v\n", serr)
							} else {
								res.Namespace = ns
							}
						}
					}
				}
			}

			// 5. Summary.
			out := cmd.OutOrStdout()
			verb := "Updated"
			if res.Created {
				verb = "Wrote"
			}
			_, _ = fmt.Fprintf(out, "%s %s\n", verb, res.Path)
			_, _ = fmt.Fprintf(out, "  kind:               %s\n", res.Kind)
			_, _ = fmt.Fprintf(out, "  fallback hub:       %s\n", res.Hub)
			_, _ = fmt.Fprintf(out, "  fallback namespace: %s\n", res.Namespace)
			for _, w := range res.Warnings {
				_, _ = fmt.Fprintf(stderr, "warning: %s: %s\n", w.Field, w.Message)
			}
			if res.HubURL != "" && !loggedIn {
				_, _ = fmt.Fprintf(out, "\nNext: run `tap auth login` to authenticate with %s.\n", res.Hub)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "", "deployment kind: local | cloud | enterprise")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "enterprise hub endpoint URL (required for --kind enterprise)")
	cmd.Flags().StringVar(&hubName, "hub-name", "", "name to record an enterprise hub under (default: derived from the endpoint host)")
	cmd.Flags().BoolVar(&login, "login", false, "log in to the hub after writing config (cloud/enterprise)")
	cmd.Flags().BoolVar(&noLogin, "no-login", false, "skip the login step even on a TTY")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "skip interactive prompts even when stdin is a TTY")

	mustRegisterFlagCompletion(cmd, "kind", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		kinds := []string{tapper.BootstrapKindLocal, tapper.BootstrapKindCloud, tapper.BootstrapKindEnterprise}
		return filterByPrefix(kinds, toComplete), cobra.ShellCompDirectiveNoFileComp
	})
	mustRegisterFlagCompletion(cmd, "endpoint", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
	mustRegisterFlagCompletion(cmd, "hub-name", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// parseBootstrapKind normalizes a kind answer, accepting the full word, a
// single-letter shortcut, or empty (which defaults to cloud).
func parseBootstrapKind(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "c", "cloud":
		return tapper.BootstrapKindCloud, nil
	case "l", "local":
		return tapper.BootstrapKindLocal, nil
	case "e", "enterprise":
		return tapper.BootstrapKindEnterprise, nil
	default:
		return "", fmt.Errorf("invalid kind %q: expected local, cloud, or enterprise", s)
	}
}

// hostOf returns the host portion of a URL for display, falling back to the
// raw string when it does not parse.
func hostOf(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return rawURL
}

// bootstrapHubNamespace probes the hub for the just-authenticated user's home
// namespace so `tap bootstrap` can adopt it. It reads the token runAuthLogin
// just persisted, calls the hub's whoami endpoint through the same seam
// `tap auth login --with-token` validates against, and returns
// default_namespace (falling back to the username). Best-effort by design: any
// failure — no stored token, hub unreachable, rejected token — yields "" and
// the caller keeps the provisional namespace rather than failing the bootstrap.
func bootstrapHubNamespace(ctx context.Context, deps *Deps, hubURL string) string {
	rt := deps.Runtime
	store, err := tapper.LoadAuthStore(ctx, rt, deps.Tap.PathService.AuthStorePath())
	if err != nil {
		return ""
	}
	entry, ok := store.Get(tapper.CanonicalHubURL(hubURL))
	if !ok || entry == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	who, err := deps.AuthValidateTokenFn(ctx, rt, hubURL, entry.AccessToken)
	if err != nil || who == nil {
		return ""
	}
	if ns := strings.TrimSpace(who.DefaultNamespace); ns != "" {
		return ns
	}
	return strings.TrimSpace(who.Username)
}
