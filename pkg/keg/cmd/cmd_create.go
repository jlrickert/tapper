package cmd

import (
	// "fmt"
	// "io"
	// "os"
	"strings"

	// "github.com/jlrickert/tapper/pkg/internal"
	"github.com/spf13/cobra"
)

// newCreateCmdWithDeps constructs the `keg create` command using the provided
// injectable dependencies. Passing a CmdDeps lets tests and callers provide a
// MemoryRepo-backed Keg service and custom IO streams. The command returned is
// fully wired with flags and behavior, but the implementation is intentionally
// stubbed: persistence is a no-op unless a real *keg.Keg is injected and the
// caller opts into destructive behavior (for example via --force).
func newCreateCmdWithDeps(deps *CmdDeps) *cobra.Command {
	var (
		flagID     int
		flagTitle  string
		flagTags   string
		flagAuthor string
		flagStdin  bool
		flagForce  bool
	)

	cmd := &cobra.Command{
		Use:     "create",
		Aliases: []string{"c"},
		Short:   "Create a new node (reads content from stdin or opens editor)",
		Long: `Create a KEG node.

By default this command will attempt to read content from stdin (if piped)
or open an editor to compose README.md. Several flags allow providing
metadata non-interactively: --id, --title, --tags, --author, and --stdin.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println("TEST")
			return nil
			// fmt.Fprintln(deps.Out, "TEST")
			// // Try to read content from the provided stdin file flag or from piped stdin.
			// var contentBytes []byte
			//
			// // Helper to read all from an io.Reader without adding new imports.
			// readAllFrom := func(r *os.File) ([]byte, error) {
			// 	if r == nil {
			// 		return nil, nil
			// 	}
			// 	out := make([]byte, 0, 4096)
			// 	buf := make([]byte, 4096)
			// 	for {
			// 		n, err := r.Read(buf)
			// 		if n > 0 {
			// 			out = append(out, buf[:n]...)
			// 		}
			// 		if err != nil {
			// 			// Any non-nil error we'll stop on (covers EOF and other read errors).
			// 			break
			// 		}
			// 	}
			// 	return out, nil
			// }
			//
			// isPipe, err := internal.IsPipe()
			// if err != nil {
			// 	fmt.Fprintln(deps.Out, "(no content provided; an empty README.md would be created)")
			// }
			//
			// if isPipe {
			// 	flagStdin = true
			// }
			//
			// if flagStdin {
			// 	var inFile *os.File
			// 	if f, ok := deps.In.(*os.File); ok && f != nil {
			// 		inFile = f
			// 	} else {
			// 		inFile = os.Stdin
			// 	}
			// 	data, err := io.ReadAll(inFile)
			// 	if err != nil {
			// 		fmt.Fprintln(deps.Out, "")
			// 	}
			// 	contentBytes = data
			// } else {
			// 	// No explicit stdin flag. If caller piped data into the command, read it.
			// 	// Basic heuristic: try reading from options.In (if it's not os.Stdin fallback).
			// 	var inFile *os.File
			// 	if f, ok := deps.In.(*os.File); ok && f != nil {
			// 		inFile = f
			// 	} else {
			// 		inFile = os.Stdin
			// 	}
			// 	// Attempt a non-blocking read: try to read once; if nothing returned, assume no piped input.
			// 	peekBuf := make([]byte, 0)
			// 	tmp := make([]byte, 4096)
			// 	n, err := inFile.Read(tmp)
			// 	if n > 0 {
			// 		peekBuf = append(peekBuf, tmp[:n]...)
			// 		// Read the rest
			// 		rest, _ := readAllFrom(inFile)
			// 		contentBytes = append(peekBuf, rest...)
			// 	} else {
			// 		// if we got an error or no bytes, we conservatively assume no piped input.
			// 		_ = err // ignore; behavior is simply to create an empty node if no content provided.
			// 	}
			// }
			//
			// // Normalize tags (the helper in this file is lightweight).
			// tags := normalizeTags(flagTags)
			//
			// // If title not provided, try to derive from content's first H1 line.
			// derivedTitle := flagTitle
			// if derivedTitle == "" && len(contentBytes) > 0 {
			// 	lines := strings.SplitN(string(contentBytes), "\n", 20)
			// 	for _, l := range lines {
			// 		l = strings.TrimSpace(l)
			// 		if after, ok := strings.CutPrefix(l, "# "); ok {
			// 			derivedTitle = strings.TrimSpace(after)
			// 			break
			// 		}
			// 	}
			// }
			//
			// // Output what we will do (non-destructive): show gathered inputs and basic diagnostics.
			// fmt.Fprintln(deps.Out, "keg create: running (experimental)")
			// fmt.Fprintf(deps.Out, "  id: %d\n", flagID)
			// if derivedTitle != "" {
			// 	fmt.Fprintf(deps.Out, "  title: %s\n", derivedTitle)
			// } else {
			// 	fmt.Fprintf(deps.Out, "  title: (none)\n")
			// }
			// fmt.Fprintf(deps.Out, "  tags: %v\n", tags)
			// if flagAuthor != "" {
			// 	fmt.Fprintf(deps.Out, "  author: %s\n", flagAuthor)
			// }
			// if flagStdin {
			// 	fmt.Fprintf(deps.Out, "  stdin-file: %t\n", flagStdin)
			// }
			// fmt.Fprintf(deps.Out, "  force: %v\n", flagForce)
			// if len(args) > 0 {
			// 	fmt.Fprintf(deps.Out, "  args: %v\n", args)
			// }
			//
			// // Show a short preview of content (first ~1k chars) if any was provided.
			// if len(contentBytes) > 0 {
			// 	preview := contentBytes
			// 	if len(preview) > 1024 {
			// 		preview = preview[:1024]
			// 	}
			// 	fmt.Fprintln(deps.Out, "\n--- content preview ---")
			// 	fmt.Fprintln(deps.Out, string(preview))
			// 	if len(contentBytes) > len(preview) {
			// 		fmt.Fprintln(deps.Out, "…(truncated)")
			// 	}
			// 	fmt.Fprintln(deps.Out, "-----------------------")
			// } else {
			// 	fmt.Fprintln(deps.Out, "(no content provided; an empty README.md would be created)")
			// }
			//
			// // If a Keg service was injected, inform the user that integration is available.
			// if deps.Keg != nil {
			// 	fmt.Fprintln(deps.Out, "\nNote: a Keg service is available via --with-keg; real persistence may be implemented to write nodes to the repository.")
			// } else {
			// 	fmt.Fprintln(deps.Out, "\nNote: no Keg service injected; this command is currently a no-op for persistence.")
			// }
			//
			// // For now this command is intentionally non-destructive (requires `--force` and a full implementation to actually write).
			// if !flagForce {
			// 	fmt.Fprintln(deps.Out, "\nTo perform a real create you must re-run with --force once you have verified the preview above.")
			// 	return nil
			// }
			//
			// // If --force was provided, but there is no injected Keg, we still avoid making assumptions
			// // about repo layout in this stubbed implementation; return success to indicate preview accepted.
			// if flagForce && deps.Keg == nil {
			// 	fmt.Fprintln(deps.Out, "Force accepted but no Keg repository configured; nothing written.")
			// 	return nil
			// }
			//
			// // If we reach here and a Keg service exists, callers can extend this block to persist
			// // the node using the injected service (options.Keg). At present we return success.
			// fmt.Fprintln(deps.Out, "Force accepted. If integrated, node creation would proceed now (not implemented).")
			// return nil
		},
	}

	// Common flags for create and subcommands
	cmd.Flags().IntVar(&flagID, "id", 0, "Optional explicit node id to allocate")
	cmd.Flags().StringVar(&flagTitle, "title", "", "Optional node title")
	cmd.Flags().StringVar(&flagTags, "tags", "", "Comma-separated list of tags (example: a,b,c)")
	cmd.Flags().StringVar(&flagAuthor, "author", "", "Author string (e.g., 'Name <email>')")
	cmd.Flags().BoolVar(&flagStdin, "stdin", false, "If true, read content from stdin/pipe instead of opening an editor")
	cmd.Flags().BoolVar(&flagForce, "force", false, "Force creation / overwrite if applicable (stubbed)")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// normalizeTags accepts a comma-separated tag string and returns a slice of
// trimmed tokens. Real implementation would perform normalization (lowercase,
// hyphenation, dedupe, sort). This helper keeps the behavior simple for stubs.
func normalizeTags(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
