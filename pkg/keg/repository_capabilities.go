package keg

import "context"

func repoListFiles(ctx context.Context, repo Repository, id NodeId) ([]string, error) {
	withFiles, ok := repo.(RepositoryFiles)
	if !ok {
		return []string{}, nil
	}
	return withFiles.ListFiles(ctx, id)
}

func repoListImages(ctx context.Context, repo Repository, id NodeId) ([]string, error) {
	withImages, ok := repo.(RepositoryImages)
	if !ok {
		return []string{}, nil
	}
	return withImages.ListImages(ctx, id)
}

// RepositoryNodeCounter is an optional repository capability: a counter
// that Next draws from, which an import raises past the ids it lands on
// before allocating any other id.
type RepositoryNodeCounter interface {
	// RaiseNodeCounter makes Next hand out only ids greater than past.
	// It never lowers the counter.
	RaiseNodeCounter(ctx context.Context, past NodeId) error
}

func repoSnapshots(repo Repository) (RepositorySnapshots, bool) {
	withSnapshots, ok := repo.(RepositorySnapshots)
	if !ok {
		return nil, false
	}
	return withSnapshots, true
}
