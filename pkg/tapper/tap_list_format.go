package tapper

import (
	"fmt"
	"strings"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
)

// defaultListFormat is the format used when none is supplied. It is
// tab-separated so listing output stays machine-readable by default.
const defaultListFormat = "%i\t%d\t%t"

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

// nodeFieldSource carries everything needed to expand a compiled format for
// one node. meta and stats are nil unless the format required them.
type nodeFieldSource struct {
	entry keg.NodeIndexEntry
	meta  *keg.NodeMeta
	stats *keg.NodeStats
}

// fieldValue resolves one selector against a node.
//
// Intrinsics and index timestamps come from the index entry, never from
// stats.json, so a displayed value always agrees with the same predicate in a
// query expression and the default format stays free of per-node reads.
func fieldValue(sel keg.FieldSelector, src nodeFieldSource) string {
	switch sel.Kind {
	case keg.FieldID:
		return src.entry.ID
	case keg.FieldTitle:
		return src.entry.Title
	case keg.FieldIndexTime:
		switch sel.Key {
		case "updated":
			return formatEntryTime(src.entry.Updated)
		case "created":
			return formatEntryTime(src.entry.Created)
		case "accessed":
			return formatEntryTime(src.entry.Accessed)
		}
		return ""
	case keg.FieldStat:
		value, _ := keg.StatsFieldValue(src.stats, sel.Key)
		return value
	case keg.FieldTags, keg.FieldMetaKey:
		if src.meta == nil {
			return ""
		}
		// An absent key, a non-scalar value, and an empty tag list all
		// collapse to empty here. That keeps a tabular format's column count
		// stable regardless of which nodes carry the key.
		value, _ := src.meta.Get(sel.Key)
		return value
	}
	return ""
}

func formatEntryTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
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
