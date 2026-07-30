package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/keg"
)

// formatHelp is the shared --format documentation for every listing command.
// It is one block so the five commands cannot drift apart, which they had
// already done: three of them documented no placeholders at all, and two
// stated a default they did not use.
const formatHelp = `Format placeholders:
  %i  node id            (same as %{id})
  %t  title              (same as %{title})
  %d  updated date       (same as %{.updated})
  %c  created date       (same as %{.created})
  %a  accessed date      (same as %{.accessed})
  %%  literal percent

Named selectors use %{...} and share the query expression vocabulary:
  %{type}, %{status}   any metadata key
  %{tags}              the node's tag list
  %{.accessCount}      a statistics field: updated, created, accessed,
                       hash, accessCount, lead, omega

Selectors other than id, title, and the three dates read one file per
node. Absent values render empty.`

// registerFormatCompletion offers the closed part of the field vocabulary for
// a --format flag. Metadata keys are open-ended and so cannot be suggested.
//
// NoSpace is set because a format string is composite: the user is usually
// building a longer template around the token they just accepted.
func registerFormatCompletion(cmd *cobra.Command) {
	mustRegisterFlagCompletion(cmd, "format", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		directive := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
		suggestions := keg.FormatSelectorSuggestions()
		if toComplete == "" {
			return suggestions, directive
		}
		// Suggest continuations of whatever selector the user has started,
		// keeping any prefix they have already typed.
		open := strings.LastIndex(toComplete, "%{")
		if open < 0 || strings.Contains(toComplete[open:], "}") {
			return nil, directive
		}
		prefix := toComplete[:open]
		partial := toComplete[open:]
		out := make([]string, 0, len(suggestions))
		for _, s := range suggestions {
			if strings.HasPrefix(s, partial) {
				out = append(out, prefix+s)
			}
		}
		return out, directive
	})
}

// registerQueryFieldCompletion offers the dot-prefix statistics fields for a
// --query flag. Tags and metadata keys are keg-specific and open-ended, so
// only the closed vocabulary is suggested.
func registerQueryFieldCompletion(cmd *cobra.Command) {
	mustRegisterFlagCompletion(cmd, "query", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if strings.HasPrefix(toComplete, ".") || toComplete == "" {
			suggestions := make([]string, len(keg.StatsFieldNames))
			for i, name := range keg.StatsFieldNames {
				suggestions[i] = "." + name
			}
			return suggestions, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
}
