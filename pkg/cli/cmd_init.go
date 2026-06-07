package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewInitCmd returns the `tap init` cobra command.
//
// Usage examples:
//
//	tap init --keg blog
//	tap init --project
//	tap init --keg blog --cwd
//	tap init --keg blog --hub knut --namespace me
//	tap init --keg blog --path ./kegs/blog --title "Blog" --creator "me"
func NewInitCmd(deps *Deps) *cobra.Command {
	initOpts := tapper.InitOptions{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "create a new keg target",
		Args:  cobra.NoArgs,
		Long: strings.TrimSpace(`
Create a keg target and initialize it in one of three destinations:

1. user (default)
   Creates a filesystem-backed keg on the local hub at <basePath>/@local/<alias>
   (the local hub's basePath, or the platform default when unset) and
   writes/updates the alias in user config.

2. local (--project, --cwd, or --path)
   Creates a local filesystem-backed keg. By default this resolves to
   <project>/kegs/<alias>,
   where <project> is the git root when available. Use --cwd to base it on the
   current working directory instead, or use --path to set an explicit
   location. --path implies a local destination even when --project is not
   passed.

3. hub (--hub <name>)
   Creates a hub/API keg target named <name> and stores it in config without
   creating local keg files. The hub name is required when --hub is used.

Alias behavior:
- --keg sets the alias written to config and the directory name.
- If --keg is omitted, alias is inferred from the current working directory basename.

Metadata:
- --title and --creator are written into the keg config for filesystem-backed kegs.

Interactive mode:
- When stdin is a TTY and no destination/alias flags are provided, tap init
  prompts for the alias, location category, title, and creator. Pass
  --non-interactive to skip the prompt and rely on flag-driven defaults
  (e.g. for CI or scripted invocations).
`),
		Example: strings.TrimSpace(`
tap init --keg blog
tap init --project --cwd
tap init --keg blog --cwd
tap init --keg blog --path ./kegs/blog
tap init --keg blog --user
tap init --keg blog --hub knut --namespace me
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if shouldPromptInit(deps, &initOpts) {
				if err := promptInitOptions(cmd, deps, &initOpts); err != nil {
					return err
				}
			}

			if strings.TrimSpace(initOpts.Keg) == "" {
				cwd, err := deps.Runtime.Getwd()
				if err != nil {
					return fmt.Errorf("unable to determine working directory for alias inference: %w", err)
				}
				initOpts.Keg = filepath.Base(cwd)
			}

			target, err := deps.Tap.InitKeg(cmd.Context(), initOpts)
			if err != nil {
				return err
			}

			label := tapper.KegBackendLabel(target)
			if label == "" {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "keg %s created", initOpts.Keg)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "keg %s created (%s)", initOpts.Keg, label)
			return err
		},
	}

	cmd.Flags().BoolVar(&initOpts.Project, "project", false, "create a project-local keg")
	cmd.Flags().BoolVar(&initOpts.User, "user", false, "create a user keg on the local hub at <basePath>/@local/<alias>")
	cmd.Flags().StringVar(&initOpts.Hub, "hub", "", "hub name (selects API-style hub target when set)")
	cmd.Flags().BoolVar(&initOpts.Cwd, "cwd", false, "use cwd instead of git root for local destination resolution")
	cmd.Flags().StringVar(&initOpts.Path, "path", "", "explicit local destination path; implies local mode")
	cmd.Flags().StringVar(&initOpts.UserName, "namespace", "", "hub namespace/user to use with --hub")
	cmd.Flags().StringVarP(&initOpts.Keg, "keg", "k", "", "alias of keg to add to config")
	cmd.Flags().StringVar(&initOpts.Title, "title", "", "human title to write into the keg config")
	cmd.Flags().StringVar(&initOpts.Creator, "creator", "", "creator identifier to include in the keg config")
	cmd.Flags().StringVar(&initOpts.TokenEnv, "token-env", "", "environment variable name to store token reference (API targets)")
	cmd.Flags().BoolVar(&initOpts.NonInteractive, "non-interactive", false, "skip the interactive prompt even when stdin is a TTY")

	return cmd
}

// shouldPromptInit reports whether the cobra RunE handler should fire the
// interactive `tap init` prompt. The prompt is gated on three conditions:
// stdin is a TTY, --non-interactive is not set, and the user has supplied no
// destination flags or alias on the command line. Any explicit flag means the
// user has already declared their intent; only the bare `tap init` invocation
// triggers the conversational path.
func shouldPromptInit(deps *Deps, opts *tapper.InitOptions) bool {
	if deps == nil || deps.Runtime == nil {
		return false
	}
	if !deps.Runtime.Stream().IsTTY {
		return false
	}
	if opts.NonInteractive {
		return false
	}
	if opts.User || opts.Project || opts.Cwd {
		return false
	}
	if strings.TrimSpace(opts.Path) != "" || strings.TrimSpace(opts.Hub) != "" {
		return false
	}
	if strings.TrimSpace(opts.Keg) != "" {
		return false
	}
	return true
}

// promptInitOptions walks the user through alias / location / metadata when
// `tap init` is invoked bare on a TTY. Prompts go to stderr (so stdout stays
// clean for the success line that downstream tooling may pipe), and answers
// come from cmd.InOrStdin() so tests can pipe scripted answers via
// Process.RunWithIO.
//
// The hub branch is intentionally skipped: hub init still requires the user
// to pass --hub explicitly, since hub setup needs a namespace + token and the
// terse prompt is not the right place to teach that flow.
func promptInitOptions(cmd *cobra.Command, deps *Deps, opts *tapper.InitOptions) error {
	reader := bufio.NewReader(cmd.InOrStdin())
	stderr := cmd.ErrOrStderr()

	defaultAlias := ""
	if deps != nil && deps.Runtime != nil {
		if cwd, err := deps.Runtime.Getwd(); err == nil && cwd != "" {
			defaultAlias = filepath.Base(cwd)
		}
	}

	alias, err := promptLine(stderr, reader, fmt.Sprintf("keg alias [%s]: ", defaultAlias))
	if err != nil {
		return err
	}
	if alias == "" {
		alias = defaultAlias
	}
	if err := tapper.ValidateKegAlias(alias); err != nil {
		return err
	}
	opts.Keg = alias

	location, err := promptLine(stderr, reader, "location [user/project] (default user): ")
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(location)) {
	case "", "user", "u":
		opts.User = true
	case "project", "p":
		opts.Project = true
	default:
		return fmt.Errorf("invalid location %q: expected user or project", location)
	}

	title, err := promptLine(stderr, reader, "title (optional): ")
	if err != nil {
		return err
	}
	if title != "" {
		opts.Title = title
	}

	creator, err := promptLine(stderr, reader, "creator (optional): ")
	if err != nil {
		return err
	}
	if creator != "" {
		opts.Creator = creator
	}

	return nil
}

// promptLine writes prompt to w, reads a single line from r, and returns the
// trimmed answer. Treats io.EOF as a terminating empty answer so a piped
// stdin that closes after fewer responses than prompts behaves as if each
// remaining prompt accepted its default.
func promptLine(w io.Writer, r *bufio.Reader, prompt string) (string, error) {
	if _, err := fmt.Fprint(w, prompt); err != nil {
		return "", err
	}
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
