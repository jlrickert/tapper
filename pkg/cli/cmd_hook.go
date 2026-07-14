package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/spf13/cobra"
)

const skipRootInitializationAnnotation = "tapper.io/skip-root-initialization"

const hookOrientationReminder = "Before KEG work, call `mcp__tapper__orient` and follow the returned flight and KEG instructions. If that tool is unavailable, use the `tapper-mcp-reset` skill. Continue using only `mcp__tapper__*` tools for KEG work; never read or write tapper node storage files directly. Snapshot before meaningful node edits with `mcp__tapper__node_snapshot`. For cross-keg work, pass the `keg` parameter instead of changing directories or restarting the MCP server. Direct `tap` / `keg` CLI use from Codex remains blocked except help, version, and completion probes."

const hookDenyReason = "Direct tap/keg CLI invocations are blocked for the agent. Use the mcp__tapper__* tools instead. See integrations/content/agent-orient.md (the 'never read or write node files directly' policy). Allowlisted: 'tap completion', '--version', '--help'."

var (
	hookAllowlist = map[string]bool{"completion": true, "--version": true, "-v": true, "--help": true, "-h": true}
	hookWrappers  = map[string]bool{"sudo": true, "command": true, "exec": true, "builtin": true, "time": true}
	hookShells    = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true}
)

type hookInputError struct{ message string }

func (e *hookInputError) Error() string { return e.message }

// NewHookCmd builds the hidden protocol used by the native Codex and Claude
// plugins. It is intentionally host-facing rather than a public user workflow.
func NewHookCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "run native Tapper plugin hooks",
		Hidden: true,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:         "session-start",
			Short:       "restore Tapper guidance at a host session boundary",
			Hidden:      true,
			Args:        cobra.NoArgs,
			Annotations: map[string]string{skipRootInitializationAnnotation: "true"},
			RunE: func(_ *cobra.Command, _ []string) error {
				runSessionStartHook(deps.Runtime)
				return nil
			},
		},
		&cobra.Command{
			Use:         "pre-tool-use",
			Short:       "guard direct tap and keg CLI use by agents",
			Hidden:      true,
			Args:        cobra.NoArgs,
			Annotations: map[string]string{skipRootInitializationAnnotation: "true"},
			RunE: func(_ *cobra.Command, _ []string) error {
				return runPreToolUseHook(deps.Runtime)
			},
		},
	)
	return cmd
}

func skipsRootInitialization(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Annotations[skipRootInitializationAnnotation] == "true"
}

func runSessionStartHook(rt *toolkit.Runtime) {
	if rt == nil {
		return
	}
	raw, err := io.ReadAll(rt.Stream().In)
	if err != nil {
		hookFailOpenDiagnostic(rt, fmt.Errorf("read input: %w", err))
		return
	}
	payload, err := decodeHookObject(raw)
	if err != nil {
		hookFailOpenDiagnostic(rt, err)
		return
	}
	if event, ok := hookStringField(payload, "hook_event_name"); ok && event != "SessionStart" {
		return
	}
	out := struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}{}
	out.HookSpecificOutput.HookEventName = "SessionStart"
	out.HookSpecificOutput.AdditionalContext = hookOrientationReminder
	if err := writeHookJSON(rt.Stream().Out, out); err != nil {
		hookFailOpenDiagnostic(rt, fmt.Errorf("write output: %w", err))
	}
}

func hookFailOpenDiagnostic(rt *toolkit.Runtime, err error) {
	if rt != nil && err != nil {
		_, _ = fmt.Fprintf(rt.Stream().Err, "tap hook session-start: %v; allowing session startup\n", err)
	}
}

func runPreToolUseHook(rt *toolkit.Runtime) error {
	if rt == nil {
		return &hookInputError{message: "hook pre-tool-use: runtime is required"}
	}
	raw, err := io.ReadAll(rt.Stream().In)
	if err != nil {
		return &hookInputError{message: fmt.Sprintf("hook pre-tool-use: read input: %v", err)}
	}
	payload, err := decodeHookObject(raw)
	if err != nil {
		return &hookInputError{message: "hook pre-tool-use: " + err.Error()}
	}
	toolInput, ok := hookObjectField(payload, "tool_input")
	if !ok {
		return nil
	}
	command, ok := hookStringField(toolInput, "command")
	if !ok || command == "" || !hookCommandDenied(command, 0) {
		return nil
	}
	out := struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}{}
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = hookDenyReason
	if err := writeHookJSON(rt.Stream().Out, out); err != nil {
		return &hookInputError{message: fmt.Sprintf("hook pre-tool-use: write output: %v", err)}
	}
	return nil
}

