package cli

// `tap bootstrap` — first-run onboarding. Walks the user through a deployment
// kind (local / cloud / enterprise), writes a usable user config, and
// optionally drives a hub login by reusing runAuthLogin. CLI-only: login and
// the conversational prompt are not agent operations, so there is no MCP
// surface (Tap.Bootstrap is listed in pkg/parity's tapMethodsExcluded).

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
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
		defaultKeg     string
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

Bootstrap writes the matching fallback hub and ensures the built-in local hub
is always available. The namespace comes from the hub itself: @local for local,
and your home namespace (adopted at login) for cloud/enterprise. It is
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
			resolvedHub := ""
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
					hub, lerr := runAuthLogin(ctx, deps, authLoginParams{HubURL: res.HubURL, Method: methodBrowser})
					if lerr != nil {
						if login {
							return lerr
						}
						_, _ = fmt.Fprintf(stderr, "login failed (continuing): %v\n", lerr)
					} else {
						loggedIn = true
						resolvedHub = hub
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

			// 4b. Choose a default keg so a plain `tap` command resolves one
			// after bootstrap (otherwise the first `tap 0` fails with no keg
			// configured). An explicit --default-keg wins; otherwise prompt on a
			// TTY, listing the hub's reachable kegs when we just logged in.
			chosenKeg := strings.TrimSpace(defaultKeg)
			if chosenKeg == "" && interactive {
				var available []string
				if loggedIn && res.HubURL != "" {
					if token := bootstrapHubToken(ctx, deps, resolvedHub); token != "" {
						if kegs, lerr := tapper.ListUserKegs(ctx, res.HubURL, token); lerr == nil {
							for _, k := range kegs {
								available = append(available, "@"+k.Namespace+"/"+k.Alias)
							}
						}
					}
				}
				ref, perr := promptDefaultKeg(stderr, reader, available)
				if perr != nil {
					return perr
				}
				chosenKeg = ref
			}
			if chosenKeg != "" {
				if serr := deps.Tap.SetFallbackKeg(ctx, chosenKeg); serr != nil {
					_, _ = fmt.Fprintf(stderr, "warning: could not set default keg: %v\n", serr)
					chosenKeg = ""
				}
			}

			// 4c. For a local deployment, create the chosen keg now so the user
			// is immediately up and running — plain `tap` commands work without a
			// separate `tap keg create`. Local only: a remote create needs a live
			// login and hub permissions, so cloud/enterprise just record the keg.
			// Idempotent: a keg that already exists is fine.
			createdKegPath := ""
			if chosenKeg != "" && res.Kind == tapper.BootstrapKindLocal {
				ns, name, perr := parseKegArg(chosenKeg)
				if perr != nil {
					_, _ = fmt.Fprintf(stderr, "warning: could not create keg %q: %v\n", chosenKeg, perr)
				} else {
					target, cerr := deps.Tap.InitKeg(ctx, tapper.InitOptions{
						Keg:            name,
						Namespace:      ns,
						NonInteractive: true,
					})
					switch {
					case cerr == nil:
						if target != nil {
							createdKegPath = target.Path()
						}
					case errors.Is(cerr, keg.ErrExist):
						// Already exists — the user is still ready to go.
					default:
						_, _ = fmt.Fprintf(stderr, "warning: could not create keg %q: %v\n", chosenKeg, cerr)
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
			_, _ = fmt.Fprintf(out, "  kind:         %s\n", res.Kind)
			_, _ = fmt.Fprintf(out, "  fallback hub: %s\n", res.Hub)
			if res.Namespace != "" {
				_, _ = fmt.Fprintf(out, "  namespace:    %s\n", res.Namespace)
			}
			if chosenKeg != "" {
				_, _ = fmt.Fprintf(out, "  default keg:  %s\n", chosenKeg)
			}
			if createdKegPath != "" {
				_, _ = fmt.Fprintf(out, "  created keg:  %s\n", createdKegPath)
			}
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
	cmd.Flags().StringVar(&defaultKeg, "default-keg", "", "keg reference plain `tap` commands resolve by default (e.g. @you/notes); recorded as the user-level fallbackKeg so a project's defaultKeg or kegMap can override; prompts on a TTY when unset")
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

// bootstrapHubToken reads the bearer token runAuthLogin just persisted for
// hubURL so bootstrap can list the user's kegs. Best-effort: a missing store or
// entry yields "" and the caller falls back to a free-text default-keg prompt.
func bootstrapHubToken(ctx context.Context, deps *Deps, hubURL string) string {
	store, err := tapper.LoadAuthStore(ctx, deps.Runtime, deps.Tap.PathService.AuthStorePath())
	if err != nil {
		return ""
	}
	entry, ok := store.Get(tapper.CanonicalHubURL(hubURL))
	if !ok || entry == nil {
		return ""
	}
	return strings.TrimSpace(entry.AccessToken)
}

// promptDefaultKeg asks which keg plain `tap` commands resolve by default. When
// the hub returned reachable kegs, they are shown as a numbered menu the user
// can pick by number; the user may also type a reference (@ns/name or a path),
// or leave it blank to skip. Returns the chosen reference ("" when skipped).
func promptDefaultKeg(w io.Writer, r *bufio.Reader, available []string) (string, error) {
	if len(available) > 0 {
		_, _ = fmt.Fprintln(w, "Available kegs:")
		for i, k := range available {
			_, _ = fmt.Fprintf(w, "  %d) %s\n", i+1, k)
		}
		ans, err := promptLine(w, r, "Default keg (number, @ns/name, or blank to skip): ")
		if err != nil {
			return "", err
		}
		ans = strings.TrimSpace(ans)
		if ans == "" {
			return "", nil
		}
		if n, err := strconv.Atoi(ans); err == nil && n >= 1 && n <= len(available) {
			return available[n-1], nil
		}
		return ans, nil
	}
	ans, err := promptLine(w, r, "Default keg for plain `tap` commands (e.g. @you/notes, blank to skip): ")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(ans), nil
}
