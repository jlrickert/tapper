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
