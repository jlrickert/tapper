package app

import (
	"context"
	"fmt"
	"io"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tap"
)

type Runner struct {
	Root string

	project *tap.TapProject
}

// Runner coordinates high-level application operations.
//
// It holds an optional cached TapProject instance constructed with the
// Runner.Root value. Methods on Runner provide CLI-style behaviors such as
// initializing new kegs and creating node content.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Streams holds the IO streams used by the application logic.
//
// Tests and Cobra commands should provide injectable readers/writers so no
// global os.* changes are required.
type CreateOptions struct {
	Title string
	Tags  []string
}

// InitOptions configures behavior for Runner.Init.
type InitOptions struct {
	// Type could be local, file, or registry
	Type string

	// AddConfig adds config to user config
	AddUserConfig bool

	// AddLocalConfig adds the alias to the local project
	AddLocalConfig bool

	Creator string
	Title   string
	Alias   string
}

// Init creates a keg entry for the given name.
//
// If name is empty an ErrInvalid-wrapped error is returned. Init obtains the
// project via getProject and then performs the actions required to create a
// keg. The current implementation defers to project.DefaultKeg for further
// resolution and returns any error encountered when obtaining the project.
func (r *Runner) Init(ctx context.Context, name string, options *InitOptions) error {
	switch options.Type {
	case "registry":
	case "file":
	case "local":
	default:
		if name == "." {
			return r.InitLocal(ctx, InitLocalOptions{
				Alias:          options.Alias,
				AddUserConfig:  options.AddUserConfig,
				AddLocalConfig: options.AddLocalConfig,
				Creator:        options.Creator,
				Title:          options.Title,
			})
		}
		u, err := kegurl.Parse(name)
		if err != nil {
			return fmt.Errorf("unable to init keg: %w", err)
		}
		switch u.Scheme() {
		case kegurl.SchemeFile:
		}
		u.Scheme()
	}
	if name == "." {
		return r.InitLocal(ctx, InitLocalOptions{
			Alias:          options.Alias,
			AddUserConfig:  options.AddUserConfig,
			AddLocalConfig: options.AddLocalConfig,

			Title:   options.Title,
			Creator: options.Type,
		})
	}
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

type InitLocalOptions struct {
	Alias          string
	AddUserConfig  bool
	AddLocalConfig bool

	Creator string
	Title   string
}

// InitLocal creates a filesystem-backed keg repository at path.
//
// If path is empty the current working directory is used. The function uses
// the Env from ctx to resolve the working directory when available and falls
// back to os.Getwd otherwise. The destination directory is created. An initial
// keg configuration is written as YAML to "keg" inside the destination. A
// dex/ directory and a nodes.tsv file containing the zero node entry are
// created. A zero node README is written to "0/README.md".
//
// Errors are wrapped with contextual messages to aid callers.
func (r *Runner) InitLocal(ctx context.Context, opts InitLocalOptions) error {
	proj, err := r.getProject(ctx)
	if err != nil {
		return fmt.Errorf("unable to init keg: %w", err)
	}

	k, err := keg.NewKegFromTarget(ctx, kegurl.NewFile(proj.Root()))
	if err != nil {
		return fmt.Errorf("unable to init keg: %w", err)
	}
	return k.Init(ctx)
}

type InitFileOptions struct {
	Path string

	AddUserConfig  bool
	AddLocalConfig bool

	Alias   string
	Creator string
	Title   string
}

func (r *Runner) InitFile(ctx context.Context, opt InitFileOptions) error {
	return nil
}

type InitRegistryOptions struct {
	Repo  string
	User  string
	Alias string

	AddUserConfig  bool
	AddLocalConfig bool

	Creator string
	Title   string
}

func (r *Runner) InitRegistry(ctx context.Context, opts InitRegistryOptions) error {
	return nil
}

// Create is a high-level command to create a new node in the current keg.
//
// The provided options control initial metadata such as title and tags. The
// function returns an error on failure. The current implementation is a stub.
func (r *Runner) Create(ctx context.Context, options CreateOptions) error {
	return nil
}

// DoCreate performs the implementation detail of creating a node.
//
// This helper is intended to be invoked by higher-level entry points once
// input has been validated. It returns an error if the creation process fails.
func (r *Runner) DoCreate(ctx context.Context, options CreateOptions) error {
	return nil
}

// DoEdit opens the editor for the given node and persists changes.
//
// The function should use the configured editor from the environment and
// update the node content and metadata when the editor exits. It returns any
// encountered error. Current implementation is a stub.
func (r *Runner) DoEdit(ctx context.Context, node keg.Node) error {
	return nil
}

// DoView renders the node content to the output streams.
//
// This function should format and print the node content and metadata in a
// human-friendly manner. Current implementation is a stub.
func (r *Runner) DoView(ctx context.Context, node keg.Node) error {
	return nil
}

// DoTag adds tags to a node or performs tag-related actions.
//
// The function accepts a single tag string to apply. It returns an error on
// failure. Current implementation is a stub.
func (r *Runner) DoTag(ctx context.Context, tag string) error {
	return nil
}

// DoTagList lists known tags and their counts.
//
// It should print or return a representation of tags available in the current
// keg. Current implementation is a stub.
func (r *Runner) DoTagList(ctx context.Context) error {
	return nil
}

// DoImport imports external content into the keg.
//
// The import source and behavior are determined by flags or higher-level
// callers. Current implementation is a stub.
func (r *Runner) DoImport(ctx context.Context) error {
	return nil
}

// DoGrep searches node contents for matching text.
//
// It should return or print matching node references. Current implementation is
// a stub.
func (r *Runner) DoGrep(ctx context.Context) error {
	return nil
}

// DoTitles extracts and lists titles from nodes.
//
// The helper is intended to provide a compact view of node titles. Current
// implementation is a stub.
func (r *Runner) DoTitles(ctx context.Context) error {
	return nil
}

// DoLink creates or resolves a link by alias.
//
// The alias parameter identifies the target node or keg alias to link to.
// Current implementation is a stub.
func (r *Runner) DoLink(ctx context.Context, alias string) error {
	return nil
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
