package tapper_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestTapCreateDefaultsToHumanSchemaPolicy(t *testing.T) {
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)
	ctx := fx.Context()
	local, err := keg.NewKegFromTarget(ctx, keg.NewFile("/home/testuser/kegs/@local/test"), fx.Runtime())
	require.NoError(t, err)
	require.NoError(t, local.CreateSchema(ctx, "task", []byte(`type: task
meta:
  type: object
  required: [type]
  properties:
    type: {const: task}
`)))
	require.NoError(t, keg.UpdateConfig(ctx, local, func(cfg *keg.Config) {
		cfg.SchemaPolicy = &keg.SchemaPolicy{
			Strict: false,
			Human:  keg.ValidationModeOff,
			Agent:  keg.ValidationModeBlock,
			API:    keg.ValidationModeBlock,
		}
	}))

	_, err = tap.Create(ctx, tapper.CreateOptions{Title: "Human policy write"})
	require.NoError(t, err, "CLI write should use human:off rather than agent:block")
}
