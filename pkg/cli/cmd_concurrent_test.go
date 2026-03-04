package cli_test

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// TestConcurrent_Creates_WithTitle verifies that 10 goroutines running
// create --title concurrently all succeed with unique numeric IDs.
func TestConcurrent_Creates_WithTitle(t *testing.T) {
	t.Parallel()
	const N = 10
	fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))

	type result struct {
		stdout string
		err    error
	}
	results := make([]result, N)
	var wg sync.WaitGroup

	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			proc := NewProcess(t, false, "create", "--title", fmt.Sprintf("Node %d", idx))
			res := proc.Run(fx.Context(), fx.Runtime())
			results[idx] = result{stdout: strings.TrimSpace(string(res.Stdout)), err: res.Err}
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	idPattern := regexp.MustCompile(`^\d+$`)
	for i, r := range results {
		require.NoError(t, r.err, "goroutine %d failed", i)
		require.Regexp(t, idPattern, r.stdout, "goroutine %d did not return numeric ID", i)
		require.False(t, seen[r.stdout], "duplicate ID %s from goroutine %d", r.stdout, i)
		seen[r.stdout] = true
	}
}

// TestConcurrent_Creates_PipedStdin verifies that 10 goroutines piping
// markdown via stdin to create all succeed with unique IDs.
func TestConcurrent_Creates_PipedStdin(t *testing.T) {
	t.Parallel()
	const N = 10
	fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))

	type result struct {
		stdout string
		err    error
	}
	results := make([]result, N)
	var wg sync.WaitGroup

	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("# Piped Node %d\n\nContent for node %d.\n", idx, idx)
			proc := NewProcess(t, true, "create")
			res := proc.RunWithIO(fx.Context(), fx.Runtime(), strings.NewReader(content))
			results[idx] = result{stdout: strings.TrimSpace(string(res.Stdout)), err: res.Err}
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	idPattern := regexp.MustCompile(`^\d+$`)
	for i, r := range results {
		require.NoError(t, r.err, "goroutine %d failed", i)
		require.Regexp(t, idPattern, r.stdout, "goroutine %d did not return numeric ID", i)
		require.False(t, seen[r.stdout], "duplicate ID %s from goroutine %d", r.stdout, i)
		seen[r.stdout] = true
	}
}

// TestConcurrent_Edits_DifferentNodes pre-creates 5 nodes, then edits
// each from a separate goroutine via piped stdin. Verifies each node
// gets its specific content.
func TestConcurrent_Edits_DifferentNodes(t *testing.T) {
	t.Parallel()
	const N = 5
	fx := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/false"))
	fx.Runtime().Unset("VISUAL")

	// Pre-create 5 nodes in the personal keg.
	nodeIDs := make([]string, N)
	for i := range N {
		proc := NewProcess(t, false, "create", "--keg", "personal", "--title", fmt.Sprintf("Pre %d", i))
		res := proc.Run(fx.Context(), fx.Runtime())
		require.NoError(t, res.Err, "pre-create %d failed", i)
		nodeIDs[i] = strings.TrimSpace(string(res.Stdout))
	}

	// Concurrently edit each node with unique content.
	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("---\ntags:\n  - edited\n---\n# Edited %d\n\nUnique content %d.\n", idx, idx)
			proc := NewProcess(t, false, "edit", nodeIDs[idx], "--keg", "personal")
			res := proc.RunWithIO(fx.Context(), fx.Runtime(), strings.NewReader(content))
			errs[idx] = res.Err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "edit goroutine %d failed", i)
	}

	// Verify each node has its specific content.
	for i, id := range nodeIDs {
		proc := NewProcess(t, false, "cat", id, "--keg", "personal", "--content-only")
		res := proc.Run(fx.Context(), fx.Runtime())
		require.NoError(t, res.Err, "cat node %s failed", id)
		out := string(res.Stdout)
		require.Contains(t, out, fmt.Sprintf("Unique content %d.", i), "node %s has wrong content", id)
	}
}

