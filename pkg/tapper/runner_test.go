package tapper

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestParseClaudeOutput(t *testing.T) {
	t.Parallel()
	got := parseClaudeOutput([]byte(`{"type":"result","subtype":"success","is_error":false,"duration_ms":2840,"duration_api_ms":2500,"num_turns":1,"result":"The tests pass.","session_id":"5a3c1d9e-2f4b-4c8a-9e1d-7b6f0a2c3d4e","total_cost_usd":0.0123,"usage":{"input_tokens":4,"output_tokens":12}}` + "\n"))
	require.Equal(t, runnerResult{Session: "5a3c1d9e-2f4b-4c8a-9e1d-7b6f0a2c3d4e", Reply: "The tests pass."}, got)

	got = parseClaudeOutput([]byte(`{"type":"result","subtype":"error_max_turns","is_error":true,"result":"Reached max turns","session_id":"s-2"}`))
	require.True(t, got.IsError)
	require.Equal(t, "s-2", got.Session)

	got = parseClaudeOutput([]byte(`[{"type":"system","subtype":"init","session_id":"s-3"},{"type":"assistant","session_id":"s-3"},{"type":"result","is_error":false,"result":"Done.","session_id":"s-3"}]`))
	require.Equal(t, runnerResult{Session: "s-3", Reply: "Done."}, got, "--verbose prints an array")

	got = parseClaudeOutput([]byte("Error: not logged in\n"))
	require.True(t, got.IsError)
}

func TestParseCodexOutput(t *testing.T) {
	t.Parallel()
	stdout := []byte(`{"type":"thread.started","thread_id":"0199a213-81c0-7800-8aa1-bbab2a035a53"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"**Looking at the tests**"}}
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"bash -lc ls","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"bash -lc ls","aggregated_output":"go.mod\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"The repo has a go.mod."}}
{"type":"turn.completed","usage":{"input_tokens":24763,"cached_input_tokens":24448,"output_tokens":122}}
`)
	got := parseCodexOutput(stdout, nil)
	require.Equal(t, runnerResult{Session: "0199a213-81c0-7800-8aa1-bbab2a035a53", Reply: "The repo has a go.mod."}, got,
		"without the -o file, the last agent message")

	got = parseCodexOutput(stdout, []byte("Final answer from -o.\n"))
	require.Equal(t, "Final answer from -o.", got.Reply, "the -o file wins")

	got = parseCodexOutput([]byte(`{"type":"thread.started","thread_id":"t-1"}
{"type":"turn.started"}
{"type":"error","message":"stream disconnected before completion"}
{"type":"turn.failed","error":{"message":"stream disconnected before completion"}}
`), nil)
	require.True(t, got.IsError)
	require.Equal(t, "t-1", got.Session)
	require.Contains(t, got.Reply, "stream disconnected")
}

func TestParseOpencodeOutput(t *testing.T) {
	t.Parallel()
	got := parseOpencodeOutput([]byte(`{"type":"step_start","timestamp":1758000000000,"sessionID":"ses_6a1b2c3d4e5fXyZ","part":{"id":"prt_1","sessionID":"ses_6a1b2c3d4e5fXyZ","messageID":"msg_1","type":"step-start"}}
{"type":"tool_use","timestamp":1758000000100,"sessionID":"ses_6a1b2c3d4e5fXyZ","part":{"id":"prt_2","type":"tool","tool":"read","state":{"status":"completed"}}}
{"type":"text","timestamp":1758000000200,"sessionID":"ses_6a1b2c3d4e5fXyZ","part":{"id":"prt_3","type":"text","text":"First part.","time":{"start":1,"end":2}}}
{"type":"step_finish","timestamp":1758000000300,"sessionID":"ses_6a1b2c3d4e5fXyZ","part":{"id":"prt_4","type":"step-finish","reason":"tool-calls"}}
{"type":"text","timestamp":1758000000400,"sessionID":"ses_6a1b2c3d4e5fXyZ","part":{"id":"prt_5","type":"text","text":"Second part."}}
`))
	require.Equal(t, runnerResult{Session: "ses_6a1b2c3d4e5fXyZ", Reply: "First part.\n\nSecond part."}, got)

	got = parseOpencodeOutput([]byte(`{"type":"error","timestamp":1,"sessionID":"ses_x","error":{"name":"ProviderAuthError","data":{"message":"no API key"}}}`))
	require.True(t, got.IsError)
	require.Equal(t, "ses_x", got.Session)
	require.Contains(t, got.Reply, "no API key")
}

