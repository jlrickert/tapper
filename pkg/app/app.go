package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	std "github.com/jlrickert/go-std/pkg"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tap"
)

type Runner struct {
	Root string

	project *tap.TapProject
}

// Streams holds the IO streams used by the application logic. Tests and Cobra
// commands should provide injectable readers/writers so no global os.* changes
// are required.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

func (r *Runner) getProject(ctx context.Context) (*tap.TapProject, error) {
	if r.project != nil {
		return r.project, nil
	}
	project, err := tap.NewProject(ctx, tap.WithRoot(r.Root))
	if err != nil {
		return nil, fmt.Errorf("unable to create project: %w", err)
	}
	r.project = project
	return r.project, nil
}

type InitOptions struct {
	User string
}

// Creates a keg in the current directory
func (r *Runner) Init(ctx context.Context, name string, options InitOptions) error {
	if name == "" {
		return fmt.Errorf("name required: %w", keg.ErrInvalid)
	}
	project, err := r.getProject(ctx)
	if err != nil {
		return err
	}
	project.DefaultKeg(ctx)
	return nil
}

// Creates a repo at the path. Defaults to current working directory if path is
// empty
func (r *Runner) InitLocal(ctx context.Context, path string) error {
	env := std.EnvFromContext(ctx)

	// Resolve destination directory
	var dest string
	if path == "" {
		wd, err := env.Getwd()
		if err != nil {
			// fallback to os.Getwd to be robust in non-test contexts
			wd2, err2 := os.Getwd()
			if err2 != nil {
				return fmt.Errorf("determine working directory: %w", err)
			}
			dest = wd2
		} else {
			dest = wd
		}
	} else {
		// Expand and clean a supplied path using AbsPath helper
		dest = std.AbsPath(ctx, path)
	}

	// Ensure destination directory exists
	if err := std.Mkdir(ctx, dest, 0o755, true); err != nil {
		return fmt.Errorf("create destination dir %q: %w", dest, err)
	}

	// Build initial keg config
	cfg := keg.NewKegConfig()
	cfg.Kegv = keg.ConfigV2VersionString
	now := std.ClockFromContext(ctx).Now().UTC().Format(time.RFC3339)
	cfg.Updated = now

	// Serialize config to YAML
	data, err := cfg.ToYAML()
	if err != nil {
		return fmt.Errorf("serialize keg config: %w", err)
	}

	// Write keg file
	kegPath := filepath.Join(dest, "keg")
	if err := std.AtomicWriteFile(ctx, kegPath, data, 0o644); err != nil {
		return fmt.Errorf("write keg file %q: %w", kegPath, err)
	}

	// Create dex directory and initial nodes.tsv with zero node entry
	dexDir := filepath.Join(dest, "dex")
	if err := std.Mkdir(ctx, dexDir, 0o755, true); err != nil {
		return fmt.Errorf("create dex dir %q: %w", dexDir, err)
	}

	nodesData := fmt.Appendf(nil, "0\t%s\tZero Node\n", now)
	nodesPath := filepath.Join(dexDir, "nodes.tsv")
	if err := std.AtomicWriteFile(ctx, nodesPath, nodesData, 0o644); err != nil {
		return fmt.Errorf("write nodes index %q: %w", nodesPath, err)
	}

	// Create zero node directory with README placeholder
	readmePath := filepath.Join(dest, "0", keg.MarkdownContentFilename)
	if err := std.AtomicWriteFile(ctx, readmePath, []byte(keg.RawZeroNodeContent), 0o644); err != nil {
		return fmt.Errorf("write zero node README %q: %w", readmePath, err)
	}

	return nil
}

type CreateOptions struct {
	Title string
	Tags  []string
}

func (r *Runner) Create(ctx context.Context, options CreateOptions) error {
	return nil
}

func (r *Runner) DoCreate(ctx context.Context, options CreateOptions) error {
	return nil
}

func (r *Runner) DoEdit(ctx context.Context, node keg.Node) error {
	return nil
}

func (r *Runner) DoView(ctx context.Context, node keg.Node) error {
	return nil
}

func (r *Runner) DoTag(ctx context.Context, tag string) error {
	return nil
}

func (r *Runner) DoTagList(ctx context.Context) error {
	return nil
}

func (r *Runner) DoImport(ctx context.Context) error {
	return nil
}

func (r *Runner) DoGrep(ctx context.Context) error {
	return nil
}

func (r *Runner) DoTitles(ctx context.Context) error {
	return nil
}

func (r *Runner) DoLink(ctx context.Context, alias string) error {
	return nil
}
