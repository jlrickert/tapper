package cli

// `tap bootstrap` — first-run onboarding. Walks the user through a deployment
// kind (local / cloud / enterprise), writes a usable user config, and
// optionally drives a hub login by reusing runAuthLogin. CLI-only: login and
// the conversational prompt are not agent operations, so there is no MCP
// surface (Tap.Bootstrap is listed in pkg/parity's tapMethodsExcluded).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

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

			stderr := cmd.ErrOrStderr()
			interactive := rt.Stream().IsTTY && !nonInteractive

			// 1. Resolve the deployment kind.
			if strings.TrimSpace(kind) == "" {
				if interactive {
					ans, err := deps.BootstrapPrompter.SelectBootstrapKind()
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
					ans, err := deps.BootstrapPrompter.PromptBootstrapEndpoint()
					if err != nil {
						return err
					}
					endpoint = ans
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
					ok, perr := deps.BootstrapPrompter.ConfirmBootstrapLogin(hostOf(res.HubURL))
					if perr != nil {
						return perr
					}
					doLogin = ok
				}
				if doLogin {
					method := methodBrowser
					var token string
					if interactive {
						m, perr := deps.AuthPrompter.SelectMethod()
						if perr != nil {
							return perr
						}
						method = m
						if method == methodToken {
							tok, perr := deps.AuthPrompter.PromptToken()
							if perr != nil {
								return perr
							}
							token = tok
						}
					}
					loginRes, lerr := runAuthLogin(ctx, deps, authLoginParams{HubURL: res.HubURL, Method: method, Token: token})
					if lerr != nil {
						if login {
							return lerr
						}
						_, _ = fmt.Fprintf(stderr, "login failed (continuing): %v\n", lerr)
					} else {
						loggedIn = true
						resolvedHub = loginRes.HubURL
						_, _ = fmt.Fprintf(stderr, "Logged in to %s\n", resolvedHub)
						if loginRes.Namespace != "" {
							res.Namespace = loginRes.Namespace
						}
					}
				}
			}

			// 4b. Choose a default keg so a plain `tap` command resolves one
			// after bootstrap (otherwise the first `tap 0` fails with no keg
			// configured). An explicit --default-keg wins; otherwise prompt on a
			// TTY, listing the hub's reachable kegs when we just logged in. If
			// a freshly-authenticated hub reports no kegs, immediately offer the
			// create flow so the user leaves bootstrap with a usable default.
			chosenKeg := strings.TrimSpace(defaultKeg)
			createdKegLocation := ""
			if chosenKeg == "" && interactive {
				ref, created, perr := chooseBootstrapDefaultKeg(ctx, deps, res, loggedIn, resolvedHub, stderr)
				if perr != nil {
					return perr
				}
				chosenKeg = ref
				createdKegLocation = created
			}

			// 4c. For a local deployment, create the chosen keg now so the user
			// is immediately up and running — plain `tap` commands work without a
			// separate `tap keg create`. Remote bootstrap creates during the
			// interactive chooser above only after a successful login; explicit
			// --default-keg stays a recorded default. Idempotent: a keg that
			// already exists is fine.
			if chosenKeg != "" && createdKegLocation == "" && res.Kind == tapper.BootstrapKindLocal {
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
							createdKegLocation = bootstrapCreatedKegSummary(bootstrapKegRef(ns, name, res.Namespace), target)
						}
					case errors.Is(cerr, keg.ErrExist):
						// Already exists — the user is still ready to go.
					default:
						_, _ = fmt.Fprintf(stderr, "warning: could not create keg %q: %v\n", chosenKeg, cerr)
					}
				}
			}

			if chosenKeg != "" {
				if serr := deps.Tap.SetFallbackKeg(ctx, chosenKeg); serr != nil {
					_, _ = fmt.Fprintf(stderr, "warning: could not set default keg: %v\n", serr)
					chosenKeg = ""
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
			if createdKegLocation != "" {
				_, _ = fmt.Fprintf(out, "  created keg:  %s\n", createdKegLocation)
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

func chooseBootstrapDefaultKeg(ctx context.Context, deps *Deps, res *tapper.BootstrapResult, loggedIn bool, resolvedHub string, stderr io.Writer) (string, string, error) {
	if loggedIn && res.HubURL != "" {
		hubURL := strings.TrimSpace(resolvedHub)
		if hubURL == "" {
			hubURL = res.HubURL
		}
		available, listed, err := bootstrapListKegRefs(ctx, deps, hubURL)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "warning: could not list kegs from %s: %v\n", res.Hub, err)
		}
		if listed {
			if len(available) == 0 {
				name, err := deps.BootstrapPrompter.PromptNewKegName()
				if err != nil {
					return "", "", err
				}
				return bootstrapCreateRemoteDefaultKeg(ctx, deps, res, name)
			}
			choice, err := deps.BootstrapPrompter.SelectDefaultKeg(available)
			if err != nil {
				return "", "", err
			}
			switch choice.Action {
			case bootstrapDefaultKegUseRef:
				return strings.TrimSpace(choice.Ref), "", nil
			case bootstrapDefaultKegManual:
				return promptManualBootstrapDefaultKeg(deps)
			case bootstrapDefaultKegCreate:
				name, err := deps.BootstrapPrompter.PromptNewKegName()
				if err != nil {
					return "", "", err
				}
				return bootstrapCreateRemoteDefaultKeg(ctx, deps, res, name)
			case bootstrapDefaultKegSkip:
				return "", "", nil
			default:
				return "", "", fmt.Errorf("unknown default keg action %d", choice.Action)
			}
		}
	}
	return promptManualBootstrapDefaultKeg(deps)
}

func promptManualBootstrapDefaultKeg(deps *Deps) (string, string, error) {
	ref, err := deps.BootstrapPrompter.PromptManualDefaultKeg()
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(ref), "", nil
}

func bootstrapListKegRefs(ctx context.Context, deps *Deps, hubURL string) ([]string, bool, error) {
	token := bootstrapHubToken(ctx, deps, hubURL)
	if token == "" {
		return nil, false, nil
	}
	kegs, err := tapper.ListUserKegs(ctx, hubURL, token)
	if err != nil {
		return nil, false, err
	}
	seen := map[string]bool{}
	refs := make([]string, 0, len(kegs))
	for _, k := range kegs {
		ns := strings.TrimSpace(k.Namespace)
		alias := strings.TrimSpace(k.Alias)
		if ns == "" || alias == "" {
			continue
		}
		ref := "@" + ns + "/" + alias
		if seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs, true, nil
}

func bootstrapCreateRemoteDefaultKeg(ctx context.Context, deps *Deps, res *tapper.BootstrapResult, alias string) (string, string, error) {
	alias = strings.TrimSpace(alias)
	if err := tapper.ValidateKegAlias(alias); err != nil {
		return "", "", err
	}
	namespace := strings.TrimSpace(res.Namespace)
	if namespace == "" {
		return "", "", fmt.Errorf("cannot create a keg before %s reports a default namespace; run `tap auth login` and try `tap keg create %s`", res.Hub, alias)
	}
	ref := bootstrapKegRef(namespace, alias, "")
	target, err := deps.Tap.InitKeg(ctx, tapper.InitOptions{
		Keg:              alias,
		Hub:              res.Hub,
		Namespace:        namespace,
		NonInteractive:   true,
		RequireBootstrap: true,
	})
	switch {
	case err == nil:
		return ref, bootstrapCreatedKegSummary(ref, target), nil
	case errors.Is(err, keg.ErrExist):
		return ref, "", nil
	default:
		return "", "", fmt.Errorf("create keg %s: %w", ref, err)
	}
}

func bootstrapKegRef(namespace, name, fallbackNamespace string) string {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = strings.TrimSpace(fallbackNamespace)
	}
	name = strings.TrimSpace(name)
	if ns == "" {
		return name
	}
	return "@" + ns + "/" + name
}

func bootstrapCreatedKegSummary(ref string, target *keg.Target) string {
	ref = strings.TrimSpace(ref)
	loc := tapper.KegLocation(target)
	switch {
	case ref != "" && loc != "":
		return ref + " " + loc
	case ref != "":
		return ref
	default:
		return loc
	}
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
