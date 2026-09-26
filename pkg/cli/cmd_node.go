package cli

import "github.com/spf13/cobra"

// NewNodeCmd groups the commands that read and change KEG nodes. Each
// subcommand is the CLI peer of the node_<verb> MCP tool of the same verb.
//
//	tap node read 12
//	tap node list --query 'tag:draft'
//	tap node search 'TODO'
//	tap node create < note.md
//	tap node edit 12
//	tap node move 12 40
//	tap node delete 12
func NewNodeCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "read, search and change KEG nodes",
	}
	cmd.AddCommand(
		NewCatCmd(deps),
		NewListCmd(deps),
		NewGrepCmd(deps),
		NewLinksCmd(deps),
		NewBacklinksCmd(deps),
		NewStatsCmd(deps),
		NewCreateCmd(deps),
		NewEditCmd(deps),
		NewMetaCmd(deps),
		NewMoveCmd(deps),
		NewRemoveCmd(deps),
		NewWatchCmd(deps),
	)
	return cmd
}

// NewTagCmd groups tag commands: `tap tag list` is the peer of tag_list.
func NewTagCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "list tags and filter nodes by tag",
	}
	cmd.AddCommand(NewTagsCmd(deps))
	return cmd
}
