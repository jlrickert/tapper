package tapper

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// WebURL kinds: what a Hub web page shows.
const (
	WebURLKindNamespace = "namespace"
	WebURLKindKeg       = "keg"
	WebURLKindNode      = "node"
	WebURLKindFlight    = "flight"
)

// WebURLOptions configures Tap.WebURL.
type WebURLOptions struct {
	KegTargetOptions

	// Ref names the page: empty for the current KEG, a node id, a keg or node
	// reference, a flight, a lone @namespace, or a Hub page URL.
	Ref string
}

// WebURL is the Hub web page for a reference.
type WebURL struct {
	// URL is the absolute browser link.
	URL string `json:"url"`
	// Kind is namespace, keg, node, or flight.
	Kind string `json:"kind"`
	// Ref is the canonical reference the page shows: @ns, keg:@ns/keg,
	// keg:@ns/keg/42, or @ns/+slug.
	Ref string `json:"ref"`
}

// webSegment is the Hub's route segment shape. Every namespace, keg, and
// flight segment that reaches a path matches it, so paths need no escaping.
var webSegment = regexp.MustCompile(`^[a-z0-9_-]+$`)

// webNodeID is a note page's plain decimal node id.
var webNodeID = regexp.MustCompile(`^[0-9]+$`)

const webURLForms = "use a node id (42), a KEG (keg:alias, keg:~alias, @ns/keg), " +
	"a note (keg:alias/42, keg:~alias/42, @ns/keg/42), a flight (+slug, @ns/+slug), " +
	"a namespace (@ns), or a Hub page URL"

// IsFlightRef reports whether raw is spelled as a flight reference: a bare
// +slug or @namespace/+slug.
func IsFlightRef(raw string) bool {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "+") {
		return true
	}
	if rest, ok := strings.CutPrefix(raw, "@"); ok {
		_, slug, found := strings.Cut(rest, "/")
		return found && strings.HasPrefix(slug, "+")
	}
	return false
}

// WebURL returns the Hub web page for a KEG, note, flight, or namespace. It
// builds the link from configuration alone where it can: only a keg-local
// ~alias reads the current KEG's settings. The page shapes are the Hub's
// browser routes (see "Hub Web Links" in docs/architecture/configuration-resolution.md):
//
//	/@ns           namespace
//	/@ns/keg       KEG
//	/@ns/keg/42    note
//	/@ns/+slug     flight
func (t *Tap) WebURL(ctx context.Context, opts WebURLOptions) (WebURL, error) {
	raw := strings.TrimSpace(opts.Ref)
	switch {
	case raw == "":
		return t.webKegURL(ctx, opts.KegTargetOptions, "")
	case strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://"):
		return t.webPageURL(opts.KegTargetOptions, raw)
	case IsFlightRef(raw):
		return t.webFlightURL(opts.KegTargetOptions, raw)
	case strings.HasPrefix(raw, "@") && !strings.Contains(raw, "/"):
		ns := strings.TrimPrefix(raw, "@")
		if !webSegment.MatchString(ns) {
			return WebURL{}, fmt.Errorf("invalid namespace %q: %s: %w", raw, webURLForms, keg.ErrInvalid)
		}
		base, err := t.webBaseURL(opts.KegTargetOptions)
		if err != nil {
			return WebURL{}, err
		}
		return WebURL{URL: base + "/@" + ns, Kind: WebURLKindNamespace, Ref: "@" + ns}, nil
	}

	nodeRaw := raw
	if strings.HasPrefix(raw, "@") && strings.Count(raw, "/") == 2 {
		nodeRaw = "keg:" + raw
	}
	if ref, err := keg.ParseNodeRef(nodeRaw); err == nil {
		return t.webNodeURL(ctx, opts.KegTargetOptions, ref)
	}

	if alias, ok := strings.CutPrefix(raw, "keg:~"); ok {
		return t.webSettingsAliasURL(ctx, opts.KegTargetOptions, alias)
	}
	ref := parseKegRef(raw)
	if ValidateKegAlias(ref.Name) != nil || (ref.Namespace != "" && !webSegment.MatchString(ref.Namespace)) {
		return WebURL{}, fmt.Errorf("unrecognized reference %q: %s: %w", raw, webURLForms, keg.ErrInvalid)
	}
	return t.webKegURL(ctx, opts.KegTargetOptions, kegRefSelector(ref))
}

// kegRefSelector renders a parsed keg reference as the selector every resolver
// understands: @ns/name, or a bare name.
func kegRefSelector(ref KegRef) string {
	if ref.Namespace != "" {
		return "@" + strings.TrimPrefix(ref.Namespace, "@") + "/" + ref.Name
	}
	return ref.Name
}

