package tapper

import "testing"

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
