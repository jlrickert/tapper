package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

type EditOptions struct {
	// NodeID is the node identifier to edit (e.g., "0", "42")
	NodeID string

	KegTargetOptions

	// LockToken is an optional cross-process lock token. When provided, the
	// command validates it against any held lock before proceeding.
	LockToken string
	Schema    string

	// Stream carries stdin piping information.
	Stream *toolkit.Stream
}

// MetaOptions configures behavior for Tap.Meta.
type MetaOptions struct {
	// NodeID is the node identifier to inspect (e.g., "0", "42")
	NodeID string

	KegTargetOptions

	// LockToken is an optional cross-process lock token. When provided, the
	// command validates it against any held lock before proceeding.
	LockToken string
	Schema    string

	// Edit opens metadata in the editor.
	Edit bool

	// Stream carries stdin piping information.
	Stream *toolkit.Stream
}

// Cat reads and displays node(s) content with metadata as frontmatter.
//

func (t *Tap) Meta(ctx context.Context, opts MetaOptions) (string, error) {
	// Meta only writes when editing or consuming piped input; a plain
	// read must stay available under a viewer-cap flight cover.
	role := FlightRoleViewer
	if opts.Edit || (opts.Stream != nil && opts.Stream.IsPiped) {
		role = FlightRoleEditor
	}
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, role)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	if role == FlightRoleEditor {
		ctx = keg.WithDefaultValidationActor(ctx, keg.ValidationActorHuman)
	}

	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return "", err
	}

	exists, err := t.nodeExistsWithContent(ctx, k, id)
	if err != nil {
		return "", fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("node %s not found in %s", id.Path(), describeKeg(k))
	}

	if opts.Edit {
		if err := validateLockToken(ctx, k, id, opts.LockToken); err != nil {
			return "", err
		}
		if err := t.editMetaSchemaLocked(ctx, k, id, opts.Schema, keg.LockToken(opts.LockToken), opts.Stream); err != nil {
			return "", err
		}
		return "", nil
	}

	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return "", fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			if err := validateLockToken(ctx, k, id, opts.LockToken); err != nil {
				return "", err
			}
			metaNode, parseErr := keg.ParseMeta(ctx, pipedRaw)
			if parseErr != nil {
				return "", fmt.Errorf("metadata from stdin is invalid: %w", parseErr)
			}
			results, err := k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: id, Schema: opts.Schema, Meta: []byte(metaNode.ToYAML()), HasMeta: true, LockToken: keg.LockToken(opts.LockToken)}})
			if err != nil {
				return "", fmt.Errorf("unable to save node metadata: %w", err)
			}
			if len(results) > 0 {
				t.warnSchemaValidation(results[0].Validation, id, opts.Stream)
			}
			return "", nil
		}
	}

	raw, err := k.GetMetaRaw(ctx, id)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return "", fmt.Errorf("unable to read node metadata: %w", err)
	}
	metaNode, err := keg.ParseMeta(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("node metadata is invalid: %w", err)
	}
	return strings.TrimRight(metaNode.ToYAML(), "\n"), nil
}

// Edit opens a node in an editor. When the repository is an FsRepo, the real
// README.md is opened directly for in-place editing. Otherwise a temporary
// file with frontmatter is used and changes are split back on save.
//
// The temp file format (non-FsRepo) is:
//
//	---
//	<meta yaml>
//	---
//	<markdown body>
//
// If stdin is piped, it seeds the content directly without opening an editor.
func (t *Tap) Edit(ctx context.Context, opts EditOptions) error {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}
	ctx = keg.WithDefaultValidationActor(ctx, keg.ValidationActorHuman)

	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return err
	}

	// Handle piped stdin: apply content directly without opening an editor.
	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			view, err := k.OpenNode(ctx, keg.NodeOpenOptions{ID: id, LockToken: keg.LockToken(opts.LockToken)})
			if err != nil {
				if errors.Is(err, keg.ErrNotExist) {
					return fmt.Errorf("node %s not found: %w", id.Path(), err)
				}
				return fmt.Errorf("unable to open node: %w", err)
			}
			expectedHash := ""
			if view.Stats != nil {
				expectedHash = view.Stats.Hash()
			}
			_, err = t.applyEditedNodeRawExpectedSchema(ctx, k, id, pipedRaw, keg.LockToken(opts.LockToken), expectedHash, opts.Schema)
			return err
		}
	}

	return t.editWithTempFileLockedSchema(ctx, k, id, keg.LockToken(opts.LockToken), opts.Schema)
}

