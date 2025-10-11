package tap_test

import (
	"embed"
	"testing"

	"github.com/jlrickert/go-std/testutils"
)

//go:embed data/**
var testdata embed.FS

func NewFixture(t *testing.T, opts ...testutils.FixtureOption) *testutils.Fixture {
	return testutils.NewFixture(t, testdata, opts...)
}