func (t *Tap) webKegURL(ctx context.Context, opts KegTargetOptions, selector string) (WebURL, error) {
	base, ns, name, err := t.webKeg(ctx, opts, selector)
	if err != nil {
		return WebURL{}, err
	}
	return webKegPage(base, ns, name), nil
}

func webKegPage(base, ns, name string) WebURL {
	return WebURL{URL: base + "/@" + ns + "/" + name, Kind: WebURLKindKeg, Ref: "keg:@" + ns + "/" + name}
}

func (t *Tap) webNodeURL(ctx context.Context, opts KegTargetOptions, ref *keg.NodeRef) (WebURL, error) {
	var base, ns, name string
	var err error
	switch ref.Form {
	case keg.RefLocal:
		base, ns, name, err = t.webKeg(ctx, opts, "")
	case keg.RefAlias:
		base, ns, name, err = t.webKeg(ctx, opts, ref.Alias)
	case keg.RefQualified:
		opts.Namespace = ""
		base, ns, name, err = t.webKeg(ctx, opts, "@"+ref.Namespace+"/"+ref.KegName)
	case keg.RefSettingsAlias:
		base, ns, name, err = t.webSettingsAliasKeg(ctx, opts, ref.Alias)
	default:
		err = fmt.Errorf("unknown node ref form")
	}
	if err != nil {
		return WebURL{}, err
	}
	id := strconv.Itoa(ref.Node.ID)
	return WebURL{
		URL:  base + "/@" + ns + "/" + name + "/" + id,
		Kind: WebURLKindNode,
		Ref:  "keg:@" + ns + "/" + name + "/" + id,
	}, nil
}

func (t *Tap) webSettingsAliasURL(ctx context.Context, opts KegTargetOptions, alias string) (WebURL, error) {
	base, ns, name, err := t.webSettingsAliasKeg(ctx, opts, alias)
	if err != nil {
		return WebURL{}, err
	}
	return webKegPage(base, ns, name), nil
}

// webSettingsAliasKeg resolves a keg-local ~alias against the current KEG's
// settings, the one form that reads the network.
func (t *Tap) webSettingsAliasKeg(ctx context.Context, opts KegTargetOptions, alias string) (base, ns, name string, err error) {
	alias = strings.TrimSpace(alias)
	if !webSegment.MatchString(alias) {
		return "", "", "", fmt.Errorf("invalid settings alias %q: %s: %w", "~"+alias, webURLForms, keg.ErrInvalid)
	}
	current, err := t.resolveKeg(ctx, opts)
	if err != nil {
		return "", "", "", err
	}
	settings, err := current.Settings(ctx)
	if err != nil {
		return "", "", "", err
	}
	ns, name, err = keg.ResolveSettingsAlias(settings.Aliases, alias)
	if err != nil {
		return "", "", "", err
	}
	opts.Namespace = ""
	if target := current.Target(); target != nil && strings.TrimSpace(target.Hub) != "" && t.KegResolver == nil {
		opts.Hub = target.Hub
	}
	return t.webKeg(ctx, opts, "@"+ns+"/"+name)
}

// webKeg resolves a KEG selector (empty for the current KEG) to its Hub base
// URL, namespace, and name. A Hub serving its own MCP resolves through its
// injected KegResolver; the CLI resolves from configuration without opening
// the KEG.
func (t *Tap) webKeg(ctx context.Context, opts KegTargetOptions, selector string) (base, ns, name string, err error) {
	if selector != "" {
		opts.Keg = selector
	}
	var target *keg.Target
	if t.KegResolver != nil {
		if base, err = t.webBaseURL(opts); err != nil {
			return "", "", "", err
		}
		k, rerr := t.resolveKeg(ctx, opts)
		if rerr != nil {
			return "", "", "", rerr
		}
		target = k.Target()
	} else {
		target, err = t.ConfigService.ResolveTarget(opts.Keg, opts.Namespace, opts.Hub)
		if err != nil {
			if sel := t.webKegSelector(opts); strings.HasPrefix(sel, "http://") || strings.HasPrefix(sel, "https://") {
				return "", "", "", fmt.Errorf("keg %q has no Hub web page: %w", sel, err)
			}
			return "", "", "", err
		}
		if target != nil && strings.TrimSpace(target.HubURL) != "" {
			base = strings.TrimRight(CanonicalConfiguredHubURL(target.HubURL), "/")
		}
	}
	if target == nil || base == "" || target.Namespace == "" || target.KegName == "" {
		label := t.webKegSelector(opts)
		if target != nil && target.Url != "" {
			label = target.Url
		}
		return "", "", "", fmt.Errorf("keg %q has no Hub web page", label)
	}
	if !webSegment.MatchString(target.Namespace) || !webSegment.MatchString(target.KegName) {
		return "", "", "", fmt.Errorf("keg @%s/%s has no Hub web page address", target.Namespace, target.KegName)
	}
	return base, target.Namespace, target.KegName, nil
}

