package kegurl

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"gopkg.in/yaml.v3"
)

var scalarApiRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+):\s*(.+)$`)
var dupSlashRE = regexp.MustCompile(`/+`)

// Target describes a resolved KEG repository target.
//
// Schema is the URI scheme when the target was written as a URL (for example
// "file", "http", "https"). Path is the URL path component or an absolute
// filesystem path when the target was supplied as a file path.
//
// The Target type is the canonical, minimal shape used by tooling. Valid
// input forms that map into Target include:
//
// - File targets:
//   - Scalar file paths such as "/abs/path", "./rel/path", "../rel/path",
//     "~/path", or Windows drive paths.
//   - Mapping form with a "file" key. File values are cleaned with
//     filepath.Clean; Expand will attempt to expand a leading tilde.
//
// - API or HTTP targets:
//   - Full URL scalars (http:// or https://).
//   - Mapping form with "url" and optional user/password/token/tokenEnv.
//     Query params like "readonly", "token", and "token-env" are honored.
//
// - Hub API shorthand and structured form:
//   - Compact scalar shorthand "hub:@user/keg" (canonical), with
//     "hub:user/keg" and "hub:/@user/keg" accepted as input variants.
//   - Mapping form with "hub", "user", and "keg" fields.
//
// Fields:
//
//   - File: filesystem path for a local keg target.
//   - Hub: hub name when using an API style target.
//   - Url: canonical URL when provided or parsed from a scalar.
//   - User/Keg: structured hub pieces used to compose API paths.
//   - Password/Token/TokenEnv: credential hints. TokenEnv is preferred for
//     production usage.
//   - Readonly: when true the target was requested read only.
type Target struct {
	// File is the file to use when the Target is a file
	File string `yaml:"file,omitempty"`

	// Hub is the hub name used to resolve the User and Keg into an API target
	Hub string `yaml:"hub,omitempty"`

	// Url is the url for the target when represented as a scalar or explicit
	// mapping value. Url is used when the target was http/s, git, ssh, etc
	Url string `yaml:"url,omitempty"`

	Memory bool

	// Other options
	User     string `yaml:"user,omitempty"`
	Keg      string `yaml:"keg,omitempty"`
	Password string `yaml:"password,omitempty"`
	Token    string `yaml:"token,omitempty"`
	TokenEnv string `yaml:"tokenEnv,omitempty"`

	// Readonly specifies in the target is readonly. Only api and file are
	// writable
	Readonly bool `yaml:"readonly,omitempty"`
}

type TargetOption = func(t *Target)
type HTTPOption = func(t *Target)

const (
	SchemeMemory = "memory"
	SchemeFile   = "file"
	SchemeGit    = "git"
	SchemeSSH    = "ssh"
	SchemeHTTP   = "http"
	SchemeHTTPs  = "https"
	SchemeAlias  = "keg"
	SchemeHub    = "hub"
	SchemeS3     = "s3"
)

// NewApi constructs a Target representing a keg API endpoint.
func NewApi(hub string, user, keg string, opts ...TargetOption) Target {
	t := Target{
		Hub:  hub,
		User: user,
		Keg:  keg,
	}
	for _, o := range opts {
		o(&t)
	}
	return t
}

// NewFile constructs a file target for a local filesystem path. The path is
// cleaned using filepath.Clean.
func NewFile(path string, opts ...TargetOption) Target {
	p := filepath.Clean(path)
	t := Target{
		File: p,
	}
	for _, o := range opts {
		o(&t)
	}
	return t
}

func NewMemory(kegalias string, opts ...TargetOption) Target {
	t := Target{
		Memory: true,
		Keg:    kegalias,
	}
	for _, o := range opts {
		o(&t)
	}
	return t
}

func WithReadonly() TargetOption {
	return func(t *Target) {
		t.Readonly = true
	}
}

func WithBasicAuth(user, pass string) HTTPOption {
	return func(target *Target) {
		target.User = user
		target.Password = pass
	}
}

func WithToken(token string) HTTPOption {
	return func(target *Target) {
		target.Token = token
	}
}

// Parse parses a user-supplied target scalar into a Target.
//
// Accepted input forms:
//   - File paths (absolute, ./, ../, ~, Windows drive). These produce File
//     targets.
//   - Compact hub shorthand "hub:@user/keg" (canonical) or its accepted
//     variants "hub:user/keg" and "hub:/@user/keg". The leading "@"
//     sigil marks the namespace; it is stripped on parse so the stored
//     User never carries it, and re-applied by Path() and String().
//   - HTTP/HTTPS URL scalars.
//   - Any URL-like scalar parsed by url.Parse.
//
// The function is permissive with common variants (extra whitespace, duplicate
// slashes). It returns an error for empty or malformed shorthand inputs.
func Parse(raw string) (*Target, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("empty target")
	}

	detectedScheme := detectScheme(value)
	switch detectedScheme {
	case SchemeFile:
		t := Target{
			File: filepath.Clean(strings.TrimPrefix(value, "file://")),
		}
		return &t, nil
	case SchemeHub:
		// Accept compact hub shorthand. Canonical form is
		// "hub:@user/keg"; "hub:user/keg" and "hub:/@user/keg" parse
		// equivalently. The "@" sigil is stripped here so the stored
		// User never carries it; Path() and String() re-apply it.
		if m := scalarApiRE.FindStringSubmatch(value); m != nil {
			hub := m[1]
			rest := strings.TrimSpace(m[2])
			rest = strings.TrimPrefix(rest, "/")
			parts := strings.SplitN(rest, "/", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return nil, fmt.Errorf("malformed target shorthand: %s", raw)
			}
			user := strings.TrimPrefix(parts[0], "@")
			if user == "" {
				return nil, fmt.Errorf("malformed target shorthand: %s", raw)
			}
			keg := parts[1]
			t := Target{
				Hub:  hub,
				User: user,
				Keg:  keg,
			}
			return &t, nil
		}
	case SchemeHTTP:
		if !strings.HasPrefix(value, "http://") {
			value = "http://" + value
		}
	case SchemeHTTPs:
		if !strings.HasPrefix(value, "https://") {
			value = "https://" + value
		}
	}

	// Otherwise, treat as URL-like and parse with url.Parse.
	u, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse %s: %w", value, err)
	}

	// Normalize path component by collapsing duplicate slashes.
	u.Path = dupSlashRE.ReplaceAllString(u.Path, "/")

	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		if p, ok := u.User.Password(); ok {
			pass = p
		}
	}

	kt := Target{
		Url:      value,
		User:     user,
		Password: pass,
	}

	// Honor common truthy query values for readonly.
	if q := u.Query().Get("readonly"); q != "" {
		q = strings.ToLower(q)
		if q == "1" || q == "true" || q == "yes" {
			kt.Readonly = true
		}
	}

	if q := u.Query().Get("token"); q != "" {
		kt.Token = strings.TrimSpace(q)
	}

	if q := u.Query().Get("token-env"); q != "" {
		kt.TokenEnv = strings.TrimSpace(q)
	}

	return &kt, nil
}

// Expand replaces environment variables and expands a leading tilde in File
// and Hub-related fields. It uses std.ExpandEnv and std.ExpandPath so behavior
// matches the rest of the code base.
//
// Errors from ExpandPath are collected and returned as a joined error so callers
// can see expansion issues.
func (k *Target) Expand(env toolkit.Env) error {
	var errs []error

	expand := func(value string) string {
		va := toolkit.ExpandEnv(env, value)
		vb, err := toolkit.ExpandPath(env, va)
		if err != nil {
			errs = append(errs, err)
			return va
		}
		return vb
	}
	k.File = expand(k.File)
	k.Url = toolkit.ExpandEnv(env, k.Url)
	k.Hub = toolkit.ExpandEnv(env, k.Hub)
	k.Password = toolkit.ExpandEnv(env, k.Password)
	k.Token = toolkit.ExpandEnv(env, k.Token)
	k.TokenEnv = toolkit.ExpandEnv(env, k.TokenEnv)
	return errors.Join(errs...)
}

// UnmarshalYAML accepts either a scalar string (the URL or shorthand or file)
// or a mapping node that decodes into the full Target struct. Mapping form may
// include structured hub/user/keg or an explicit file field.
//
// When a scalar is provided the value is parsed via Parse which recognizes
// file scalars, shorthand hub forms, and URL scalars.
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
		kt, err := Parse(s)
		if err != nil {
			return err
		}
		*k = *kt
		return nil
	case yaml.MappingNode:
		type tmp Target
		var t tmp
		if err := node.Decode(&t); err != nil {
			return fmt.Errorf("decode keg url mapping: %w", err)
		}
		*k = Target(t)
		if k.Url != "" {
			switch detectScheme(k.Url) {
			case SchemeHTTP:
				if !strings.HasPrefix(k.Url, "http://") {
					k.Url = "http://" + k.Url
				}
			case SchemeHTTPs:
				if !strings.HasPrefix(k.Url, "https://") {
					k.Url = "https://" + k.Url
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported yaml node kind %d for KegUrl", node.Kind)
	}
}

// String returns a human-friendly representation of the target. For hub
// API form it returns the canonical "hub:@user/keg" form. For file it
// returns the file path. For HTTP targets it returns the canonical Url.
func (kt *Target) String() string {
	switch kt.Scheme() {
	case SchemeFile:
		return kt.File
	case SchemeHub:
		return kt.Hub + ":@" + kt.User + "/" + kt.Keg
	case SchemeHTTP, SchemeHTTPs:
		return kt.Url
	default:
		u, _ := url.Parse(kt.Url)
		return u.String()
	}
}

// Scheme reports the inferred scheme for this Target value. Hub implies the
// keg API scheme. File implies a local file scheme. Otherwise we fall back to
// detectScheme on the Url.
func (kt *Target) Scheme() string {
	if kt.File != "" {
		return SchemeFile
	}
	if kt.Hub != "" {
		return SchemeHub
	}
	return detectScheme(kt.Url)
}

// Host returns the hostname portion for HTTP/HTTPS targets. For file targets
// it returns an empty string.
func (kt *Target) Host() string {
	switch kt.Scheme() {
	case SchemeFile:
		return ""
	case SchemeHTTP, SchemeHTTPs:
		u, _ := url.Parse(kt.Url)
		return u.Hostname()
	default:
		u, _ := url.Parse(kt.Url)
		return u.Hostname()
	}
}

func (kt *Target) Port() string {
	switch kt.Scheme() {
	case SchemeFile:
		return ""
	default:
		u, _ := url.Parse(kt.Url)
		return u.Port()
	}
}

func (kt *Target) Path() string {
	switch kt.Scheme() {
	case SchemeFile:
		return filepath.Clean(kt.File)
	case SchemeHub:
		// Preserve a leading @ on user when composing a path for display.
		return filepath.Join("@"+kt.User, kt.Keg)
	default:
		u, _ := url.Parse(kt.Url)
		return u.Path
	}
}

// detectScheme returns SchemeHTTPs or SchemeFile based on the form of raw.
// It recognizes explicit http/https/file schemes and the compact hub
// shorthand form. Typical filesystem path forms are classified as SchemeFile.
func detectScheme(raw string) string {
	if raw == "" {
		return SchemeFile
	}
	if m := scalarApiRE.FindStringSubmatch(raw); m != nil {
		rest := strings.TrimSpace(m[2])
		rest = strings.TrimPrefix(rest, "/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return SchemeHub
		}
	}

	// Try to parse as a URL first. This catches explicit schemes like
	// "https://" or "file://".
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		switch u.Scheme {
		case "http":
			return SchemeHTTP
		case "https":
			return SchemeHTTPs
		case "file":
			return SchemeFile
		}
	}

	// Avoid classifying absolute or relative file paths as hosts.
	if strings.HasPrefix(raw, "/") ||
		strings.HasPrefix(raw, ".") ||
		strings.HasPrefix(raw, "..") ||
		strings.HasPrefix(raw, "./") ||
		strings.HasPrefix(raw, "../") ||
		strings.HasPrefix(raw, "~") {
		return SchemeFile
	}

	// Check for implicit http website.
	head := getHostLikePath(raw)
	if head != "" && strings.Contains(head, ".") {
		return SchemeHTTPs
	}

	// Windows drive letter like "C:" should be treated as file.
	if len(raw) >= 2 && raw[1] == ':' && ((raw[0] >= 'A' && raw[0] <= 'Z') ||
		(raw[0] >= 'a' && raw[0] <= 'z')) {
		return SchemeFile
	}

	// Fallback: treat as a local file path.
	return SchemeFile
}

func getHostLikePath(raw string) string {
	// Look at the host-like part before the first slash.
	firstSlash := strings.IndexRune(raw, '/')
	if firstSlash == -1 {
		return raw
	} else if firstSlash > 0 {
		return raw[:firstSlash]
	}
	// If no head could be extracted, return empty.
	return ""
}
