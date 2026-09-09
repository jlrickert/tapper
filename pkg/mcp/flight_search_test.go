package mcp

import (
	"fmt"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestFlightSearchLiteralBoundedMetadata(t *testing.T) {
	rows := []*tapper.Flight{}
	for i := 60; i >= 0; i-- {
		rows = append(rows, &tapper.Flight{Name: fmt.Sprintf("@n/+f%02d", i), FlightManifest: tapper.FlightManifest{Title: "Title", Description: "literal %_ [x]", Instructions: "instruction-only"}})
	}
	out := SearchIdentityFlights(rows, "%_")
	require.Len(t, out.Flights, 50)
	require.True(t, out.Truncated)
	require.Equal(t, "@n/+f00", out.Flights[0].Ref)
	require.Empty(t, SearchIdentityFlights(rows, "instruction-only").Flights)
	require.Empty(t, SearchIdentityFlights(rows, " ").Flights)
	require.Len(t, SearchIdentityFlights(rows, "@n/+f60").Flights, 1)
	require.Len(t, SearchIdentityFlights(rows, "TITLE").Flights, 50)
}
