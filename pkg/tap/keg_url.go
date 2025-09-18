package tap

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	std "github.com/jlrickert/go-std/pkg"
)

// KegUrl describes the repo-local "keg" hint shape.
type KegUrl struct {
	// http, https, ssh, git, api, file, postgresql
	Type     string `yaml:"type,omitempty"`
	Value    string `yaml:"link,omitempty"`
	Readonly bool   `yaml:"readonly,omitempty"`
}

func ParseKegTarget(raw string) (KegUrl, error) {
	if raw == "" {
		return KegUrl{}, fmt.Errorf("empty target")
	}

	// First try to parse as a URL
	u, err := url.Parse(raw)
	if err == nil && u.Scheme != "" {
		// scheme present
		kt := KegUrl{
			Type: u.Scheme,
		}

		if u.Scheme == "file" {
			// For file:// URIs, prefer the path component as the local path.
			// On Windows url.Path might start with a leading '/' before the drive letter.
			path := u.Path
			if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") && len(path) > 2 && path[2] == ':' {
				path = path[1:] // remove leading slash
			}
			kt.Value = path
		} else {
			// For non-file schemes keep the original string so query/authority preserve
			kt.Value = raw
		}

		if q := u.Query().Get("readonly"); q != "" {
			q = strings.ToLower(q)
			if q == "1" || q == "true" || q == "yes" {
				kt.Readonly = true
			}
		}
		return kt, nil
	}

	// Treat as a file path
	abs, err := filepath.Abs(raw)
	if err != nil {
		// fallback to raw cleaned
		abs = filepath.Clean(raw)
	}
	return KegUrl{
		Type:  "file",
		Value: abs,
	}, nil
}

// ToURI returns a canonical URI string for the KegTarget.
// - file paths are returned as file://<abs-path>
// - other schemes return kt.Uri (assumed to be a full URL)
// - if Readonly is true and the URI has no readonly query param, readonly=true is appended
func (kt *KegUrl) ToURI() string {
	if kt.Type == "file" {
		// ensure path is absolute/clean
		p := kt.Value
		if !filepath.IsAbs(p) {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
		}
		// Windows: ensure leading slash for file URI path component is handled by url.URL
		u := &url.URL{
			Scheme: "file",
			Path:   p,
		}
		if kt.Readonly {
			q := url.Values{}
			q.Set("readonly", "true")
			u.RawQuery = q.Encode()
		}
		return u.String()
	}

	// for non-file types try to parse so we can append readonly if needed
	if kt.Value != "" {
		u, err := url.Parse(kt.Value)
		if err != nil {
			// if parsing fails, return stored Uri (best-effort)
			return kt.Value
		}
		if kt.Readonly {
			q := u.Query()
			if q.Get("readonly") == "" {
				q.Set("readonly", "true")
				u.RawQuery = q.Encode()
			}
		}
		return u.String()
	}

	// fallback: produce scheme:// with empty uri
	if kt.Type != "" {
		u := &url.URL{Scheme: kt.Type}
		if kt.Readonly {
			q := url.Values{}
			q.Set("readonly", "true")
			u.RawQuery = q.Encode()
		}
		return u.String()
	}
	return ""
}

// Normalize expands env vars for file paths and makes them absolute/clean for Type == "file".
func (kt *KegUrl) Normalize(ctx context.Context) error {
	env := std.EnvFromContext(ctx)
	if kt == nil {
		return nil
	}
	if kt.Type == "file" {
		kt.Value = std.ExpandEnv(env, kt.Value)
		abs, err := filepath.Abs(kt.Value)
		if err != nil {
			// keep cleaned path if Abs fails
			kt.Value = filepath.Clean(kt.Value)
			return err
		}
		kt.Value = filepath.Clean(abs)
	} else {
		// expand env vars in other URIs too
		kt.Value = std.ExpandEnv(env, kt.Value)
	}
	return nil
}

func (kt *KegUrl) IsLocal() bool {
	return kt.Type == "file"
}
func (kt *KegUrl) IsRemote() bool {
	return !kt.IsLocal()
}
