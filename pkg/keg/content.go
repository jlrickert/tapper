package keg

import (
	"bufio"
	"bytes"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Content represents the extracted pieces of a node's primary content file
// (README.md or README.rst). Title is the canonical title (first H1 for
// Markdown, or the RST title detected), Lead is the first paragraph after the
// title, Links are discovered numeric node links (../N), and Format is a short
// hint like "markdown" or "rst".
type Content struct {
	Title  string
	Lead   string
	Links  []NodeID
	Format string
}

// ParseContent attempts to extract title, lead paragraph, and numeric outgoing
// links from the provided file bytes. The filename can be used as a hint for
// format detection (e.g., README.md vs README.rst). When unsure, Markdown
// heuristics are attempted first.
func ParseContent(data []byte, filename string) (*Content, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return &Content{Format: "empty"}, nil
	}

	format := detectFormat(data, filename)

	var title, lead string
	switch format {
	case "rst":
		title, lead = extractRSTTitleAndLead(data)
	default:
		// default to markdown heuristics
		title, lead = extractMarkdownTitleAndLead(data)
		format = "markdown"
	}

	links := extractNumericLinks(data)

	// sort & dedupe node ids (stable deterministic order)
	links = dedupeAndSortNodeIDs(links)

	return &Content{
		Title:  title,
		Lead:   lead,
		Links:  links,
		Format: format,
	}, nil
}

// detectFormat uses filename and small heuristics to pick "rst" or "markdown".
func detectFormat(data []byte, filename string) string {
	lower := strings.ToLower(filename)
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

func isAllRunes(s string, runeChar rune) bool {
	for _, r := range s {
		if r != runeChar {
			return false
		}
	}
	return len(s) > 0
}

// extractMarkdownTitleAndLead finds the first H1 line ("# Title") and the
// first paragraph immediately following it. If no H1 is found, it will try a
// shallow fallback: first non-empty line as title and the next paragraph as lead.
func extractMarkdownTitleAndLead(data []byte) (string, string) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	title := ""
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		if title == "" {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(trim, "# "))
				// stop scanning title; we'll scan lines after for lead
				break
			}
			// also accept underline-style h1 (line followed by ===) is uncommon in MD,
			// so skip here and rely on rst detection.
		}
	}
	// If title still empty, fallback to first non-empty line in whole file
	if title == "" {
		for _, l := range lines {
			if t := strings.TrimSpace(l); t != "" {
				title = t
				break
			}
		}
		// if still empty, scan from beginning to collect paragraphs
	}

	// Find lead paragraph: first non-empty paragraph after the title line.
	// To do this we need to continue scanning from the point after the detected title.
	remaining := bytes.NewReader(data)
	scanner = bufio.NewScanner(remaining)
	foundTitle := false
	for scanner.Scan() {
		line := scanner.Text()
		if !foundTitle {
			trim := strings.TrimSpace(line)
			// mark title found when we encounter it (matching our title heuristics)
			if title != "" && (strings.HasPrefix(trim, "# ") && strings.Contains(trim, title) || trim == title) {
				foundTitle = true
			}
			// If title was from fallback (first non-empty line) we also consider the first match as found.
			if title != "" && !foundTitle {
				// If fallback title equals this line, mark foundTitle true
				if strings.TrimSpace(line) == title {
					foundTitle = true
				}
			}
			continue
		}
		// After title: skip blank lines until we hit paragraph content
		// collect contiguous non-blank lines as paragraph
		// First non-empty paragraph is lead.
		// skip possible heading lines that start with '#' (a subsequent heading isn't a paragraph)
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

// extractRSTTitleAndLead finds an RST-style title where the first line is text
// and the second line is a run of '=' or '-' matching length. Lead is the first
// paragraph after the title block.
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

// extractNumericLinks finds occurrences of "../N" where N is a positive integer
// and returns them as a slice of NodeID (may contain duplicates).
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