// webKegSelector is the keg selector ResolveTarget reads: the explicit one,
// else the configured keg.
func (t *Tap) webKegSelector(opts KegTargetOptions) string {
	if sel := strings.TrimSpace(opts.Keg); sel != "" {
		return sel
	}
	if cfg, err := t.ConfigService.Config(); err == nil && cfg != nil {
		return strings.TrimSpace(cfg.Keg())
	}
	return ""
}

func (t *Tap) webFlightURL(opts KegTargetOptions, raw string) (WebURL, error) {
	defaultNS := ""
	if t.KegResolver == nil {
		if cfg, err := t.ConfigService.Config(); err == nil {
			defaultNS = t.defaultFlightNamespace(cfg)
		}
	}
	if defaultNS == "" {
		defaultNS = strings.TrimPrefix(strings.TrimSpace(opts.Namespace), "@")
	}
	ref, err := ParseFlightRef(raw, defaultNS)
	if err != nil {
		return WebURL{}, err
	}
	if ref.Namespace == "" {
		return WebURL{}, fmt.Errorf("flight %q needs a namespace; use @namespace/+slug: %w", raw, keg.ErrInvalid)
	}
	if !webSegment.MatchString(ref.Namespace) || !webSegment.MatchString(ref.Slug) {
		return WebURL{}, fmt.Errorf("invalid flight reference %q: %w", raw, keg.ErrInvalid)
	}
	base, err := t.webBaseURL(opts)
	if err != nil {
		return WebURL{}, err
	}
	canonical := ref.Canonical()
	return WebURL{URL: base + "/" + canonical, Kind: WebURLKindFlight, Ref: canonical}, nil
}

// webPageURL validates a Hub page URL: it must be on the Hub this context
// resolves to and name one of the four page shapes. It is returned unchanged.
func (t *Tap) webPageURL(opts KegTargetOptions, raw string) (WebURL, error) {
	base, err := t.webBaseURL(opts)
	if err != nil {
		return WebURL{}, err
	}
	if err := ValidateBrowserURL(raw, base); err != nil {
		return WebURL{}, fmt.Errorf("%q is not a page on the Hub at %s: %w", raw, base, keg.ErrInvalid)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return WebURL{}, err
	}
	basePath := ""
	if b, err := url.Parse(base); err == nil {
		basePath = strings.TrimRight(b.Path, "/")
	}
	path, ok := strings.CutPrefix(u.Path, basePath+"/")
	if !ok {
		return WebURL{}, fmt.Errorf("%q is not a page on the Hub at %s: %w", raw, base, keg.ErrInvalid)
	}
	kind, ref, ok := classifyWebPath(strings.TrimSuffix(path, "/"))
	if !ok {
		return WebURL{}, fmt.Errorf("%q is not a namespace, KEG, note, or flight page: %w", raw, keg.ErrInvalid)
	}
	return WebURL{URL: raw, Kind: kind, Ref: ref}, nil
}

// classifyWebPath reads a Hub page path (without its leading slash) back into
// its kind and canonical reference.
func classifyWebPath(path string) (kind, ref string, ok bool) {
	rest, found := strings.CutPrefix(path, "@")
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if !webSegment.MatchString(parts[0]) {
		return "", "", false
	}
	ns := parts[0]
	switch len(parts) {
	case 1:
		return WebURLKindNamespace, "@" + ns, true
	case 2:
		if slug, isFlight := strings.CutPrefix(parts[1], "+"); isFlight {
			if !webSegment.MatchString(slug) {
				return "", "", false
			}
			return WebURLKindFlight, "@" + ns + "/+" + slug, true
		}
		if !webSegment.MatchString(parts[1]) {
			return "", "", false
		}
		return WebURLKindKeg, "keg:@" + ns + "/" + parts[1], true
	case 3:
		if !webNodeID.MatchString(parts[2]) || !webSegment.MatchString(parts[1]) {
			return "", "", false
		}
		id, err := strconv.Atoi(parts[2])
		if err != nil {
			return "", "", false
		}
		return WebURLKindNode, "keg:@" + ns + "/" + parts[1] + "/" + strconv.Itoa(id), true
	}
	return "", "", false
}

// webBaseURL returns the Hub's web origin: the hub serving this Tap when one
// is injected, otherwise the selected configured hub.
func (t *Tap) webBaseURL(opts KegTargetOptions) (string, error) {
	if t.KegResolver != nil {
		base := strings.TrimRight(strings.TrimSpace(t.WebBaseURL), "/")
		if base == "" {
			return "", fmt.Errorf("this Hub has no public web address configured")
		}
		return base, nil
	}
	_, entry, err := t.ConfigService.SelectedHub(strings.TrimSpace(opts.Hub))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(CanonicalConfiguredHubURL(entry.URL), "/"), nil
}
