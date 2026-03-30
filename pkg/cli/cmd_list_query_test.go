package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// newQuerySandbox creates a sandbox using the queryuser fixture which has 10
// nodes (0-9) with varied omega, entity, status, tags, and stats values.
//
// Fixture node summary:
//
//	0: zero node, no entity/omega/status, tags=[planned]
//	1: entity=plan,   omega=0.7, status=done,        tags=[api,design],         accessCount=10
//	2: entity=task,   omega=0.5, status=in-progress,  tags=[api,security],       accessCount=7
//	3: entity=concept,omega=1.0, status=done,         tags=[api,rest],           accessCount=15
//	4: entity=task,   omega=0.3, status=pending,      tags=[backend,database],   accessCount=2
//	5: entity=retrospect,omega=0.5,status=done,       tags=[agile],              accessCount=3
//	6: entity=feature,omega=0.0, status=pending,      tags=[backend,feature],    accessCount=0
//	7: entity=concept,omega=0.7, status=done,         tags=[backend,logging],    accessCount=8
//	8: entity=plan,   omega=0.3, status=in-progress,  tags=[ci,devops],          accessCount=4
//	9: entity=task,   omega=1.0, status=done,         tags=[backend,performance],accessCount=20
func newQuerySandbox(t *testing.T) *testutils.Sandbox {
	t.Helper()
	return NewSandbox(t, testutils.WithFixture("queryuser", "~"))
}

// queryListIDs runs `tap list --id-only --query <expr>` and returns the
// resulting node IDs as a trimmed string slice.
func queryListIDs(t *testing.T, sb *testutils.Sandbox, query string) []string {
	t.Helper()
	res := NewProcess(t, false, "list", "--id-only", "--query", query).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "query %q should not error: %s", query, string(res.Stderr))
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return []string{}
	}
	lines := strings.Split(out, "\n")
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimSpace(l)
	}
	return trimmed
}

// --- Omega + Stats Combined Tests ---

func TestQueryExpr_OmegaGteAndCreatedGt(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// omega>=0.5 and .created>2025-04-01T00:00:00Z
	// omega>=0.5: nodes 1(0.7), 2(0.5), 3(1.0), 5(0.5), 7(0.7), 9(1.0)
	// .created>2025-04-01: nodes 2(Apr 1 is NOT >, exact), 4(Jun 1), 5(Jul 1),
	//   6(Aug 1), 7(May 1), 8(Sep 1), 9(Jan 15 is NOT >)
	// Wait -- 2025-04-01T08:00:00Z > 2025-04-01T00:00:00Z is true.
	// So .created>2025-04-01T00:00:00Z: 2,4,5,6,7,8 (all created after midnight Apr 1)
	// Intersection of omega>=0.5 {1,2,3,5,7,9} and .created>2025-04-01T00:00:00Z {2,4,5,6,7,8}:
	// => {2, 5, 7}
	ids := queryListIDs(t, sb, "omega>=0.5 and .created>2025-04-01T00:00:00Z")
	require.Contains(t, ids, "2", "omega=0.5, created Apr 1 should match")
	require.Contains(t, ids, "5", "omega=0.5, created Jul 1 should match")
	require.Contains(t, ids, "7", "omega=0.7, created May 1 should match")
	require.NotContains(t, ids, "1", "omega=0.7 but created Mar 1 should NOT match")
	require.NotContains(t, ids, "3", "omega=1.0 but created Feb 1 should NOT match")
	require.NotContains(t, ids, "9", "omega=1.0 but created Jan 15 should NOT match")
	require.NotContains(t, ids, "4", "created Jun 1 but omega=0.3 should NOT match")
}

func TestQueryExpr_OmegaGteAndEntityTask(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// omega>=0.7 and entity=task
	// omega>=0.7: nodes 1(0.7), 3(1.0), 7(0.7), 9(1.0)
	// entity=task: nodes 2, 4, 9
	// Intersection: {9}
	ids := queryListIDs(t, sb, "omega>=0.7 and entity=task")
	require.Contains(t, ids, "9", "omega=1.0, entity=task should match")
	require.NotContains(t, ids, "1", "entity=plan should NOT match")
	require.NotContains(t, ids, "2", "omega=0.5 should NOT match")
	require.NotContains(t, ids, "4", "omega=0.3 should NOT match")
}

