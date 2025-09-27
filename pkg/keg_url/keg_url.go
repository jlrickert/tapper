package kegurl

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	std "github.com/jlrickert/go-std/pkg"
	"gopkg.in/yaml.v3"
)

// Target describes a resolved KEG repository target.
//
// Schema is the URI scheme when the target was written as a URL (for example
// "file", "http", "s3", "git", "https", "kegapi"). Path is the URL path
// component or an absolute file system path when the target was supplied as a
// file path. Readonly indicates whether the target was explicitly requested
// read only.
type Target struct {
	// schema is the canonical scheme for the target. Examples include
	// "file", "https", "git", and "kegapi". It is typically inferred from the
	// original input when possible.
	schema string

	// url is the raw URL or original input string. For network schemas this is
	// a URL; for file targets it is a cleaned filesystem path.
	url string `yaml:"url,omitempty"`

	// api holds the API base when the schema refers to a keg api endpoint.
	api string `yaml:"api,omitempty"`

	// path is the URL path component. For file targets it is an absolute,
	// cleaned filesystem path. For network targets it is the path portion of
	// the URL.
	path string `yaml:"path,omitempty"`

	// readonly indicates whether the target should be treated as read only.
	// When missing the effective default may be true for some schemas. Only
	// file and kegapi schemas may be writable.
	readonly bool `yaml:"readonly,omitempty"`

	// host is the network host portion of the target when applicable.
	host string `yaml:"host,omitempty"`

	// port is the network port as a string when present.
	port string `yaml:"port,omitempty"`

	// user is the username to be used for basic auth or similar auth schemes.
	user string `yaml:"user,omitempty"`

	// password is the password to use for basic auth.
	password string `yaml:"password,omitempty"`

	// token is an inline access token. Prefer tokenEnv for storing tokens in
	// the environment where possible.
	token string `yaml:"token,omitempty"`

	// tokenEnv names an environment variable from which to read a token at
	// runtime. If present the code should prefer the environment value over the
	// inline token.
	tokenEnv string `yaml:"tokenEnv,omitempty"`
}

type TargetOption = func(t *Target)
type HTTPOption = func(t *Target)

const (
	SchemaFile  = "file"
	SchemaGit   = "git"
	SchemaSSH   = "ssh"
	SchemaHTTP  = "http"
	SchemaHTTPs = "https"
	SchemaAlias = "keg"
	SchemaApi   = "kegapi"
	SchemaS3    = "s3"
)

// NewApi constructs a Target representing a keg API endpoint.
func NewApi(urlStr string, token string, opts ...TargetOption) Target {
	t := Target{
		schema: SchemaApi,
		url:    urlStr,
		token:  token,
		api:    urlStr,
	}
	for _, o := range opts {
		o(&t)
	}
	return t
}

// NewGithub constructs a github.com HTTPS target for the given user/repo.
func NewGithub(user string, repo string, opts ...TargetOption) Target {
	path := "/" + strings.TrimPrefix(user, "/") + "/" +
		strings.TrimPrefix(repo, "/")
	u := url.URL{Scheme: "https", Host: "github.com", Path: path}
	t := Target{
		schema:   SchemaHTTPs,
		url:      u.String(),
		path:     path,
		host:     "github.com",
		readonly: true,
	}
	for _, o := range opts {
		o(&t)
	}
	return t
}

// NewBitbucket constructs a bitbucket.org HTTPS target for the provided path.
func NewBitbucket(path string) Target {
	p := path
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "https", Host: "bitbucket.org", Path: p}
	return Target{
		schema:   SchemaHTTPs,
		url:      u.String(),
		path:     p,
		host:     "bitbucket.org",
		readonly: true,
	}
}

// NewGit constructs a git:// target for the given host and path.
func NewGit(host, path string) Target {
	p := path
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "git", Host: host, Path: p}
	return Target{
		schema:   SchemaGit,
		url:      u.String(),
		path:     p,
		host:     host,
		readonly: true,
	}
}

// NewFile constructs a file target for a local filesystem path.
func NewFile(path string, opts ...TargetOption) Target {
	p := filepath.Clean(path)
	t := Target{
		schema: SchemaFile,
		path:   p,
		url:    p,
	}
	for _, o := range opts {
		o(&t)
	}
	return t
}

func WithReadonly() TargetOption {
	return func(t *Target) {
		t.readonly = true
	}
}

func WithBasicAuth(user, pass string) HTTPOption {
	return func(target *Target) {
		target.user = user
		target.password = pass
	}
}

func WithToken(token string) HTTPOption {
	return func(target *Target) {
		target.token = token
	}
}

