package cli

import (
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// newKegCreateCmd returns the hub-only `tap keg create` command.
func newKegCreateCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [name | @namespace/name]",
		Short: "create a new KEG on a configured hub",
		Long:  "Create a KEG through the configured Tapper Hub. Filesystem destinations are not supported.",
		Example: strings.TrimSpace(`
tap keg create notes
tap keg create @acme/engineering --title "Engineering"
tap keg create notes --hub enterprise --namespace alice
`),
	}
	configureKegCreateCmd(deps, cmd)
	return cmd
}

func configureKegCreateCmd(deps *Deps, cmd *cobra.Command) {
	options := tapper.InitOptions{}
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		options.RequireBootstrap = deps.Profile.withDefaults().IncludeConfigCommand
		if len(args) == 1 {
			namespace, name, err := parseKegArg(args[0])
			if err != nil {
				return err
			}
			options.Keg = name
			if options.Namespace == "" {
				options.Namespace = namespace
			}
		}
		if strings.TrimSpace(options.Keg) == "" {
			return fmt.Errorf("KEG name is required")
		}
		if options.RequireBootstrap && deps.Tap != nil && !deps.Tap.ConfigService.UserConfigExists() {
			return tapper.ErrNotBootstrapped
		}

		target, err := deps.Tap.InitKeg(cmd.Context(), options)
		if err != nil {
			return err
		}
		message := fmt.Sprintf("keg %s created", options.Keg)
		if label := tapper.KegBackendLabel(target); label != "" {
			message += fmt.Sprintf(" (%s)", label)
		}
		if location := tapper.KegLocation(target); location != "" {
			message += " " + location
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), message)
		return err
	}

	cmd.Flags().StringVar(&options.Hub, "hub", "", "configured hub name")
	cmd.Flags().StringVar(&options.Namespace, "namespace", "", "namespace the KEG belongs to")
	cmd.Flags().StringVarP(&options.Keg, "keg", "k", "", "KEG name")
	cmd.Flags().StringVar(&options.Title, "title", "", "human-readable KEG title")
	cmd.Flags().StringVar(&options.Visibility, "visibility", "", "KEG visibility: private or public")
}

func parseKegArg(arg string) (namespace, name string, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", nil
	}
	if strings.HasPrefix(arg, "@") {
		namespace, name, ok := strings.Cut(strings.TrimPrefix(arg, "@"), "/")
		namespace = strings.TrimSpace(namespace)
		name = strings.TrimSpace(name)
		if !ok || namespace == "" || name == "" {
			return "", "", fmt.Errorf("invalid KEG reference %q: expected @namespace/name", arg)
		}
		return namespace, name, nil
	}
	if strings.Contains(arg, "/") {
		return "", "", fmt.Errorf("invalid KEG name %q: use @namespace/name to qualify a namespace", arg)
	}
	return "", arg, nil
}
