package app

// Runner implements a small, testable unit of application logic that can be
// exercised directly in unit tests or invoked via a Cobra command. It accepts
// injectable streams and an in-memory Keg repository for tests.
import (
// "context"
// "fmt"
// "io"
// "strings"
// std "github.com/jlrickert/go-std/pkg"
// "github.com/jlrickert/tapper/pkg/keg"
)

// Runner performs business logic. It is constructed with dependencies so it is
// easy to test in isolation.
// type Runner struct {
// 	Root string
// }

// NewRunner constructs a Runner with the provided repository backend.
// func NewRunner(repo keg.KegRepository) *Runner {
// 	return &Runner{}
// }

// Run executes the command behavior.
// Behavior summary:
//   - If args[0] == "stdin" the runner reads all bytes from streams.In.
//   - Otherwise if args are present they are joined as the input message.
//   - If no input is provided a default greeting is used.
//   - The runner writes a simple result line to streams.Out, logs an info entry,
//     and writes the input as node content to the repository.
// func (r *Runner) Run(ctx context.Context, streams *Streams, args []string) error {
// 	// lg := std.LoggerFromContext(ctx)
//
// 	// Ensure non-nil streams to avoid extra checks.
// 	var in io.Reader = nil
// 	var out io.Writer = io.Discard
// 	var errw io.Writer = io.Discard
// 	if streams != nil {
// 		in = streams.In
// 		if streams.Out != nil {
// 			out = streams.Out
// 		}
// 		if streams.Err != nil {
// 			errw = streams.Err
// 		}
// 	}
//
// 	// Determine input content.
// 	var content string
// 	if len(args) > 0 && args[0] == "stdin" {
// 		// Read from stdin-like stream when requested.
// 		if in == nil {
// 			fmt.Fprintln(errw, "no stdin available")
// 			content = ""
// 		} else {
// 			b, err := io.ReadAll(in)
// 			if err != nil {
// 				return fmt.Errorf("reading stdin: %w", err)
// 			}
// 			content = string(b)
// 		}
// 	} else if len(args) > 0 {
// 		content = strings.Join(args, " ")
// 	} else {
// 		content = "hello from runner"
// 	}
//
// 	// Write a simple result to stdout.
// 	_, _ = fmt.Fprintln(out, "result:", strings.TrimSpace(content))
//
// 	// // Persist content to the repository so tests can assert on repo state.
// 	// // Use repo.Next to obtain a fresh Node id then WriteContent.
// 	// if r.repo != nil {
// 	// 	node, err := r.repo.Next(ctx)
// 	// 	if err != nil {
// 	// 		lg.Info("failed to allocate node id", "err", err)
// 	// 		return fmt.Errorf("next node: %w", err)
// 	// 	}
// 	//
// 	// 	if err := r.repo.WriteContent(ctx, node, []byte(content)); err != nil {
// 	// 		lg.Info("failed to write node content", "node", node.Path(), "err", err)
// 	// 		return fmt.Errorf("write content: %w", err)
// 	// 	}
// 	//
// 	// 	lg.Info("created node", "node", node.Path())
// 	// } else {
// 	// 	lg.Info("no repository configured; skipping persist")
// 	// }
//
// 	return nil
// }
