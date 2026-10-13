package keg

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// maxPatchBytes caps a content patch. A patch that size is already far
// larger than any note it could sensibly edit; send content instead.
const maxPatchBytes = 1 << 20

// maxPatchComparisons caps the line comparisons spent placing a patch's
// hunks, so a patch whose lines repeat throughout a large note cannot keep a
// write busy for long.
const maxPatchComparisons = 20_000_000

// ApplyUnifiedPatch applies a unified diff to base and returns the patched
// text.
//
// The patch is the hunk part of a `diff -u`: optional `diff`/`---`/`+++`
// header lines, then `@@ -a,b +c,d @@` hunks of context (" "), removed ("-")
// and added ("+") lines. A bare empty line inside a hunk counts as an empty
// context line, since editors and models often strip the single space.
//
// Content is never guessed at: each hunk's context and removed lines must
// appear in base exactly. The hunk header's line number is only a hint, so a
// hunk whose numbers are off still applies when its lines match in exactly
// one place; its counts only decide where a hunk may end, so a removed
// "-- x" line followed by an added "++ y" line is not mistaken for another
// file's header. A hunk that matches nowhere, or in more than one place none
// of which is at its stated line, is refused with ErrInvalid. Hunks apply in
// order and may not overlap.
//
// Line endings are kept: a base with CRLF line endings stays CRLF, and the
// patch's lines match base lines whatever their ending. "\ No newline at end
// of file" may only follow a hunk that reaches the end of the note.
func ApplyUnifiedPatch(base []byte, patch string) ([]byte, error) {
	if len(patch) > maxPatchBytes {
		return nil, fmt.Errorf("patch is %d bytes; the limit is %d, so send the content instead: %w", len(patch), maxPatchBytes, ErrInvalid)
	}
	hunks, err := parseUnifiedPatch(patch)
	if err != nil {
		return nil, err
	}
	lines, eofNewline := splitPatchLines(string(base))
	baseEOF := eofNewline
	// Untouched lines keep their own endings, so a mixed body stays as it
	// was. A line a hunk writes takes the ending of the line it replaces, or
	// the body's predominant ending.
	crlf := predominantCRLF(lines, eofNewline)
	ending := func(line string, fallback bool) string {
		if fallback {
			return line + "\r"
		}
		return line
	}
	// lastBase is where base's final line landed in out when base had no
	// final newline: if a line follows it now, it needs an ending.
	lastBase := -1
	keep := func(out []string, i int) []string {
		if i == len(lines)-1 && !baseEOF {
			lastBase = len(out)
		}
		return append(out, lines[i])
	}
	m := &patchMatcher{lines: lines, budget: maxPatchComparisons}
	out := make([]string, 0, len(lines))
	cursor := 0
	for i, h := range hunks {
		old := h.oldLines()
		at, err := m.locateHunk(old, cursor, h.oldStart)
		if err != nil {
			return nil, fmt.Errorf("patch hunk %d: %w", i+1, err)
		}
		if (h.oldNoNewline || h.newNoNewline) && at+len(old) != len(lines) {
			return nil, fmt.Errorf("patch hunk %d marks 'No newline at end of file' but does not reach the end of the note: %w", i+1, ErrInvalid)
		}
		for j := cursor; j < at; j++ {
			out = keep(out, j)
		}
		j := at
		// replaced is the ending of the last line this run removed, if any.
		replaced := -1
		for _, l := range h.lines {
			switch l.op {
			case ' ':
				out = keep(out, j)
				j++
				replaced = -1
			case '-':
				switch {
				case j == len(lines)-1 && !baseEOF:
					// Base's last line has no ending to pass on.
					replaced = -1
				case strings.HasSuffix(lines[j], "\r"):
					replaced = 1
				default:
					replaced = 0
				}
				j++
			case '+':
				withCR := crlf
				if replaced >= 0 {
					withCR = replaced == 1
				}
				out = append(out, ending(l.text, withCR))
			}
		}
		cursor = at + len(old)
		if h.oldNoNewline || h.newNoNewline {
			eofNewline = !h.newNoNewline
		}
	}
	for j := cursor; j < len(lines); j++ {
		out = keep(out, j)
	}
	if len(out) == 0 {
		return []byte{}, nil
	}
	if lastBase >= 0 && (lastBase < len(out)-1 || eofNewline) && crlf && !strings.HasSuffix(out[lastBase], "\r") {
		out[lastBase] += "\r"
	}
	if !eofNewline {
		// No newline follows the last line, so it carries no "\r" either.
		out[len(out)-1] = strings.TrimSuffix(out[len(out)-1], "\r")
	}
	text := strings.Join(out, "\n")
	if eofNewline {
		text += "\n"
	}
	return []byte(text), nil
}

