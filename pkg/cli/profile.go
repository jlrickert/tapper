package cli

// Profile configures CLI behavior for a specific binary.
type Profile struct {
	// Use is the root command name shown in help.
	Use string

	// AllowKegAliasFlags enables alias-based selection flags such as --keg.
	AllowKegAliasFlags bool

	// IncludeConfigCommand enables the config command tree.
	IncludeConfigCommand bool

	// IncludeIntegrations enables host plugin installation and the hidden
	// host-facing hook protocol. These commands belong only to the full tap
	// binary because installed plugins invoke tap directly.
	IncludeIntegrations bool
}

func TapProfile() Profile {
	return Profile{
		Use:                  "tap",
		AllowKegAliasFlags:   true,
		IncludeConfigCommand: true,
		IncludeIntegrations:  true,
	}
}

func (p Profile) withDefaults() Profile {
	if p.Use == "" {
		return TapProfile()
	}
	return p
}