// TestConcurrent_Creates_And_List runs 5 creator goroutines alongside
// 5 list --id-only goroutines. Verifies no errors or panics.
func TestConcurrent_Creates_And_List(t *testing.T) {
	t.Parallel()
	const creators = 5
	const listers = 5
	fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))

	createErrs := make([]error, creators)
	listErrs := make([]error, listers)
	var wg sync.WaitGroup

	for i := range creators {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			proc := NewProcess(t, false, "create", "--title", fmt.Sprintf("Created %d", idx))
			res := proc.Run(fx.Context(), fx.Runtime())
			createErrs[idx] = res.Err
		}(i)
	}

	for i := range listers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			proc := NewProcess(t, false, "list", "--id-only")
			res := proc.Run(fx.Context(), fx.Runtime())
			listErrs[idx] = res.Err
		}(i)
	}
	wg.Wait()

	for i, err := range createErrs {
		require.NoError(t, err, "creator %d failed", i)
	}
	for i, err := range listErrs {
		require.NoError(t, err, "lister %d failed", i)
	}
}

// TestConcurrent_Creates_And_Cat runs 5 creator goroutines alongside
// 4 goroutines catting the pre-existing node 0. Verifies no errors.
func TestConcurrent_Creates_And_Cat(t *testing.T) {
	t.Parallel()
	const creators = 5
	const readers = 4
	fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))

	createErrs := make([]error, creators)
	catErrs := make([]error, readers)
	var wg sync.WaitGroup

	for i := range creators {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			proc := NewProcess(t, false, "create", "--title", fmt.Sprintf("Created %d", idx))
			res := proc.Run(fx.Context(), fx.Runtime())
			createErrs[idx] = res.Err
		}(i)
	}

	for i := range readers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			proc := NewProcess(t, false, "cat", "0", "--content-only")
			res := proc.Run(fx.Context(), fx.Runtime())
			catErrs[idx] = res.Err
		}(i)
	}
	wg.Wait()

	for i, err := range createErrs {
		require.NoError(t, err, "creator %d failed", i)
	}
	for i, err := range catErrs {
		require.NoError(t, err, "cat reader %d failed", i)
	}
}

// TestConcurrent_Creates_And_Edits runs 5 creator goroutines alongside
// 5 editor goroutines that edit pre-existing nodes. Verifies all creates
// produce unique IDs and all edits land the correct content.
func TestConcurrent_Creates_And_Edits(t *testing.T) {
	t.Parallel()
	const creators = 5
	const editors = 5
	fx := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/false"))
	fx.Runtime().Unset("VISUAL")

	// Pre-create nodes for the editors to target.
	editTargets := make([]string, editors)
	for i := range editors {
		proc := NewProcess(t, false, "create", "--keg", "personal", "--title", fmt.Sprintf("EditTarget %d", i))
		res := proc.Run(fx.Context(), fx.Runtime())
		require.NoError(t, res.Err, "pre-create edit target %d failed", i)
		editTargets[i] = strings.TrimSpace(string(res.Stdout))
	}

	type createResult struct {
		stdout string
		err    error
	}
	createResults := make([]createResult, creators)
	editErrs := make([]error, editors)
	var wg sync.WaitGroup

	// Creators.
	for i := range creators {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			proc := NewProcess(t, false, "create", "--keg", "personal", "--title", fmt.Sprintf("Concurrent %d", idx))
			res := proc.Run(fx.Context(), fx.Runtime())
			createResults[idx] = createResult{stdout: strings.TrimSpace(string(res.Stdout)), err: res.Err}
		}(i)
	}

	// Editors.
	for i := range editors {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("---\ntags:\n  - mixed\n---\n# Mixed Edit %d\n\nMixed content %d.\n", idx, idx)
			proc := NewProcess(t, false, "edit", editTargets[idx], "--keg", "personal")
			res := proc.RunWithIO(fx.Context(), fx.Runtime(), strings.NewReader(content))
			editErrs[idx] = res.Err
		}(i)
	}
	wg.Wait()

	// Verify all creates succeeded with unique IDs.
	seen := make(map[string]bool)
	idPattern := regexp.MustCompile(`^\d+$`)
	for i, r := range createResults {
		require.NoError(t, r.err, "creator %d failed", i)
		require.Regexp(t, idPattern, r.stdout, "creator %d did not return numeric ID", i)
		require.False(t, seen[r.stdout], "duplicate ID %s from creator %d", r.stdout, i)
		seen[r.stdout] = true
	}

	// Verify all edits succeeded and content is correct.
	for i, err := range editErrs {
		require.NoError(t, err, "editor %d failed", i)
	}
	for i, id := range editTargets {
		proc := NewProcess(t, false, "cat", id, "--keg", "personal", "--content-only")
		res := proc.Run(fx.Context(), fx.Runtime())
		require.NoError(t, res.Err, "cat node %s failed", id)
		out := string(res.Stdout)
		require.Contains(t, out, fmt.Sprintf("Mixed content %d.", i), "node %s has wrong content", id)
	}
}