// Parse parses a user-supplied target string into a Keg Target.
//
// Behavior:
//   - The raw string has environment variables expanded via std.ExpandEnv and
//     leading tildes expanded via std.ExpandPath.
//   - The value is cleaned with filepath.Clean.
//   - We first attempt to parse the cleaned value as a URL using url.Parse.
//     When a scheme is present it is used as the target Schema and the parsed
//     path is used for Path.
//   - When the input looks like a plain file path (no URL scheme but an
//     absolute path in the filesystem), the Path will hold the file path and
//     Schema will be empty. Callers may set Schema to "file" if they prefer an
//     explicit file URI form.
//   - The query parameter "readonly" is honored. Recognized true values are
//     "1", "true", and "yes" in a case-insensitive comparison. If present and
//     true, Readonly is set to true.
//
// Returns an error when the input is empty or when path expansion fails.
//
// Examples:
//
//	"https://example.com/user/repo"
//	  - Schema: "https"
//	  - Host: example.com
//	  - Path: /user/repo
//
//	"git@github.com:owner/repo.git"
//	  - Schema: "git" (ssh form will be parsed by adding an ssh:// prefix)
//	  - Host: github.com
//	  - Path: /owner/repo.git
//
//	"github.com/owner/repo"
//	  - Schema: "https" (detected as host-like)
//	  - Host: github.com
//	  - Path: /owner/repo
//
//	"/home/me/projects/kegalias"
//	  - Schema: "file" (local path)
//	  - Path: /home/me/projects/keg
//
//	"https://api.example.com/kegalias?readonly=true&token=abc123"
//	  - Schema: "https"
//	  - Path: kegalias
//	  - Readonly: true
//	  - Token: "abc123"
func Parse(ctx context.Context, raw string) (*Target, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty target")
	}

	// Expand environment variables and tildes.
	value := std.ExpandEnv(ctx, raw)
	value, err := std.ExpandPath(ctx, value)
	if err != nil {
		return nil, fmt.Errorf("unable to expand %s: %w", value, err)
	}

	// Keep a clean copy for parsing; do not lose the original host/path shape.
	value = filepath.Clean(value)

	// Try to parse as a URL. url.Parse will accept absolute file paths and
	// yield an empty Scheme with the path populated.
	u, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse %s: %w", value, err)
	}

	// If the input had no scheme, attempt to detect likely schema and, when
	// appropriate, reparse using an explicit scheme so url.Parse fills Host.
	schema := u.Scheme
	if schema == "" {
		schema = detectSchema(value)
		// Only reparse when we detected a network-like schema.
		switch schema {
		case SchemaGit:
			// If the form is "git@host:owner/repo" use ssh scheme for parsing.
			if strings.HasPrefix(value, "git@") {
				if nu, perr := url.Parse("ssh://" + value); perr == nil {
					u = nu
				}
			} else {
				if nu, perr := url.Parse("git://" + value); perr == nil {
					u = nu
				}
			}
		case SchemaHTTP, SchemaHTTPs:
			// Prefer https when we detected SchemaHTTPs.
			prefix := "http://"
			if schema == SchemaHTTPs {
				prefix = "https://"
			}
			if nu, perr := url.Parse(prefix + value); perr == nil {
				u = nu
			}
		}
	}

	paswd, _ := u.User.Password()
	kt := Target{
		url:      u.String(),
		schema:   schema,
		path:     u.Path,
		host:     u.Hostname(),
		port:     u.Port(),
		user:     u.User.Username(),
		password: paswd,
	}

	// Honor common truthy query values for readonly.
	if q := u.Query().Get("readonly"); q != "" {
		q = strings.ToLower(q)
		if q == "1" || q == "true" || q == "yes" {
			kt.readonly = true
		}
	}

	if q := u.Query().Get("token"); q != "" {
		q = strings.TrimSpace(q)
		kt.token = q
	}

	if q := u.Query().Get("token-env"); q != "" {
		q = strings.TrimSpace(q)
		kt.tokenEnv = q
	}

	return &kt, nil
}

func (kt *Target) User() string     { return kt.user }
func (kt *Target) Pass() string     { return kt.password }
func (kt *Target) Schema() string   { return kt.schema }
func (kt *Target) Path() string     { return kt.path }
func (kt *Target) Host() string     { return kt.host }
func (kt *Target) Port() string     { return kt.port }
func (kt *Target) Hostname() string { return kt.host + ":" + kt.Port() }

