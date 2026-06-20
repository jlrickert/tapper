package tapper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jlrickert/tapper/pkg/keg"
)

type ListFilesOptions struct {
	KegTargetOptions
	NodeID string
}

// UploadFileOptions configures behavior for Tap.UploadFile.
type UploadFileOptions struct {
	KegTargetOptions
	NodeID   string
	FilePath string
	Data     []byte
	Name     string
}

// DownloadFileOptions configures behavior for Tap.DownloadFile.
type DownloadFileOptions struct {
	KegTargetOptions
	NodeID string
	Name   string
	Dest   string
}

// DeleteFileOptions configures behavior for Tap.DeleteFile.
type DeleteFileOptions struct {
	KegTargetOptions
	NodeID string
	Name   string
}

// ListImagesOptions configures behavior for Tap.ListImages.
type ListImagesOptions struct {
	KegTargetOptions
	NodeID string
}

// UploadImageOptions configures behavior for Tap.UploadImage.
type UploadImageOptions struct {
	KegTargetOptions
	NodeID   string
	FilePath string
	Data     []byte
	Name     string
}

// DownloadImageOptions configures behavior for Tap.DownloadImage.
type DownloadImageOptions struct {
	KegTargetOptions
	NodeID string
	Name   string
	Dest   string
}

// DeleteImageOptions configures behavior for Tap.DeleteImage.
type DeleteImageOptions struct {
	KegTargetOptions
	NodeID string
	Name   string
}

// ListFiles returns the names of file attachments for a node.
func (t *Tap) ListFiles(ctx context.Context, opts ListFilesOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return nil, err
	}
	names, err := k.ListFiles(ctx, id)
	if err != nil {
		if errors.Is(err, keg.ErrNotSupported) {
			return nil, fmt.Errorf("keg backend does not support file attachments")
		}
		return nil, err
	}
	return names, nil
}

// UploadFile reads a local file and stores it as a node file attachment.
// Returns the stored filename.
func (t *Tap) UploadFile(ctx context.Context, opts UploadFileOptions) (string, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return "", err
	}
	exists, err := t.nodeExistsWithContent(ctx, k, id)
	if err != nil {
		return "", fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("node %s not found in %s", id.Path(), describeKeg(k))
	}
	data := opts.Data
	if data == nil {
		var err error
		data, err = t.Runtime.ReadFile(opts.FilePath)
		if err != nil {
			return "", fmt.Errorf("unable to read local file %q: %w", opts.FilePath, err)
		}
	}
	name := opts.Name
	if name == "" && opts.FilePath != "" {
		name = filepath.Base(opts.FilePath)
	}
	if name == "" {
		return "", fmt.Errorf("filename is required")
	}
	if err := k.WriteFile(ctx, id, name, data); err != nil {
		return "", fmt.Errorf("unable to upload file: %w", err)
	}
	return name, nil
}

// DownloadFile retrieves a node file attachment and writes it to a local path.
// Returns the destination path.
func (t *Tap) DownloadFile(ctx context.Context, opts DownloadFileOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return "", err
	}
	data, err := k.ReadFile(ctx, id, opts.Name)
	if err != nil {
		return "", fmt.Errorf("unable to download file %q: %w", opts.Name, err)
	}
	dest := opts.Dest
	if dest == "-" {
		if _, err := t.Runtime.Stream().Out.Write(data); err != nil {
			return "", fmt.Errorf("unable to write to stdout: %w", err)
		}
		return "", nil
	}
	if dest == "" {
		cwd, cwdErr := t.Runtime.Getwd()
		if cwdErr != nil {
			return "", fmt.Errorf("unable to determine working directory: %w", cwdErr)
		}
		dest = filepath.Join(cwd, opts.Name)
	}
	if err := t.Runtime.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("unable to write file to %q: %w", dest, err)
	}
	return dest, nil
}

// DeleteFile removes a file attachment from a node.
func (t *Tap) DeleteFile(ctx context.Context, opts DeleteFileOptions) error {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return err
	}
	if err := k.DeleteFile(ctx, id, opts.Name); err != nil {
		return fmt.Errorf("unable to delete file %q: %w", opts.Name, err)
	}
	return nil
}

// ListImages returns the names of images for a node.
func (t *Tap) ListImages(ctx context.Context, opts ListImagesOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return nil, err
	}
	names, err := k.ListImages(ctx, id)
	if err != nil {
		if errors.Is(err, keg.ErrNotSupported) {
			return nil, fmt.Errorf("keg backend does not support image storage")
		}
		return nil, err
	}
	return names, nil
}

// UploadImage reads a local file and stores it as a node image.
// Returns the stored filename.
func (t *Tap) UploadImage(ctx context.Context, opts UploadImageOptions) (string, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return "", err
	}
	exists, err := t.nodeExistsWithContent(ctx, k, id)
	if err != nil {
		return "", fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("node %s not found in %s", id.Path(), describeKeg(k))
	}
	data := opts.Data
	if data == nil {
		var err error
		data, err = t.Runtime.ReadFile(opts.FilePath)
		if err != nil {
			return "", fmt.Errorf("unable to read local file %q: %w", opts.FilePath, err)
		}
	}
	name := opts.Name
	if name == "" && opts.FilePath != "" {
		name = filepath.Base(opts.FilePath)
	}
	if name == "" {
		return "", fmt.Errorf("filename is required")
	}
	if _, err := keg.ValidateImage(data); err != nil {
		return "", fmt.Errorf("unable to upload image: %w", err)
	}
	if err := k.WriteImage(ctx, id, name, data); err != nil {
		return "", fmt.Errorf("unable to upload image: %w", err)
	}
	return name, nil
}

// DownloadImage retrieves a node image and writes it to a local path.
// Returns the destination path.
func (t *Tap) DownloadImage(ctx context.Context, opts DownloadImageOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return "", err
	}
	data, err := k.ReadImage(ctx, id, opts.Name)
	if err != nil {
		return "", fmt.Errorf("unable to download image %q: %w", opts.Name, err)
	}
	dest := opts.Dest
	if dest == "-" {
		if _, err := t.Runtime.Stream().Out.Write(data); err != nil {
			return "", fmt.Errorf("unable to write to stdout: %w", err)
		}
		return "", nil
	}
	if dest == "" {
		cwd, cwdErr := t.Runtime.Getwd()
		if cwdErr != nil {
			return "", fmt.Errorf("unable to determine working directory: %w", cwdErr)
		}
		dest = filepath.Join(cwd, opts.Name)
	}
	if err := t.Runtime.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("unable to write image to %q: %w", dest, err)
	}
	return dest, nil
}

// DeleteImage removes an image from a node.
func (t *Tap) DeleteImage(ctx context.Context, opts DeleteImageOptions) error {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return err
	}
	if err := k.DeleteImage(ctx, id, opts.Name); err != nil {
		return fmt.Errorf("unable to delete image %q: %w", opts.Name, err)
	}
	return nil
}
