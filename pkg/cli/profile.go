package cli

// Profile configures CLI behavior for a specific binary.
type Profile struct {
	// Use is the root command name shown in help.
	Use string

	// ForceProjectResolution makes node operations resolve only against
	// project-local kegs.
	ForceProjectResolution bool

	// AllowKegAliasFlags enables alias-based selection flags such as --keg.
	AllowKegAliasFlags bool

	// IncludeConfigCommand enables the config command tree.
	IncludeConfigCommand bool

	// IncludeRepoCommand enables the repo command tree.
	IncludeRepoCommand bool
}

func TapProfile() Profile {
	return Profile{
		Use:                    "tap",
		ForceProjectResolution: false,
		AllowKegAliasFlags:     true,
		IncludeConfigCommand:   true,
		IncludeRepoCommand:     true,
	}
}

func KegV2Profile() Profile {
	return Profile{
		Use:                    "kegv2",
		ForceProjectResolution: true,
		AllowKegAliasFlags:     false,
		IncludeConfigCommand:   false,
		IncludeRepoCommand:     false,
	}
}

func (p Profile) withDefaults() Profile {
	if p.Use == "" {
		return TapProfile()
	}
	return p
}
