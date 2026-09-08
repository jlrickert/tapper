package cli

// Interactive surface for `tap bootstrap`. It mirrors the `tap auth login`
// prompt style by using huh-backed selects/confirms in production while keeping
// the command logic injectable for tests.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/jlrickert/tapper/pkg/tapper"
)

type bootstrapKegAction int

const (
	bootstrapKegUseRef bootstrapKegAction = iota
	bootstrapKegManual
	bootstrapKegCreate
	bootstrapKegSkip
)

type bootstrapKegSelection struct {
	Action bootstrapKegAction
	Ref    string
}

// BootstrapPrompter is the interactive surface of `tap bootstrap`.
type BootstrapPrompter interface {
	// SelectBootstrapKind chooses the cloud or enterprise bootstrap path.
	SelectBootstrapKind() (string, error)
	// PromptBootstrapEndpoint collects the enterprise hub endpoint URL.
	PromptBootstrapEndpoint() (string, error)
	// ConfirmBootstrapLogin reports whether the user wants to log in to host.
	ConfirmBootstrapLogin(host string) (bool, error)
	// SelectKeg chooses an available keg or a create, manual, or skip action.
	SelectKeg(available []string) (bootstrapKegSelection, error)
	// SelectFlight chooses an available MCP flight, preserving current as the
	// default selection when possible.
	SelectFlight(available []string, current string) (string, error)
	// PromptManualKeg collects a keg reference not present in the picker.
	PromptManualKeg() (string, error)
	// PromptNewKegName collects and validates the alias for a new keg.
	PromptNewKegName() (string, error)
}

func (huhAuthPrompter) SelectFlight(available []string, current string) (string, error) {
	selection := ""
	opts := make([]huh.Option[string], 0, len(available)+1)
	for i, ref := range available {
		opt := huh.NewOption(ref, ref)
		if ref == current || (current == "" && i == 0) {
			selection = ref
			opt = opt.Selected(true)
		}
		opts = append(opts, opt)
	}
	opts = append(opts, huh.NewOption("Skip for now", ""))
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Baseline flight for MCP sessions").
			Options(opts...).
			Value(&selection),
	)).Run()
	return strings.TrimSpace(selection), err
}

func (huhAuthPrompter) SelectBootstrapKind() (string, error) {
	kind := tapper.BootstrapKindCloud
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Where should your kegs live?").
			Options(
				huh.NewOption("Cloud - atlas.foldwise.ai", tapper.BootstrapKindCloud),
				huh.NewOption("Enterprise - your own hub", tapper.BootstrapKindEnterprise),
			).
			Value(&kind),
	)).Run()
	return kind, err
}

func (huhAuthPrompter) PromptBootstrapEndpoint() (string, error) {
	var s string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Enterprise hub endpoint URL").
			Placeholder("https://hub.example.com").
			Validate(validateEndpointURL).
			Value(&s),
	)).Run()
	return strings.TrimSpace(s), err
}

func (huhAuthPrompter) ConfirmBootstrapLogin(host string) (bool, error) {
	login := true
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Log in to %s now?", host)).
			Affirmative("Log in").
			Negative("Skip").
			Value(&login),
	)).Run()
	return login, err
}

func (huhAuthPrompter) SelectKeg(available []string) (bootstrapKegSelection, error) {
	selection := bootstrapKegSelection{Action: bootstrapKegSkip}
	opts := make([]huh.Option[bootstrapKegSelection], 0, len(available)+3)
	for i, ref := range available {
		value := bootstrapKegSelection{
			Action: bootstrapKegUseRef,
			Ref:    ref,
		}
		opt := huh.NewOption(ref, value)
		if i == 0 {
			selection = value
			opt = opt.Selected(true)
		}
		opts = append(opts, opt)
	}
	opts = append(opts,
		huh.NewOption("Create a new keg", bootstrapKegSelection{Action: bootstrapKegCreate}),
		huh.NewOption("Type another keg reference", bootstrapKegSelection{Action: bootstrapKegManual}),
		huh.NewOption("Skip for now", bootstrapKegSelection{Action: bootstrapKegSkip}),
	)
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[bootstrapKegSelection]().
			Title("Default keg for plain tap commands").
			Options(opts...).
			Value(&selection),
	)).Run()
	return selection, err
}

func (huhAuthPrompter) PromptManualKeg() (string, error) {
	var s string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Default keg for plain tap commands").
			Placeholder("@you/notes").
			Value(&s),
	)).Run()
	return strings.TrimSpace(s), err
}

func (huhAuthPrompter) PromptNewKegName() (string, error) {
	var s string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Create a keg").
			Placeholder("notes").
			Validate(func(v string) error {
				return tapper.ValidateKegAlias(strings.TrimSpace(v))
			}).
			Value(&s),
	)).Run()
	return strings.TrimSpace(s), err
}
