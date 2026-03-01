package cli

import (
	"fmt"
	"regexp"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// kegImportArgRefRE matches keg:ALIAS/N argument format.
var kegImportArgRefRE = regexp.MustCompile(`^keg:([a-zA-Z0-9][a-zA-Z0-9_-]*)/([0-9]+)$`)

func NewImportCmd(deps *Deps) *cobra.Command {
	var opts tapper.ImportFromKegOptions
	var fromKeg string

	opts.SkipZeroNode = true

	cmd := &cobra.Command{
		Use:   "import [NODE_ID | keg:ALIAS/NODE_ID]...",
		Short: "import nodes from another keg",
		Long: `Import nodes from a source keg into the target keg.

Each imported node is assigned a fresh ID. Links in the copied content are
rewritten:

  ../N (imported)          -> ../NEW_ID
  ../N (not imported)      -> keg:SOURCE/N
  keg:TARGET/N             -> ../N
  keg:SOURCE/N (imported)  -> ../NEW_ID
  keg:SOURCE/N (other)     -> unchanged
  keg:OTHER/N              -> unchanged

Nodes may be specified as bare IDs with --from SOURCE, or as keg:ALIAS/NODE_ID
references. All must come from the same source keg.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Extract source alias from keg:ALIAS/N args when --from is absent.
			if fromKeg == "" {
				for _, arg := range args {
					if m := kegImportArgRefRE.FindStringSubmatch(arg); m != nil {
						if fromKeg != "" && m[1] != fromKeg {
							return fmt.Errorf("conflicting source keg aliases %q and %q in arguments", fromKeg, m[1])
						}
						fromKeg = m[1]
					}
				}
			}
			if fromKeg == "" {
				return fmt.Errorf("--from SOURCE is required (or use keg:ALIAS/NODE_ID references)")
			}

			opts.Source.Keg = fromKeg
			opts.NodeIDs = args
			applyKegTargetProfile(deps, &opts.Target)

			imported, err := deps.Tap.ImportFromKeg(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, node := range imported {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s -> %s\n",
					node.SourceID.Path(), node.TargetID.Path()); err != nil {
					return err
				}
			}
			if len(imported) > 0 {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "\nimported %d node(s)\n", len(imported)); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&fromKeg, "from", "", "source keg alias; required when using bare node IDs")
	cmd.Flags().StringVar(&opts.TagQuery, "tags", "", `boolean tag expression to select source nodes (e.g. "golang and not archived")`)
	cmd.Flags().BoolVar(&opts.LeaveStubs, "leave-stubs", false, "write forwarding stubs at source node locations after import")
	cmd.Flags().BoolVar(&opts.SkipZeroNode, "skip-zero", true, "skip source node 0 (default true)")

	_ = cmd.RegisterFlagCompletionFunc("from", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if deps.Tap == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		kegs, _ := deps.Tap.ListKegs(true)
		return kegs, cobra.ShellCompDirectiveNoFileComp
	})

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		src, _ := cmd.Flags().GetString("from")
		if src == "" || deps.Tap == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ids, err := deps.Tap.List(cmd.Context(), tapper.ListOptions{
			KegTargetOptions: tapper.KegTargetOptions{Keg: src},
			IdOnly:           true,
		})
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return ids, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}
