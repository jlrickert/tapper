package tap

// Config version strings identify KEG configuration schema versions. When a
// new configuration schema is introduced, add a new constant and update the
// Config alias to point to the latest version. These constants are used by
// parsing and migration logic (e.g., ParseConfigData) to detect and upgrade
// older config formats.
const (
	// ConfigV1VersionString is the initial configuration version identifier.
	ConfigV1VersionString = "2023-01"

	// ConfigV2VersionString is the current configuration version identifier.
	ConfigV2VersionString = "2025-07"

	// ConfigAppName is the base directory name used for Tapper configuration.
	// Helpers use this value to construct platform-specific config paths such as:
	//   $XDG_CONFIG_HOME/tapper (or ~/.config/tapper) on Unix-like systems
	//   %APPDATA%\tapper on Windows
	// Example config file: $XDG_CONFIG_HOME/tapper/aliases.yaml
	ConfigAppName = "tapper"

	LocalConfigDir = ".tapper"
)
