package tapper

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jlrickert/cli-toolkit/toolkit"

	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// `tap runner serve` is an MCP server that hands a task to a coding agent CLI
// installed on this machine (Claude Code, Codex, opencode, pi) and returns its
// reply. The relay offers it as a built-in server, so a Hub chat or another
// agent can delegate work to the machine's runners. Each runner applies its
// own permission settings; nothing here bypasses them.

// Environment variables `tap runner serve` reads and sets.
const (
	// RunnerRootsEnv lists the directories (os.PathListSeparator-separated)
	// a task's cwd must be under. Unset means $HOME.
	RunnerRootsEnv = "TAP_RUNNER_ROOTS"
	// RunnerTimeoutEnv bounds one task, as a Go duration. Unset means
	// DefaultRunnerTimeout.
	RunnerTimeoutEnv = "TAP_RUNNER_TIMEOUT"
	// RunnerDepthEnv counts how many runner delegations deep a process is.
	// A runner's child gets it incremented; a server that sees 1 or more
	// refuses every call, so a delegated agent cannot delegate again.
	RunnerDepthEnv = "TAP_RUNNER_DEPTH"
	// RelayToolsEnv set to "off" keeps `tap mcp` from offering relayed tools.
	// Runner children get it so they cannot reach back into the relay.
	RelayToolsEnv = "TAP_RELAY_TOOLS"
)

// DefaultRunnerTimeout bounds one runner task when nothing else is set.
const DefaultRunnerTimeout = 30 * time.Minute

const (
	// maxRunnerStdout caps the runner output kept for parsing. Output past it
	// is dropped; the reply normally comes early (Claude Code's one JSON
	// object) or from a file (Codex).
	maxRunnerStdout = 8 << 20
	// maxRunnerStderr keeps the tail of stderr, for error reports.
	maxRunnerStderr = 16 << 10
	// maxRunnerReply caps the reply. The relay rejects a tool result over
	// relaycontract.MaxToolResultBytes, and the reply is sent twice (text and
	// structured content), JSON-escaped, so it gets a quarter of that.
	maxRunnerReply = relaycontract.MaxToolResultBytes / 4
)

// RunnerSpec describes one coding agent CLI `tap runner serve` can drive.
type RunnerSpec struct {
	// Name is the runner's short name, reported in results.
	Name string
	// Binary is the executable looked up on PATH.
	Binary string
	// Tool is the MCP tool name that runs it.
	Tool string
	// Title is the human name used in tool descriptions.
	Title string
	// Permissions says, for the tool description, what the runner may do
	// without asking in this non-interactive mode.
	Permissions string
}

// Runners are the coding agent CLIs `tap runner serve` knows, in tool order.
var Runners = []RunnerSpec{
	{Name: "claude", Binary: "claude", Tool: "claude_code_run", Title: "Claude Code",
		Permissions: "Claude Code applies your permission settings; in print mode it denies any action that would need an interactive prompt."},
	{Name: "codex", Binary: "codex", Tool: "codex_run", Title: "Codex",
		Permissions: "Codex applies your config; `codex exec` runs in a read-only sandbox unless your config grants more."},
	{Name: "opencode", Binary: "opencode", Tool: "opencode_run", Title: "opencode",
		Permissions: "opencode applies your permission config to every tool it uses."},
	{Name: "pi", Binary: "pi", Tool: "pi_run", Title: "pi",
		Permissions: "pi runs with the tools and settings of your pi configuration."},
}

// InstalledRunner is a runner found on PATH.
type InstalledRunner struct {
	RunnerSpec
	// Path is the resolved executable.
	Path string
}

// DetectRunners returns the runners installed on pathEnv (a PATH value), in
// Runners order.
func DetectRunners(pathEnv string) []InstalledRunner {
	var out []InstalledRunner
	for _, spec := range Runners {
		if p, ok := lookPathIn(pathEnv, spec.Binary); ok {
			out = append(out, InstalledRunner{RunnerSpec: spec, Path: p})
		}
	}
	return out
}

