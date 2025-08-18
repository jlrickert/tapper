package tapper

const (
	// ConfigAppName is the base directory name used for Tapper configuration.
	// Helpers use this value to construct platform-specific config paths such as:
	//   $XDG_CONFIG_HOME/tapper (or ~/.config/tapper) on Unix-like systems
	//   %APPDATA%\tapper on Windows
	// Example config file: $XDG_CONFIG_HOME/tapper/aliases.yaml
	ConfigAppName = "tapper"

	DefaultLocalTapperDir = ".tapper"
)
