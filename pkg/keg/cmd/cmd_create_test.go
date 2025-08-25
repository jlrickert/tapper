package cmd_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
)

// TestCreate_HelpRenders ensures the create subcommand help text renders.
// Uses TestFixture to capture output and execute the CLI.
func TestCreate_HelpRenders(t *testing.T) {
	t.Parallel()

	f := NewTestFixture(t)

	// Request help for the create subcommand.
	f.RunOrFail([]string{"create", "--help"})

	got := f.Stdout()
	if !(strings.Contains(got, "Usage") || strings.Contains(got, "Flags") || strings.Contains(got, "create")) {
		t.Fatalf("help output looks wrong; got:\n%s", got)
	}
}

// TestCreate_FromStdin_CreatesNode verifies that piping content to `keg create`
// results in a node being created in the repository (MemoryRepo) using TestFixture.
func TestCreate_FromStdin_CreatesNode(t *testing.T) {
	t.SkipNow()
	t.Parallel()

	f := NewTestFixture(t)

	f.SetInput("# My Test Title\n\nThis is a short lead paragraph.\n")
	f.RunOrFail([]string{"create"})

	ids, err := f.Repo.ListNodesID()
	if err != nil {
		t.Fatalf("ListNodesID failed: %v", err)
	}
	if len(ids) == 0 {
		t.Fatalf("expected at least one node created, got 0")
	}

	// Ensure the content for the created node contains the title we provided.
	content, err := f.Repo.ReadContent(ids[0])
	if err != nil {
		t.Fatalf("ReadContent failed: %v", err)
	}
	if !strings.Contains(string(content), "My Test Title") {
		t.Fatalf("created content missing expected title; got:\n%s", string(content))
	}
}

// TestCreate_WithTitleFlag_SetsMetaTitle verifies that passing --title to create
// results in the node metadata containing the provided title, using TestFixture.
func TestCreate_WithTitleFlag_SetsMetaTitle(t *testing.T) {
	t.Parallel()

	f := NewTestFixture(t)

	// Provide some stdin content and pass an explicit title flag.
	f.SetInput("Lead paragraph only.\n")

	title := "Explicit Title From Flag"
	f.RunOrFail([]string{"create", "--title", title})

	idStr := strings.TrimSpace(f.Stdout())
	id, err := strconv.Atoi(idStr)
	if err != nil {
		t.Fatalf("failed to parse id from stdout %q: %v", idStr, err)
	}

	ids, err := f.Repo.ListNodesID()
	if err != nil {
		t.Fatalf("ListNodesID failed: %v", err)
	}
	if len(ids) == 0 {
		t.Fatalf("expected at least one node created, got 0")
	}

	meta, err := f.Repo.ReadMeta(keg.NodeID(id))
	if err != nil {
		t.Fatalf("ReadMeta failed: %v", err)
	}
	if !strings.Contains(string(meta), title) {
		t.Fatalf("meta does not contain provided title %q; meta:\n%s", title, string(meta))
	}
}
