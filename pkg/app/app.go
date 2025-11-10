package app

import (
	"context"
	"github.com/jlrickert/tapper/pkg/keg"
)

// Streams holds the IO streams used by the application logic.

// // Streams holds the IO streams used by the application logic.
// //
// // Tests and Cobra commands should provide injectable readers/writers so no
// // global os.* changes are required.
// type CreateOptions struct {
// 	Title string
// 	Tags  []string
// }
//
// // Create is a high-level command to create a new node in the current keg.
// //
// // The provided options control initial metadata such as title and tags. The
// // function returns an error on failure. The current implementation is a stub.
// func (r *Runner) Create(ctx context.Context, options CreateOptions) error {
// 	return nil
// }

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
