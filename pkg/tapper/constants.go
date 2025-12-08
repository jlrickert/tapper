package tapper

// Config version strings identify KEG configuration schema versions. Each
// constant is a stable identifier for a particular config schema. When a new
// schema is introduced add a new constant and update the Config alias to
// point to the latest version. These values are used by parsing and migration
// code (for example ParseConfigData) to detect older formats and perform
// upgrades. Use a YYYY-MM format for easy sorting and human readability.
const (
	// DefaultAppName is the base directory name used for Tapper user
	// configuration. Helpers use this value to build platform specific config
	// paths, for example:
	//   $XDG_CONFIG_HOME/tapper   (or ~/.config/tapper) on Unix-like systems
	//   %APPDATA%\tapper          on Windows
	// Example config file:
	//   $XDG_CONFIG_HOME/tapper/aliases.yaml
	DefaultAppName = "tapper"

	// DefaultLocalConfigDir is the directory name used for repository or
	// project local configuration.
	DefaultLocalConfigDir = ".tapper"
)
