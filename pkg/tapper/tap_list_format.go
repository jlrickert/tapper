package tapper

import (
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// defaultListFormat is the format used when none is supplied. It is
// tab-separated so listing output stays machine-readable by default.
const defaultListFormat = "%i\t%d\t%t"

// formatEscapes are the backslash escapes a format string may contain. They
// exist because a shell does not expand "\t" inside double quotes, so the
// separator most listings want is otherwise impossible to type without
// resorting to $'...' quoting.
var formatEscapes = map[byte]byte{
	't':  '\t',
	'n':  '\n',
	'r':  '\r',
	'\\': '\\',
}

// formatSegment is one piece of a compiled format: either literal text, or a
// field selector to expand per node.
type formatSegment struct {
	literal string
	sel     keg.FieldSelector
	isField bool
}

// compiledFormat is a format string parsed once, ahead of any I/O. needsMeta
// and needsStats report whether expanding it requires per-node reads, which
// lets the renderer skip enrichment entirely for the common formats.
type compiledFormat struct {
	segments   []formatSegment
	needsMeta  bool
	needsStats bool
}

// compileListFormat parses a listing format into segments.
//
// The scanner is a single left-to-right pass rather than a chain of
// replacements. That is what makes expansion safe: an expanded value is
// appended to the output and never rescanned, so a node whose title contains
// "%c" renders literally instead of being expanded again.
//
// Escaping is deliberately asymmetric. "%{" is a new introducer with no
// legacy meaning, so a malformed one is an error and typos surface
// immediately. A bare "%" followed by anything else passes through as
// literal text, matching the historical behaviour, so no format string that
// works today starts failing.
func compileListFormat(format string) (compiledFormat, error) {
	if format == "" {
		format = defaultListFormat
	}

	var out compiledFormat
	var lit strings.Builder

	flush := func() {
		if lit.Len() > 0 {
			out.segments = append(out.segments, formatSegment{literal: lit.String()})
			lit.Reset()
		}
	}
	appendField := func(sel keg.FieldSelector) {
		flush()
		out.segments = append(out.segments, formatSegment{sel: sel, isField: true})
		if sel.NeedsMeta() {
			out.needsMeta = true
		}
		if sel.NeedsStats() {
			out.needsStats = true
		}
	}

	for i := 0; i < len(format); {
		c := format[i]

		// Interpret backslash escapes. A shell's double quotes do not expand
		// "\t", so without this the documented default format "%i\t%d\t%t"
		// cannot be typed at a prompt: it arrives as a literal backslash and
		// a t. Unknown escapes pass through untouched, mirroring the rule for
		// unknown percent verbs.
		if c == '\\' && i+1 < len(format) {
			if escaped, ok := formatEscapes[format[i+1]]; ok {
				lit.WriteByte(escaped)
				i += 2
				continue
			}
			lit.WriteByte('\\')
			lit.WriteByte(format[i+1])
			i += 2
			continue
		}

		if c != '%' {
			lit.WriteByte(c)
			i++
			continue
		}
		// A trailing '%' is literal.
		if i+1 >= len(format) {
			lit.WriteByte('%')
			i++
			continue
		}

		switch next := format[i+1]; {
		case next == '%':
			lit.WriteByte('%')
			i += 2

		case next == '{':
			end := strings.IndexByte(format[i+2:], '}')
			if end < 0 {
				return compiledFormat{}, fmt.Errorf("invalid format: unterminated %q", "%{")
			}
			inner := format[i+2 : i+2+end]
			sel, err := keg.ParseFieldSelector(inner)
			if err != nil {
				return compiledFormat{}, fmt.Errorf("invalid format: %w", err)
			}
			appendField(sel)
			i += 2 + end + 1

		default:
			name, ok := keg.LegacyFormatVerbs[next]
			if !ok {
				// Unknown verb: pass both bytes through untouched.
				lit.WriteByte('%')
				lit.WriteByte(next)
				i += 2
				continue
			}
			sel, err := keg.ParseFieldSelector(name)
			if err != nil {
				return compiledFormat{}, fmt.Errorf("invalid format: %w", err)
			}
			appendField(sel)
			i += 2
		}
	}
	flush()

	return out, nil
}

// selectorTexts returns the distinct field selectors this format needs the
// server to resolve. Intrinsics and index timestamps are omitted: they come
// from the index entry that every listing already carries, so asking for them
// would make the server do work the client can do for free.
func (c compiledFormat) selectorTexts(idOnly bool) []string {
	if idOnly {
		return nil
	}
	seen := make(map[string]struct{}, len(c.segments))
	out := make([]string, 0, len(c.segments))
	for _, seg := range c.segments {
		if !seg.isField {
			continue
		}
		if !seg.sel.NeedsMeta() && !seg.sel.NeedsStats() {
			continue
		}
		if _, dup := seen[seg.sel.Text]; dup {
			continue
		}
		seen[seg.sel.Text] = struct{}{}
		out = append(out, seg.sel.Text)
	}
	return out
}

// renderListView formats rows the server already resolved. No I/O happens
// here: every field value the format names is either on the index entry or in
// the row's resolved map.
func renderListView(compiled compiledFormat, rows []keg.ListViewRow, opts renderOptions) []string {
	lines := make([]string, 0, len(rows))
	start, end, step := iterationBounds(len(rows), opts.Reverse)
	for i := start; i != end; i += step {
		if opts.IdOnly {
			lines = append(lines, rows[i].Entry.ID)
			continue
		}
		lines = append(lines, expandFormat(compiled, nodeFieldSource{
			entry:    rows[i].Entry,
			resolved: rows[i].Fields,
		}))
	}
	return lines
}

// nodeFieldSource carries everything needed to expand a compiled format for
// one node.
//
// Values resolved by the server arrive already rendered in `resolved`, keyed by
// selector text. meta and stats are only populated on the fallback path, where
// the client had to read them itself.
type nodeFieldSource struct {
	entry    keg.NodeIndexEntry
	resolved map[string]string
	meta     *keg.NodeMeta
	stats    *keg.NodeStats
}

// fieldValue resolves one selector against a node, preferring a value the
// server already resolved. Resolution itself lives in pkg/keg so the client
// renderer and the server-side projection cannot disagree about what a
// selector means.
func fieldValue(sel keg.FieldSelector, src nodeFieldSource) string {
	if src.resolved != nil {
		if value, ok := src.resolved[sel.Text]; ok {
			return value
		}
	}
	return keg.FieldValue(sel, src.entry, src.meta, src.stats)
}

// expandFormat renders one line for a node.
func expandFormat(compiled compiledFormat, src nodeFieldSource) string {
	var b strings.Builder
	for _, seg := range compiled.segments {
		if !seg.isField {
			b.WriteString(seg.literal)
			continue
		}
		b.WriteString(sanitizeFieldValue(fieldValue(seg.sel, src)))
	}
	return b.String()
}

// sanitizeFieldValue collapses control characters in an expanded value to
// single spaces.
//
// This is required, not cosmetic: each rendered string is one output line, and
// values such as .lead or a YAML block scalar routinely contain newlines. An
// unsanitised value would silently emit extra lines and desynchronise every
// caller that counts lines. Only expanded values are sanitised — literal text
// the caller typed into the format string is left exactly as written, so an
// explicit "\t" separator survives.
func sanitizeFieldValue(value string) string {
	if strings.IndexFunc(value, isControlRune) < 0 {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	prevControl := false
	for _, r := range value {
		if isControlRune(r) {
			if !prevControl {
				b.WriteByte(' ')
			}
			prevControl = true
			continue
		}
		prevControl = false
		b.WriteRune(r)
	}
	return b.String()
}

func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f
}
