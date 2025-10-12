package keg_test

import (
	"embed"
	"github.com/jlrickert/go-std/testutils"
	"testing"
)

//go:embed data/**
var testdata embed.FS

func NewFixture(t *testing.T, opts ...testutils.FixtureOption) *testutils.Fixture {
	return testutils.NewFixture(t, &testutils.FixtureOptions{
		Data: testdata,
		Home: "/home/testuser",
		User: "testuser",
	}, opts...)
}
