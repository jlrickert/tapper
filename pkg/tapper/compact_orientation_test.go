package tapper

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestCompactOrientationProgressiveMetadata(t *testing.T) {
	root := &Flight{Name: "@n/+root", Namespace: "n", FlightManifest: FlightManifest{Title: "Dispatcher", Description: "<script>alert(1)</script>", Instructions: "  Full instructions\n\nNo trimming.  \n", Subflights: []string{"+child", "+hidden"}}}
	child := &Flight{Name: "@n/+child", Namespace: "n", FlightManifest: FlightManifest{Title: "Child title", Description: strings.Repeat("界", 300), Instructions: "CHILD SECRET", Subflights: []string{"+grandchild"}}}
	grand := &Flight{Name: "@n/+grandchild", Namespace: "n", FlightManifest: FlightManifest{Instructions: "GRAND SECRET"}}
	rows := []*Flight{root, child, grand}
	children := ImmediateFlightChildren(root, rows)
	require.Equal(t, []*Flight{child}, children)
	kegs := []OrientationKeg{{Ref: "@n/deep", Namespace: "n", Alias: "deep", Description: "DEEP CATALOG", Role: "admin"}}
	payload, err := BuildOrientationPayload(root, "", "", kegs, nil, &OrientationAuthority{Root: root, Children: children})
	require.NoError(t, err)
	for _, want := range []string{"Dispatcher", "@n/+root", root.Instructions, "&lt;script&gt;", "No covered"} {
		require.Contains(t, payload, want)
	}
	for _, absent := range []string{"Child title", "@n/+child", "CHILD SECRET", "GRAND SECRET", "@n/+grandchild", "@n/+hidden", "DEEP CATALOG", "Selectable flights:", "# Linking conventions", "<script>"} {
		require.NotContains(t, payload, absent)
	}
	next, err := BuildOrientationPayload(child, "", "", nil, nil, &OrientationAuthority{Children: ImmediateFlightChildren(child, rows)})
	require.NoError(t, err)
	require.Contains(t, next, "CHILD SECRET")
	require.NotContains(t, next, "@n/+grandchild")
	require.NotContains(t, next, "GRAND SECRET")
	noflight, err := BuildOrientationPayload(nil, "", "", kegs, nil, &OrientationAuthority{FullAccess: true, AvailableFlights: []string{root.Name}, Children: children})
	require.NoError(t, err)
	require.NotContains(t, noflight, "@n/")
	require.Contains(t, noflight, "flight_search")
	// Representative legacy payload sections: rules, full catalog, and all canonical guides.
	var before strings.Builder
	before.WriteString(OrientationOperatingRules())
	for i := 0; i < 30; i++ {
		before.WriteString("| KEG | Title | " + strings.Repeat("description ", 80) + " | admin | root | hub |\n")
	}
	for _, name := range []string{"snapshot-policy.md", "secret-handling.md", "agent-orient.md", "tool-inventory.md", "linking.md", "troubleshooting.md"} {
		require.NoError(t, appendCanonical(&before, name))
	}
	t.Logf("Representative legacy sections: %d bytes; compact dispatcher: %d bytes; reduction %.1f%%", before.Len(), len(payload), 100*(1-float64(len(payload))/float64(before.Len())))
}
func TestDescriptionPreviewUnicodeAndWhitespace(t *testing.T) {
	require.Equal(t, "a b c", DescriptionPreview(" a\n\t b   c "))
	require.Equal(t, 240, utf8.RuneCountInString(DescriptionPreview(strings.Repeat("🙂", 241))))
	require.Equal(t, strings.Repeat("🙂", 239)+"…", DescriptionPreview(strings.Repeat("🙂", 241)))
}
func TestFlightDescriptionHashAndEditorEquality(t *testing.T) {
	a := FlightManifest{Title: "Title", Description: "before", Instructions: "instructions"}
	b := a
	b.Description = "after"
	require.NotEqual(t, FlightManifestHash(a), FlightManifestHash(b))
	require.False(t, flightManifestSemanticallyEqual(a, b))
}