func TestQueryExpr_OmegaLtOrEntityDraft(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// omega<0.3 or entity=draft
	// omega<0.3: node 6 (omega=0.0). Node 0 has no omega, so omega<0.3
	//   doesn't match it (field not present, != fails but < doesn't apply).
	// entity=draft: no nodes have entity=draft
	// Union: {6}
	ids := queryListIDs(t, sb, "omega<0.3 or entity=draft")
	require.Contains(t, ids, "6", "omega=0.0 should match omega<0.3")
	require.NotContains(t, ids, "4", "omega=0.3 is NOT < 0.3")
	require.NotContains(t, ids, "1", "omega=0.7 should NOT match")
}

func TestQueryExpr_OmegaNotEqualZero(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// omega!=0.0
	// Nodes with omega != 0.0: 1(0.7), 2(0.5), 3(1.0), 4(0.3), 5(0.5),
	//   7(0.7), 8(0.3), 9(1.0)
	// Node 6 has omega=0.0, so it should NOT match.
	// Node 0 has no omega field, so != should match (missing != "0.0" is true).
	ids := queryListIDs(t, sb, "omega!=0.0")
	require.NotContains(t, ids, "6", "omega=0.0 should NOT match omega!=0.0")
	require.Contains(t, ids, "1", "omega=0.7 should match omega!=0.0")
	require.Contains(t, ids, "3", "omega=1.0 should match omega!=0.0")
	require.Contains(t, ids, "9", "omega=1.0 should match omega!=0.0")
	// Node 0 has no omega at all; != matches missing fields.
	require.Contains(t, ids, "0", "node without omega should match omega!=0.0")
}

// --- Stats Field Comparison Tests ---

func TestQueryExpr_AccessCountGt5(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// .accessCount>5
	// accessCount: 0(0), 1(10), 2(7), 3(15), 4(2), 5(3), 6(0), 7(8), 8(4), 9(20)
	// > 5: nodes 1(10), 2(7), 3(15), 7(8), 9(20)
	ids := queryListIDs(t, sb, ".accessCount>5")
	require.Contains(t, ids, "1", "accessCount=10 should match >5")
	require.Contains(t, ids, "2", "accessCount=7 should match >5")
	require.Contains(t, ids, "3", "accessCount=15 should match >5")
	require.Contains(t, ids, "7", "accessCount=8 should match >5")
	require.Contains(t, ids, "9", "accessCount=20 should match >5")
	require.NotContains(t, ids, "4", "accessCount=2 should NOT match >5")
	require.NotContains(t, ids, "5", "accessCount=3 should NOT match >5")
	require.NotContains(t, ids, "6", "accessCount=0 should NOT match >5")
	require.NotContains(t, ids, "8", "accessCount=4 should NOT match >5")
}

func TestQueryExpr_AccessCountGte0(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// .accessCount>=0 should match all nodes that have a stats.json with
	// an accessCount field. Since accessCount=0 is omitted from JSON
	// (omitempty), nodes with 0 access count will read as 0 from the
	// parsed stats and 0 >= 0 is true.
	ids := queryListIDs(t, sb, ".accessCount>=0")
	// All 10 nodes should match because they all have stats.json.
	require.Len(t, ids, 10, "all nodes with stats should match .accessCount>=0")
}

func TestQueryExpr_HashExactMatch(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// .hash=c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6 (node 3's hash)
	ids := queryListIDs(t, sb, ".hash=c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6")
	require.Len(t, ids, 1, "exact hash match should return exactly one node")
	require.Contains(t, ids, "3", "node 3 should match its own hash")
}

// --- Attribute Comparison Tests ---

func TestQueryExpr_EntityNotEqualTask(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// entity!=task
	// Tasks: 2, 4, 9
	// Non-tasks (including nodes without entity): 0, 1, 3, 5, 6, 7, 8
	ids := queryListIDs(t, sb, "entity!=task")
	require.NotContains(t, ids, "2", "entity=task should NOT match entity!=task")
	require.NotContains(t, ids, "4", "entity=task should NOT match entity!=task")
	require.NotContains(t, ids, "9", "entity=task should NOT match entity!=task")
	require.Contains(t, ids, "0", "no entity should match entity!=task")
	require.Contains(t, ids, "1", "entity=plan should match entity!=task")
	require.Contains(t, ids, "3", "entity=concept should match entity!=task")
	require.Contains(t, ids, "5", "entity=retrospect should match entity!=task")
	require.Contains(t, ids, "6", "entity=feature should match entity!=task")
	require.Contains(t, ids, "7", "entity=concept should match entity!=task")
	require.Contains(t, ids, "8", "entity=plan should match entity!=task")
}