// lookPathIn finds an executable named file in the directories of pathEnv.
// It searches the runtime's PATH rather than the process's, so tests can
// control what is installed. Relative PATH entries are ignored.
func lookPathIn(pathEnv, file string) (string, bool) {
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		// A name with a separator is checked directly, never searched for.
		if p, err := exec.LookPath(filepath.Join(dir, file)); err == nil {
			return p, true
		}
	}
	return "", false
}

// RunnerInput is one task for a runner.
type RunnerInput struct {
	Task    string `json:"task" jsonschema:"the instruction for the coding agent, as you would type it"`
	Cwd     string `json:"cwd" jsonschema:"absolute path of the directory to work in; must be under one of the relay's allowed roots"`
	Session string `json:"session,omitempty" jsonschema:"session id from an earlier result, to continue that conversation"`
	Model   string `json:"model,omitempty" jsonschema:"model to use, in the runner's own naming; omit for its default"`
}

// RunnerOutput is a runner's answer to one task.
type RunnerOutput struct {
	Runner  string `json:"runner" jsonschema:"the runner that ran the task"`
	Session string `json:"session,omitempty" jsonschema:"session id; pass it back as session to continue"`
	Reply   string `json:"reply" jsonschema:"the runner's final reply"`
	IsError bool   `json:"is_error" jsonschema:"whether the run failed"`
}

// RunnerService runs tasks on the installed runners. Build it with
// NewRunnerService.
type RunnerService struct {
	rt *toolkit.Runtime
	// Installed are the runners found on the runtime's PATH.
	Installed []InstalledRunner
	// Roots are the directories a task's cwd must resolve under.
	Roots []string
	// Timeout bounds one task.
	Timeout time.Duration
	// Depth is this process's delegation depth; at 1 or more every call is
	// refused.
	Depth int
}

// NewRunnerService reads the runner settings from rt's environment.
func NewRunnerService(rt *toolkit.Runtime) (*RunnerService, error) {
	s := &RunnerService{rt: rt, Installed: DetectRunners(rt.Get("PATH")), Timeout: DefaultRunnerTimeout}
	if v := strings.TrimSpace(rt.Get(RunnerTimeoutEnv)); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("%s %q must be a positive duration such as 30m", RunnerTimeoutEnv, v)
		}
		s.Timeout = d
	}
	if v := strings.TrimSpace(rt.Get(RunnerDepthEnv)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%s %q must be a non-negative integer", RunnerDepthEnv, v)
		}
		s.Depth = n
	}
	for _, root := range filepath.SplitList(rt.Get(RunnerRootsEnv)) {
		if root = strings.TrimSpace(root); root != "" {
			s.Roots = append(s.Roots, root)
		}
	}
	if len(s.Roots) == 0 {
		home, err := rt.GetHome()
		if err != nil || home == "" {
			return nil, fmt.Errorf("no %s set and no home directory to default to", RunnerRootsEnv)
		}
		s.Roots = []string{home}
	}
	return s, nil
}

// Only narrows the service to the named runner, so it serves that one tool
// and refuses the rest. It fails when the runner is unknown or not installed.
func (s *RunnerService) Only(name string) error {
	known := false
	for _, spec := range Runners {
		known = known || spec.Name == name
	}
	if !known {
		names := make([]string, 0, len(Runners))
		for _, spec := range Runners {
			names = append(names, spec.Name)
		}
		return fmt.Errorf("unknown runner %q (expected one of %s)", name, strings.Join(names, ", "))
	}
	for _, r := range s.Installed {
		if r.Name == name {
			s.Installed = []InstalledRunner{r}
			return nil
		}
	}
	return fmt.Errorf("runner %q is not installed on this machine", name)
}

