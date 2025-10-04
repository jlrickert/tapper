package cmd

// import (
// 	"fmt"
// 	"os"
// 	"os/exec"
// 	"strings"
// )
//
// const defaultEditor = "nano"
//
// // Edit launches the user's editor to edit the provided file path.
// // It checks $VISUAL first, then $EDITOR. If neither is set, it falls back to
// // "nano". The function attaches the current process's stdio to the editor so
// // interactive editors work as expected.
// func Edit(path string) error {
// 	if path == "" {
// 		return fmt.Errorf("empty filepath")
// 	}
//
// 	editor := os.Getenv("VISUAL")
// 	if strings.TrimSpace(editor) == "" {
// 		editor = os.Getenv("EDITOR")
// 	}
// 	if strings.TrimSpace(editor) == "" {
// 		editor = defaultEditor
// 	}
//
// 	parts := strings.Fields(editor)
// 	name := parts[0]
// 	args := append(parts[1:], path)
//
// 	cmd := exec.Command(name, args...)
// 	cmd.Stdin = os.Stdin
// 	cmd.Stdout = os.Stdout
// 	cmd.Stderr = os.Stderr
//
// 	if err := cmd.Run(); err != nil {
// 		return fmt.Errorf("running editor %q: %w", editor, err)
// 	}
// 	return nil
// }