// String returns a canonical representation of the target as a URI-like string.
//
// Notes:
//   - When Schema is "file" callers can expect a file URI style value. If Schema
//     is empty the returned string will be the URL form built from Schema and
//     Path which may be just the path component for local file paths.
//   - If Readonly was set but the underlying URI contains no readonly query
//     parameter, callers can append one if they need an explicit readonly form.
func (kt *Target) String() string {
	u := url.URL{
		Scheme: kt.schema,
		Host:   kt.host,
		Path:   kt.path,
	}
	if kt.port != "" {
		u.Host = kt.host + ":" + kt.port
	} else {
		u.Host = kt.host
	}
	if kt.user != "" && kt.password != "" {
		u.User = url.UserPassword(kt.user, kt.password)
	}
	// url.Query returns a copy, so mutate the values and set RawQuery.
	q := u.Query()
	if kt.token != "" {
		q.Add("token", kt.token)
	}
	if kt.tokenEnv != "" {
		q.Add("token-env", kt.tokenEnv)
	}
	if kt.readonly {
		q.Add("readonly", "true")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// UnmarshalYAML accepts either a scalar string (the URL) or a mapping node that
// decodes into the full Keg Target struct.
func (k *Target) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return fmt.Errorf("decode keg url scalar: %w", err)
		}
		kt, err := Parse(context.Background(), s)
		if err != nil {
			return err
		}
		// Copy parsed fields into receiver so callers get a fully populated Target.
		k.schema = kt.schema
		k.url = kt.url
		k.path = kt.path
		k.readonly = kt.readonly
		k.host = kt.host
		k.port = kt.port
		k.user = kt.user
		k.password = kt.password
		k.token = kt.token
		k.tokenEnv = kt.tokenEnv
		k.api = kt.api
		return nil
	case yaml.MappingNode:
		// decode into a temporary alias to avoid recursion issues
		type tmp Target
		var t tmp
		if err := node.Decode(&t); err != nil {
			return fmt.Errorf("decode keg url mapping: %w", err)
		}
		*k = Target(t)
		return nil
	default:
		return fmt.Errorf("unsupported yaml node kind %d for KegUrl", node.Kind)
	}
}

// MarshalYAML emits a scalar string when only the URL is set and all other
// fields are zero values. Otherwise it emits a mapping with fields.
func (k Target) MarshalYAML() (any, error) {
	onlyURL := k.url != "" &&
		!k.readonly &&
		k.user == "" &&
		k.password == "" &&
		k.token == "" &&
		k.tokenEnv == "" &&
		k.host == "" &&
		k.path == ""
	if onlyURL {
		return k.url, nil
	}
	// return struct mapping
	return struct {
		URL      string `yaml:"url,omitempty"`
		Readonly bool   `yaml:"readonly,omitempty"`
		User     string `yaml:"user,omitempty"`
		Password string `yaml:"password,omitempty"`
		Token    string `yaml:"token,omitempty"`
		TokenEnv string `yaml:"tokenEnv,omitempty"`
	}{
		URL:      k.url,
		Readonly: k.readonly,
		User:     k.user,
		Password: k.password,
		Token:    k.token,
		TokenEnv: k.tokenEnv,
	}, nil
}

// detectSchema returns one of SchemaFile, SchemaGit, or SchemaHTTPs based on
// the form of the provided raw string.
//
// Examples the function handles:
// - "github.com/jlrickert/project"   -> SchemaGit
// - "jlrickert.me/@user/project"     -> SchemaHTTPs
// - "repos/keg"                      -> SchemaFile
func detectSchema(raw string) string {
	// Try to parse as a URL first. This catches explicit schemes like
	// "https://", "git://", "file://", and "ssh://".
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		switch u.Scheme {
		case "http", "https":
			return SchemaHTTP
		case "git", "ssh":
			return SchemaGit
		case "file":
			return SchemaFile
		}
	}

	// Common git indicators when scheme is omitted.
	if strings.HasSuffix(raw, ".git") ||
		strings.HasPrefix(raw, "git@") ||
		strings.Contains(raw, "bitbucket.org") ||
		strings.Contains(raw, "github.com") {
		return SchemaGit
	}

	// Host-like patterns without scheme, e.g. "example.com/user/proj".
	// Only treat as HTTP(S) when the input does not look like a file path and
	// the first segment (before the first "/") contains a dot.
	if raw != "" {
		// Avoid classifying absolute or relative file paths as hosts.
		if strings.HasPrefix(raw, "/") ||
			strings.HasPrefix(raw, "./") ||
			strings.HasPrefix(raw, "../") ||
			strings.HasPrefix(raw, "~") {
			return SchemaFile
		}

		// Windows drive letter like "C:" should be treated as file.
		if len(raw) >= 2 && raw[1] == ':' && ((raw[0] >= 'A' && raw[0] <= 'Z') ||
			(raw[0] >= 'a' && raw[0] <= 'z')) {
			return SchemaFile
		}

		// Look at the host-like part before the first slash.
		firstSlash := strings.IndexRune(raw, '/')
		var head string
		if firstSlash == -1 {
			head = raw
		} else if firstSlash > 0 {
			head = raw[:firstSlash]
		} else {
			head = ""
		}

		// If the head part contains a dot and is not empty, treat as https.
		if head != "" && strings.Contains(head, ".") {
			return SchemaHTTPs
		}
	}

	// Fallback: treat as a local or repo-file path.
	return SchemaFile
}