func decodeHookObject(raw []byte) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("empty stdin")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("could not parse JSON payload: %w", err)
	}
	if payload == nil {
		return nil, errors.New("JSON payload must be an object")
	}
	return payload, nil
}

func hookObjectField(payload map[string]json.RawMessage, name string) (map[string]json.RawMessage, bool) {
	raw, ok := payload[name]
	if !ok {
		return nil, false
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}

func hookStringField(payload map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := payload[name]
	if !ok {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func writeHookJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(value)
}

func hookCommandDenied(command string, depth int) bool {
	for _, segment := range splitHookSegments(command) {
		argv, err := splitHookWords(strings.TrimSpace(segment))
		if err != nil || len(argv) == 0 {
			continue
		}
		argv = stripHookAssignments(argv)
		if len(argv) == 0 {
			continue
		}
		if hookWrappers[argv[0]] {
			argv = stripHookAssignments(argv[1:])
			if len(argv) == 0 {
				continue
			}
		}
		base := normalizeHookCommand(argv[0])
		if hookShells[base] && len(argv) >= 3 && argv[1] == "-c" && depth < 1 {
			if hookCommandDenied(argv[2], depth+1) {
				return true
			}
			continue
		}
		if base != "tap" && base != "keg" {
			continue
		}
		if len(argv) < 2 || !hookAllowlist[argv[1]] {
			return true
		}
	}
	return false
}

func splitHookSegments(command string) []string {
	var segments []string
	var current strings.Builder
	var quote rune
	escaped := false
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			current.WriteRune(ch)
			escaped = true
			continue
		}
		if quote != 0 {
			current.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			current.WriteRune(ch)
			continue
		}
		if ch == ';' || ch == '|' || ch == '&' {
			segments = append(segments, current.String())
			current.Reset()
			if i+1 < len(runes) && runes[i+1] == ch {
				i++
			}
			continue
		}
		current.WriteRune(ch)
	}
	segments = append(segments, current.String())
	return segments
}

func splitHookWords(text string) ([]string, error) {
	var words []string
	var current strings.Builder
	var quote rune
	token := false
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if quote == 0 {
			switch {
			case unicode.IsSpace(ch):
				if token {
					words = append(words, current.String())
					current.Reset()
					token = false
				}
			case ch == '\'' || ch == '"':
				quote = ch
				token = true
			case ch == '\\':
				if i+1 >= len(runes) {
					return nil, errors.New("trailing escape")
				}
				i++
				current.WriteRune(runes[i])
				token = true
			default:
				current.WriteRune(ch)
				token = true
			}
			continue
		}
		if ch == quote {
			quote = 0
			continue
		}
		if ch == '\\' && quote == '"' {
			if i+1 >= len(runes) {
				return nil, errors.New("trailing escape")
			}
			i++
			current.WriteRune(runes[i])
			continue
		}
		current.WriteRune(ch)
	}
	if quote != 0 {
		return nil, errors.New("unclosed quote")
	}
	if token {
		words = append(words, current.String())
	}
	return words, nil
}

func stripHookAssignments(argv []string) []string {
	for len(argv) > 0 && isHookAssignment(argv[0]) {
		argv = argv[1:]
	}
	return argv
}

func isHookAssignment(value string) bool {
	name, _, ok := strings.Cut(value, "=")
	if !ok || name == "" || (name[0] < 'A' || name[0] > 'Z') && name[0] != '_' {
		return false
	}
	for i := 1; i < len(name); i++ {
		ch := name[i]
		if (ch < 'A' || ch > 'Z') && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' {
			return false
		}
	}
	return true
}

func normalizeHookCommand(command string) string {
	return filepath.Base(strings.TrimLeft(command, "\\/"))
}