// editWithTempFile is the editing flow that composes frontmatter + body into
// a temporary file. When the repository is an FsRepo, a reverse sync watcher
// monitors the real node files (README.md, meta.yaml) and re-composes the
// temp file when external changes are detected, so the editor can reload
// with :e! to pick up changes from other tap instances.
func (t *Tap) editWithTempFile(ctx context.Context, k keg.Keg, id keg.NodeId) error {
	return t.editWithTempFileSchema(ctx, k, id, "")
}

func (t *Tap) editWithTempFileLocked(ctx context.Context, k keg.Keg, id keg.NodeId, lockToken keg.LockToken) error {
	return t.editWithTempFileLockedSchema(ctx, k, id, lockToken, "")
}

func (t *Tap) editWithTempFileSchema(ctx context.Context, k keg.Keg, id keg.NodeId, schema string) error {
	return t.editWithTempFileLockedSchema(ctx, k, id, "", schema)
}

func (t *Tap) editWithTempFileLockedSchema(ctx context.Context, k keg.Keg, id keg.NodeId, lockToken keg.LockToken, schema string) error {
	view, err := k.OpenNode(ctx, keg.NodeOpenOptions{ID: id, Touch: true, LockToken: lockToken})
	if err != nil {
		return fmt.Errorf("unable to open node: %w", err)
	}
	content, meta := view.Content, view.Meta
	revision := &editRevision{}
	if view.Stats != nil {
		revision.set(view.Stats.Hash())
	}

	initialRaw, err := composeEditNodeFileWithSchema(ctx, meta, content, schema)
	if err != nil {
		return err
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, editorTempFilePrefix(k, id, "edit"), ".md")
	if err != nil {
		return fmt.Errorf("unable to create temp file path: %w", err)
	}
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return fmt.Errorf("unable to write temp edit file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	editCtx, editCancel := context.WithCancel(ctx)
	defer editCancel()

	// wg tracks background goroutines so we can wait for them to drain
	// before returning. This prevents silent resource leaks if the editor
	// exits before the goroutines finish processing events.
	var wg sync.WaitGroup

	// Start reverse sync: watch the backing repository and update the temp
	// file when another client changes the node. The watch is scoped to
	// editCtx — cancelling it closes the event channel and unblocks
	// reverseSync. external marks reverse-sync writes so the live-save
	// watcher does not push them back as an echo.
	external := &externalWrites{}
	if ch, chErr := k.Watch(editCtx, id); chErr == nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reverseSync(editCtx, t.Runtime, k, id, tempPath, external, revision, ch)
		}()
	} else if lg := t.Runtime.Logger(); lg != nil {
		lg.Debug("edit: live reverse sync unavailable",
			"node", id.String(), "error", chErr)
	}

	editErr := editWithLiveSaves(editCtx, t.Runtime, tempPath, external, func(editedRaw []byte) error {
		newHash, err := t.applyEditedNodeRawExpectedSchema(ctx, k, id, editedRaw, lockToken, revision.get(), schema)
		if err == nil {
			revision.set(newHash)
		}
		return err
	})

	// Cancel the edit context to signal goroutines, then wait for them
	// to drain before returning.
	editCancel()
	wg.Wait()

	if editErr != nil {
		return fmt.Errorf("unable to edit node: %w", editErr)
	}
	return nil
}

// composeCurrentNodeFile reads the node's current content and meta from the
// repository and composes the edit-file representation. ok is false when the
// reads fail (e.g. the node vanished mid-edit).
func composeCurrentNodeFile(ctx context.Context, k keg.Keg, id keg.NodeId) ([]byte, string, bool) {
	view, err := k.OpenNode(ctx, keg.NodeOpenOptions{ID: id})
	if err != nil {
		return nil, "", false
	}
	hash := ""
	if view.Stats != nil {
		hash = view.Stats.Hash()
	}
	return composeEditNodeFile(ctx, view.Meta, view.Content), hash, true
}

type editRevision struct {
	mu   sync.Mutex
	hash string
}

func (r *editRevision) get() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hash
}

func (r *editRevision) set(hash string) {
	r.mu.Lock()
	r.hash = hash
	r.mu.Unlock()
}

