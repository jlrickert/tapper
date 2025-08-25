package keg

import (
	"bufio"
	"bytes"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Content holds the extracted pieces of a node's primary content file
// (README.md or README.rst).
//
// Fields:
//   - Hash: stable content hash computed by the repository hasher.
//   - Title: canonical title (first H1 for Markdown, or RST title detected).
//   - Lead: first paragraph immediately following the title (used as a short summary).
//   - Links: numeric outgoing node links discovered in the content (../N).
//   - Format: short hint of the detected format ("markdown", "rst", or "empty").
type Content struct {
	Hash   string
	Title  string
	Lead   string
	Links  []NodeID
	Format string
}

// ParseContent extracts a Content value from raw file bytes.
//
// The format parameter is a filename hint (e.g., "README.md", "README.rst").
// When format is ambiguous the function applies simple heuristics to choose
// between Markdown and reStructuredText. The returned Content contains a
// deterministic, deduplicated, sorted list of discovered numeric links.
//
// ParseContent requires a non-nil deps pointer whose hasher is used to compute
// the content Hash. If the input is empty or only whitespace, a Content with
// Format == "empty" is returned.
func ParseContent(data []byte, format string, deps *Deps) (*Content, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return &Content{Format: "empty"}, nil
	}

	fmt := detectFormat(data, format)

	var title, lead string
	switch fmt {
	case "rst":
		title, lead = extractRSTTitleAndLead(data)
	default:
		// default to markdown heuristics
		title, lead = extractMarkdownTitleAndLead(data)
		fmt = "markdown"
	}

	links := extractNumericLinks(data)

	// sort & dedupe node ids (stable deterministic order)
	links = dedupeAndSortNodeIDs(links)

	return &Content{
		Hash:   deps.Hasher.Hash(data),
		Title:  title,
		Lead:   lead,
		Links:  links,
		Format: fmt,
	}, nil
}

// detectFormat returns "rst" or "markdown" using a filename hint and a small
// content-based heuristic. If the provided format string ends with ".rst" or
// ".rest" we prefer "rst". Otherwise we inspect the second line of the file:
// an RST title is commonly followed by a line of === or --- that matches the
// underline style.
func detectFormat(data []byte, format string) string {
	lower := strings.ToLower(format)
	if strings.HasSuffix(lower, ".rst") || strings.HasSuffix(lower, ".rest") {
		return "rst"
	}
	// simple heuristic: rst titles often use underline of === or --- on 2nd line
	scanner := bufio.NewScanner(bytes.NewReader(data))

	// We only need the second line for the underline heuristic; skip the first.
	if !scanner.Scan() {
		return "markdown"
	}
	var second string
	if scanner.Scan() {
		second = scanner.Text()
	}
	secondTrim := strings.TrimSpace(second)
	if secondTrim != "" && (isAllRunes(secondTrim, '=') || isAllRunes(secondTrim, '-')) {
		return "rst"
	}
	return "markdown"
}

// isAllRunes reports whether s is non-empty and consists entirely of runeChar.
func isAllRunes(s string, runeChar rune) bool {
	for _, r := range s {
		if r != runeChar {
			return false
		}
	}
	return len(s) > 0
}

