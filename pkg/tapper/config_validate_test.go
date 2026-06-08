package tapper_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestValidateConfig_NilConfig(t *testing.T) {
	t.Parallel()
	require.Nil(t, tapper.ValidateConfig(nil))
}

func TestValidateConfig_ValidConfig(t *testing.T) {
	t.Parallel()

	cfg, err := tapper.ParseConfig([]byte(`
defaultKeg: pub
logLevel: info
kegMap:
  - alias: pub
    pathPrefix: ~/Documents/kegs
`))
	require.NoError(t, err)
	require.Empty(t, tapper.ValidateConfig(cfg))
}

func TestValidateConfig_InvalidLogLevel(t *testing.T) {
	t.Parallel()

	cfg, err := tapper.ParseConfig([]byte(`logLevel: verbose`))
	require.NoError(t, err)

	warnings := tapper.ValidateConfig(cfg)
	require.Len(t, warnings, 1)
	require.Equal(t, "logLevel", warnings[0].Field)
	require.Contains(t, warnings[0].Message, "verbose")
}

func TestValidateConfig_KegMapMissingPattern(t *testing.T) {
	t.Parallel()

	cfg, err := tapper.ParseConfig([]byte(`
kegMap:
  - alias: orphan
`))
	require.NoError(t, err)

	warnings := tapper.ValidateConfig(cfg)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0].Message, "no pathPrefix or pathRegex")
}

func TestValidateConfig_KegMapMissingAlias(t *testing.T) {
	t.Parallel()

	cfg, err := tapper.ParseConfig([]byte(`
kegMap:
  - pathPrefix: ~/repos
`))
	require.NoError(t, err)

	warnings := tapper.ValidateConfig(cfg)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0].Message, "no alias")
}

func TestValidateConfig_KegMapInvalidRegex(t *testing.T) {
	t.Parallel()

	cfg, err := tapper.ParseConfig([]byte(`
kegMap:
  - alias: test
    pathRegex: "[invalid"
`))
	require.NoError(t, err)

	warnings := tapper.ValidateConfig(cfg)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0].Message, "invalid regex")
}

func TestValidateConfig_DuplicateKegMapEntries(t *testing.T) {
	t.Parallel()

	cfg, err := tapper.ParseConfig([]byte(`
kegMap:
  - alias: pub
    pathPrefix: ~/repos
  - alias: pub
    pathPrefix: ~/repos
`))
	require.NoError(t, err)

	warnings := tapper.ValidateConfig(cfg)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0].Message, "duplicate kegMap entry")
}
