package cli

import (
	"context"
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewExploreCmd returns the `explore` cobra command: the hub's public
// directory. Everything the caller already holds is listed by `keg list`,
// `flight list`, and `namespace list` instead.
//
// Usage examples:
//
//	tap explore kegs golang
//	tap explore flights onboarding
//	tap explore people acme
func NewExploreCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explore",
		Short: "search the hub's public directory",
		Long: `Search the public directory of the active Hub: public KEGs, public
flights, and the people and org namespaces on it. Discovery only: a match
grants nothing. Your own KEGs, flights, and namespaces come from the list
commands.`,
	}
	cmd.AddCommand(
		newExploreKindCmd(deps, "kegs", "search the hub's public KEGs", (*tapper.Tap).ExploreKegs),
		newExploreKindCmd(deps, "flights", "search the hub's public flights", (*tapper.Tap).ExploreFlights),
		newExploreKindCmd(deps, "people", "search the people and org namespaces on the hub", (*tapper.Tap).ExplorePeople),
	)
	return cmd
}

func newExploreKindCmd(deps *Deps, use, short string, run func(*tapper.Tap, context.Context, tapper.ExploreOptions) (tapper.ExploreResult, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use + " QUERY",
		Short: short,
		Long: short + `, printing REF, NAME, DESCRIPTION, ROLE.
ROLE is set where you already belong. The query is matched literally
against references, names, and descriptions.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := run(deps.Tap, cmd.Context(), tapper.ExploreOptions{Query: args[0], Hub: globalKegTarget(deps).Hub})
			if err != nil {
				return err
			}
			for _, row := range res.Results {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", row.Ref, row.Name, row.Description, row.Role)
			}
			if res.Truncated {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: results truncated; refine the query")
			}
			return nil
		},
	}
}
