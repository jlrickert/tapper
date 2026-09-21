// Tapper guard for opencode.
//
// Claude and Codex enforce the guard through a JSON hook table that runs
// `tap hook pre-tool-use`. opencode has no such table, but its plugins can
// block a call from `tool.execute.before` by throwing, so this shells out to
// the same binary: one implementation of the policy across all three hosts.
//
// The module imports nothing on purpose. opencode resolves plugin imports
// against a package.json in its config directory, and a guard that stops
// loading because @opencode-ai/plugin is absent is a guard that silently
// stops guarding.
//
// It fails closed. Anything that prevents a verdict — tap missing from PATH,
// a non-zero exit, output that will not parse — blocks the call. An agent that
// can disable the guard by breaking it is not a guard.

const HOOK_COMMAND = "tap hook pre-tool-use";

export const TapperGuard = async ({ $ }: { $: any }) => ({
  "tool.execute.before": async (
    input: { tool: string },
    output: { args: Record<string, unknown> },
  ) => {
    const payload = new TextEncoder().encode(
      JSON.stringify({
        hook_event_name: "PreToolUse",
        tool_name: input.tool,
        tool_input: output.args,
      }),
    );

    let stdout: string;
    try {
      const result = await $`tap hook pre-tool-use < ${payload}`.quiet().nothrow();
      if (result.exitCode !== 0) {
        const detail = result.stderr.toString().trim();
        throw new Error(detail || `exit status ${result.exitCode}`);
      }
      stdout = result.stdout.toString().trim();
    } catch (err) {
      throw new Error(
        `Tapper guard could not reach \`${HOOK_COMMAND}\`: ${err}. Blocking this call. ` +
          `Repair the tap binary on PATH, or run \`tap integrate opencode --no-safety\` to remove the guard.`,
      );
    }

    // Silence is the allow verdict: tap writes a payload only to deny.
    if (stdout === "") {
      return;
    }

    let decision: { permissionDecision?: string; permissionDecisionReason?: string };
    try {
      decision = JSON.parse(stdout).hookSpecificOutput ?? {};
    } catch (err) {
      throw new Error(
        `Tapper guard could not read the verdict from \`${HOOK_COMMAND}\`: ${err}. Blocking this call.`,
      );
    }
    if (decision.permissionDecision === "deny") {
      throw new Error(decision.permissionDecisionReason ?? "Tapper guard denied this call.");
    }
  },
});
