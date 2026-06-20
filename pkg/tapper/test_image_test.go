package tapper_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==")
	require.NoError(t, err)
	return data
}
