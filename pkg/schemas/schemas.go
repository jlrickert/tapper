// Package schemas owns the JSON Schemas that back tap's editor modelines.
//
// Every YAML document tap hands to $EDITOR — and every config file it
// persists — carries a `# yaml-language-server: $schema=<uri>` modeline so a
// language server can offer completion, hover, and validation. Pointing that
// modeline at the published GitHub URL means the editor resolves whatever is
// on main, which is the wrong answer for anyone running a build that is ahead
// of (or behind) main, and no answer at all offline.
//
// Instead the schemas are embedded in the binary and materialized under the
// user's data dir on demand. The modeline then points at a file:// URI whose
// contents are guaranteed to match the binary that wrote it. Materialization
// compares content rather than versions, so a schema edit is picked up by the
// next command that writes a modeline.
//
// The published URLs remain the canonical $id values inside the schema files,
// and are the fallback whenever materialization is not possible.
package schemas

import (
	"bytes"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	schemasfs "github.com/jlrickert/tapper/schemas"
)

// Schema file names, as they appear both in the embedded FS and on disk.
const (
	TapConfig           = "tap-config.json"
	FlightManifest      = "flight-manifest.json"
	KegSettings         = "keg-settings.json"
	KegSchemaDefinition = "keg-schema-definition.json"
)

// publicBase is where the schemas are published. It is the $id prefix used
// inside the schema documents themselves, and the modeline fallback when the
// embedded copy cannot be written to disk.
const publicBase = "https://raw.githubusercontent.com/jlrickert/tapper/main/schemas/"

// Published URLs for each schema. Exported so pkg/tapper and pkg/keg can keep
// their long-standing *SchemaURL constants pointing at a single definition.
const (
	TapConfigURL           = publicBase + TapConfig
	FlightManifestURL      = publicBase + FlightManifest
	KegSettingsURL         = publicBase + KegSettings
	KegSchemaDefinitionURL = publicBase + KegSchemaDefinition
)

// ModelinePrefix is the literal yaml-language-server directive. A modeline is
// this prefix followed by a schema URI, alone on its own line.
const ModelinePrefix = "# yaml-language-server: $schema="

// Names lists every embedded schema, in a stable order.
func Names() []string {
	return []string{TapConfig, FlightManifest, KegSettings, KegSchemaDefinition}
}

// PublicURL returns the published URL for a schema file name.
func PublicURL(name string) string {
	return publicBase + name
}

// Read returns the embedded bytes for a schema file name.
func Read(name string) ([]byte, error) {
	data, err := schemasfs.FS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read embedded schema %s: %w", name, err)
	}
	return data, nil
}

// Dir returns the directory the embedded schemas are materialized into:
// <user data dir>/tapper/schemas. Resolution flows through the runtime, so a
// sandboxed test gets its own directory rather than the developer's.
func Dir(rt *toolkit.Runtime) (string, error) {
	dataDir, err := toolkit.UserDataPath(rt)
	if err != nil {
		return "", fmt.Errorf("resolve user data dir: %w", err)
	}
	return filepath.Join(dataDir, "tapper", "schemas"), nil
}

// Materialize writes every embedded schema whose on-disk copy is missing or
// differs, and returns the directory holding them. Comparing content rather
// than a version stamp keeps it correct across development builds, where the
// version does not move but the schema does.
func Materialize(rt *toolkit.Runtime) (string, error) {
	dir, err := Dir(rt)
	if err != nil {
		return "", err
	}
	if err := rt.Mkdir(dir, 0o755, true); err != nil {
		return "", fmt.Errorf("create schema dir %s: %w", dir, err)
	}

	for _, name := range Names() {
		want, err := Read(name)
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, name)
		if got, err := rt.ReadFile(path); err == nil && bytes.Equal(got, want) {
			continue
		}
		if err := rt.AtomicWriteFile(path, want, 0o644); err != nil {
			return "", fmt.Errorf("write schema %s: %w", path, err)
		}
	}
	return dir, nil
}

// ModelineURI returns the URI a modeline for name should point at: a file://
// URI for the materialized copy, or the published URL when the schemas cannot
// be written (a read-only data dir, a constrained sandbox). It never fails —
// a stale modeline is a worse outcome than a remote one, and both are only
// comments.
func ModelineURI(rt *toolkit.Runtime, name string) string {
	dir, err := Materialize(rt)
	if err != nil {
		return PublicURL(name)
	}
	return FileURI(filepath.Join(dir, name))
}

// Modeline returns the complete modeline line, newline included, for name.
func Modeline(rt *toolkit.Runtime, name string) string {
	return ModelinePrefix + ModelineURI(rt, name) + "\n"
}

// FileURI converts an absolute filesystem path to a file:// URI. Windows paths
// gain the extra leading slash (file:///C:/...) and forward slashes.
func FileURI(path string) string {
	p := path
	if runtime.GOOS == "windows" {
		p = filepath.ToSlash(p)
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// HasModeline reports whether data already carries a schema modeline in its
// leading comment block. Only the comments before the first content line are
// considered — a `# yaml-language-server:` string further down is data, not a
// directive.
func HasModeline(data []byte) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		if bytes.HasPrefix(trimmed, []byte(ModelinePrefix)) {
			return true
		}
		if bytes.HasPrefix(trimmed, []byte("#")) {
			continue
		}
		return false
	}
	return false
}

// EnsureModeline prepends modeline to data unless data already carries one.
func EnsureModeline(data []byte, modeline string) []byte {
	if HasModeline(data) {
		return data
	}
	out := make([]byte, 0, len(modeline)+len(data))
	out = append(out, modeline...)
	out = append(out, data...)
	return out
}

// StripModeline removes the schema modeline from data's leading comment block,
// leaving everything else byte-for-byte. Documents that are stored rather than
// merely displayed run through this on the way in, so the modeline stays an
// editor affordance instead of becoming content: it names a path that is only
// meaningful on the machine that opened the editor.
func StripModeline(data []byte) []byte {
	if !HasModeline(data) {
		return data
	}

	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte(ModelinePrefix)) {
			return bytes.Join(append(lines[:i:i], lines[i+1:]...), []byte("\n"))
		}
		if len(trimmed) == 0 || bytes.HasPrefix(trimmed, []byte("#")) {
			continue
		}
		break
	}
	return data
}

// ReplaceModeline swaps whatever schema modeline data carries for modeline,
// prepending it when there is none. This is the choke point every write path
// runs its serialized YAML through: the serializers emit the published URL as
// a stable default, and the write path rewrites it to the local copy.
func ReplaceModeline(data []byte, modeline string) []byte {
	if !HasModeline(data) {
		return EnsureModeline(data, modeline)
	}

	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || (bytes.HasPrefix(trimmed, []byte("#")) && !bytes.HasPrefix(trimmed, []byte(ModelinePrefix))) {
			continue
		}
		if !bytes.HasPrefix(trimmed, []byte(ModelinePrefix)) {
			break
		}
		lines[i] = []byte(strings.TrimSuffix(modeline, "\n"))
		return bytes.Join(lines, []byte("\n"))
	}
	return data
}