// Run hands in.Task to the named runner and waits for its reply. Bad input or
// a refused call is an error; a runner that ran and failed is an output with
// IsError set.
func (s *RunnerService) Run(ctx context.Context, name string, in RunnerInput) (RunnerOutput, error) {
	if s.Depth >= 1 {
		return RunnerOutput{}, fmt.Errorf("refusing to delegate: this runner server was started by a delegated task (%s=%d), and a delegated agent cannot delegate again", RunnerDepthEnv, s.Depth)
	}
	var runner *InstalledRunner
	for i := range s.Installed {
		if s.Installed[i].Name == name {
			runner = &s.Installed[i]
		}
	}
	if runner == nil {
		return RunnerOutput{}, fmt.Errorf("runner %q is not installed on this machine", name)
	}
	if strings.TrimSpace(in.Task) == "" {
		return RunnerOutput{}, errors.New("task is required")
	}
	dir, err := s.checkCwd(in.Cwd)
	if err != nil {
		return RunnerOutput{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	// Codex writes its last message to a file, which is more reliable than
	// picking it out of the event stream.
	var lastMessage string
	if runner.Name == "codex" {
		tmp := s.rt.GetTempDir()
		if err := s.rt.Mkdir(tmp, 0o700, true); err != nil {
			return RunnerOutput{}, fmt.Errorf("create temp directory: %w", err)
		}
		lastMessage = filepath.Join(tmp, "tap-runner-"+randomHex(8)+".txt")
		defer func() { _ = s.rt.Remove(lastMessage, false) }()
	}
	lastMessageHost := ""
	if lastMessage != "" {
		if lastMessageHost, err = s.rt.HostPath(lastMessage); err != nil {
			return RunnerOutput{}, err
		}
	}

	args := runnerArgs(runner.Name, in, lastMessageHost)
	cmd := exec.CommandContext(ctx, runner.Path, args...)
	cmd.Dir = dir
	cmd.Env = append(stripEnv(s.rt.Environ(), []string{RunnerDepthEnv, RelayToolsEnv}),
		RunnerDepthEnv+"="+strconv.Itoa(s.Depth+1),
		RelayToolsEnv+"=off",
	)
	stdout := &headBuffer{max: maxRunnerStdout}
	stderr := &tailBuffer{max: maxRunnerStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// Stdin stays nil, the null device: a runner that waits for input fails
	// instead of hanging. WaitDelay stops a grandchild that keeps the output
	// pipes open from holding the call past the timeout.
	cmd.WaitDelay = 10 * time.Second
	runErr := cmd.Run()

	var res runnerResult
	switch runner.Name {
	case "claude":
		res = parseClaudeOutput(stdout.Bytes())
	case "codex":
		var last []byte
		if data, err := s.rt.ReadFile(lastMessage); err == nil {
			last = data
		}
		res = parseCodexOutput(stdout.Bytes(), last)
	case "opencode":
		res = parseOpencodeOutput(stdout.Bytes())
	case "pi":
		res = parsePiOutput(stdout.Bytes())
	}
	out := RunnerOutput{Runner: runner.Name, Session: res.Session, Reply: res.Reply, IsError: res.IsError}
	if out.Session == "" {
		out.Session = in.Session
	}
	if runErr != nil {
		out.IsError = true
		reason := runErr.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = fmt.Sprintf("timed out after %s", s.Timeout)
		}
		msg := fmt.Sprintf("%s failed: %s", runner.Title, reason)
		if tail := strings.TrimSpace(stderr.String()); tail != "" {
			msg += "\n\nstderr:\n" + tail
		}
		if out.Reply != "" {
			msg = out.Reply + "\n\n" + msg
		}
		out.Reply = msg
	} else if out.Reply == "" && out.IsError {
		if tail := strings.TrimSpace(stderr.String()); tail != "" {
			out.Reply = tail
		}
	}
	out.Reply = truncateReply(out.Reply)
	return out, nil
}

// checkCwd validates a task's directory and returns the host path to run in:
// absolute, an existing directory, and, with symlinks resolved, under one of
// the allowed roots.
func (s *RunnerService) checkCwd(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("cwd is required")
	}
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("cwd %q must be an absolute path", cwd)
	}
	resolved, err := s.rt.ResolvePath(filepath.Clean(cwd), true)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("cwd %q does not exist", cwd)
	}
	if err != nil {
		return "", fmt.Errorf("cwd %q cannot be resolved", cwd)
	}
	info, err := s.rt.Stat(resolved, true)
	if err != nil {
		return "", fmt.Errorf("cwd %q does not exist", cwd)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", cwd)
	}
	allowed := false
	for _, root := range s.Roots {
		r, err := s.rt.ResolvePath(filepath.Clean(root), true)
		if err != nil {
			continue
		}
		if pathWithin(r, resolved) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("cwd %q is outside the allowed roots (%s)", cwd, strings.Join(s.Roots, string(filepath.ListSeparator)))
	}
	return s.rt.HostPath(resolved)
}

