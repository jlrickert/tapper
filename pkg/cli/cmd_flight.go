package cli

import (
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewFlightCmd returns the `flight` cobra command group. A flight carries MCP
// cover caps, capabilities, and agent instructions.
//
//	tap flight list
//	tap flight show <name>
//	tap flight create @ns/+slug --cover @ns/keg=viewer
func NewFlightCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flight",
		Short: "manage flights",
		Long:  `A flight carries MCP cover caps plus agent instructions. Discover flights with "flight list", inspect one with "flight show", and manage Hub-backed flights with create/edit/delete.`,
	}
	cmd.AddCommand(
		newFlightListCmd(deps),
		newFlightShowCmd(deps),
		newFlightCreateCmd(deps),
		newFlightEditCmd(deps),
		newFlightDeleteCmd(deps),
	)
	return cmd
}

func newFlightListCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list available flights",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var warnings []string
			names, err := deps.Tap.ListFlights(cmd.Context(), tapper.ListFlightsOptions{Warnings: &warnings})
			if err != nil {
				return err
			}
			for _, w := range warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		},
	}
}

func newFlightShowCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <ref>",
		Short: "show a flight's cover roles and instructions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flight, err := deps.Tap.GetFlight(cmd.Context(), tapper.GetFlightOptions{Name: args[0]})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "flight: %s\n", flight.Name)
			if flight.Title != "" {
				fmt.Fprintf(out, "title:  %s\n", flight.Title)
			}
			fmt.Fprintf(out, "source: %s\n", flight.Source)
			fmt.Fprintf(out, "visibility: %s\n", flight.Visibility)
			if len(flight.Capabilities) > 0 {
				fmt.Fprintf(out, "capabilities: %s\n", strings.Join(flightCapabilityStrings(flight.Capabilities), ", "))
			}
			if len(flight.Cover) > 0 {
				fmt.Fprintln(out, "cover:")
				for _, c := range flight.Cover {
					ns := c.Namespace
					if ns != "" {
						fmt.Fprintf(out, "  @%s/%s=%s\n", ns, c.Keg, c.Role)
					} else {
						fmt.Fprintf(out, "  %s=%s\n", c.Keg, c.Role)
					}
				}
			} else {
				fmt.Fprintln(out, "cover: (none; denies all KEG access)")
			}
			if flight.Instructions != "" {
				fmt.Fprintf(out, "\n%s\n", flight.Instructions)
			}
			return nil
		},
	}
	cmd.ValidArgsFunction = flightArgCompletionFunc(deps)
	return cmd
}

func newFlightCreateCmd(deps *Deps) *cobra.Command {
	var title, visibility, instructions, instructionsFile string
	var coverSpecs []string
	var capabilities []string
	cmd := &cobra.Command{
		Use:   "create <ref>",
		Short: "create a Hub-backed flight",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cover, err := parseFlightCoverSpecs(coverSpecs)
			if err != nil {
				return err
			}
			body, err := readFlightInstructions(deps, instructions, instructionsFile)
			if err != nil {
				return err
			}
			flight, err := deps.Tap.CreateFlight(cmd.Context(), tapper.CreateFlightOptions{
				Ref:          args[0],
				Title:        title,
				Visibility:   visibility,
				Capabilities: flightCapabilities(capabilities),
				Instructions: body,
				Cover:        cover,
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), flight.Name)
			return nil
		},
	}
	addFlightWriteFlags(cmd, &title, &instructions, &instructionsFile, &coverSpecs)
	cmd.Flags().StringVar(&visibility, "visibility", tapper.FlightVisibilityPrivate, "flight visibility: public or private")
	cmd.Flags().StringArrayVar(&capabilities, "capability", nil, "agent capability (repeatable; supported: full_access, manage_flights)")
	return cmd
}

func flightCapabilities(values []string) []tapper.FlightCapability {
	out := make([]tapper.FlightCapability, 0, len(values))
	for _, value := range values {
		out = append(out, tapper.FlightCapability(value))
	}
	return out
}

func flightCapabilityStrings(values []tapper.FlightCapability) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func newFlightEditCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <ref>",
		Short: "edit a Hub-backed flight's manifest in the default editor",
		Long:  `Opens the flight manifest (title, visibility, capabilities, cover, instructions) as YAML in the configured editor with a yaml-language-server schema modeline; every save is applied to the hub. Piped stdin applies a full manifest without opening an editor.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flight, err := deps.Tap.EditFlight(cmd.Context(), tapper.EditFlightOptions{
				Ref:    args[0],
				Stream: deps.Runtime.Stream(),
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), flight.Name)
			return nil
		},
	}
	cmd.ValidArgsFunction = flightArgCompletionFunc(deps)
	return cmd
}

func newFlightDeleteCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <ref>",
		Short: "delete a Hub-backed flight",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deps.Tap.DeleteFlight(cmd.Context(), tapper.DeleteFlightOptions{Ref: args[0]})
		},
	}
	cmd.ValidArgsFunction = flightArgCompletionFunc(deps)
	return cmd
}

func addFlightWriteFlags(cmd *cobra.Command, title, instructions, instructionsFile *string, cover *[]string) {
	cmd.Flags().StringVar(title, "title", "", "flight title")
	cmd.Flags().StringVar(instructions, "instructions", "", "markdown instructions")
	cmd.Flags().StringVar(instructionsFile, "instructions-file", "", "read markdown instructions from a file")
	cmd.Flags().StringArrayVar(cover, "cover", nil, "covered keg and role cap, e.g. @ns/keg=viewer, @ns/keg=editor, or @ns/keg=admin")
}

func flightArgCompletionFunc(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		names, err := deps.Tap.ListFlights(cmd.Context(), tapper.ListFlightsOptions{})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return filterByPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func parseFlightCoverSpecs(specs []string) ([]tapper.FlightCover, error) {
	return tapper.ParseFlightCoverSpecs(specs)
}

func readFlightInstructions(deps *Deps, inline, file string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return inline, nil
	}
	if deps == nil || deps.Runtime == nil {
		return "", fmt.Errorf("runtime is required")
	}
	b, err := deps.Runtime.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("read instructions file: %w", err)
	}
	return string(b), nil
}
