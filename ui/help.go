package ui

import (
	"fmt"
	"strings"
)

const helpText = `zfs-allow — ZFS delegated administration editor (zfs allow / zfs unallow front-end)

What it shows
  The delegations of one dataset, exactly as "zfs allow DATASET" prints them:
    Permission sets        @name = a bundle of permissions, usable on this
                           dataset and its descendants (defined by root)
    Create-time            permissions given locally to whoever creates a new
                           descendant file system
    Entries                a user, a group or everyone, where the permissions
                           apply and the permissions:
                             This dataset and its descendants   (zfs allow, no flag)
                             This dataset only                  (zfs allow -l)
                             Descendants only                   (zfs allow -d)
  Ancestors that delegate to their descendants also apply here; i lists them.

Editing
  Edit an entry (enter/e), add one (a), delete one (d), revoke it on the
  whole subtree (R: zfs unallow -r). The editor is a
  grouped checklist of every permission with its description; / filters by
  name or description, space toggles, space on a group header toggles the
  whole group, p loads a preset bundle. Nothing is written until you apply
  (A): the plan screen shows the exact zfs allow / zfs unallow commands that
  turn the current state into the edited one, plus pre-flight warnings:
    - dependencies from zfs-allow(8): create/destroy/snapshot/rollback need
      mount; clone/rename/receive need create and mount; …
    - vfs.usermount=0 makes every mount-dependent delegation useless
    - a mount point the user cannot create sub-mount-points in
    - what an unprivileged operator cannot do: zfs unallow anyone but
      themselves, define permission sets, delegate what they do not hold

Effective permissions (E) answer "what may this user do here?": their own
entries, their groups, everyone, and the descendant entries of every
ancestor, with permission sets expanded and every source listed.

Facts worth knowing
  - Delegated permissions never override the mount point: to create file
    systems a user also needs to create directories in the parent's mount
    point (chown it, or grant add_subdirectory with an NFSv4 ACL — facl).
  - Root is never restricted; delegations only matter for other users.
  - zfs allow by an unprivileged user needs the "allow" permission and only
    grants what that user holds; zfs unallow by an unprivileged user only
    works on their own entries; permission sets need root.
  - Removing the last permission from an entry is "zfs unallow WHO": it
    removes the whole entry. Nothing is ever denied — a permission granted by
    an ancestor stays in effect.
  - On FreeBSD mlslabel and zoned are refused ("operation not applicable");
    the editor hides them unless x is pressed.
`

// keymap is the compact key reference (h).
var keymap = [][2]string{
	{"Main screen", ""},
	{"↑/↓ j/k, pgup/pgdn, g/G", "move"},
	{"enter, e", "edit the entry"},
	{"a", "add an entry / create-time permissions / a permission set"},
	{"d", "delete the entry (all its permissions)"},
	{"R", "revoke the entry here and on every descendant (zfs unallow -r)"},
	{"A", "apply: preview the zfs commands, then run them"},
	{"u", "undo the last edit"},
	{"r", "reload from the kernel (discarding edits)"},
	{"E", "effective permissions of a user here"},
	{"i", "ancestors' delegations that apply here"},
	{"D", "switch dataset"},
	{"q, esc", "back to the dataset list when started there, else quit"},
	{"?", "help   h  this key list"},
	{"Editor", ""},
	{"space", "toggle the permission / the whole group / cycle the scope"},
	{"enter", "toggle and move down; on Who: choose the grantee"},
	{"←/→", "change the scope, move between OK and Cancel"},
	{"/", "filter by name or description (esc clears)"},
	{"a / n", "select all / none of the shown permissions"},
	{"p", "load a preset bundle"},
	{"x", "show permissions FreeBSD refuses (mlslabel, zoned)"},
	{"tab", "jump between Who, scope, list and buttons"},
	{"ctrl+s, F10", "OK"},
	{"esc, q", "cancel"},
	{"Plan screen", ""},
	{"y, enter", "run the commands"},
	{"esc, n", "back to editing"},
}

func keymapView(width int) string {
	var b strings.Builder
	for _, k := range keymap {
		if k[1] == "" {
			b.WriteString("\n" + styleHeader.Render(k[0]) + "\n")
			continue
		}
		fmt.Fprintf(&b, "  %s %s\n", styleHelpKey.Render(fit(k[0], 26)), k[1])
	}
	return b.String()
}
