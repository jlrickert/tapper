package cmd

//
// import (
// 	"fmt"
// 	"io"
// 	"os"
// 	"sort"
// 	"strings"
// 	"time"
//
// 	"github.com/jlrickert/tapper/pkg/internal"
// 	"github.com/jlrickert/tapper/pkg/keg"
// 	"github.com/spf13/cobra"
// 	"gopkg.in/yaml.v3"
// )
//
// // newCreateCmdWithDeps constructs the `keg create` command using the provided
// // injectable dependencies. Passing a CmdDeps lets tests and callers provide a
// // MemoryRepo-backed Keg service and custom IO streams. The command returned is
// // fully wired with flags and behavior.
// func newCreateCmdWithDeps(deps *CmdDeps) *cobra.Command {
// 	var (
// 		flagID     int
// 		flagTitle  string
// 		flagTags   string
// 		flagAuthor string
// 		flagForce  bool
// 		flagEdit   bool
// 	)
//
// 	cmd := &cobra.Command{
// 		Use:     "create",
// 		Aliases: []string{"c"},
// 		Short:   "Create a new node (reads content from stdin or opens editor)",
// 		Long: `Create a KEG node.
//
// By default this command will attempt to read content from stdin (if piped)
// or open an editor to compose README.md. Several flags allow providing
// metadata non-interactively: --id, --title, --tags, --author, and --stdin.`,
// 		RunE: func(cmd *cobra.Command, args []string) error {
// 			// If no Keg service provided, remain in stubbed behavior but show
// 			// TEST marker for backward compatibility with tests that expect
// 			// the simple output.
// 			if deps.Keg == nil || deps.Keg.Repo == nil {
// 				return nil
// 			}
// 			fmt.Fprintln(cmd.OutOrStdout())
// 			return nil
//
// 			// Ensure deps have sensible defaults so callers/tests that pass nil still work.
// 			// Allow CmdDeps.ApplyDefaults to set any sensible defaults (no-op in tests by design).
// 			_ = deps.ApplyDefaults()
//
// 			// Decide whether to read content from stdin.
// 			readFromStdin, _ := internal.IsPipe()
//
// 			var content []byte
// 			var err error
//
// 			////////////////////////////////////////
// 			// Get the content
// 			//
// 			// Get the content first as title and tags may be derived from it
// 			////////////////////////////////////////
// 			if readFromStdin {
// 				// Drain/read stdin so callers can pipe into the command.
// 				content, err = io.ReadAll(deps.In)
// 				if err != nil {
// 					// Report and continue with empty content (caller may still want to create metadata-only node).
// 					fmt.Fprintln(deps.Err, "warning: failed reading stdin:", err)
// 					content = nil
// 				}
// 			} else if flagEdit {
// 				// Open an editor on a temp file and read the result.
// 				tmp, err := os.CreateTemp("", "keg-create-*.md")
// 				if err != nil {
// 					fmt.Fprintln(deps.Err, "warning: failed to create temp file:", err)
// 				} else {
// 					tmpPath := tmp.Name()
// 					_ = tmp.Close()
//
// 					// Prefer injected editor runner if present.
// 					if deps.Editor != nil {
// 						err = deps.Editor(tmpPath)
// 					} else {
// 						// Fallback to the package helper that respects $VISUAL/$EDITOR.
// 						err = deps.Editor(tmpPath)
// 					}
// 					if err != nil {
// 						fmt.Fprintln(deps.Err, "warning: editor returned error:", err)
// 					} else {
// 						content, _ = os.ReadFile(tmpPath)
// 					}
// 					_ = os.Remove(tmpPath)
// 				}
// 			}
//
// 			// Normalize tags
// 			var tags []string
// 			if strings.TrimSpace(flagTags) != "" {
// 				tags = keg.ParseTags(strings.TrimSpace(flagTags))
// 			}
//
// 			repo := deps.Keg.Repo
// 			dex := deps.Keg.Dex
//
// 			// Determine target ID
// 			var targetID keg.Node
// 			if flagID > 0 {
// 				targetID = keg.Node(flagID)
// 				exisitingNode := dex.GetNode(targetID)
// 				if exisitingNode != nil && flagForce {
// 					targetID = exisitingNode.ID
// 				} else if exisitingNode != nil {
// 					return keg.NewDestinationExistsError(targetID)
// 				} else {
// 					targetID = keg.Node(flagID)
// 				}
// 			} else {
// 				targetID, err = deps.Keg.NextID(cmd.Context())
// 			}
//
// 			deps.Keg.CreateNode(cmd.Context(), keg.NodeCreateOptions{
// 				ID:      targetID,
// 				Meta:    nil,
// 				Content: content,
// 			})
//
// 			// Build meta map
// 			now := time.Now().UTC()
// 			// Allow tests to override clock via deps.Clock if present
// 			if deps.Clock != nil {
// 				now = deps.Clock.Now().UTC()
// 			}
//
// 			metaMap := map[string]any{
// 				"updated": now.Format(time.RFC3339),
// 			}
// 			if strings.TrimSpace(flagTitle) != "" {
// 				metaMap["title"] = flagTitle
// 			}
// 			if len(tags) > 0 {
// 				// ensure deterministic ordering and uniqueness
// 				sort.Strings(tags)
// 				metaMap["tags"] = tags
// 			}
// 			if strings.TrimSpace(flagAuthor) != "" {
// 				metaMap["authors"] = []string{flagAuthor}
// 			}
//
// 			metaBytes, err := yaml.Marshal(metaMap)
// 			if err != nil {
// 				return fmt.Errorf("failed to marshal meta.yaml: %w", err)
// 			}
//
// 			// Write meta and content atomically via repo interface.
// 			// Behavior: write meta then content. Repositories are expected to handle
// 			// id creation/updating semantics (MemoryRepo creates nodes on WriteContent).
// 			if err := repo.WriteMeta(targetID, metaBytes); err != nil {
// 				// Try to wrap or return as-is
// 				return fmt.Errorf("failed to write meta for node %d: %w", int(targetID), err)
// 			}
//
// 			if content != nil {
// 				if err := repo.WriteContent(targetID, content); err != nil {
// 					return fmt.Errorf("failed to write content for node %d: %w", int(targetID), err)
// 				}
// 			} else {
// 				// Ensure content exists as empty if repo expects it
// 				if err := repo.WriteContent(targetID, []byte{}); err != nil {
// 					return fmt.Errorf("failed to write empty content for node %d: %w", int(targetID), err)
// 				}
// 			}
//
// 			// Success: print created node id
// 			fmt.Fprintf(deps.Out, "created node %d\n", int(targetID))
// 			return nil
// 		},
// 	}
//
// 	// Common flags for create and subcommands
// 	cmd.Flags().IntVar(&flagID, "id", 0, "Optional explicit node id to allocate")
// 	cmd.Flags().StringVar(&flagTitle, "title", "", "Optional node title")
// 	cmd.Flags().StringVar(&flagTags, "tags", "", "Comma-separated list of tags (example: a,b,c)")
// 	cmd.Flags().StringVar(&flagAuthor, "author", "", "Author string (e.g., 'Name <email>')")
// 	cmd.Flags().BoolVar(&flagStdin, "stdin", false, "If true, read content from stdin/pipe instead of opening an editor")
// 	cmd.Flags().BoolVar(&flagForce, "force", false, "Force creation / overwrite if applicable")
// 	cmd.Flags().BoolVar(&flagEdit, "edit", false, "Open node content in editor")
//
// 	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
// 		return nil, cobra.ShellCompDirectiveNoFileComp
// 	}
//
// 	return cmd
// }