// pathWithin reports whether path is root or below it. Both are clean and
// absolute.
func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// runnerArgs is the command line for one task, options before the task.
func runnerArgs(name string, in RunnerInput, lastMessage string) []string {
	switch name {
	case "claude":
		args := []string{"-p", "--output-format", "json"}
		if in.Session != "" {
			args = append(args, "--resume", in.Session)
		}
		if in.Model != "" {
			args = append(args, "--model", in.Model)
		}
		return append(args, in.Task)
	case "codex":
		// Exec options go before the resume subcommand, where every codex
		// version accepts them.
		args := []string{"exec", "--json", "--skip-git-repo-check", "-o", lastMessage}
		if in.Model != "" {
			args = append(args, "-m", in.Model)
		}
		if in.Session != "" {
			args = append(args, "resume", in.Session)
		}
		return append(args, in.Task)
	case "opencode":
		args := []string{"run", "--format", "json"}
		if in.Session != "" {
			args = append(args, "-s", in.Session)
		}
		if in.Model != "" {
			args = append(args, "-m", in.Model)
		}
		return append(args, in.Task)
	case "pi":
		args := []string{"--mode", "json"}
		if in.Session != "" {
			args = append(args, "--session-id", in.Session)
		}
		if in.Model != "" {
			args = append(args, "--model", in.Model)
		}
		return append(args, in.Task)
	}
	return nil
}

// runnerResult is what a runner's output says.
type runnerResult struct {
	Session string
	Reply   string
	IsError bool
}

// parseClaudeOutput reads `claude -p --output-format json`: one result object
// ({"type":"result","session_id":...,"result":...,"is_error":...}), or, with
// --verbose, an array of messages ending in one.
func parseClaudeOutput(stdout []byte) runnerResult {
	type result struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		Result    string `json:"result"`
		IsError   bool   `json:"is_error"`
	}
	trimmed := bytes.TrimSpace(stdout)
	var one result
	if err := json.Unmarshal(trimmed, &one); err == nil && (one.SessionID != "" || one.Result != "") {
		return runnerResult{Session: one.SessionID, Reply: one.Result, IsError: one.IsError}
	}
	var many []result
	if err := json.Unmarshal(trimmed, &many); err == nil {
		for i := len(many) - 1; i >= 0; i-- {
			if many[i].Type == "result" {
				return runnerResult{Session: many[i].SessionID, Reply: many[i].Result, IsError: many[i].IsError}
			}
		}
	}
	// Some builds stream one object per line; the last result wins.
	var out runnerResult
	found := false
	eachJSONLine(stdout, func(line []byte) {
		var r result
		if json.Unmarshal(line, &r) != nil {
			return
		}
		if out.Session == "" && r.SessionID != "" {
			out.Session = r.SessionID
		}
		if r.Type == "result" {
			out.Reply, out.IsError, found = r.Result, r.IsError, true
			if r.SessionID != "" {
				out.Session = r.SessionID
			}
		}
	})
	if !found {
		out.IsError = true
		out.Reply = "Claude Code returned no result"
	}
	return out
}

// parseCodexOutput reads `codex exec --json` events and the file -o wrote.
// The thread id comes from {"type":"thread.started","thread_id":...}; the
// reply is the -o file, else the last completed agent_message item.
func parseCodexOutput(stdout, lastMessage []byte) runnerResult {
	var out runnerResult
	var lastAgent, failure string
	eachJSONLine(stdout, func(line []byte) {
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Message  string `json:"message"`
			Error    *struct {
				Message string `json:"message"`
			} `json:"error"`
			Item *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(line, &ev) != nil {
			return
		}
		switch ev.Type {
		case "thread.started":
			if out.Session == "" {
				out.Session = ev.ThreadID
			}
		case "item.completed":
			if ev.Item != nil && ev.Item.Type == "agent_message" && ev.Item.Text != "" {
				lastAgent = ev.Item.Text
			}
		case "turn.failed":
			if ev.Error != nil {
				failure = ev.Error.Message
			}
			out.IsError = true
		case "error":
			if failure == "" {
				failure = ev.Message
			}
			out.IsError = true
		}
	})
	out.Reply = strings.TrimSpace(string(lastMessage))
	if out.Reply == "" {
		out.Reply = lastAgent
	}
	if out.IsError && failure != "" {
		if out.Reply != "" {
			out.Reply += "\n\n"
		}
		out.Reply += "Codex error: " + failure
	}
	return out
}

