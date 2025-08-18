package cmd_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	cmdpkg "github.com/jlrickert/tapper/pkg/keg/cmd"
)

// TestCreate_HelpRenders ensures the create subcommand help text renders.
// Uses WithIO to avoid touching global STDOUT/STDERR and Run to execute the CLI.
func TestCreate_HelpRenders(t *testing.T) {
	// Provision a memory repo and service
	mem := keg.NewMemoryRepo()
	k := keg.NewKeg(mem, nil)

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	if err := cmdpkg.Run(context.Background(), []string{"create", "--help"},
		cmdpkg.WithKeg(k),
		cmdpkg.WithIO(nil, out, errOut),
	); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Usage") && !strings.Contains(got, "Flags") {
		t.Fatalf("help output looks wrong; got:\n%s", got)
	}
}

// // TestCreate_HelpIsIdempotent runs help twice to ensure repeated help invocations remain stable.
// func TestCreate_HelpIsIdempotent(t *testing.T) {
// 	mem := keg.NewMemoryRepo()
// 	k := keg.NewKeg(mem, nil)
//
// 	out := &bytes.Buffer{}
// 	errOut := &bytes.Buffer{}
//
// 	for i := range 2 {
// 		out.Reset()
// 		if err := cmdpkg.Run(context.Background(), []string{"create", "--help"},
// 			cmdpkg.WithKeg(k),
// 			cmdpkg.WithIO(nil, out, errOut),
// 		); err != nil {
// 			t.Fatalf("help execution #%d failed: %v", i+1, err)
// 		}
// 		if out.Len() == 0 {
// 			t.Fatalf("help execution #%d produced no output", i+1)
// 		}
// 	}
// }
//
// // TestCreate_EmptyNode verifies that running `keg create` with no stdin creates
// // an empty node and returns a valid node ID.
// func TestCreate_EmptyNode(t *testing.T) {
// 	t.SkipNow()
// 	mem := keg.NewMemoryRepo()
// 	k := keg.NewKeg(mem, nil)
//
// 	// Prepare IO for the command: stdin provides the node body; stdout captured.
// 	// in := strings.NewReader("# Test Node\n\nThis is the lead paragraph\n")
// 	var out bytes.Buffer
// 	var errOut bytes.Buffer
// 	cmdpkg.Run(t.Context(),
// 		[]string{"create"},
// 		cmdpkg.WithKeg(k), cmdpkg.WithIO(nil, &out, &errOut),
// 	)
//
// 	ids, err := k.Repo.ListNodesID()
// 	if err != nil {
// 		t.Fatalf("ListNodesID returned error: %v", err)
// 	}
// 	if len(ids) == 0 {
// 		t.Fatalf("expected at least one node to be created, got 0; stdout=%q stderr=%q", out.String(), errOut.String())
// 	}
//
// 	// t.Logf("created empty node id: %s", id.Path())
// }

// // TestCreate_WithMemoryRepo exercises the create flow end-to-end using the in-memory repository.
// func TestCreate_WithMemoryRepo(t *testing.T) {
// 	// Provision a memory repo and service
// 	mem := keg.NewMemoryRepo()
// 	k := keg.NewKeg(mem, nil)
//
// 	// Prepare IO for the command: stdin provides the node body; stdout captured.
// 	in := strings.NewReader("# Test Node\n\nThis is the lead paragraph\n")
// 	var out bytes.Buffer
// 	var errOut bytes.Buffer
//
// 	// Execute the create command via Run with injected Keg and IO
// 	if err := cmdpkg.Run(context.Background(), []string{
// 		"create",
// 		"--tags", "alpha,beta",
// 	},
// 		cmdpkg.WithKeg(k),
// 		cmdpkg.WithIO(in, &out, &errOut),
// 	); err != nil {
// 		t.Fatalf("execute failed: %v", err)
// 	}
//
// 	// Inspect the memory repo to ensure a node was created.
// 	ids, err := mem.ListNodesID()
// 	if err != nil {
// 		t.Fatalf("ListNodesID returned error: %v", err)
// 	}
// 	if len(ids) != 1 {
// 		t.Fatalf("expected 1 node in memory repo, got %d", len(ids))
// 	}
//
// 	// Optionally, fetch the node and assert more properties if mem repo offers that.
// 	// For example:
// 	// node, err := mem.GetNode(ids[0])
// 	// if err != nil { t.Fatalf("GetNode failed: %v", err) }
// 	// if node.Title != "test node" { t.Fatalf("unexpected title: %q", node.Title) }
// }
