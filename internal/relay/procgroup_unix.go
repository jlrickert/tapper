//go:build unix

package relay

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts a stdio MCP server in its own process group, so
// the relay can stop everything it spawned (npx and the node it runs, say)
// and a Ctrl-C meant for the relay reaches the server only through it.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kills whatever is left of the server's process group
// after its session closed.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
