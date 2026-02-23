package tapper

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jlrickert/cli-toolkit/toolkit"
)

// editWithLiveSaves runs the user's editor and invokes onSave whenever the
// edited file is saved with changed content.
func editWithLiveSaves(ctx context.Context, rt *toolkit.Runtime, path string, onSave func([]byte) error) error {
	if rt == nil {
		return fmt.Errorf("runtime is required")
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty filepath")
	}
	if onSave == nil {
		return fmt.Errorf("save callback is required")
	}

	resolvedPath, err := rt.ResolvePath(path, true)
	if err != nil {
		return fmt.Errorf("resolve edit path: %w", err)
	}
	editorPath := resolvedPath
	if jail := strings.TrimSpace(rt.GetJail()); jail != "" {
		trimmed := strings.TrimPrefix(resolvedPath, string(filepath.Separator))
		editorPath = filepath.Join(jail, trimmed)
	}

	editor := strings.TrimSpace(rt.Get("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(rt.Get("EDITOR"))
	}
	if editor == "" {
		editor = toolkit.DefaultEditor
	}

	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return fmt.Errorf("invalid editor command %q", editor)
	}

	cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], editorPath)...)
	stream := rt.Stream()
	cmd.Stdin = stream.In
	cmd.Stdout = stream.Out
	cmd.Stderr = stream.Err
	cmd.Env = rt.Environ()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watch edit file: %w", err)
	}
	defer func() {
		_ = watcher.Close()
	}()

	watchDir := filepath.Dir(editorPath)
	if err := watcher.Add(watchDir); err != nil {
		return fmt.Errorf("watch edit directory: %w", err)
	}

	var (
		hasHash      bool
		lastHash     [sha256.Size]byte
		attempted    bool
		applied      bool
		lastApplyErr error
	)

	if initial, err := os.ReadFile(editorPath); err == nil {
		lastHash = sha256.Sum256(initial)
		hasHash = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read edit file: %w", err)
	}

	process := func() {
		raw, err := os.ReadFile(editorPath)
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			attempted = true
			lastApplyErr = fmt.Errorf("unable to read edited file: %w", err)
			_, _ = fmt.Fprintf(stream.Err, "Warning: %v\n", lastApplyErr)
			return
		}

		sum := sha256.Sum256(raw)
		if hasHash && sum == lastHash {
			return
		}
		lastHash = sum
		hasHash = true
		attempted = true

		if err := onSave(raw); err != nil {
			lastApplyErr = err
			_, _ = fmt.Fprintf(stream.Err, "Warning: %v\n", err)
			return
		}
		applied = true
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("running editor %q: %w", editor, err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var (
		pending     bool
		pendingFrom time.Time
	)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if pending && time.Since(pendingFrom) >= 120*time.Millisecond {
				process()
				pending = false
			}
		case event, ok := <-watcher.Events:
			if !ok {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Chmod|fsnotify.Remove) != 0 {
				pending = true
				pendingFrom = time.Now()
			}
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				continue
			}
			_, _ = fmt.Fprintf(stream.Err, "Warning: editor file watcher error: %v\n", watchErr)
		case err := <-done:
			process()
			if err != nil {
				return fmt.Errorf("running editor %q: %w", editor, err)
			}
			if attempted && !applied && lastApplyErr != nil {
				return lastApplyErr
			}
			return nil
		case <-ctx.Done():
			err := <-done
			if err != nil {
				return fmt.Errorf("running editor %q: %w", editor, err)
			}
			return ctx.Err()
		}
	}
}