// predominantCRLF reports whether most of the body's line breaks are CRLF,
// the first line deciding a tie. lines are split on "\n"; the last has no
// break after it unless eofNewline.
func predominantCRLF(lines []string, eofNewline bool) bool {
	breaks := len(lines)
	if !eofNewline {
		breaks--
	}
	crlf := 0
	for i := 0; i < breaks; i++ {
		if strings.HasSuffix(lines[i], "\r") {
			crlf++
		}
	}
	lf := breaks - crlf
	if crlf != lf {
		return crlf > lf
	}
	return breaks > 0 && strings.HasSuffix(lines[0], "\r")
}

type patchLine struct {
	op   byte
	text string
}

type patchHunk struct {
	// oldStart is the 1-based line the hunk says it starts at, or 0 when the
	// header carries no usable number.
	oldStart int
	// oldCount and newCount are the header's line counts; counted says the
	// header had them.
	oldCount, newCount int
	counted            bool
	lines              []patchLine
	oldNoNewline       bool
	newNoNewline       bool
}

// complete reports whether the hunk already holds as many lines as its
// header declares.
func (h patchHunk) complete() bool {
	if !h.counted {
		return false
	}
	oldSeen, newSeen := 0, 0
	for _, l := range h.lines {
		if l.op != '+' {
			oldSeen++
		}
		if l.op != '-' {
			newSeen++
		}
	}
	return oldSeen >= h.oldCount && newSeen >= h.newCount
}

func (h patchHunk) oldLines() []string {
	var out []string
	for _, l := range h.lines {
		if l.op != '+' {
			out = append(out, l.text)
		}
	}
	return out
}

var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func parseUnifiedPatch(patch string) ([]patchHunk, error) {
	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	if strings.TrimSpace(patch) == "" {
		return nil, fmt.Errorf("patch is empty: %w", ErrInvalid)
	}
	// Trailing blank lines are padding, not empty context lines.
	raw := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	var hunks []patchHunk
	var cur *patchHunk
	for n, line := range raw {
		if strings.HasPrefix(line, "@@") {
			h := patchHunk{}
			if m := hunkHeaderPattern.FindStringSubmatch(line); m != nil {
				h.oldStart, _ = strconv.Atoi(m[1])
				// "-a,0" names the line an insertion follows, so the
				// insertion starts one line later.
				if m[2] == "0" {
					h.oldStart++
				}
				h.oldCount, h.newCount, h.counted = 1, 1, true
				if m[2] != "" {
					h.oldCount, _ = strconv.Atoi(m[2])
				}
				if m[4] != "" {
					h.newCount, _ = strconv.Atoi(m[4])
				}
			}
			hunks = append(hunks, h)
			cur = &hunks[len(hunks)-1]
			continue
		}
		if cur == nil {
			if isPatchHeader(line) || strings.TrimSpace(line) == "" {
				continue
			}
			return nil, fmt.Errorf("patch line %d: expected a hunk header (@@ -a,b +c,d @@) before %q: %w", n+1, line, ErrInvalid)
		}
		if line == "" {
			cur.lines = append(cur.lines, patchLine{op: ' '})
			continue
		}
		// A line that looks like the next file's header only is one once
		// the hunk holds all the lines its header declared; before that it
		// is a removed "-- x" or an added "++ y" line. A header without
		// counts gives nothing to go on, so the look alone decides.
		if (!cur.counted || cur.complete()) &&
			(strings.HasPrefix(line, "diff ") || (strings.HasPrefix(line, "--- ") && n+1 < len(raw) && strings.HasPrefix(raw[n+1], "+++ "))) {
			return nil, fmt.Errorf("patch line %d: a patch edits one note; %q starts another file: %w", n+1, line, ErrInvalid)
		}
		switch line[0] {
		case ' ', '-', '+':
			cur.lines = append(cur.lines, patchLine{op: line[0], text: line[1:]})
		case '\\':
			if len(cur.lines) == 0 {
				return nil, fmt.Errorf("patch line %d: %q follows no line: %w", n+1, line, ErrInvalid)
			}
			switch cur.lines[len(cur.lines)-1].op {
			case ' ':
				cur.oldNoNewline, cur.newNoNewline = true, true
			case '-':
				cur.oldNoNewline = true
			case '+':
				cur.newNoNewline = true
			}
		default:
			return nil, fmt.Errorf("patch line %d: %q must start with ' ', '-' or '+': %w", n+1, line, ErrInvalid)
		}
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("patch has no hunks (@@ -a,b +c,d @@): %w", ErrInvalid)
	}
	for i, h := range hunks {
		changed := false
		for _, l := range h.lines {
			if l.op != ' ' {
				changed = true
				break
			}
		}
		if !changed {
			return nil, fmt.Errorf("patch hunk %d changes nothing: %w", i+1, ErrInvalid)
		}
	}
	return hunks, nil
}

