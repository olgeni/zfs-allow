package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/olgeni/zfs-allow/delegation"
)

func kmsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestEditorFilter(t *testing.T) {
	ed := newEditor(editGrant, "tank/x", delegation.User("bob"), delegation.ScopeBoth, delegation.ParsePerms("mount"), []string{"@ops"}, 120, 35)
	for _, k := range []string{"/", "s", "n", "a", "p"} {
		ed.Update(kmsg(k))
	}
	if ed.filter != "snap" || !ed.typing {
		t.Fatalf("filter %q typing %v", ed.filter, ed.typing)
	}
	v := ed.View()
	if !strings.Contains(v, "filter:") || !strings.Contains(v, "snapshot") || strings.Contains(v, "[x] mount") {
		t.Fatalf("view:\n%s", v)
	}
	// enter leaves typing, cursor on a perm row; space toggles it
	ed.Update(kmsg("enter"))
	if ed.typing || ed.row().kind != erPerm {
		t.Fatalf("after enter: typing=%v kind=%v", ed.typing, ed.row().kind)
	}
	name := ed.row().perm.Name
	ed.Update(kmsg(" "))
	if !ed.perms.Has(name) {
		t.Fatal("space did not toggle")
	}
	// esc clears the filter, second esc cancels
	if ed.Update(kmsg("esc")) != actNone || ed.filter != "" {
		t.Fatal("esc did not clear the filter")
	}
	if ed.Update(kmsg("esc")) != actCancel {
		t.Fatal("second esc did not cancel")
	}
}

func TestEditorGroupToggleAndConfirm(t *testing.T) {
	ed := newEditor(editGrant, "tank/x", delegation.Who{Kind: delegation.WhoUser, ID: -1}, delegation.ScopeBoth, nil, nil, 100, 30)
	// confirm without a who is refused
	if ed.Update(tea.KeyMsg{Type: tea.KeyCtrlS}) != actNone || ed.errMsg == "" {
		t.Fatal("confirmed without who")
	}
	ed.SetWho(delegation.Group("staff"))
	// move to the first group header and toggle the whole group
	ed.cursor = ed.firstPermRow() - 1
	if ed.row().kind != erGroup {
		t.Fatal("not on a group")
	}
	ed.Update(kmsg(" "))
	if !ed.perms.Has("create") || !ed.perms.Has("diff") || ed.perms.Has("send") {
		t.Fatalf("group toggle: %v", ed.perms)
	}
	ed.Update(kmsg(" "))
	if !ed.perms.Empty() {
		t.Fatal("group untoggle")
	}
	ed.Update(kmsg("a"))
	if len(ed.perms) < 80 {
		t.Fatal("select all")
	}
	ed.Update(kmsg("n"))
	ed.ApplyPreset(delegation.Presets[0].Perms)
	if ed.Update(tea.KeyMsg{Type: tea.KeyCtrlS}) != actOK {
		t.Fatal("confirm")
	}
	// scope cycles with right
	ed.cursor = 1
	ed.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ed.scope != delegation.ScopeLocal {
		t.Fatal("scope cycle")
	}
	_ = ed.View()
}