func TestParsePiOutput(t *testing.T) {
	t.Parallel()
	got := parsePiOutput([]byte(`{"type":"session","version":3,"id":"7f0e3c2a-1b4d-4e5f-8a9b-0c1d2e3f4a5b","timestamp":"2026-10-04T12:00:00.000Z","cwd":"/home/me/proj"}
{"type":"agent_start"}
{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"Fix it"}],"timestamp":1}}
{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"Fix it"}],"timestamp":1}}
{"type":"message_update","message":{"role":"assistant","content":[{"type":"text","text":"Work"}]},"assistantMessageEvent":{"type":"text_delta","delta":"Work"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Working on it."}],"stopReason":"toolUse"}}
{"type":"message_end","message":{"role":"toolResult","toolName":"bash","content":[{"type":"text","text":"ok"}]}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Fixed."}],"stopReason":"stop"}}
{"type":"agent_end","messages":[{"role":"user","content":"Fix it"},{"role":"assistant","content":[{"type":"text","text":"Fixed."}],"stopReason":"stop"}]}
`))
	require.Equal(t, runnerResult{Session: "7f0e3c2a-1b4d-4e5f-8a9b-0c1d2e3f4a5b", Reply: "Fixed."}, got)

	got = parsePiOutput([]byte(`{"type":"session","id":"s"}
{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"401 unauthorized"}}
`))
	require.True(t, got.IsError)
	require.Contains(t, got.Reply, "401 unauthorized")
}

func TestRunnerArgs(t *testing.T) {
	t.Parallel()
	in := RunnerInput{Task: "fix it", Session: "S", Model: "M"}
	require.Equal(t, []string{"-p", "--output-format", "json", "--resume", "S", "--model", "M", "fix it"}, runnerArgs("claude", in, ""))
	require.Equal(t, []string{"exec", "--json", "--skip-git-repo-check", "-o", "/tmp/o", "-m", "M", "resume", "S", "fix it"}, runnerArgs("codex", in, "/tmp/o"))
	require.Equal(t, []string{"exec", "--json", "--skip-git-repo-check", "-o", "/tmp/o", "fix it"}, runnerArgs("codex", RunnerInput{Task: "fix it"}, "/tmp/o"))
	require.Equal(t, []string{"run", "--format", "json", "-s", "S", "-m", "M", "fix it"}, runnerArgs("opencode", in, ""))
	require.Equal(t, []string{"--mode", "json", "--session-id", "S", "--model", "M", "fix it"}, runnerArgs("pi", in, ""))
}

func TestTruncateReply(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", maxRunnerReply)
	got := truncateReply(long)
	require.LessOrEqual(t, len(got), maxRunnerReply)
	require.True(t, strings.HasSuffix(got, "[reply truncated]"))
	require.Equal(t, "short", truncateReply("short"))
}

// writeScript writes an executable shell script named name into dir.
func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755))
}

// fakeClaude records its arguments, working directory and delegation
// variables, then prints a Claude Code result.
const fakeClaude = `printf '%s\n' "$@" > args.txt
echo "$TAP_RUNNER_DEPTH $TAP_RELAY_TOOLS" > env.txt
echo '{"type":"result","subtype":"success","is_error":false,"result":"All green.","session_id":"sess-claude-1"}'
`

// fakeCodex writes its last message to the -o file, as codex exec does.
const fakeCodex = `out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out="$a"; fi
  prev="$a"
done
printf '%s\n' "$@" > args.txt
echo '{"type":"thread.started","thread_id":"thread-9"}'
echo '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"from stream"}}'
printf 'Codex fixed it.' > "$out"
`