func isPatchHeader(line string) bool {
	return strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") ||
		strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ")
}

// splitPatchLines splits text into lines without their "\n" and reports
// whether the text ended with one. A CRLF line keeps its "\r".
func splitPatchLines(text string) ([]string, bool) {
	if text == "" {
		return nil, true
	}
	eofNewline := strings.HasSuffix(text, "\n")
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n"), eofNewline
}

// patchMatcher places hunks in a note's lines within a comparison budget.
type patchMatcher struct {
	lines    []string
	budget   int
	exceeded bool
}

var errPatchTooExpensive = fmt.Errorf("this patch is too expensive to place; add context lines or send content: %w", ErrInvalid)

// locateHunk finds where old starts in the lines at or after from. The
// stated line wins when old is there; otherwise old must occur exactly once.
func (m *patchMatcher) locateHunk(old []string, from, oldStart int) (int, error) {
	lines := m.lines
	if len(old) == 0 {
		// A pure insertion has nothing to match, so its stated line is all
		// there is to go on.
		at := oldStart - 1
		if oldStart == 0 || at < from || at > len(lines) {
			return 0, fmt.Errorf("an insertion with no context lines needs a valid line number in its header: %w", ErrInvalid)
		}
		return at, nil
	}
	if at := oldStart - 1; at >= from && m.matchesAt(old, at) {
		return at, nil
	}
	found := -1
	count := 0
	for at := from; at+len(old) <= len(lines) && !m.exceeded; at++ {
		if m.matchesAt(old, at) {
			if found < 0 {
				found = at
			}
			count++
		}
	}
	if m.exceeded {
		return 0, errPatchTooExpensive
	}
	switch {
	case count == 1:
		return found, nil
	case count == 0 && m.occursBefore(old, from):
		return 0, fmt.Errorf("its lines only occur before the end of the previous hunk; hunks must be in order and must not overlap: %w", ErrInvalid)
	case count == 0 && m.exceeded:
		return 0, errPatchTooExpensive
	case count == 0:
		return 0, fmt.Errorf("its context and removed lines do not match the current content (did the note change, or is a context line wrong?); expected:\n%s: %w", quotePatchLines(old), ErrInvalid)
	default:
		return 0, fmt.Errorf("its context and removed lines match %d places and none is at its stated line; add context lines to make it unique: %w", count, ErrInvalid)
	}
}

func (m *patchMatcher) occursBefore(old []string, end int) bool {
	for at := 0; at < end && !m.exceeded; at++ {
		if m.matchesAt(old, at) {
			return true
		}
	}
	return false
}

// matchesAt reports whether old matches the lines starting at at, ignoring
// each line's "\r". It spends one comparison per line it compares.
func (m *patchMatcher) matchesAt(old []string, at int) bool {
	lines := m.lines
	if at < 0 || at+len(old) > len(lines) {
		return false
	}
	for i, l := range old {
		if m.budget <= 0 {
			m.exceeded = true
			return false
		}
		m.budget--
		if strings.TrimSuffix(lines[at+i], "\r") != l {
			return false
		}
	}
	return true
}

func quotePatchLines(lines []string) string {
	const max = 8
	var b strings.Builder
	for i, l := range lines {
		if i == max {
			fmt.Fprintf(&b, "  … %d more\n", len(lines)-max)
			break
		}
		fmt.Fprintf(&b, "  %q\n", l)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
