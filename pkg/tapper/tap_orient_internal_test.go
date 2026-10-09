package tapper

import (
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
)

func TestFlightCapForKeg_OutsideCoverDenies(t *testing.T) {
	flight := &Flight{FlightManifest: FlightManifest{
		Cover: []FlightCover{
			{Namespace: "local", Keg: "personal", Role: FlightRoleViewer},
		},
	}}

	capRole, ok := flightCapForKeg(flight, "local", "outside-cover")
	if ok || capRole != "" {
		t.Fatalf("flightCapForKeg outside cover = %q, %t; want empty, false", capRole, ok)
	}
}

func TestFlightCapForKeg_EmptyCoverDeniesAll(t *testing.T) {
	if capRole, ok := flightCapForKeg(&Flight{}, "local", "personal"); ok || capRole != "" {
		t.Fatalf("flightCapForKeg empty cover = %q, %t; want empty, false", capRole, ok)
	}
}

func TestOrientationKegTable_LabelsKegVersion(t *testing.T) {
	var b strings.Builder
	writeOrientationKegTable(&b, []OrientationKeg{
		{Ref: "@acme/graph", Title: "Graph", Role: "viewer", KegVersion: keg.KegVersionV2},
		{Ref: "@acme/notes", Title: "Notes", Role: "viewer", KegVersion: keg.KegVersionV1},
	}, "")
	out := b.String()
	if !strings.Contains(out, "| Graph (KEGv2 preview) | `@acme/graph`") {
		t.Fatalf("KEGv2 row is not labeled:\n%s", out)
	}
	if !strings.Contains(out, "| Notes | `@acme/notes`") {
		t.Fatalf("KEGv1 row must stay unlabeled:\n%s", out)
	}
}