// tempMatchesComposed reports whether the temp file already reflects the
// composed repository state — byte-for-byte, or semantically modulo meta
// serialization and trailing-newline drift. A match means the repository
// change was caused by our own save, so the editor should not be disturbed.
func tempMatchesComposed(ctx context.Context, rt *toolkit.Runtime, tempPath string, composed []byte) bool {
	current, err := rt.ReadFile(tempPath)
	if err != nil {
		return false
	}
	return bytes.Equal(current, composed) || editFilesEquivalent(ctx, current, composed)
}

// reverseSync watches for real node file changes and re-composes the temp
// file so the editor can reload with :e! to pick up external modifications.
// It compares the composed content against the current temp file to avoid
// writing when our own saves caused the real file change.
func reverseSync(
	ctx context.Context,
	rt *toolkit.Runtime,
	k keg.Keg,
	id keg.NodeId,
	tempPath string,
	external *externalWrites,
	revision *editRevision,
	ch <-chan keg.NodeEvent,
) {
	errOut := rt.Stream().Err
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// Only sync on content or meta modifications, not access events.
			if ev.Kind == keg.NodeEventAccessed {
				continue
			}
			if ev.Kind == keg.NodeEventDeleted {
				_, _ = fmt.Fprintf(errOut,
					"Info: node %s was removed externally — stop editing or save to recreate it\n",
					id.Path())
				continue
			}
			if ev.Field != "content" && ev.Field != "meta" {
				continue
			}
			composed, hash, ok := composeCurrentNodeFile(ctx, k, id)
			if !ok {
				continue
			}
			revision.set(hash)
			if tempMatchesComposed(ctx, rt, tempPath, composed) {
				continue
			}

			// Record the hash before writing so the live-save watcher
			// cannot observe the file ahead of the record.
			external.note(composed)
			if writeErr := rt.WriteFile(tempPath, composed, 0o600); writeErr != nil {
				_, _ = fmt.Fprintf(errOut,
					"Warning: failed to sync external change to temp file: %v\n", writeErr)
				continue
			}
			_, _ = fmt.Fprintf(errOut,
				"Info: node %s updated externally (%s) — reload with :e! to see changes\n",
				id.Path(), ev.Field)
		}
	}
}

func (t *Tap) applyEditedNodeRaw(ctx context.Context, k keg.Keg, id keg.NodeId, editedRaw []byte) error {
	return t.applyEditedNodeRawWithLock(ctx, k, id, editedRaw, "")
}

func (t *Tap) applyEditedNodeRawWithLock(ctx context.Context, k keg.Keg, id keg.NodeId, editedRaw []byte, lockToken keg.LockToken) error {
	view, err := k.OpenNode(ctx, keg.NodeOpenOptions{ID: id, LockToken: lockToken})
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return fmt.Errorf("node %s not found: %w", id.Path(), err)
		}
		return fmt.Errorf("unable to open node: %w", err)
	}
	expectedHash := ""
	if view.Stats != nil {
		expectedHash = view.Stats.Hash()
	}
	_, err = t.applyEditedNodeRawExpected(ctx, k, id, editedRaw, lockToken, expectedHash)
	return err
}

func (t *Tap) applyEditedNodeRawExpected(ctx context.Context, k keg.Keg, id keg.NodeId, editedRaw []byte, lockToken keg.LockToken, expectedHash string) (string, error) {
	return t.applyEditedNodeRawExpectedSchema(ctx, k, id, editedRaw, lockToken, expectedHash, "")
}

func (t *Tap) applyEditedNodeRawExpectedSchema(ctx context.Context, k keg.Keg, id keg.NodeId, editedRaw []byte, lockToken keg.LockToken, expectedHash, schema string) (string, error) {
	hasFrontmatter, frontmatterRaw, bodyRaw, err := splitEditNodeFile(editedRaw)
	if err != nil {
		return "", err
	}

	if hasFrontmatter {
		if _, parseErr := keg.ParseMeta(ctx, frontmatterRaw); parseErr != nil {
			return "", fmt.Errorf("invalid frontmatter metadata: %w", parseErr)
		}
	}
	result, err := k.UpdateNode(ctx, keg.NodeUpdateOptions{ID: id, Schema: schema, Content: bodyRaw, Meta: frontmatterRaw, HasMeta: hasFrontmatter, LockToken: lockToken, ExpectedHash: expectedHash})
	if err != nil {
		return "", fmt.Errorf("unable to save node: %w", err)
	}
	t.warnSchemaValidation(result.Validation, id, t.Runtime.Stream())
	return result.Hash, nil
}