func newRunnerSandbox(t *testing.T, env map[string]string) (*sandbox.Sandbox, string) {
	t.Helper()
	bin := t.TempDir()
	writeScript(t, bin, "claude", fakeClaude)
	writeScript(t, bin, "codex", fakeCodex)
	writeScript(t, bin, "failing", "exit 1\n")
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	rt := sb.Runtime()
	require.NoError(t, rt.Set("PATH", bin))
	for k, v := range env {
		require.NoError(t, rt.Set(k, v))
	}
	require.NoError(t, rt.Mkdir("/home/testuser/proj", 0o755, true))
	require.NoError(t, rt.Mkdir("/srv/other", 0o755, true))
	require.NoError(t, rt.WriteFile("/home/testuser/file.txt", []byte("x"), 0o644))
	return sb, bin
}

func TestRunnerServiceSettings(t *testing.T) {
	t.Parallel()
	sb, _ := newRunnerSandbox(t, map[string]string{RunnerRootsEnv: "/srv" + string(filepath.ListSeparator) + "/home/testuser/proj", RunnerTimeoutEnv: "5m"})
	svc, err := NewRunnerService(sb.Runtime())
	require.NoError(t, err)
	require.Equal(t, []string{"/srv", "/home/testuser/proj"}, svc.Roots)
	require.Equal(t, "5m0s", svc.Timeout.String())
	names := []string{}
	for _, r := range svc.Installed {
		names = append(names, r.Tool)
	}
	require.Equal(t, []string{"claude_code_run", "codex_run"}, names, "only runners on PATH")

	sb2, _ := newRunnerSandbox(t, map[string]string{RunnerTimeoutEnv: "soon"})
	_, err = NewRunnerService(sb2.Runtime())
	require.ErrorContains(t, err, RunnerTimeoutEnv)

	sb3, _ := newRunnerSandbox(t, nil)
	svc, err = NewRunnerService(sb3.Runtime())
	require.NoError(t, err)
	require.Equal(t, []string{"/home/testuser"}, svc.Roots, "home is the default root")
}

// --runner narrows a server to one harness: it serves that tool alone and
// refuses the others.
func TestRunnerServiceOnly(t *testing.T) {
	t.Parallel()
	sb, _ := newRunnerSandbox(t, nil)
	svc, err := NewRunnerService(sb.Runtime())
	require.NoError(t, err)
	require.ErrorContains(t, svc.Only("emacs"), "unknown runner")
	require.ErrorContains(t, svc.Only("pi"), "not installed")
	require.NoError(t, svc.Only("codex"))
	require.Len(t, svc.Installed, 1)
	require.Equal(t, "codex_run", svc.Installed[0].Tool)
	_, err = svc.Run(context.Background(), "claude", RunnerInput{Task: "x", Cwd: "/home/testuser/proj"})
	require.ErrorContains(t, err, "not installed", "a narrowed server refuses the other harnesses")
}

func TestRunnerCwdGuard(t *testing.T) {
	t.Parallel()
	sb, _ := newRunnerSandbox(t, nil)
	svc, err := NewRunnerService(sb.Runtime())
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().Symlink("/srv/other", "/home/testuser/escape"))

	for cwd, want := range map[string]string{
		"":                        "cwd is required",
		"proj":                    "must be an absolute path",
		"/home/testuser/missing":  "does not exist",
		"/home/testuser/file.txt": "not a directory",
		"/srv/other":              "outside the allowed roots",
		"/home/testuser/escape":   "outside the allowed roots",
		"/home/testuserx":         "does not exist",
	} {
		_, err := svc.checkCwd(cwd)
		require.ErrorContains(t, err, want, "cwd %q", cwd)
	}
	dir, err := svc.checkCwd("/home/testuser/proj/../proj")
	require.NoError(t, err)
	host, err := sb.Runtime().HostPath("/home/testuser/proj")
	require.NoError(t, err)
	require.Equal(t, host, dir)

	require.True(t, pathWithin("/a/b", "/a/b"))
	require.True(t, pathWithin("/a/b", "/a/b/c"))
	require.False(t, pathWithin("/a/b", "/a/bc"))
	require.False(t, pathWithin("/a/b", "/a"))
}