// TestConcurrent_Creates_And_PipedEdits runs 5 creator goroutines piping
// stdin alongside 5 editor goroutines piping stdin to edit pre-existing
// nodes. Verifies unique IDs for creates and correct content for edits.
func TestConcurrent_Creates_And_PipedEdits(t *testing.T) {
	t.Parallel()
	const creators = 5
	const editors = 5
	fx := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/false"))
	fx.Runtime().Unset("VISUAL")

	// Pre-create nodes for the editors.
	editTargets := make([]string, editors)
	for i := range editors {
		proc := NewProcess(t, false, "create", "--keg", "personal", "--title", fmt.Sprintf("PipedTarget %d", i))
		res := proc.Run(fx.Context(), fx.Runtime())
		require.NoError(t, res.Err, "pre-create piped target %d failed", i)
		editTargets[i] = strings.TrimSpace(string(res.Stdout))
	}

	type createResult struct {
		stdout string
		err    error
	}
	createResults := make([]createResult, creators)
	editErrs := make([]error, editors)
	var wg sync.WaitGroup

	// Creators via piped stdin.
	for i := range creators {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("# Piped Create %d\n\nPiped create content %d.\n", idx, idx)
			proc := NewProcess(t, true, "create", "--keg", "personal")
			res := proc.RunWithIO(fx.Context(), fx.Runtime(), strings.NewReader(content))
			createResults[idx] = createResult{stdout: strings.TrimSpace(string(res.Stdout)), err: res.Err}
		}(i)
	}

	// Editors via piped stdin.
	for i := range editors {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("---\ntags:\n  - piped-mixed\n---\n# Piped Edit %d\n\nPiped edit content %d.\n", idx, idx)
			proc := NewProcess(t, false, "edit", editTargets[idx], "--keg", "personal")
			res := proc.RunWithIO(fx.Context(), fx.Runtime(), strings.NewReader(content))
			editErrs[idx] = res.Err
		}(i)
	}
	wg.Wait()

	// Verify creates.
	seen := make(map[string]bool)
	idPattern := regexp.MustCompile(`^\d+$`)
	for i, r := range createResults {
		require.NoError(t, r.err, "piped creator %d failed", i)
		require.Regexp(t, idPattern, r.stdout, "piped creator %d did not return numeric ID", i)
		require.False(t, seen[r.stdout], "duplicate ID %s from piped creator %d", r.stdout, i)
		seen[r.stdout] = true
	}

	// Verify edits.
	for i, err := range editErrs {
		require.NoError(t, err, "piped editor %d failed", i)
	}
	for i, id := range editTargets {
		proc := NewProcess(t, false, "cat", id, "--keg", "personal", "--content-only")
		res := proc.Run(fx.Context(), fx.Runtime())
		require.NoError(t, res.Err, "cat node %s failed", id)
		out := string(res.Stdout)
		require.Contains(t, out, fmt.Sprintf("Piped edit content %d.", i), "node %s has wrong content", id)
	}
}

// TestConcurrent_Reindex_During_Creates runs one goroutine creating
// 5 nodes sequentially while another runs index rebuild concurrently.
// Verifies neither operation crashes.
func TestConcurrent_Reindex_During_Creates(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))

	var wg sync.WaitGroup
	var createErr, reindexErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 5 {
			proc := NewProcess(t, false, "create", "--title", fmt.Sprintf("During reindex %d", i))
			res := proc.Run(fx.Context(), fx.Runtime())
			if res.Err != nil {
				createErr = res.Err
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		proc := NewProcess(t, false, "index", "rebuild")
		res := proc.Run(fx.Context(), fx.Runtime())
		reindexErr = res.Err
	}()

	wg.Wait()

	require.NoError(t, createErr, "sequential creates should not fail during reindex")
	require.NoError(t, reindexErr, "index rebuild should not fail during creates")
}

