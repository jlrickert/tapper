package cli

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// TestBuildHubChoices_NilConfig covers the "no config" case: atlas is offered
// first as the default, and the Other-endpoint sentinel is always last.
func TestBuildHubChoices_NilConfig(t *testing.T) {
	t.Parallel()
	choices := buildHubChoices(nil)
	require.Len(t, choices, 2)

	require.Equal(t, tapper.CanonicalHubURL(tapper.DefaultHubURL), choices[0].URL)
	require.Contains(t, choices[0].Label, "atlas.foldwise.ai")
	require.Contains(t, choices[0].Label, "(default)")
	require.False(t, choices[0].Other)

	require.True(t, choices[len(choices)-1].Other, "last choice should be the Other endpoint sentinel")
}

// TestBuildHubChoices_RemoteHubsOnly_DedupAndScheme confirms local hubs are
// excluded, a hub duplicating atlas is deduped, bare hostnames gain an https
// scheme, and the menu stays atlas-first / Other-last.
func TestBuildHubChoices_RemoteHubsOnly_DedupAndScheme(t *testing.T) {
	t.Parallel()
	cfg := &tapper.Config{}
	require.NoError(t, cfg.SetHub("keg-example", tapper.HubEntry{Kind: tapper.HubKindRemote, URL: "keg.example.com"})) // bare host
	require.NoError(t, cfg.SetHub("home", tapper.HubEntry{Kind: tapper.HubKindLocal, BasePath: "/tmp/kegs"}))          // excluded
	require.NoError(t, cfg.SetHub("atlas-again", tapper.HubEntry{Kind: tapper.HubKindRemote, URL: "https://atlas.foldwise.ai"}))

	choices := buildHubChoices(cfg)

	var urls []string
	for _, c := range choices {
		if !c.Other {
			urls = append(urls, c.URL)
		}
	}
	require.Equal(t, []string{
		"https://atlas.foldwise.ai", // atlas first
		"https://keg.example.com",   // bare host gained an https:// scheme
	}, urls, "local hub excluded, duplicate atlas deduped, scheme added")
	require.True(t, choices[len(choices)-1].Other)
}
