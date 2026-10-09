package keg

// RawZeroNodeContent is the fallback content used when a node has no content.
// It serves as a friendly placeholder indicating the content is planned but
// not yet available. Callers may display this as the node README. If you
// want the content created sooner, open an issue describing the request.
var RawZeroNodeContent = `# Sorry, planned but not yet available

This is a placeholder until content is created for the link that brought you
here. If you need this content sooner, please open an issue describing why
you would like this content created.
`

var (
	// SettingsV1VersionString is the initial KEG settings version identifier.
	SettingsV1VersionString = "2023-01"

	// SettingsV2VersionString is the current KEG settings version identifier.
	SettingsV2VersionString = "2025-07"

	// FormatMarkdown is the short format identifier for Markdown content.
	FormatMarkdown = "markdown"

	// MarkdownContentFilename is the canonical filename hint used by KEG
	// archives and content parsers. It does not identify a storage backend.
	MarkdownContentFilename = "README.md"

	// FormatRST is the short format identifier for reStructuredText content.
	FormatRST = "rst"
)

// KEG versions select the storage format a Hub KEG is created with. A KEG's
// version is immutable and owned by the Hub's catalog. It is not the settings
// document version above, even though KEGv1 and the current settings document
// share the value 2025-07.
const (
	// KegVersionV1 is the Markdown-first KEG format, the Hub's default.
	KegVersionV1 = "2025-07"

	// KegVersionV2 is the graph-authoritative KEG format (preview).
	KegVersionV2 = "2026-09"
)

// ValidKegVersion reports whether v names a known KEG version. The empty
// string is not a version: callers treat it as "let the Hub choose".
func ValidKegVersion(v string) bool {
	return v == KegVersionV1 || v == KegVersionV2
}
