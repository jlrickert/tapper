package keg

import (
	"bufio"
	"bytes"
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/jlrickert/go-std/toolkit"
	"github.com/yuin/goldmark"
	gm_ast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

// Content holds the extracted pieces of a node's primary content file
// (README.md or README.rst).
//
// Fields:
//   - Hash: stable content hash computed by the repository hasher.
//   - Title: canonical title (first H1 for Markdown, or RST title detected).
//   - Lead: first paragraph immediately following the title (used as a short
//     summary).
//   - Links: numeric outgoing node links discovered in the content (../N).
//   - Format: short hint of the detected format ("markdown", "rst", or "empty").
//   - Frontmatter: parsed YAML frontmatter when present (Markdown only).
//   - Body: the raw body bytes of the content file with frontmatter removed for
//     Markdown (or the original bytes for other formats), represented as a
//     string.
type Content struct {
	// Hash is the stable content hash computed by the repository hasher.
	Hash string

	// Title is the canonical title for the content. For Markdown this is the
	// first H1; for RST it is the detected title.
	Title string

	// Lead is the first paragraph immediately following the title. It is used
	// as a short summary or preview of the content.
	Lead string

	// Links is the list of numeric outgoing node links discovered in the
	// content (for example "../42"). Entries are normalized Node values.
	Links []Node

	// Format is a short hint of the detected format. Typical values are
	// "markdown", "rst", or "empty".
	Format string

	// Body is the content body with Markdown frontmatter removed when present.
	// For non-Markdown formats this is the original file content.
	Body string

	// Frontmatter is the parsed YAML frontmatter when present. It is non-nil
	// only for Markdown documents that include a leading YAML block.
	Frontmatter map[string]any
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
func ParseContent(ctx context.Context, data []byte, format string) (*Content, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return &Content{Format: "empty"}, nil
	}

	fmt := detectFormat(data, format)
	hasher := toolkit.HasherFromContext(ctx)

	var title, lead string
	var fm map[string]any
	var contentData []byte

	switch fmt {
	case "rst":
		// RST: no frontmatter handling for now
		title, lead = extractRSTTitleAndLead(data)
		contentData = data
	default:
		// default to markdown heuristics
		// Support YAML frontmatter at the start of the document.
		fm, contentData = extractMarkdownFrontmatter(data)
		title, lead = extractMarkdownTitleAndLead(contentData)
		fmt = "markdown"
	}

	links := extractNumericLinks(contentData)

	// sort & dedupe node ids (stable deterministic order)
	links = dedupeAndSortNodeIDs(links)

	return &Content{
		Hash:        hasher.Hash(data),
		Title:       title,
		Lead:        lead,
		Links:       links,
		Format:      fmt,
		Frontmatter: fm,
		Body:        string(contentData),
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
// integer and returns them as a slice of NodeID. For Markdown content the
// function uses goldmark to parse the document and extract numeric destinations
// from Link and AutoLink nodes and also scans text nodes for bare "../N" tokens.
// For non-markdown content the function falls back to a simple regex scan.
// The returned slice may contain duplicates; callers should call dedupeAndSortNodeIDs
// to normalize the result.
func extractNumericLinks(data []byte) []Node {
	out := make([]Node, 0)

	// Attempt to parse as Markdown using goldmark. If parsing fails, fall back to regex.
	md := goldmark.New()
	reader := text.NewReader(data)
	doc := md.Parser().Parse(reader)

	// regex to match a destination that is exactly ../N (allowing optional whitespace)
	destExactRE := regexp.MustCompile(`^\s*\.\./\s*([0-9]+)\s*$`)

	_ = gm_ast.Walk(doc, func(n gm_ast.Node, entering bool) (gm_ast.WalkStatus, error) {
		if !entering {
			return gm_ast.WalkContinue, nil
		}
		switch n.Kind() {
		case gm_ast.KindLink:
			if l, ok := n.(*gm_ast.Link); ok {
				dest := string(l.Destination)
				if m := destExactRE.FindStringSubmatch(dest); len(m) == 2 {
					if id, err := ParseNode(m[1]); err == nil {
						out = append(out, *id)
					}
				}
			}
		case gm_ast.KindAutoLink:
			if al, ok := n.(*gm_ast.AutoLink); ok {
				dest := string(al.URL(data))
				if m := destExactRE.FindStringSubmatch(dest); len(m) == 2 {
					if id, err := ParseNode(m[1]); err == nil {
						out = append(out, *id)
					}
				}
			}
		}
		return gm_ast.WalkContinue, nil
	})

	// If goldmark produced no nodes (unlikely) or out is empty, fall back to scanning the raw bytes.
	if len(out) == 0 {
		matches := numericLinkRE.FindAllSubmatch(data, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			if id, err := ParseNode(string(m[1])); err == nil {
				out = append(out, *id)
			}
		}
	}

	return out
}

// dedupeAndSortNodeIDs removes duplicates from the input slice and returns a
// new slice sorted in ascending numeric order. The operation is deterministic
// and suitable for producing stable index outputs.
func dedupeAndSortNodeIDs(in []Node) []Node {
	set := make(map[string]struct{})
	out := make([]Node, 0, len(in))
	for _, id := range in {
		key := id.Path()
		if _, ok := set[key]; ok {
			continue
		}
		set[key] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		a := out[i].Path()
		b := out[j].Path()
		return strings.Compare(a, b) < 0
	})
	return out
}

// extractMarkdownFrontmatter looks for a YAML frontmatter block at the very
// start of the document. If found it returns the parsed map and the content
// with the frontmatter removed. If not found, returns (nil, original).
//
// The function is tolerant: if YAML unmarshal fails we ignore the frontmatter
// and return nil with the original data.
func extractMarkdownFrontmatter(data []byte) (map[string]any, []byte) {
	if len(data) == 0 {
		return nil, data
	}
	// Accept leading UTF-8 BOM or direct '---' at start.
	trimmed := data
	// Check for BOM
	if bytes.HasPrefix(trimmed, []byte("\xef\xbb\xbf")) {
		trimmed = trimmed[3:]
	}
	if !bytes.HasPrefix(trimmed, []byte("---\n")) && !bytes.HasPrefix(trimmed, []byte("---\r\n")) {
		return nil, data
	}
	// rest is after the opening '---\n' or '---\r\n'
	var rest []byte
	if bytes.HasPrefix(trimmed, []byte("---\r\n")) {
		rest = trimmed[len([]byte("---\r\n")):]
	} else {
		rest = trimmed[len([]byte("---\n")):]
	}

	// Try several common closing markers.
	// Prefer the most specific sequence first.
	choices := [][]byte{
		[]byte("\n---\r\n"),
		[]byte("\n---\n"),
		[]byte("\r\n---\n"),
		[]byte("\n---"),
	}
	var endIdx int = -1
	var endMarkerLen int
	for _, m := range choices {
		if i := bytes.Index(rest, m); i >= 0 {
			endIdx = i
			endMarkerLen = len(m)
			break
		}
	}
	if endIdx < 0 {
		// No closing marker found; treat as no frontmatter.
		return nil, data
	}

	fmBytes := rest[:endIdx]
	remaining := rest[endIdx+endMarkerLen:]

	// If we stripped a BOM earlier, reconstruct remaining relative to original
	// to preserve exact bytes around start if needed.
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		remaining = append([]byte("\xef\xbb\xbf"), remaining...)
	}

	var fm map[string]any
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		// On parse error, ignore and return original data.
		return nil, data
	}
	// Trim leading newline(s) from remaining so title detection starts at first content line.
	remaining = bytes.TrimLeft(remaining, "\r\n")
	return fm, remaining
}
