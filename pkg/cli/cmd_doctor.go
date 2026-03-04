package cli

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

var tagMissingRE = regexp.MustCompile(`^tag "(.+)" not documented in keg config$`)

func NewDoctorCmd(deps *Deps) *cobra.Command {
	var opts tapper.DoctorOptions
	var tagsMissing bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "check keg health and report issues",
		Long: `Scan the resolved keg and report health issues.

Checks include: config validation, entity and tag consistency,
node structural integrity, and broken link detection.

Exit code 0 when no errors found, 1 when errors are present.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			ctx := cmd.Context()
			issues, err := deps.Tap.Doctor(ctx, opts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			if tagsMissing {
				seen := make(map[string]struct{})
				for _, issue := range issues {
					if issue.Kind != "tag-missing" {
						continue
					}
					if m := tagMissingRE.FindStringSubmatch(issue.Message); len(m) == 2 {
						seen[m[1]] = struct{}{}
					}
				}
				tags := make([]string, 0, len(seen))
				for tag := range seen {
					tags = append(tags, tag)
				}
				sort.Strings(tags)
				for _, tag := range tags {
					fmt.Fprintln(out, tag)
				}
				return nil
			}

			errorCount := 0
			warningCount := 0
			for _, issue := range issues {
				if issue.Level == "error" {
					errorCount++
				} else {
					warningCount++
				}
				if issue.NodeID != "" {
					fmt.Fprintf(out, "%s: [node %s] %s\n", issue.Level, issue.NodeID, issue.Message)
				} else {
					fmt.Fprintf(out, "%s: %s\n", issue.Level, issue.Message)
				}
			}

			if errorCount == 0 && warningCount == 0 {
				fmt.Fprintln(out, "ok: keg is healthy")
			} else {
				fmt.Fprintf(out, "%d error(s), %d warning(s)\n", errorCount, warningCount)
			}

			if errorCount > 0 {
				return fmt.Errorf("%d error(s) found", errorCount)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&tagsMissing, "tags-missing", false, "list only undocumented tag names")

	return cmd
}