func TestQueryExpr_StatusNotEqualDone(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// status!=done
	// Done: 1, 3, 5, 7, 9
	// Not done: 0(no status), 2(in-progress), 4(pending), 6(pending), 8(in-progress)
	ids := queryListIDs(t, sb, "status!=done")
	require.NotContains(t, ids, "1", "status=done should NOT match status!=done")
	require.NotContains(t, ids, "3", "status=done should NOT match status!=done")
	require.NotContains(t, ids, "5", "status=done should NOT match status!=done")
	require.NotContains(t, ids, "7", "status=done should NOT match status!=done")
	require.NotContains(t, ids, "9", "status=done should NOT match status!=done")
	require.Contains(t, ids, "0", "no status should match status!=done")
	require.Contains(t, ids, "2", "status=in-progress should match status!=done")
	require.Contains(t, ids, "4", "status=pending should match status!=done")
	require.Contains(t, ids, "6", "status=pending should match status!=done")
	require.Contains(t, ids, "8", "status=in-progress should match status!=done")
}

func TestQueryExpr_EntityNotTaskAndOmegaGte(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// entity!=task and omega>=0.5
	// entity!=task: 0, 1, 3, 5, 6, 7, 8
	// omega>=0.5: 1(0.7), 2(0.5), 3(1.0), 5(0.5), 7(0.7), 9(1.0)
	// Intersection: {1, 3, 5, 7}
	ids := queryListIDs(t, sb, "entity!=task and omega>=0.5")
	require.Contains(t, ids, "1", "entity=plan, omega=0.7 should match")
	require.Contains(t, ids, "3", "entity=concept, omega=1.0 should match")
	require.Contains(t, ids, "5", "entity=retrospect, omega=0.5 should match")
	require.Contains(t, ids, "7", "entity=concept, omega=0.7 should match")
	require.NotContains(t, ids, "2", "entity=task should NOT match entity!=task")
	require.NotContains(t, ids, "9", "entity=task should NOT match entity!=task")
	require.NotContains(t, ids, "6", "omega=0.0 should NOT match omega>=0.5")
	require.NotContains(t, ids, "8", "omega=0.3 should NOT match omega>=0.5")
}

// --- Combined Multi-Type Tests ---

func TestQueryExpr_OmegaGteCreatedGtEntityNotRetrospect(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// omega>=0.5 and .created>2025-04-01T00:00:00Z and entity!=retrospect
	// omega>=0.5: {1, 2, 3, 5, 7, 9}
	// .created>2025-04-01T00:00:00Z: {2, 4, 5, 6, 7, 8}
	// entity!=retrospect: all except node 5
	// Triple intersection: {2, 7}
	ids := queryListIDs(t, sb, "omega>=0.5 and .created>2025-04-01T00:00:00Z and entity!=retrospect")
	require.Contains(t, ids, "2", "omega=0.5, created Apr 1, entity=task should match")
	require.Contains(t, ids, "7", "omega=0.7, created May 1, entity=concept should match")
	require.NotContains(t, ids, "5", "entity=retrospect should be excluded")
	require.NotContains(t, ids, "1", "created Mar 1 should be excluded by date filter")
}

func TestQueryExpr_NotEntityTaskAndAccessCountGt0(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// not entity=task and .accessCount>0
	// entity=task: {2, 4, 9}
	// not entity=task: {0, 1, 3, 5, 6, 7, 8}
	// .accessCount>0: {1(10), 2(7), 3(15), 4(2), 5(3), 7(8), 8(4), 9(20)}
	// Intersection: {1, 3, 5, 7, 8}
	ids := queryListIDs(t, sb, "not entity=task and .accessCount>0")
	require.Contains(t, ids, "1", "entity=plan, accessCount=10 should match")
	require.Contains(t, ids, "3", "entity=concept, accessCount=15 should match")
	require.Contains(t, ids, "5", "entity=retrospect, accessCount=3 should match")
	require.Contains(t, ids, "7", "entity=concept, accessCount=8 should match")
	require.Contains(t, ids, "8", "entity=plan, accessCount=4 should match")
	require.NotContains(t, ids, "2", "entity=task should be excluded")
	require.NotContains(t, ids, "4", "entity=task should be excluded")
	require.NotContains(t, ids, "9", "entity=task should be excluded")
	require.NotContains(t, ids, "0", "accessCount=0 should NOT match >0")
	require.NotContains(t, ids, "6", "accessCount=0 should NOT match >0")
}

