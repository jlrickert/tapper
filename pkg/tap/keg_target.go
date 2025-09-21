package tap

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	std "github.com/jlrickert/go-std/pkg"
)

type KegTarget struct {
	Schema   string
	Path     string
	Readonly bool
}

func ParseKegTarget(ctx context.Context, raw string) (*KegTarget, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty target")
	}

	value := std.ExpandEnv(ctx, raw)
	value, err := std.ExpandPath(ctx, value)
	if err != nil {
		return nil, fmt.Errorf("unable to expand %s: %w", value, err)
	}

	value = filepath.Clean(value)

	// First try to parse as a URL
	u, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse %s: %w", value, err)
	}

	// scheme present
	kt := KegTarget{
		Schema:   u.Scheme,
		Path:     u.Path,
		Readonly: false,
	}

	if q := u.Query().Get("readonly"); q != "" {
		q = strings.ToLower(q)
		if q == "1" || q == "true" || q == "yes" {
			kt.Readonly = true
		}
	}

	return &kt, nil
}

// String returns a canonical URI string for the KegTarget.
// - file paths are returned as file://<abs-path>
// - other schemes return kt.Uri (assumed to be a full URL)
// - if Readonly is true and the URI has no readonly query param, readonly=true is appended
func (kt *KegTarget) String() string {
	u := url.URL{Scheme: kt.Schema, Path: kt.Path}
	return u.String()
}