// normalizeMetaYAML renders raw repository metadata as block-style YAML.
// Hub-backed repositories store metadata as JSON (Postgres JSONB), so the
// raw bytes may be `{}` or `{"tags":["test"]}`; JSON is a YAML subset, so a
// parse + re-emit round trip yields YAML regardless of backend. All fields
// are preserved — this is a formatting pass, not the field-filtering
// serialization NodeMeta.ToYAML performs. Empty metadata yields "" rather
// than `{}`; unparseable bytes are returned trimmed but otherwise unchanged.
func normalizeMetaYAML(ctx context.Context, raw []byte) string {
	_ = ctx
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return strings.TrimRight(string(raw), "\n")
	}
	// Clear flow style recursively so JSON input re-renders as block YAML;
	// the emitter re-adds any quoting a scalar actually requires.
	clearYAMLStyle(&doc)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return strings.TrimRight(string(raw), "\n")
	}
	_ = enc.Close()

	out := strings.TrimRight(buf.String(), "\n")
	if out == "{}" || out == "null" {
		return ""
	}
	return out
}

// clearYAMLStyle recursively resets node styles so the emitter chooses
// block style instead of preserving JSON/flow formatting from the source.
func clearYAMLStyle(n *yaml.Node) {
	if n == nil {
		return
	}
	n.Style = 0
	for _, c := range n.Content {
		clearYAMLStyle(c)
	}
}

func composeEditNodeFile(ctx context.Context, meta []byte, content []byte) []byte {
	metaText := normalizeMetaYAML(ctx, meta)
	return []byte(fmt.Sprintf("---\n%s\n---\n%s", metaText, string(content)))
}

func composeEditNodeFileWithSchema(ctx context.Context, meta []byte, content []byte, schema string) ([]byte, error) {
	if strings.TrimSpace(schema) == "" {
		return composeEditNodeFile(ctx, meta, content), nil
	}
	parsed, err := keg.ParseMeta(ctx, meta)
	if err != nil {
		return nil, fmt.Errorf("node metadata is invalid: %w", err)
	}
	if err := parsed.Set(ctx, "type", strings.TrimSpace(schema)); err != nil {
		return nil, err
	}
	return composeEditNodeFile(ctx, []byte(parsed.ToYAML()), content), nil
}

// editFilesEquivalent reports whether two composed edit files describe the
// same node state, ignoring metadata serialization differences and trailing
// newline drift in the body. reverseSync uses it to recognize the echo of
// the user's own save (which round-trips through the repository's storage
// encoding) and skip the rewrite + notification.
func editFilesEquivalent(ctx context.Context, a, b []byte) bool {
	_, aMeta, aBody, aErr := splitEditNodeFile(a)
	_, bMeta, bBody, bErr := splitEditNodeFile(b)
	if aErr != nil || bErr != nil {
		return false
	}
	if !bytes.Equal(bytes.TrimRight(aBody, "\r\n"), bytes.TrimRight(bBody, "\r\n")) {
		return false
	}
	return normalizeMetaYAML(ctx, aMeta) == normalizeMetaYAML(ctx, bMeta)
}

func splitEditNodeFile(raw []byte) (bool, []byte, []byte, error) {
	if len(raw) == 0 {
		return false, nil, raw, nil
	}

	trimmed := raw
	if bytes.HasPrefix(trimmed, []byte("\xef\xbb\xbf")) {
		trimmed = trimmed[3:]
	}

	var rest []byte
	switch {
	case bytes.HasPrefix(trimmed, []byte("---\n")):
		rest = trimmed[len([]byte("---\n")):]
	case bytes.HasPrefix(trimmed, []byte("---\r\n")):
		rest = trimmed[len([]byte("---\r\n")):]
	default:
		return false, nil, raw, nil
	}

	choices := [][]byte{
		[]byte("\n---\r\n"),
		[]byte("\n---\n"),
		[]byte("\r\n---\n"),
		[]byte("\n---"),
	}
	endIdx := -1
	endLen := 0
	for _, marker := range choices {
		if idx := bytes.Index(rest, marker); idx >= 0 {
			endIdx = idx
			endLen = len(marker)
			break
		}
	}
	if endIdx < 0 {
		return false, nil, nil, fmt.Errorf("invalid frontmatter: missing closing delimiter")
	}

	frontmatter := bytes.TrimSpace(rest[:endIdx])
	if len(frontmatter) > 0 {
		var check map[string]any
		if err := yaml.Unmarshal(frontmatter, &check); err != nil {
			return false, nil, nil, fmt.Errorf("invalid frontmatter yaml: %w", err)
		}
	}

	body := bytes.TrimLeft(rest[endIdx+endLen:], "\r\n")
	return true, frontmatter, body, nil
}

