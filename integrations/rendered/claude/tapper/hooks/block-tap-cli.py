#!/usr/bin/env python3
# block-tap-cli.py -- PreToolUse Bash matcher for the tapper plugin.
#
# Cross-platform Python 3 port of block-tap-cli.sh. Runs anywhere
# Python 3.8+ is on PATH (macOS, Linux, Windows). Standard library
# only -- no third-party dependencies.
#
# Purpose: blocks direct `tap` and `keg` CLI invocations from the
# agent so that all KEG access funnels through the mcp__tapper__*
# MCP tools. CLI and MCP have full surface parity (see
# integrations/content/tool-inventory.md), so a categorical deny
# with a tiny allowlist (completion, --version, --help) is safe.
#
# Reads the Claude Code PreToolUse JSON event from stdin, extracts
# .tool_input.command, walks each pipeline segment, strips env-var
# prefixes and wrapper commands (sudo/command/exec/builtin/time),
# and checks whether the resulting argv[0] basename is `tap` or
# `keg`. Also follows `bash -c`/`sh -c`/`zsh -c`/`dash -c` one
# level deep.
#
# Exit codes:
#   0 with no stdout    -- allow (default)
#   0 with deny JSON    -- explicit deny via permissionDecision
#   2                   -- hook error (empty/missing/malformed JSON)
#
# Limitations -- this is a guardrail, not a security boundary:
#   - eval "$(printf '%s\n' 'tap list')" -- eval-wrapped
#     invocations slip through; we do not run a real shell parser.
#   - cmd=tap; "$cmd" list -- variable indirection is not resolved.
#   - xargs tap -- argv[0] is `xargs`, so the wrapped `tap` is not
#     inspected. Same shape for `find ... -exec tap ...`.
#   - \tap list -- after the os.path.basename normalization we
#     strip leading `\` and `/` characters before basename, so
#     `\tap` is treated as `tap` and denied. This matches the
#     bash form, where the shell would have already discarded
#     the backslash quoting before exec.
#   - t""ap list -- shell concatenation (`t""ap` -> `tap`) is not
#     reconstructed; the matcher sees `t""ap` and allows.
#   - Nested `bash -c "bash -c '...'"` beyond one level -- only
#     one level of -c re-parsing is performed.
#   - Quoted segment content -- the segment scanner respects
#     single and double quotes, so `echo "tap list && true"` is a
#     single segment whose argv[0] is `echo`, which allows.
#
# Catching the bypasses above would require a real shell parser;
# the goal here is to stop accidental and casual direct CLI use,
# not adversarial bypass. The MCP surface is the canonical path.
"""PreToolUse hook entry point. Invoked by Claude Code with the
tool-call event JSON on stdin."""
from __future__ import annotations

import json
import os.path
import re
import shlex
import sys
from typing import List, Optional

# argv[1] tokens that gate the allowlist for `tap`/`keg`. These do
# not touch any KEG and are useful for plumbing (shell completion
# install, version probes, help text).
ALLOWLIST = {"completion", "--version", "-v", "--help", "-h"}

# Wrappers we peel off the head of a segment before inspecting
# argv[0]. Only one wrapper level is peeled -- chains like
# `sudo -E env FOO=bar tap ...` are intentionally out of scope.
WRAPPERS = {"sudo", "command", "exec", "builtin", "time"}

# Shells whose `-c "..."` argument we recurse into one level.
SHELLS = {"bash", "sh", "zsh", "dash"}

# Matches a leading `VAR=value` env assignment with no whitespace
# in the value. Multiple assignments separated by whitespace are
# stripped iteratively.
ENV_ASSIGN_RE = re.compile(r"^[A-Z_][A-Za-z0-9_]*=")

# Exact basename match for the deny set. Anchored so substring
# matches (`taproom`, `keg-foo`, `bootstrap`) do not trigger.
DENY_BASENAME_RE = re.compile(r"^(tap|keg)$")

# Deny reason copied from the bash version verbatim; the reviewer
# confirmed the bash payload is byte-correct.
DENY_REASON = (
    "Direct tap/keg CLI invocations are blocked for the agent. "
    "Use the mcp__tapper__* tools instead. See "
    "integrations/content/agent-orient.md (the 'never read or "
    "write node files directly' policy). Allowlisted: "
    "'tap completion', '--version', '--help'."
)


def deny() -> None:
    """Emit the deny payload to stdout and exit 0.

    Claude Code expects exit 0 with a permissionDecision payload on
    stdout for explicit denies; non-zero is reserved for
    hook-internal errors.
    """
    payload = {
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "deny",
            "permissionDecisionReason": DENY_REASON,
        }
    }
    # json.dumps with no indent and default separators produces the
    # same compact form as `jq -cn` in the bash original.
    sys.stdout.write(json.dumps(payload))
    sys.exit(0)


def hook_error(msg: str) -> None:
    """Log to stderr and exit 2 so Claude Code surfaces the failure
    rather than silently allowing or denying."""
    sys.stderr.write("block-tap-cli: " + msg + "\n")
    sys.exit(2)


