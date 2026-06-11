package tapper

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

// externalWrites tracks the hash of content that reverse sync wrote to the
// edit file on behalf of another client. Live-save consults it so bytes that
// originated from the repository are not pushed straight back, which would
// publish a spurious echo event for every external change.
type externalWrites struct {
	mu   sync.Mutex
	hash [sha256.Size]byte
	has  bool
}

// note records raw as the most recent externally-sourced file content. Call
// it before writing the file so the live-save watcher cannot observe the
// write ahead of the record.
func (e *externalWrites) note(raw []byte) {
	sum := sha256.Sum256(raw)
	e.mu.Lock()
	e.hash = sum
	e.has = true
	e.mu.Unlock()
}

func (e *externalWrites) matches(sum [sha256.Size]byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.has && sum == e.hash
}

// liveSaveState holds the mutable and immutable dependencies for live-save
// processing, extracted from the editWithLiveSaves closure for explicit state
// access.
type liveSaveState struct {
	// immutable
	rt       *toolkit.Runtime
	path     string
	stream   *toolkit.Stream
	onSave   func([]byte) error
	external *externalWrites

	// mutable
	hasHash      bool
	lastHash     [sha256.Size]byte
	attempted    bool
	applied      bool
	lastApplyErr error
	nodeRemoved  bool
}

func (s *liveSaveState) process() {
	// Once we detect the node was removed, stop attempting saves.
	if s.nodeRemoved {
		return
	}

	raw, err := s.rt.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		s.attempted = true
		s.lastApplyErr = fmt.Errorf("unable to read edited file: %w", err)
		_, _ = fmt.Fprintf(s.stream.Err, "Warning: %v\n", s.lastApplyErr)
		return
	}

	sum := sha256.Sum256(raw)
	if s.hasHash && sum == s.lastHash {
		return
	}
	// Skip content that reverse sync placed in the file — it already lives
	// in the repository, so saving it back would only echo the event.
	if s.external != nil && s.external.matches(sum) {
		s.lastHash = sum
		s.hasHash = true
		return
	}
	s.lastHash = sum
	s.hasHash = true
	s.attempted = true

	if err := s.onSave(raw); err != nil {
		// Detect node removal: stop further save attempts and
		// warn the user that the node was deleted externally.
		if errors.Is(err, keg.ErrNotExist) {
			s.nodeRemoved = true
			s.lastApplyErr = fmt.Errorf("node was removed while editing — save aborted: %w", err)
			_, _ = fmt.Fprintf(s.stream.Err, "Warning: %v\n", s.lastApplyErr)
			return
		}
		s.lastApplyErr = err
		_, _ = fmt.Fprintf(s.stream.Err, "Warning: %v\n", err)
		return
	}
	s.applied = true
}

// editWithLiveSaves runs the user's editor and invokes onSave whenever the
// edited file is saved with changed content. external, when non-nil, marks
// file contents written by reverse sync so they are not saved back to the
// repository.
func editWithLiveSaves(ctx context.Context, rt *toolkit.Runtime, path string, external *externalWrites, onSave func([]byte) error) error {
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

	state := &liveSaveState{
		rt:       rt,
		path:     path,
		stream:   stream,
		onSave:   onSave,
		external: external,
	}

	if initial, err := rt.ReadFile(path); err == nil {
		state.lastHash = sha256.Sum256(initial)
		state.hasHash = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read edit file: %w", err)
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
				state.process()
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
			state.process()
			if err != nil {
				return fmt.Errorf("running editor %q: %w", editor, err)
			}
			if state.attempted && !state.applied && state.lastApplyErr != nil {
				return state.lastApplyErr
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
