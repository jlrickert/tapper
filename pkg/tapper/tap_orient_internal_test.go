package tapper

import "testing"

func TestFlightCapForKeg_FullAccessIncludesEveryAuthorizedKeg(t *testing.T) {
	flight := &Flight{FlightManifest: FlightManifest{
		Capabilities: []FlightCapability{FlightCapabilityFullAccess},
		Cover: []FlightCover{
			{Namespace: "local", Keg: "personal", Role: FlightRoleViewer},
		},
	}}

	capRole, ok := flightCapForKeg(flight, "local", "outside-cover")
	if !ok || capRole != string(FlightRoleEditor) {
		t.Fatalf("flightCapForKeg full_access = %q, %t; want editor, true", capRole, ok)
	}
}

func TestFlightCapForKeg_EmptyCoverDeniesAll(t *testing.T) {
	if capRole, ok := flightCapForKeg(&Flight{}, "local", "personal"); ok || capRole != "" {
		t.Fatalf("flightCapForKeg empty cover = %q, %t; want empty, false", capRole, ok)
	}
}
