package keg

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	avifdecoder "github.com/gen2brain/avif"
	heicdecoder "github.com/gen2brain/heic"
	_ "golang.org/x/image/webp"
)

var ErrInvalidImage = errors.New("invalid image")

// ValidateImage decodes data enough to prove it is one of Tapper's supported
// image formats. It preserves caller bytes; it does not transcode or normalize.
func ValidateImage(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("%w: empty payload", ErrInvalidImage)
	}
	cfg, format, err := decodeImageConfig(data)
	if err != nil {
		return "", fmt.Errorf("%w: could not decode image", ErrInvalidImage)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", fmt.Errorf("%w: invalid dimensions", ErrInvalidImage)
	}
	switch strings.ToLower(format) {
	case "png", "jpeg", "gif", "webp", "avif", "heic":
		return strings.ToLower(format), nil
	default:
		return "", fmt.Errorf("%w: unsupported format %q", ErrInvalidImage, format)
	}
}

func decodeImageConfig(data []byte) (image.Config, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		return cfg, format, nil
	}

	// HEIF family files are ISO BMFF containers. Some decoders register only
	// their most common brands with image.Decode, so use a brand-gated fallback
	// before rejecting otherwise-valid phone images.
	if hasBMFFBrand(data, "avif", "avis") {
		cfg, err := avifdecoder.DecodeConfig(bytes.NewReader(data))
		return cfg, "avif", err
	}
	if hasBMFFBrand(data, "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1") {
		cfg, err := heicdecoder.DecodeConfig(bytes.NewReader(data))
		return cfg, "heic", err
	}
	return image.Config{}, "", err
}

func hasBMFFBrand(data []byte, brands ...string) bool {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return false
	}
	brandSet := make(map[string]bool, len(brands))
	for _, brand := range brands {
		brandSet[brand] = true
	}
	limit := len(data)
	if limit > 256 {
		limit = 256
	}
	for offset := 8; offset+4 <= limit; offset += 4 {
		if brandSet[string(data[offset:offset+4])] {
			return true
		}
	}
	return false
}