// parseOpencodeOutput reads `opencode run --format json` events. Every event
// carries the session id as "sessionID"; the reply is the text parts, in
// order.
func parseOpencodeOutput(stdout []byte) runnerResult {
	var out runnerResult
	var parts []string
	eachJSONLine(stdout, func(line []byte) {
		var ev struct {
			Type      string          `json:"type"`
			SessionID string          `json:"sessionID"`
			Error     json.RawMessage `json:"error"`
			Part      *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"part"`
		}
		if json.Unmarshal(line, &ev) != nil {
			return
		}
		if out.Session == "" && ev.SessionID != "" {
			out.Session = ev.SessionID
		}
		switch ev.Type {
		case "text":
			if ev.Part != nil && strings.TrimSpace(ev.Part.Text) != "" {
				parts = append(parts, ev.Part.Text)
			}
		case "error":
			out.IsError = true
			if msg := jsonErrorMessage(ev.Error); msg != "" {
				parts = append(parts, "opencode error: "+msg)
			}
		}
	})
	out.Reply = strings.Join(parts, "\n\n")
	return out
}

// parsePiOutput reads `pi --mode json` records. The first is
// {"type":"session","id":...}; the reply is the text of the last assistant
// message, found in any record's "message" or "messages".
func parsePiOutput(stdout []byte) runnerResult {
	type message struct {
		Role         string          `json:"role"`
		Content      json.RawMessage `json:"content"`
		StopReason   string          `json:"stopReason"`
		ErrorMessage string          `json:"errorMessage"`
	}
	var out runnerResult
	consider := func(m *message) {
		if m == nil || m.Role != "assistant" {
			return
		}
		if text := messageText(m.Content); text != "" {
			out.Reply = text
			out.IsError = false
		}
		if m.StopReason == "error" || m.StopReason == "aborted" {
			out.IsError = true
			if m.ErrorMessage != "" {
				out.Reply = strings.TrimSpace(out.Reply + "\n\npi error: " + m.ErrorMessage)
			}
		}
	}
	eachJSONLine(stdout, func(line []byte) {
		var rec struct {
			Type     string    `json:"type"`
			ID       string    `json:"id"`
			Message  *message  `json:"message"`
			Messages []message `json:"messages"`
		}
		if json.Unmarshal(line, &rec) != nil {
			return
		}
		if rec.Type == "session" && out.Session == "" {
			out.Session = rec.ID
		}
		consider(rec.Message)
		for i := range rec.Messages {
			consider(&rec.Messages[i])
		}
	})
	return out
}

// messageText is the text of a message's content: a plain string, or the
// "text" blocks of a content array.
func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// jsonErrorMessage reads an error that is a string or an object with a
// message (possibly under "data").
func jsonErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
		Name    string `json:"name"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	switch {
	case obj.Data.Message != "":
		return obj.Data.Message
	case obj.Message != "":
		return obj.Message
	}
	return obj.Name
}

// eachJSONLine calls fn with every line of stdout that looks like a JSON
// object, skipping anything else a runner prints.
func eachJSONLine(stdout []byte, fn func([]byte)) {
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64<<10), maxRunnerStdout)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) > 0 && line[0] == '{' {
			fn(line)
		}
	}
}

// truncateReply keeps a reply under maxRunnerReply, cutting on a rune
// boundary.
func truncateReply(s string) string {
	if len(s) <= maxRunnerReply {
		return s
	}
	const note = "\n\n[reply truncated]"
	cut := maxRunnerReply - len(note)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + note
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// headBuffer keeps the first max bytes written to it and discards the rest.
type headBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *headBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *headBuffer) Bytes() []byte { return b.buf.Bytes() }

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf []byte
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string { return string(b.buf) }
