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