// TestConcurrent_Reads_And_Edits_IndexConsistency runs concurrent cat
// readers alongside editors that change titles and tags. Each CLI
// process clones its own runtime (and dex), so the on-disk index may
// be stale after concurrent writes. An index rebuild reconciles it.
// Verifies that after rebuild the index reflects all edits: updated
// titles in list output, new tags present, old tags removed.
func TestConcurrent_Reads_And_Edits_IndexConsistency(t *testing.T) {
	t.Parallel()
	const N = 5
	fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/false"))
	fx.Runtime().Unset("VISUAL")

	// Pre-create N nodes, each with a unique tag.
	nodeIDs := make([]string, N)
	for i := range N {
		proc := NewProcess(t, false, "create",
			"--title", fmt.Sprintf("Original %d", i),
			"--tags", fmt.Sprintf("pre%d", i))
		res := proc.Run(fx.Context(), fx.Runtime())
		require.NoError(t, res.Err, "pre-create %d failed", i)
		nodeIDs[i] = strings.TrimSpace(string(res.Stdout))
	}

	// Concurrent: readers cat each node, editors replace tags and titles.
	editErrs := make([]error, N)
	catErrs := make([]error, N)
	var wg sync.WaitGroup

	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			proc := NewProcess(t, false, "cat", nodeIDs[idx], "--content-only")
			res := proc.Run(fx.Context(), fx.Runtime())
			catErrs[idx] = res.Err
		}(i)
	}

	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("---\ntags:\n  - updated\n---\n# Updated %d\n\nEdited content %d.\n", idx, idx)
			proc := NewProcess(t, false, "edit", nodeIDs[idx])
			res := proc.RunWithIO(fx.Context(), fx.Runtime(), strings.NewReader(content))
			editErrs[idx] = res.Err
		}(i)
	}
	wg.Wait()

	for i := range N {
		require.NoError(t, editErrs[i], "editor %d failed", i)
		require.NoError(t, catErrs[i], "reader %d failed", i)
	}

	// Node data on disk is correct (per-node locking guarantees this),
	// but the dex may be stale since each process had its own copy.
	// Rebuild to reconcile.
	rebuildRes := NewProcess(t, false, "index", "rebuild", "--full").Run(fx.Context(), fx.Runtime())
	require.NoError(t, rebuildRes.Err, "index rebuild should succeed")

	// Verify tag index: all edited nodes should have the "updated" tag.
	tagRes := NewProcess(t, false, "tags", "updated", "--id-only").Run(fx.Context(), fx.Runtime())
	require.NoError(t, tagRes.Err)
	taggedIDs := strings.Split(strings.TrimSpace(string(tagRes.Stdout)), "\n")
	require.Len(t, taggedIDs, N, "all %d edited nodes should have 'updated' tag", N)

	tagSet := make(map[string]bool)
	for _, id := range taggedIDs {
		tagSet[strings.TrimSpace(id)] = true
	}
	for i, id := range nodeIDs {
		require.True(t, tagSet[id], "node %s (index %d) missing from 'updated' tag", id, i)
	}

	// Verify old per-node tags are gone from the index.
	for i := range N {
		oldRes := NewProcess(t, false, "tags", fmt.Sprintf("pre%d", i), "--id-only").Run(fx.Context(), fx.Runtime())
		require.NoError(t, oldRes.Err)
		require.Empty(t, strings.TrimSpace(string(oldRes.Stdout)),
			"old tag pre%d should have no nodes after edit", i)
	}

	// Verify list shows updated titles.
	listRes := NewProcess(t, false, "list", "-n", "0", "--format", "%i|%t").Run(fx.Context(), fx.Runtime())
	require.NoError(t, listRes.Err)
	listOut := string(listRes.Stdout)
	for i, id := range nodeIDs {
		require.Contains(t, listOut, fmt.Sprintf("%s|Updated %d", id, i),
			"list should show updated title for node %s", id)
	}
}

