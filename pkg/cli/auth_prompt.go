package cli

// Interactive surface for `tap auth login`. The hub picker and login-method
// picker are arrow-key menus rendered with charmbracelet/huh; the URL and
// token prompts are huh inputs. All of it sits behind the AuthPrompter
// interface so the command logic can be unit-tested with a scripted fake and
// never has to drive a real terminal.

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// loginMethod selects how `tap auth login` obtains a credential.
type loginMethod int

const (
	methodBrowser loginMethod = iota // RFC 8628 device flow (open a browser)
	methodToken                      // paste an API token from the hub UI
)

// hubChoice is one row in the interactive hub picker. URL is the canonical,
// scheme-qualified hub base; Other marks the synthetic "type a new endpoint"
// row that drives PromptEndpointURL instead of using URL.
type hubChoice struct {
	Label string
	URL   string
	Other bool
}

// AuthPrompter is the interactive surface of `tap auth login`. It is an
// interface so tests inject a scripted fake and never touch a TTY; the
// production implementation renders huh menus on the controlling terminal.
type AuthPrompter interface {
	// SelectHub presents choices and returns the selected one.
	SelectHub(choices []hubChoice) (hubChoice, error)
	// SelectMethod asks browser-vs-token.
	SelectMethod() (loginMethod, error)
	// PromptEndpointURL collects a hub base URL for the "Other endpoint" path.
	PromptEndpointURL() (string, error)
	// PromptToken collects a pasted API token with masked echo.
	PromptToken() (string, error)
	// ConfirmOpenBrowser gates the device-flow browser open. It returns true
	// to open the browser (the default — pressing Enter), false when the user
	// would rather copy the URL themselves.
	ConfirmOpenBrowser(host string) (bool, error)
}

// huhAuthPrompter is the production AuthPrompter. It carries no state — each
// method stands up a one-field huh form on the controlling terminal.
type huhAuthPrompter struct{}

func (huhAuthPrompter) SelectHub(choices []hubChoice) (hubChoice, error) {
	opts := make([]huh.Option[int], len(choices))
	for i, c := range choices {
		opts[i] = huh.NewOption(c.Label, i)
	}
	var idx int
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title("Select a hub to log in to").
			Options(opts...).
			Value(&idx),
	)).Run()
	if err != nil {
		return hubChoice{}, err
	}
	return choices[idx], nil
}

func (huhAuthPrompter) SelectMethod() (loginMethod, error) {
	var m loginMethod
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[loginMethod]().
			Title("How would you like to authenticate?").
			Options(
				huh.NewOption("Login with a web browser", methodBrowser),
				huh.NewOption("Paste an authentication token", methodToken),
			).
			Value(&m),
	)).Run()
	return m, err
}

func (huhAuthPrompter) PromptEndpointURL() (string, error) {
	var s string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Hub endpoint URL").
			Placeholder("https://hub.example.com").
			Validate(validateEndpointURL).
			Value(&s),
	)).Run()
	return strings.TrimSpace(s), err
}

func (huhAuthPrompter) PromptToken() (string, error) {
	var s string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Paste your authentication token").
			EchoMode(huh.EchoModePassword).
			Validate(validateNonEmpty).
			Value(&s),
	)).Run()
	return strings.TrimSpace(s), err
}

func (huhAuthPrompter) ConfirmOpenBrowser(host string) (bool, error) {
	open := true
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Press Enter to open %s in your browser", host)).
			Affirmative("Open browser").
			Negative("Copy the URL instead").
			Value(&open),
	)).Run()
	return open, err
}

// validateEndpointURL is the huh validator for the "Other endpoint" input. It
// enforces the same http/https + host contract the login flow requires, so a
// bad URL is rejected at the prompt rather than several steps later.
func validateEndpointURL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("endpoint URL is required")
	}
	parsed, err := url.Parse(ensureScheme(s))
	if err != nil {
		return fmt.Errorf("not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("URL must use http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("URL is missing a host")
	}
	return nil
}

func validateNonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("value is required")
	}
	return nil
}

// buildHubChoices assembles the interactive hub picker: atlas first, then every
// non-local hub configured in cfg (deduped by canonical URL, sorted by name for
// a stable menu), then an "Other endpoint" row. Local hubs are excluded — you
// don't log in to a filesystem hub.
func buildHubChoices(cfg *tapper.Config) []hubChoice {
	var choices []hubChoice
	seen := map[string]bool{}
	add := func(label, rawURL string) {
		canon := tapper.CanonicalHubURL(ensureScheme(rawURL))
		if canon == "" || seen[canon] {
			return
		}
		seen[canon] = true
		choices = append(choices, hubChoice{Label: label, URL: canon})
	}

	// Atlas is always offered first as the default hosted hub.
	add(hostOf(tapper.DefaultHubURL)+" (default)", tapper.DefaultHubURL)

	if cfg != nil {
		names := make([]string, 0, len(cfg.Hubs()))
		for name := range cfg.Hubs() {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			h := cfg.Hubs()[name]
			if h.Kind == tapper.HubKindLocal || strings.TrimSpace(h.URL) == "" {
				continue
			}
			add(fmt.Sprintf("%s — %s", name, hostOf(ensureScheme(h.URL))), h.URL)
		}
	}

	choices = append(choices, hubChoice{Label: "Other endpoint…", Other: true})
	return choices
}

// ensureScheme prefixes https:// when a configured hub URL is a bare host
// (the user config template stores hostnames without a scheme). URLs that
// already carry a scheme pass through unchanged so http://-only test hubs
// keep working.
func ensureScheme(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "https://" + trimmed
}
