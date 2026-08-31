package keg

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"gopkg.in/yaml.v3"
)

var scalarAPIRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+):\s*(.+)$`)
var dupSlashRE = regexp.MustCompile(`/+`)

// Target describes a resolved KEG repository target.
//
// The Target type is the canonical, minimal shape used by tooling. Valid
// input forms that map into Target include:
//
// - API or HTTP targets:
//   - Full URL scalars (http:// or https://).
//   - Mapping form with "url" and optional user/password/token/tokenEnv.
//     Query params like "readonly", "token", and "token-env" are honored.
//
// - Keg reference shorthand and structured form:
//   - Compact scalar shorthand "keg:@namespace/keg" (canonical; namespace
//     optional as "keg:keg"). The hub is resolved from the namespace, never
//     encoded. "keg:/@namespace/keg" is accepted as an input variant.
//   - Mapping form with "namespace" and "kegName" (and an optional "hub" pin).
//
// Fields:
//
//   - Hub: hub name when using an API style target.
//   - Url: canonical URL when provided or parsed from a scalar.
//   - Namespace/KegName: structured hub pieces used to compose API paths.
//     Namespace is the owner — a user's default namespace shares their
//     username, but organizations and other namespace types are also valid.
//     The "@" sigil is implied and not stored.
//   - BasicAuthUser: HTTP basic-auth username for URL targets. Distinct
//     from Namespace, which addresses a hub-scheme namespace owner.
//   - Password/Token/TokenEnv: credential hints. TokenEnv is preferred for
//     production usage.
//   - Readonly: when true the target was requested read only.
type Target struct {
	// Hub is an optional explicit hub pin for a keg reference. It is normally
	// empty: the hub is resolved from the Namespace via the tapper settings's
	// namespaces map. The canonical keg reference does not carry a hub.
	Hub string `yaml:"hub,omitempty"`

	// HubURL is the resolved base URL for the hub (for example
	// "https://atlas.foldwise.ai"). It is derived at resolution time from the
	// tapper settings's hubs map and is intentionally not serialized. A keg
	// reference that reaches NewKegFromTarget without it was never resolved
	// against a hub and is rejected.
	HubURL string `yaml:"-"`

	// Url is the URL for a direct HTTP(S) target.
	Url string `yaml:"url,omitempty"`

	// Namespace is the namespace owner for hub targets. The "@" sigil is
	// implied; do not store it. A user's default namespace shares their
	// username; organizations and other namespace types use the same field.
	Namespace string `yaml:"namespace,omitempty"`

	// KegName is the keg's name within the Namespace.
	KegName string `yaml:"kegName,omitempty"`

	// BasicAuthUser is the HTTP basic-auth username for URL targets.
	// Distinct from Namespace, which addresses a hub-scheme namespace owner.
	BasicAuthUser string `yaml:"basicAuthUser,omitempty"`

	Password string `yaml:"password,omitempty"`
	Token    string `yaml:"token,omitempty"`
	TokenEnv string `yaml:"tokenEnv,omitempty"`

	// Readonly specifies that the target is read only.
	Readonly bool `yaml:"readonly,omitempty"`
}

type TargetOption = func(t *Target)
type HTTPOption = func(t *Target)

const (
	SchemeHTTP        = "http"
	SchemeHTTPs       = "https"
	SchemeAlias       = "keg"
	schemeUnsupported = "unsupported"
)

// NewApi constructs a Target representing a keg API endpoint. namespace is
// the namespace owner (no "@" sigil); kegName is the keg's name within it.
func NewApi(hub string, namespace, kegName string, opts ...TargetOption) Target {
	t := Target{
		Hub:       hub,
		Namespace: namespace,
		KegName:   kegName,
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

// WithHubURL sets the resolved hub base URL on a hub Target. The tapper layer
// uses this to push the URL looked up from the configured hubs map down into
// the Target so NewKegFromTarget composes the API endpoint against it.
func WithHubURL(hubURL string) TargetOption {
	return func(t *Target) {
		t.HubURL = hubURL
	}
}

func WithBasicAuth(user, pass string) HTTPOption {
	return func(target *Target) {
		target.BasicAuthUser = user
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
//   - Canonical keg reference "keg:@namespace/keg" (namespace optional as
//     "keg:keg"); "keg:/@namespace/keg" is an accepted variant. The leading
//     "@" sigil marks the namespace and is stripped on parse so the stored
//     namespace never carries it; Path() and String() re-apply it. The hub is
//     resolved from the namespace, never encoded in the reference.
//   - HTTP/HTTPS URL scalars.
//
// Filesystem paths, file:// URLs, and every other scheme are unsupported.
// The function is permissive with common HTTP and keg-reference variants
// (extra whitespace and duplicate slashes). It returns an error for empty or
// malformed references.
func Parse(raw string) (*Target, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("empty target")
	}

	detectedScheme := detectScheme(value)
	switch detectedScheme {
	case SchemeAlias:
		// Canonical keg reference: "keg:@namespace/kegName" (namespace optional →
		// "keg:kegName"). The hub is NOT encoded — it is resolved from the
		// namespace via settings. "keg:/@ns/keg" parses equivalently. The "@" sigil
		// is stripped here; Path() and String() re-apply it. To pin a hub, use
		// the structured mapping form ({hub, namespace, name}).
		body := strings.TrimSpace(strings.TrimPrefix(value, SchemeAlias+":"))
		body = strings.TrimPrefix(body, "/")
		var t Target
		if ns, ok := strings.CutPrefix(body, "@"); ok {
			parts := strings.SplitN(ns, "/", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return nil, fmt.Errorf("malformed keg reference %q: expected keg:@namespace/keg", raw)
			}
			t.Namespace = parts[0]
			t.KegName = parts[1]
		} else {
			if body == "" {
				return nil, fmt.Errorf("malformed keg reference %q", raw)
			}
			t.KegName = body
		}
		return &t, nil
	case SchemeHTTP:
		if !strings.HasPrefix(value, "http://") {
			value = "http://" + value
		}
	case SchemeHTTPs:
		if !strings.HasPrefix(value, "https://") {
			value = "https://" + value
		}
	default:
		return nil, unsupportedTarget(value)
	}

	// Otherwise, treat as URL-like and parse with url.Parse.
	u, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse %s: %w", value, err)
	}

	if u.Host == "" {
		return nil, unsupportedTarget(value)
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
		Url:           value,
		BasicAuthUser: user,
		Password:      pass,
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

// Expand replaces environment variables in target fields.
func (k *Target) Expand(env toolkit.Env) error {
	k.Url = toolkit.ExpandEnv(env, k.Url)
	k.Hub = toolkit.ExpandEnv(env, k.Hub)
	k.HubURL = toolkit.ExpandEnv(env, k.HubURL)
	k.Password = toolkit.ExpandEnv(env, k.Password)
	k.Token = toolkit.ExpandEnv(env, k.Token)
	k.TokenEnv = toolkit.ExpandEnv(env, k.TokenEnv)
	return nil
}

// UnmarshalYAML accepts either a remote URL or keg-reference scalar, or a
// mapping node that decodes into the full Target struct. Mapping form may
// include structured hub/namespace/keg fields.
//
// Filesystem fields and file:// scalars are rejected.
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
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "file" {
				return unsupportedTarget(node.Content[i+1].Value)
			}
		}
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
			default:
				return unsupportedTarget(k.Url)
			}
		}
		if k.Scheme() == schemeUnsupported {
			return unsupportedTarget(k.String())
		}
		return nil
	default:
		return fmt.Errorf("unsupported yaml node kind %d for KegUrl", node.Kind)
	}
}

// String returns a human-friendly representation of the target. A keg
// reference renders in the canonical "keg:@namespace/kegName" form (namespace
// omitted as "keg:kegName" when unset). The hub is NOT part of the reference —
// it is resolved from the namespace via settings — so the scheme is always the
// real "keg" scheme, never a hub name. HTTP targets return the canonical URL.
func (kt *Target) String() string {
	switch kt.Scheme() {
	case SchemeAlias:
		if kt.Namespace != "" {
			return SchemeAlias + ":@" + kt.Namespace + "/" + kt.KegName
		}
		return SchemeAlias + ":" + kt.KegName
	case SchemeHTTP, SchemeHTTPs:
		return kt.Url
	default:
		u, _ := url.Parse(kt.Url)
		return u.String()
	}
}

// Scheme reports the inferred scheme for this Target value. A keg reference
// (identified by a Namespace owner, KegName, or explicit Hub pin) implies the
// keg scheme. Otherwise we classify the URL.
func (kt *Target) Scheme() string {
	if kt.Hub != "" || kt.Namespace != "" || kt.KegName != "" {
		return SchemeAlias
	}
	return detectScheme(kt.Url)
}

// Host returns the hostname portion for HTTP/HTTPS targets.
func (kt *Target) Host() string {
	u, _ := url.Parse(kt.Url)
	return u.Hostname()
}

func (kt *Target) Port() string {
	u, _ := url.Parse(kt.Url)
	return u.Port()
}

func (kt *Target) Path() string {
	switch kt.Scheme() {
	case SchemeAlias:
		// Re-apply the @ sigil on the namespace; the stored value never carries it.
		if kt.Namespace == "" {
			return kt.KegName
		}
		return "@" + kt.Namespace + "/" + kt.KegName
	default:
		u, _ := url.Parse(kt.Url)
		return u.Path
	}
}

// detectScheme classifies raw into a supported remote scheme.
func detectScheme(raw string) string {
	if raw == "" {
		return schemeUnsupported
	}
	// The keg scheme is the only "<scheme>:<rest>" scalar we own. A prefix that
	// is not "keg" is not a keg reference — there is no
	// "<hub>:@ns/keg" shorthand.
	if m := scalarAPIRE.FindStringSubmatch(raw); m != nil && m[1] == SchemeAlias {
		return SchemeAlias
	}

	// Try to parse as a URL first.
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		switch u.Scheme {
		case "http":
			return SchemeHTTP
		case "https":
			return SchemeHTTPs
		default:
			return schemeUnsupported
		}
	}

	// Avoid classifying absolute or relative file paths as hosts.
	if strings.HasPrefix(raw, "/") ||
		strings.HasPrefix(raw, ".") ||
		strings.HasPrefix(raw, "..") ||
		strings.HasPrefix(raw, "./") ||
		strings.HasPrefix(raw, "../") ||
		strings.HasPrefix(raw, "~") {
		return schemeUnsupported
	}

	// Check for implicit http website.
	head := getHostLikePath(raw)
	if head != "" && strings.Contains(head, ".") {
		return SchemeHTTPs
	}

	// Windows drive letter like "C:" is an unsupported filesystem path.
	if len(raw) >= 2 && raw[1] == ':' && ((raw[0] >= 'A' && raw[0] <= 'Z') ||
		(raw[0] >= 'a' && raw[0] <= 'z')) {
		return schemeUnsupported
	}

	return schemeUnsupported
}

func unsupportedTarget(raw string) error {
	return fmt.Errorf("unsupported target %q: only keg references and HTTP(S) endpoints are supported: %w", raw, ErrNotSupported)
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
