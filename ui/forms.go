package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/olgeni/zfs-allow/delegation"
)

const manualWho = "\x00manual"

// form is a one-group huh form that remembers its group so resize can fix
// the group's height (huh sizes the group's viewport once, at its default
// width).
type form struct {
	*huh.Form
	group *huh.Group
}

func newForm(fields ...huh.Field) *form {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "cancel"))
	g := huh.NewGroup(fields...)
	return &form{Form: huh.NewForm(g).WithTheme(huhTheme()).WithKeyMap(km).WithShowHelp(true), group: g}
}

func (f *form) resize(width int) {
	f.Form.WithWidth(width)
	h := lipgloss.Height(f.group.Content())
	for _, s := range []string{f.group.Header(), f.group.Footer()} {
		if s != "" {
			h += lipgloss.Height(s)
		}
	}
	f.Form.WithHeight(h)
}

// manualWhoForm asks for a principal and validates it.
func manualWhoForm(value *string) *form {
	in := huh.NewInput().
		Title("Principal").
		Description("user:NAME, u:NAME, group:NAME, g:NAME, user:UID, group:GID or everyone").
		Placeholder("user:bob").
		Value(value).
		Validate(func(s string) error {
			_, err := delegation.ParseWho(s)
			return err
		})
	return newForm(in)
}

// addKindForm asks what kind of entry to add.
func addKindForm(value *string) *form {
	sel := huh.NewSelect[string]().
		Title("Add").
		Options(
			huh.NewOption("Permissions for a user, a group or everyone", "grant"),
			huh.NewOption("Create-time permissions (given locally to whoever creates a new descendant)", "create"),
			huh.NewOption("A permission set (@name, reusable here and on descendants; root only)", "set"),
		).
		Value(value)
	return newForm(sel)
}

// setNameForm asks for a new permission set name.
func setNameForm(value *string, existing []string) *form {
	in := huh.NewInput().
		Title("Permission set name").
		Description("Starts with @, then letters, digits, _ - . : (at most 64 characters); sets are defined by root.").
		Placeholder("@backup").
		Value(value).
		Validate(func(s string) error {
			s = strings.TrimSpace(s)
			if !strings.HasPrefix(s, "@") {
				s = "@" + s
			}
			if !delegation.ValidSetName(s) {
				return fmt.Errorf("invalid set name")
			}
			for _, e := range existing {
				if e == s {
					return fmt.Errorf("%s already exists (edit it instead)", s)
				}
			}
			return nil
		})
	return newForm(in)
}

// confirmForm is a yes/no question.
func confirmForm(title, desc string, value *bool) *form {
	c := huh.NewConfirm().Title(title).Description(desc).Affirmative("Yes").Negative("No").Value(value)
	return newForm(c)
}
