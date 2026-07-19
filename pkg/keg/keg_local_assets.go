package keg

import (
	"context"
	"fmt"
)

// ListFiles lists file attachment names for a node. Returns ErrNotSupported
// when the backend lacks file storage.
func (k *LocalKeg) ListFiles(ctx context.Context, id NodeId) ([]string, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) ([]string, error) { return k.listFiles(ctx, id) })
}

func (k *LocalKeg) listFiles(ctx context.Context, id NodeId) ([]string, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}
	files, ok := k.Repo.(RepositoryFiles)
	if !ok {
		return nil, ErrNotSupported
	}
	return files.ListFiles(ctx, id)
}

// ReadFile reads a file attachment for a node.
func (k *LocalKeg) ReadFile(ctx context.Context, id NodeId, name string) ([]byte, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) ([]byte, error) { return k.readFile(ctx, id, name) })
}

func (k *LocalKeg) readFile(ctx context.Context, id NodeId, name string) ([]byte, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	files, ok := k.Repo.(RepositoryFiles)
	if !ok {
		return nil, ErrNotSupported
	}
	return files.ReadFile(ctx, id, name)
}

// WriteFile stores a file attachment for a node.
func (k *LocalKeg) WriteFile(ctx context.Context, id NodeId, name string, data []byte) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.writeFile(ctx, id, name, data) })
}

func (k *LocalKeg) writeFile(ctx context.Context, id NodeId, name string, data []byte) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	files, ok := k.Repo.(RepositoryFiles)
	if !ok {
		return ErrNotSupported
	}
	return files.WriteFile(ctx, id, name, data)
}

// DeleteFile removes a file attachment from a node.
func (k *LocalKeg) DeleteFile(ctx context.Context, id NodeId, name string) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.deleteFile(ctx, id, name) })
}

func (k *LocalKeg) deleteFile(ctx context.Context, id NodeId, name string) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	files, ok := k.Repo.(RepositoryFiles)
	if !ok {
		return ErrNotSupported
	}
	return files.DeleteFile(ctx, id, name)
}

// ListImages lists image names for a node. Returns ErrNotSupported when the
// backend lacks image storage.
func (k *LocalKeg) ListImages(ctx context.Context, id NodeId) ([]string, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) ([]string, error) { return k.listImages(ctx, id) })
}

func (k *LocalKeg) listImages(ctx context.Context, id NodeId) ([]string, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	images, ok := k.Repo.(RepositoryImages)
	if !ok {
		return nil, ErrNotSupported
	}
	return images.ListImages(ctx, id)
}

// ReadImage reads an image payload for a node.
func (k *LocalKeg) ReadImage(ctx context.Context, id NodeId, name string) ([]byte, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) ([]byte, error) { return k.readImage(ctx, id, name) })
}

func (k *LocalKeg) readImage(ctx context.Context, id NodeId, name string) ([]byte, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to read image: %w", err)
	}
	images, ok := k.Repo.(RepositoryImages)
	if !ok {
		return nil, ErrNotSupported
	}
	return images.ReadImage(ctx, id, name)
}

// WriteImage stores an image payload for a node.
func (k *LocalKeg) WriteImage(ctx context.Context, id NodeId, name string, data []byte) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.writeImage(ctx, id, name, data) })
}

func (k *LocalKeg) writeImage(ctx context.Context, id NodeId, name string, data []byte) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to write image: %w", err)
	}
	if _, err := ValidateImage(data); err != nil {
		return err
	}
	images, ok := k.Repo.(RepositoryImages)
	if !ok {
		return ErrNotSupported
	}
	return images.WriteImage(ctx, id, name, data)
}

// DeleteImage removes an image from a node.
func (k *LocalKeg) DeleteImage(ctx context.Context, id NodeId, name string) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.deleteImage(ctx, id, name) })
}

func (k *LocalKeg) deleteImage(ctx context.Context, id NodeId, name string) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}
	images, ok := k.Repo.(RepositoryImages)
	if !ok {
		return ErrNotSupported
	}
	return images.DeleteImage(ctx, id, name)
}
