package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPickerFilterResetsCursor(t *testing.T) {
	var items []pickItem
	for _, n := range []string{"everyone", "user:amy", "user:bob", "user:olgeni", "group:olgeni", "group:wheel"} {
		items = append(items, pickItem{n, n})
	}
	p := newPicker("t", "d", items, "user:bob", 80, 24)
	if p.Value() != "user:bob" {
		t.Fatalf("initial %q", p.Value())
	}
	for i := 0; i < 4; i++ {
		p.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if p.Value() != "group:wheel" {
		t.Fatalf("after down %q", p.Value())
	}
	for _, r := range "ol" {
		p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(p.vis) != 2 || p.cursor != 0 || p.Value() != "user:olgeni" {
		t.Fatalf("after filter: vis=%d cursor=%d value=%q", len(p.vis), p.cursor, p.Value())
	}
	if p.Update(tea.KeyMsg{Type: tea.KeyEnter}) != pickDone {
		t.Fatal("enter")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	p.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if len(p.vis) != len(items) {
		t.Fatal("backspace")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})
	if p.Update(tea.KeyMsg{Type: tea.KeyEnter}) != pickNone || p.Value() != "" {
		t.Fatal("enter on no match")
	}
	if p.Update(tea.KeyMsg{Type: tea.KeyEscape}) != pickNone || p.filter != "" {
		t.Fatal("esc clears the filter")
	}
	if p.Update(tea.KeyMsg{Type: tea.KeyEscape}) != pickCancel {
		t.Fatal("esc cancels")
	}
	_ = p.View()
}
