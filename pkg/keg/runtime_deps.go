package keg

import (
	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/cli-toolkit/toolkit"
)

type repositoryRuntimeProvider interface {
	Runtime() *toolkit.Runtime
}

func repoRuntime(repo Repository) *toolkit.Runtime {
	rt := repo.(repositoryRuntimeProvider).Runtime()
	if rt == nil {
		panic("repository runtime is nil")
	}
	return rt
}

func repoClock(repo Repository) clock.Clock {
	return runtimeClock(repoRuntime(repo))
}

func repoHasher(repo Repository) toolkit.Hasher {
	return runtimeHasher(repoRuntime(repo))
}

func runtimeClock(rt *toolkit.Runtime) clock.Clock {
	return rt.Clock()
}

func runtimeHasher(rt *toolkit.Runtime) toolkit.Hasher {
	return rt.Hasher()
}
