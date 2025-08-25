package keg_test

import (
	"time"

	"github.com/jlrickert/tapper/pkg/internal"
	"github.com/jlrickert/tapper/pkg/keg"
)

func init() {
	now, _ := time.Parse("2006-01-02 15:04:05Z07:00", "2025-08-09 17:44:04Z")
	deps.Clock = internal.NewFixedClock(now)
}

var deps = &keg.Deps{
	Hasher:   &keg.MD5Hasher{},
	Resolver: &keg.BasicLinkResolver{},
}