// --- Edge Cases ---

func TestQueryExpr_OmegaGteNonNumeric(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// omega>=abc should match nothing because "abc" is not a valid float.
	// The compare logic tries float parse first; when both sides fail to
	// parse as float, it falls back to string comparison. Since stored
	// omega values like "0.7" are lexicographically less than "abc",
	// some nodes might match via string fallback. The important thing is
	// it does not error.
	res := NewProcess(t, false, "list", "--id-only", "--query", "omega>=abc").Run(sb.Context(), sb.Runtime())
	// Should not error -- it may return results or empty depending on
	// string comparison fallback behavior.
	if res.Err != nil {
		// If there's an error it should be about no nodes found, not a parse error.
		require.Contains(t, res.Err.Error(), "no nodes found",
			"non-numeric omega comparison should either succeed or fail with 'no nodes found'")
	}
}

func TestQueryExpr_EmptyQueryReturnsAll(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// An empty query (no --query flag) should return all nodes.
	res := NewProcess(t, false, "list", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := strings.TrimSpace(string(res.Stdout))
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 10, "empty query should return all 10 nodes")
}

// --- Tag Boolean Tests Using the Fixture ---

func TestQueryExpr_TagAndTag_EmptyResult(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// api and backend -- no nodes have both tags, so the command should
	// return an error indicating no nodes were found.
	res := NewProcess(t, false, "list", "--id-only", "--query", "api and backend").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err, "query with no matching nodes should error")
	require.Contains(t, res.Err.Error(), "no nodes found")
}

func TestQueryExpr_TagOrTag(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// api or agile
	// api: {1, 2, 3}
	// agile: {5}
	// Union: {1, 2, 3, 5}
	ids := queryListIDs(t, sb, "api or agile")
	require.Contains(t, ids, "1")
	require.Contains(t, ids, "2")
	require.Contains(t, ids, "3")
	require.Contains(t, ids, "5")
	require.Len(t, ids, 4, "should return exactly 4 nodes")
}

func TestQueryExpr_NotTag(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// not backend
	// backend: {4, 6, 7, 9}
	// not backend: {0, 1, 2, 3, 5, 8}
	ids := queryListIDs(t, sb, "not backend")
	require.Len(t, ids, 6, "not backend should return 6 nodes")
	require.NotContains(t, ids, "4")
	require.NotContains(t, ids, "6")
	require.NotContains(t, ids, "7")
	require.NotContains(t, ids, "9")
}

// --- Complex Combined Queries ---

func TestQueryExpr_BackendAndStatusDoneAndOmegaGte07(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// backend and status=done and omega>=0.7
	// backend: {4, 6, 7, 9}
	// status=done: {1, 3, 5, 7, 9}
	// omega>=0.7: {1, 3, 7, 9}
	// Triple intersection: {7, 9}
	ids := queryListIDs(t, sb, "backend and status=done and omega>=0.7")
	require.Contains(t, ids, "7", "concept, backend, done, omega=0.7 should match")
	require.Contains(t, ids, "9", "task, backend, done, omega=1.0 should match")
	require.Len(t, ids, 2, "should return exactly 2 nodes")
}

func TestQueryExpr_ParenGrouping(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// (api or devops) and status=done
	// api: {1, 2, 3}
	// devops: {8}
	// api or devops: {1, 2, 3, 8}
	// status=done: {1, 3, 5, 7, 9}
	// Intersection: {1, 3}
	ids := queryListIDs(t, sb, "(api or devops) and status=done")
	require.Contains(t, ids, "1", "api+done should match")
	require.Contains(t, ids, "3", "api+done should match")
	require.NotContains(t, ids, "2", "api but in-progress should NOT match")
	require.NotContains(t, ids, "8", "devops but in-progress should NOT match")
	require.Len(t, ids, 2, "should return exactly 2 nodes")
}