// extractMarkdownTitleAndLead finds the first Markdown H1 title (a line that
// begins with "# ") and the first paragraph immediately following that title.
// If no H1 is present, the function falls back to using the first non-empty
// line as the title and attempts to find the subsequent paragraph.
//
// The lead paragraph is the first contiguous block of non-blank lines after
// the title (stops at the next blank line). If a subsequent heading (line
// starting with '#') is encountered before a paragraph, no lead is returned.
func extractMarkdownTitleAndLead(data []byte) (string, string) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	title := ""
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		if title == "" {
			trim := strings.TrimSpace(line)
			if after, ok := strings.CutPrefix(trim, "# "); ok {
				title = strings.TrimSpace(after)
				// stop scanning title; we'll scan remainder for lead
				break
			}
			// underline-style H1 (title followed by ===) is uncommon in Markdown,
			// so rely on rst detection for that case.
		}
	}
	// If title still empty, fallback to first non-empty line collected so far.
	if title == "" {
		for _, l := range lines {
			if t := strings.TrimSpace(l); t != "" {
				title = t
				break
			}
		}
		// if still empty, we'll scan from the beginning below to find paragraphs
	}

	// Continue scanning from the start to find the lead paragraph after the title.
	remaining := bytes.NewReader(data)
	scanner = bufio.NewScanner(remaining)
	foundTitle := false
	for scanner.Scan() {
		line := scanner.Text()
		if !foundTitle {
			trim := strings.TrimSpace(line)
			// mark title found when we encounter a line matching our heuristics
			if title != "" && ((strings.HasPrefix(trim, "# ") && strings.Contains(trim, title)) || trim == title) {
				foundTitle = true
			}
			// If the fallback title equals this line, treat it as the found title.
			if title != "" && !foundTitle {
				if strings.TrimSpace(line) == title {
					foundTitle = true
				}
			}
			continue
		}
		// After title: skip blank lines until paragraph content is found.
		// The first non-empty paragraph is the lead. If we hit another heading
		// before a paragraph, treat as no lead.
		for scanner.Scan() {
			l := scanner.Text()
			if strings.TrimSpace(l) == "" {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				// encountered another heading; treat as no lead
				return title, ""
			}
			// collect paragraph lines until a blank line
			para := []string{strings.TrimSpace(l)}
			for scanner.Scan() {
				nl := scanner.Text()
				if strings.TrimSpace(nl) == "" {
					break
				}
				para = append(para, strings.TrimSpace(nl))
			}
			return title, strings.Join(para, " ")
		}
	}
	// No lead found
	return title, ""
}

// extractRSTTitleAndLead detects an RST-style title: first line text and the
// second line consisting entirely of '=' or '-' (a common RST underline).
// The lead is the first paragraph after the underline block. If the RST-style
// title is not present, the function falls back to the Markdown fallback logic.
func extractRSTTitleAndLead(data []byte) (string, string) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) >= 2 {
		first := strings.TrimSpace(lines[0])
		second := strings.TrimSpace(lines[1])
		// common RST underline chars: = or -
		if first != "" && (isAllRunes(second, '=') || isAllRunes(second, '-')) {
			// title detected
			title := first
			// scan remaining lines after index 1 for first paragraph
			i := 2
			// skip any blank lines immediately after underline
			for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
				i++
			}
			// collect paragraph
			if i < len(lines) {
				para := []string{}
				for ; i < len(lines); i++ {
					if strings.TrimSpace(lines[i]) == "" {
						break
					}
					para = append(para, strings.TrimSpace(lines[i]))
				}
				return title, strings.Join(para, " ")
			}
			return title, ""
		}
	}
	// fallback: treat like markdown fallback
	return extractMarkdownTitleAndLead(data)
}

var numericLinkRE = regexp.MustCompile(`\.\./\s*([0-9]+)`)

// extractNumericLinks finds occurrences of "../N" where N is a non-negative
// integer and returns them as a slice of NodeID. The returned slice may contain
// duplicates; callers should call dedupeAndSortNodeIDs to normalize the result.
func extractNumericLinks(data []byte) []NodeID {
	matches := numericLinkRE.FindAllSubmatch(data, -1)
	out := make([]NodeID, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		if idStr := string(m[1]); idStr != "" {
			if id, err := strconv.Atoi(idStr); err == nil {
				out = append(out, NodeID(id))
			}
		}
	}
	return out
}

// dedupeAndSortNodeIDs removes duplicates from the input slice and returns a
// new slice sorted in ascending numeric order. The operation is deterministic
// and suitable for producing stable index outputs.
func dedupeAndSortNodeIDs(in []NodeID) []NodeID {
	set := make(map[int]struct{})
	out := make([]NodeID, 0, len(in))
	for _, id := range in {
		key := int(id)
		if _, ok := set[key]; ok {
			continue
		}
		set[key] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return int(out[i]) < int(out[j]) })
	return out
}
