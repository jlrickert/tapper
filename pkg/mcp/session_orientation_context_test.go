package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func TestContextWithOrientationNoFlightOmitsGovernanceState(t *testing.T) {
	ctx := contextWithOrientation(context.Background(), &orientationContext{
		fullAccess: true,
		revision:   "identity-revision",
	})

	_, ok := keg.OrientationStateFromContext(ctx)
	require.False(t, ok, "no-flight calls must use normal identity ACLs without a governed-flight proof")
}

func TestContextWithOrientationExplicitFlightCarriesSelfRootedProof(t *testing.T) {
	flight := &tapper.Flight{Name: "@team/+restricted", Namespace: "team", Slug: "restricted"}
	ctx := contextWithOrientation(context.Background(), &orientationContext{
		root:     flight,
		flight:   flight,
		revision: "flight-revision",
	})

	state, ok := keg.OrientationStateFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, keg.OrientationState{
		Root:     "@team/+restricted",
		Active:   "@team/+restricted",
		Revision: "flight-revision",
	}, state)
}

func TestExplicitFlightCallRetainsNoFlightConnectionNudge(t *testing.T) {
	gate := newSessionFlightGate(nil)
	gate.publish("test-session", &orientationContext{
		fullAccess: true,
		reconnect:  "start a new connection",
	}, false)
	flight := &tapper.Flight{Name: "@team/+manager", Namespace: "team", Slug: "manager"}
	ctx := context.WithValue(context.Background(), flightSessionContextKey{}, "test-session")
	ctx = context.WithValue(ctx, orientationContextKey{}, &orientationContext{
		root: flight, flight: flight, revision: "flight-revision",
	})

	require.Equal(t, "start a new connection", gate.fullAccessReconnect(ctx))
	root, active, err := gate.orientationTarget(ctx, flight.Name)
	require.NoError(t, err)
	require.False(t, root)
	require.False(t, active, "a call-local flight must not be reported as the connection root")
}
