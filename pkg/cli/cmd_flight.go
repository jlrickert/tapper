package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewFlightCmd returns the `flight` cobra command group. A flight restricts the
// kegs available in a session and carries agent instructions; it is selected
// per-invocation with the global --flight flag.
//
//	tap flight list
//	tap flight show <name>
func NewFlightCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flight",
		Short: "list and inspect flights",
		Long:  `A flight restricts which kegs are available and carries agent instructions. Discover flights with "flight list" and inspect one with "flight show".`,
	}
	cmd.AddCommand(newFlightListCmd(deps), newFlightShowCmd(deps))
	return cmd
}

func newFlightListCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list available flights",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			names, err := deps.Tap.ListFlights(cmd.Context(), tapper.ListFlightsOptions{})
			if err != nil {
				return err
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
		Use:   "show <name>",
		Short: "show a flight's allowed kegs and instructions",
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
			if len(flight.AllowedKegs) > 0 {
				fmt.Fprintf(out, "allowed kegs: %v\n", flight.AllowedKegs)
			} else {
				fmt.Fprintln(out, "allowed kegs: (none — restricts nothing)")
			}
			if flight.Instructions != "" {
				fmt.Fprintf(out, "\n%s\n", flight.Instructions)
			}
			return nil
		},
	}
	cmd.ValidArgsFunction = func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		names, err := deps.Tap.ListFlights(cmd.Context(), tapper.ListFlightsOptions{})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return filterByPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}