def split_segments(cmd: str) -> List[str]:
    """Split `cmd` on shell metacharacters (;, &&, ||, |, &), but
    treat single- and double-quoted spans as opaque so that a
    metacharacter inside a quoted string does not start a new
    segment.

    This is the guardrail trade-off called out in the header: the
    bash form did a textual replace (no quote awareness), which
    falsely flagged `echo "a; tap list"`. The Python port keeps
    the bash form's coarseness for unquoted text but respects
    quotes so the false-positive case allows correctly.
    """
    segments: List[str] = []
    buf: List[str] = []
    i = 0
    n = len(cmd)
    quote: Optional[str] = None  # current quote char, or None
    while i < n:
        ch = cmd[i]
        if quote is not None:
            # Inside a quoted span: copy until the matching quote.
            # Backslash escapes are preserved as-is; we are not
            # interpreting the string, only locating its end.
            buf.append(ch)
            if ch == "\\" and quote == '"' and i + 1 < n:
                # In double quotes, `\"` escapes the closing quote.
                buf.append(cmd[i + 1])
                i += 2
                continue
            if ch == quote:
                quote = None
            i += 1
            continue
        if ch in ('"', "'"):
            quote = ch
            buf.append(ch)
            i += 1
            continue
        # Two-character operators: && and ||.
        if i + 1 < n and (cmd[i : i + 2] in ("&&", "||")):
            segments.append("".join(buf))
            buf = []
            i += 2
            continue
        if ch in (";", "|", "&"):
            segments.append("".join(buf))
            buf = []
            i += 1
            continue
        buf.append(ch)
        i += 1
    segments.append("".join(buf))
    return segments


def strip_env_assignments(argv: List[str]) -> List[str]:
    """Drop leading argv tokens that look like `VAR=value` env
    assignments. Mirrors the bash regex: name must match
    `[A-Z_][A-Za-z0-9_]*`."""
    while argv and ENV_ASSIGN_RE.match(argv[0]):
        argv = argv[1:]
    return argv


def strip_wrapper(argv: List[str]) -> List[str]:
    """Peel a single leading wrapper (sudo/command/exec/builtin/
    time) and any env assignments that follow it. Only one wrapper
    level is peeled -- matches the bash behavior."""
    if argv and argv[0] in WRAPPERS:
        argv = argv[1:]
        argv = strip_env_assignments(argv)
    return argv


def normalize_argv0_basename(argv0: str) -> str:
    """Return the basename of argv[0] after stripping any leading
    `\\` or `/` characters that the shell would have discarded
    before exec. `\\tap` -> `tap`, `/usr/bin/tap` -> `tap`."""
    s = argv0.lstrip("\\/")
    return os.path.basename(s)


def check_segment(segment: str, depth: int = 0) -> None:
    """Inspect one pipeline segment. Calls `deny()` (which exits)
    if the segment invokes a denied CLI; returns normally
    otherwise.

    `depth` caps shell-recursion: only one level of `bash -c "..."`
    re-parsing is performed, matching the bash original.
    """
    text = segment.strip()
    if not text:
        return

    # Tokenize via shlex in POSIX mode. shlex.split raises
    # ValueError on unbalanced quotes; treat that as "cannot
    # confidently parse" and allow -- consistent with the bash
    # form's textual approach.
    try:
        argv = shlex.split(text, posix=True)
    except ValueError:
        return
    if not argv:
        return

    argv = strip_env_assignments(argv)
    if not argv:
        return
    argv = strip_wrapper(argv)
    if not argv:
        return

    base = normalize_argv0_basename(argv[0])

    # `bash -c "..."` / `sh -c "..."` / `zsh -c "..."` / `dash -c
    # "..."`: recurse one level into the quoted command. shlex has
    # already stripped the surrounding quotes from argv[2].
    if base in SHELLS and len(argv) >= 3 and argv[1] == "-c" and depth < 1:
        inner = argv[2]
        for inner_seg in split_segments(inner):
            check_segment(inner_seg, depth=depth + 1)
        return

    if not DENY_BASENAME_RE.match(base):
        return

    # argv[0] is `tap` or `keg`. Consult the allowlist on argv[1].
    argv1 = argv[1] if len(argv) >= 2 else ""
    if argv1 in ALLOWLIST:
        return
    deny()


def main() -> None:
    raw = sys.stdin.read()
    if not raw:
        hook_error("empty stdin")
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError as exc:
        hook_error("could not parse JSON payload: " + str(exc))
        return  # unreachable; appeases type checkers

    # Missing/non-string fields collapse to empty -- treat that as
    # allow (nothing to match against). Mirrors the bash form's
    # `// empty` jq fallback.
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict):
        return
    cmd = tool_input.get("command")
    if not isinstance(cmd, str) or not cmd:
        return

    for seg in split_segments(cmd):
        check_segment(seg)


if __name__ == "__main__":
    main()
