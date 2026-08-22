package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/olgeni/zfs-allow/delegation"
)

// TestScreenshot writes the screens used in the README as ANSI text when
// ZFSALLOW_SCREENSHOT names a directory (render them with
// "freeze -o FILE.png < FILE.ansi"); it is a no-op otherwise. The data is made
// up so no real dataset shows.
func TestScreenshot(t *testing.T) {
	dir := os.Getenv("ZFSALLOW_SCREENSHOT")
	if dir == "" {
		t.Skip("ZFSALLOW_SCREENSHOT not set")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	const w, h = 110, 34

	own := &delegation.Delegations{Dataset: "tank/home", Sets: []delegation.Set{{Name: "@backup", Perms: delegation.ParsePerms("send,snapshot,hold,release,bookmark,mount")}},
		Create: delegation.ParsePerms("mount,snapshot")}
	own.SetGrant(delegation.Grant{Who: delegation.User("alice"), Local: delegation.ParsePerms("create,destroy,mount,snapshot,rollback"), Descend: delegation.ParsePerms("create,destroy,mount,snapshot,rollback")})
	own.SetGrant(delegation.Grant{Who: delegation.User("bob"), Local: delegation.ParsePerms("@backup,diff"), Descend: delegation.ParsePerms("@backup")})
	own.SetGrant(delegation.Grant{Who: delegation.Group("staff"), Local: nil, Descend: delegation.ParsePerms("snapshot,hold,release")})
	own.SetGrant(delegation.Grant{Who: delegation.Everyone, Local: delegation.ParsePerms("userused,groupused"), Descend: delegation.ParsePerms("userused,groupused")})
	parent := &delegation.Delegations{Dataset: "tank"}
	parent.SetGrant(delegation.Grant{Who: delegation.Group("operator"), Descend: delegation.ParsePerms("snapshot,send")})
	l := &delegation.Listing{Dataset: delegation.Dataset{Name: "tank/home", Type: "filesystem", Mountpoint: "/home", Mounted: true}, Own: own, Ancestors: []*delegation.Delegations{parent}}

	m := New("tank/home", "")
	m.width, m.height = 118, 22
	m.listing, m.cur = l, own.Clone()
	m.env = delegation.Env{Operator: delegation.Identity{Name: "root"}, Usermount: true}
	m.rebuildRows()
	m.cursor = 3
	write(t, filepath.Join(dir, "main.ansi"), m.mainView())

	ed := newEditor(editGrant, "tank/home", delegation.User("bob"), delegation.ScopeBoth, delegation.ParsePerms("@backup,diff"), []string{"@backup"}, w, h)
	ed.cursor = ed.firstPermRow() + 3 // a permission row, so its note shows
	write(t, filepath.Join(dir, "editor.ansi"), ed.View())
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
