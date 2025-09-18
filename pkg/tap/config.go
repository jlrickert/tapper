package tap

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	std "github.com/jlrickert/go-std/pkg"
)

// LocalGitData attempts to run `git -C repoRoot config --local --get key`.
// If git isn't present or the command fails it returns an error.
func LocalGitData(ctx context.Context, projectPath, key string) ([]byte, error) {
	lg := std.LoggerFromContext(ctx)
	// check git exists
	if _, err := exec.LookPath("git"); err != nil {
		lg.Warn("git executable not found", "projectPath", projectPath, "err", err)
		return []byte{}, fmt.Errorf("git not available: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", projectPath, "config", "--local", "--get", key)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		lg.Error("local git data not read", "projectPath", projectPath, "err", err)
		return []byte{}, fmt.Errorf("local git data not read: %w", err)
	}
	data := bytes.TrimSpace(out.Bytes())
	lg.Debug("git data read", "projectPath", projectPath, "data", data)
	return bytes.TrimSpace(out.Bytes()), nil
}
