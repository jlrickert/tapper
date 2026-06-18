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

type bootstrapDefaultKegAction int

const (
	bootstrapDefaultKegUseRef bootstrapDefaultKegAction = iota
	bootstrapDefaultKegManual
	bootstrapDefaultKegCreate
	bootstrapDefaultKegSkip
)

type bootstrapDefaultKegSelection struct {
	Action bootstrapDefaultKegAction
	Ref    string
}

// BootstrapPrompter is the interactive surface of `tap bootstrap`.
type BootstrapPrompter interface {
	SelectBootstrapKind() (string, error)
	PromptBootstrapEndpoint() (string, error)
	ConfirmBootstrapLogin(host string) (bool, error)
	SelectDefaultKeg(available []string) (bootstrapDefaultKegSelection, error)
	PromptManualDefaultKeg() (string, error)
	PromptNewKegName() (string, error)
}

func (huhAuthPrompter) SelectBootstrapKind() (string, error) {
	kind := tapper.BootstrapKindCloud
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Where should your kegs live?").
			Options(
				huh.NewOption("Cloud - atlas.foldwise.ai", tapper.BootstrapKindCloud),
				huh.NewOption("Local - this machine only", tapper.BootstrapKindLocal),
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

func (huhAuthPrompter) SelectDefaultKeg(available []string) (bootstrapDefaultKegSelection, error) {
	selection := bootstrapDefaultKegSelection{Action: bootstrapDefaultKegSkip}
	opts := make([]huh.Option[bootstrapDefaultKegSelection], 0, len(available)+3)
	for i, ref := range available {
		value := bootstrapDefaultKegSelection{
			Action: bootstrapDefaultKegUseRef,
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
		huh.NewOption("Create a new keg", bootstrapDefaultKegSelection{Action: bootstrapDefaultKegCreate}),
		huh.NewOption("Type another keg reference", bootstrapDefaultKegSelection{Action: bootstrapDefaultKegManual}),
		huh.NewOption("Skip for now", bootstrapDefaultKegSelection{Action: bootstrapDefaultKegSkip}),
	)
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[bootstrapDefaultKegSelection]().
			Title("Default keg for plain tap commands").
			Options(opts...).
			Value(&selection),
	)).Run()
	return selection, err
}

func (huhAuthPrompter) PromptManualDefaultKeg() (string, error) {
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