// TestConcurrent_Creates_And_Edits_IndexConsistency runs concurrent
// creators and editors, rebuilds the index, then verifies the index
// has the correct total node count, all created nodes appear under
// their tag, and all edited nodes appear under theirs.
func TestConcurrent_Creates_And_Edits_IndexConsistency(t *testing.T) {
	t.Parallel()
	const creators = 5
	const editors = 5
	fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/false"))
	fx.Runtime().Unset("VISUAL")

	// Pre-create nodes for the editors to target.
	editTargets := make([]string, editors)
	for i := range editors {
		proc := NewProcess(t, false, "create",
			"--title", fmt.Sprintf("EditMe %d", i))
		res := proc.Run(fx.Context(), fx.Runtime())
		require.NoError(t, res.Err, "pre-create edit target %d failed", i)
		editTargets[i] = strings.TrimSpace(string(res.Stdout))
	}

	// Concurrent creates + edits.
	type createResult struct {
		stdout string
		err    error
	}
	createResults := make([]createResult, creators)
	editErrs := make([]error, editors)
	var wg sync.WaitGroup

	for i := range creators {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			proc := NewProcess(t, false, "create",
				"--title", fmt.Sprintf("New %d", idx),
				"--tags", "freshly-created")
			res := proc.Run(fx.Context(), fx.Runtime())
			createResults[idx] = createResult{
				stdout: strings.TrimSpace(string(res.Stdout)),
				err:    res.Err,
			}
		}(i)
	}

	for i := range editors {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("---\ntags:\n  - freshly-edited\n---\n# Edited %d\n\nEdited content %d.\n", idx, idx)
			proc := NewProcess(t, false, "edit", editTargets[idx])
			res := proc.RunWithIO(fx.Context(), fx.Runtime(), strings.NewReader(content))
			editErrs[idx] = res.Err
		}(i)
	}
	wg.Wait()

	idPattern := regexp.MustCompile(`^\d+$`)
	for i, r := range createResults {
		require.NoError(t, r.err, "creator %d failed", i)
		require.Regexp(t, idPattern, r.stdout, "creator %d bad ID", i)
	}
	for i, err := range editErrs {
		require.NoError(t, err, "editor %d failed", i)
	}

	// Rebuild to reconcile the dex after concurrent writers.
	rebuildRes := NewProcess(t, false, "index", "rebuild", "--full").Run(fx.Context(), fx.Runtime())
	require.NoError(t, rebuildRes.Err, "index rebuild should succeed")

	// Verify total node count in the index.
	// Expected: 1 (zero node) + editors (pre-created) + creators (concurrent).
	expectedCount := 1 + editors + creators
	listRes := NewProcess(t, false, "list", "-n", "0", "--id-only").Run(fx.Context(), fx.Runtime())
	require.NoError(t, listRes.Err)
	allIDs := strings.Split(strings.TrimSpace(string(listRes.Stdout)), "\n")
	require.Len(t, allIDs, expectedCount,
		"index should contain %d nodes (1 zero + %d pre-created + %d concurrent)", expectedCount, editors, creators)

	// Verify created nodes have the "freshly-created" tag.
	createdRes := NewProcess(t, false, "tags", "freshly-created", "--id-only").Run(fx.Context(), fx.Runtime())
	require.NoError(t, createdRes.Err)
	createdIDs := strings.Split(strings.TrimSpace(string(createdRes.Stdout)), "\n")
	require.Len(t, createdIDs, creators,
		"tag index should have %d nodes under 'freshly-created'", creators)

	createdSet := make(map[string]bool)
	for _, id := range createdIDs {
		createdSet[strings.TrimSpace(id)] = true
	}
	for i, r := range createResults {
		require.True(t, createdSet[r.stdout],
			"created node %s (creator %d) missing from 'freshly-created' tag", r.stdout, i)
	}

	// Verify edited nodes have the "freshly-edited" tag.
	editedRes := NewProcess(t, false, "tags", "freshly-edited", "--id-only").Run(fx.Context(), fx.Runtime())
	require.NoError(t, editedRes.Err)
	editedIDs := strings.Split(strings.TrimSpace(string(editedRes.Stdout)), "\n")
	require.Len(t, editedIDs, editors,
		"tag index should have %d nodes under 'freshly-edited'", editors)

	editedSet := make(map[string]bool)
	for _, id := range editedIDs {
		editedSet[strings.TrimSpace(id)] = true
	}
	for i, id := range editTargets {
		require.True(t, editedSet[id],
			"edited node %s (editor %d) missing from 'freshly-edited' tag", id, i)
	}

	// Verify edited nodes show updated titles in list output.
	fullListRes := NewProcess(t, false, "list", "-n", "0", "--format", "%i|%t").Run(fx.Context(), fx.Runtime())
	require.NoError(t, fullListRes.Err)
	fullListOut := string(fullListRes.Stdout)
	for i, id := range editTargets {
		require.Contains(t, fullListOut, fmt.Sprintf("%s|Edited %d", id, i),
			"list should show edited title for node %s", id)
	}
}
