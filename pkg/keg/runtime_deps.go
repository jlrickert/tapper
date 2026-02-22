package keg

import (
	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/cli-toolkit/toolkit"
)

func repoRuntime(repo Repository) *toolkit.Runtime {
	return repo.Runtime()
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