func (t *Tap) editMeta(ctx context.Context, k keg.Keg, id keg.NodeId, stream *toolkit.Stream) error {
	return t.editMetaSchemaLocked(ctx, k, id, "", "", stream)
}

func (t *Tap) editMetaSchema(ctx context.Context, k keg.Keg, id keg.NodeId, schema string, stream *toolkit.Stream) error {
	return t.editMetaSchemaLocked(ctx, k, id, schema, "", stream)
}

func (t *Tap) editMetaSchemaLocked(ctx context.Context, k keg.Keg, id keg.NodeId, schema string, lockToken keg.LockToken, stream *toolkit.Stream) error {
	raw, err := k.GetMetaRaw(ctx, id)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return fmt.Errorf("unable to read node metadata: %w", err)
	}

	metaNode, err := keg.ParseMeta(ctx, raw)
	if err != nil {
		return fmt.Errorf("node metadata is invalid: %w", err)
	}
	initialRaw := []byte(metaNode.ToYAML())
	if strings.TrimSpace(schema) != "" {
		if err := metaNode.Set(ctx, "type", strings.TrimSpace(schema)); err != nil {
			return err
		}
		initialRaw = []byte(metaNode.ToYAML())
	}
	if stream != nil && stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			initialRaw = pipedRaw
		}
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, editorTempFilePrefix(k, id, "meta"), ".yaml")
	if err != nil {
		return fmt.Errorf("unable to create temp file path: %w", err)
	}
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return fmt.Errorf("unable to write temp metadata file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	if err := editWithLiveSaves(ctx, t.Runtime, tempPath, nil, func(editedRaw []byte) error {
		updatedMeta, err := keg.ParseMeta(ctx, editedRaw)
		if err != nil {
			return fmt.Errorf("node metadata is invalid after editing: %w", err)
		}
		results, err := k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: id, Schema: schema, Meta: []byte(updatedMeta.ToYAML()), HasMeta: true, LockToken: lockToken}})
		if err != nil {
			return fmt.Errorf("unable to save node metadata: %w", err)
		}
		if len(results) > 0 {
			t.warnSchemaValidation(results[0].Validation, id, t.Runtime.Stream())
		}
		return nil
	}); err != nil {
		return fmt.Errorf("unable to edit node metadata: %w", err)
	}
	return nil
}

func editorTempFilePrefix(k keg.Keg, id keg.NodeId, action string) string {
	namespace, kegName := logicalKegTempNameParts(k)
	return fmt.Sprintf("tap-%s-%s-%s-%s-",
		sanitizeEditorTempSegment(action, "edit"),
		sanitizeEditorTempSegment(namespace, "unknown"),
		sanitizeEditorTempSegment(kegName, "keg"),
		sanitizeEditorTempSegment(id.PathNumeric(), "node"))
}

func logicalKegTempNameParts(k keg.Keg) (string, string) {
	if k == nil || k.Target() == nil {
		return "unknown", "keg"
	}

	namespace := strings.TrimSpace(k.Target().Namespace)
	kegName := strings.TrimSpace(k.Target().KegName)
	if namespace != "" && kegName != "" {
		return namespace, kegName
	}
	if namespace != "" {
		return namespace, "keg"
	}
	if kegName != "" {
		return "local", kegName
	}
	if strings.TrimSpace(k.Target().File) != "" {
		return "local", "keg"
	}
	return "unknown", "keg"
}

func sanitizeEditorTempSegment(value string, fallback string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	lastDash := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-' {
			b.WriteByte(c)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	clean := strings.Trim(b.String(), ".-_")
	if clean == "" {
		return fallback
	}
	return clean
}