func TestRunnerRefusesWhenDelegated(t *testing.T) {
	t.Parallel()
	sb, _ := newRunnerSandbox(t, map[string]string{RunnerDepthEnv: "1"})
	svc, err := NewRunnerService(sb.Runtime())
	require.NoError(t, err)
	_, err = svc.Run(context.Background(), "claude", RunnerInput{Task: "x", Cwd: "/home/testuser/proj"})
	require.ErrorContains(t, err, "cannot delegate again")

	_, err = sb.Runtime().ReadFile("/home/testuser/proj/args.txt")
	require.Error(t, err, "the runner never started")
}

func TestRunnerFailureIsAnErrorResult(t *testing.T) {
	t.Parallel()
	sb, bin := newRunnerSandbox(t, nil)
	writeScript(t, bin, "claude", "echo 'boom: not logged in' >&2\nexit 3\n")
	svc, err := NewRunnerService(sb.Runtime())
	require.NoError(t, err)
	out, err := svc.Run(context.Background(), "claude", RunnerInput{Task: "x", Cwd: "/home/testuser/proj", Session: "keep"})
	require.NoError(t, err)
	require.True(t, out.IsError)
	require.Equal(t, "keep", out.Session, "the caller's session survives a failed run")
	require.Contains(t, out.Reply, "exit status 3")
	require.Contains(t, out.Reply, "boom: not logged in")

	_, err = svc.Run(context.Background(), "pi", RunnerInput{Task: "x", Cwd: "/home/testuser/proj"})
	require.ErrorContains(t, err, "not installed")
}

// The runner tools work end to end through the MCP server: the call reaches
// the runner in the requested directory, with delegation turned off for it,
// and its reply and session come back.
func TestRunnerMCPServer(t *testing.T) {
	t.Parallel()
	sb, _ := newRunnerSandbox(t, nil)
	svc, err := NewRunnerService(sb.Runtime())
	require.NoError(t, err)
	srv := NewRunnerMCPServer(svc, "test")

	ctx := context.Background()
	serverT, clientT := sdkmcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	names := []string{}
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		require.NotNil(t, tool.OutputSchema)
		require.False(t, tool.Annotations.ReadOnlyHint)
		require.True(t, *tool.Annotations.OpenWorldHint)
	}
	require.Equal(t, []string{"claude_code_run", "codex_run"}, names)

	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "claude_code_run", Arguments: map[string]any{
		"task": "run the tests", "cwd": "/home/testuser/proj", "model": "sonnet",
	}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := res.Content[0].(*sdkmcp.TextContent).Text
	require.Contains(t, text, "sess-claude-1")
	require.True(t, strings.HasSuffix(text, "All green."))
	var out RunnerOutput
	raw, _ := json.Marshal(res.StructuredContent)
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Equal(t, RunnerOutput{Runner: "claude", Session: "sess-claude-1", Reply: "All green."}, out)
	args, err := sb.Runtime().ReadFile("/home/testuser/proj/args.txt")
	require.NoError(t, err, "the runner ran in cwd")
	require.Equal(t, "-p\n--output-format\njson\n--model\nsonnet\nrun the tests\n", string(args))
	env, err := sb.Runtime().ReadFile("/home/testuser/proj/env.txt")
	require.NoError(t, err)
	require.Equal(t, "1 off\n", string(env), "the runner is marked delegated and kept off relayed tools")

	res, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codex_run", Arguments: map[string]any{
		"task": "fix it", "cwd": "/home/testuser/proj", "session": "thread-9",
	}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	raw, _ = json.Marshal(res.StructuredContent)
	out = RunnerOutput{}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Equal(t, RunnerOutput{Runner: "codex", Session: "thread-9", Reply: "Codex fixed it."}, out)
	args, err = sb.Runtime().ReadFile("/home/testuser/proj/args.txt")
	require.NoError(t, err)
	require.Contains(t, string(args), "resume\nthread-9\nfix it\n")

	res, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "codex_run", Arguments: map[string]any{
		"task": "fix it", "cwd": "/srv/other",
	}})
	require.NoError(t, err)
	require.True(t, res.IsError, "a refused cwd is a tool error")
	require.Contains(t, res.Content[0].(*sdkmcp.TextContent).Text, "outside the allowed roots")
}
