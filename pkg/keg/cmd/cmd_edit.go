package cmd

// import (
// 	"fmt"
//
// 	"github.com/spf13/cobra"
// )
//
// func newEditCmd() *cobra.Command {
// 	return &cobra.Command{
// 		Use:   "edit <id> [file]",
// 		Short: "Edit node content or a specific file within a node",
// 		Args:  cobra.MinimumNArgs(1),
// 		Run: func(cmd *cobra.Command, args []string) {
// 			id := args[0]
// 			if len(args) > 1 {
// 				fmt.Fprintf(cmd.OutOrStdout(), "keg edit: would edit node %s file %s (not implemented)\n", id, args[1])
// 				return
// 			}
// 			fmt.Fprintf(cmd.OutOrStdout(), "keg edit: would edit node %s (not implemented)\n", id)
// 		},
// 	}
// }