func TestQueryExpr_CreatedBetweenDates(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// .created>2025-04-01T00:00:00Z and .created<2025-08-01T00:00:00Z
	// Nodes created between Apr 1 and Aug 1 (exclusive):
	// 2: Apr 1 08:00 (> midnight, so yes)
	// 4: Jun 1 (yes)
	// 5: Jul 1 (yes)
	// 7: May 1 (yes)
	// Others are outside range.
	ids := queryListIDs(t, sb, ".created>2025-04-01T00:00:00Z and .created<2025-08-01T00:00:00Z")
	require.Contains(t, ids, "2", "created Apr 1 08:00 should be in range")
	require.Contains(t, ids, "4", "created Jun 1 should be in range")
	require.Contains(t, ids, "5", "created Jul 1 should be in range")
	require.Contains(t, ids, "7", "created May 1 should be in range")
	require.NotContains(t, ids, "0", "created Jan 1 should be out of range")
	require.NotContains(t, ids, "1", "created Mar 1 should be out of range")
	require.NotContains(t, ids, "3", "created Feb 1 should be out of range")
	require.NotContains(t, ids, "6", "created Aug 1 13:00 should be out of range (>=Aug 1)")
	require.NotContains(t, ids, "8", "created Sep 1 should be out of range")
	require.NotContains(t, ids, "9", "created Jan 15 should be out of range")
}

func TestQueryExpr_AccessCountBooleanCheck(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// .accessCount (boolean check: non-zero access count)
	// Nodes with accessCount > 0: 1(10), 2(7), 3(15), 4(2), 5(3), 7(8), 8(4), 9(20)
	// Nodes with accessCount = 0: 0, 6
	ids := queryListIDs(t, sb, ".accessCount")
	require.Contains(t, ids, "1")
	require.Contains(t, ids, "2")
	require.Contains(t, ids, "3")
	require.Contains(t, ids, "4")
	require.Contains(t, ids, "5")
	require.Contains(t, ids, "7")
	require.Contains(t, ids, "8")
	require.Contains(t, ids, "9")
	require.NotContains(t, ids, "0", "accessCount=0 should NOT match boolean check")
	require.NotContains(t, ids, "6", "accessCount=0 should NOT match boolean check")
}

func TestQueryExpr_EntityEqualsExact(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// entity=concept (bare = for backward compat)
	// concept: nodes 3, 7
	ids := queryListIDs(t, sb, "entity=concept")
	require.Contains(t, ids, "3")
	require.Contains(t, ids, "7")
	require.Len(t, ids, 2, "exactly 2 nodes have entity=concept")
}

func TestQueryExpr_OmegaLte03(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// omega<=0.3
	// omega values: 1(0.7), 2(0.5), 3(1.0), 4(0.3), 5(0.5), 6(0.0), 7(0.7), 8(0.3), 9(1.0)
	// <= 0.3: nodes 4(0.3), 6(0.0), 8(0.3)
	ids := queryListIDs(t, sb, "omega<=0.3")
	require.Contains(t, ids, "4", "omega=0.3 should match <=0.3")
	require.Contains(t, ids, "6", "omega=0.0 should match <=0.3")
	require.Contains(t, ids, "8", "omega=0.3 should match <=0.3")
	require.NotContains(t, ids, "2", "omega=0.5 should NOT match <=0.3")
	require.NotContains(t, ids, "1", "omega=0.7 should NOT match <=0.3")
}

func TestQueryExpr_UpdatedGt(t *testing.T) {
	t.Parallel()
	sb := newQuerySandbox(t)

	// .updated>2025-11-01T00:00:00Z
	// Updated after Nov 1: 3(Dec 1), 7(Nov 15), 9(Dec 20)
	ids := queryListIDs(t, sb, ".updated>2025-11-01T00:00:00Z")
	require.Contains(t, ids, "3", "updated Dec 1 should match")
	require.Contains(t, ids, "7", "updated Nov 15 should match")
	require.Contains(t, ids, "9", "updated Dec 20 should match")
	require.NotContains(t, ids, "8", "updated Oct 20 should NOT match")
	require.NotContains(t, ids, "0", "updated Jan 1 should NOT match")
}
