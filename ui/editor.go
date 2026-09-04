package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/olgeni/zfs-allow/delegation"
)

// editorAction is what the editor asks the root model to do after a key.
type editorAction int

const (
	actNone editorAction = iota
	actOK
	actCancel
	actPickWho
	actPickPreset
)

// editKind says what the editor edits.
type editKind int

const (
	editGrant  editKind = iota // a who + scope + permissions
	editSet                    // a permission set definition
	editCreate                 // the create-time permissions
)

type edRowKind int

const (
	erWho edRowKind = iota
	erScope
	erGroup
	erPerm
	erButtons
)

type edRow struct {
	kind  edRowKind
	group string          // erGroup / erPerm
	perm  delegation.Perm // erPerm
}

// editor is the "Permission entry" dialog: who, scope and a grouped,
// filterable checklist of every permission with its description.
type editor struct {
	kind    editKind
	who     delegation.Who
	scope   delegation.Scope
	setName string
	perms   delegation.PermSet // selected
	sets    []string           // @sets usable here (defined on the dataset or an ancestor)
	dataset string

	rows    []edRow
	cursor  int
	col     int // buttons: 0 OK, 1 Cancel
	filter  editLine
	typing  bool // keystrokes go to the filter
	showAll bool // include permissions zfs refuses on FreeBSD
	width   int
	height  int
	errMsg  string
}

func newEditor(kind editKind, dataset string, who delegation.Who, scope delegation.Scope, perms delegation.PermSet, sets []string, width, height int) *editor {
	ed := &editor{kind: kind, dataset: dataset, who: who, scope: scope, perms: perms.With(), sets: sets, width: width, height: height}
	ed.filter = newEditLine("")
	ed.filter.Focus()
	ed.rebuild()
	// start on the first permission row for sets/create-time, on who for grants
	if kind != editGrant {
		ed.cursor = ed.firstPermRow()
	}
	return ed
}

func (ed *editor) setSize(w, h int) { ed.width, ed.height = w, h }

// SetWho replaces the grantee (after the who picker).
func (ed *editor) SetWho(w delegation.Who) { ed.who = w }

// ApplyPreset replaces the selection with the preset's permissions.
func (ed *editor) ApplyPreset(ps delegation.PermSet) { ed.perms = ps.With() }

func (ed *editor) firstPermRow() int {
	for i, r := range ed.rows {
		if r.kind == erPerm {
			return i
		}
	}
	return 0
}

// rebuild recomputes the row list for the current filter.
func (ed *editor) rebuild() {
	cur := ""
	if ed.cursor < len(ed.rows) && ed.rows[ed.cursor].kind == erPerm {
		cur = ed.rows[ed.cursor].perm.Name
	}
	ed.rows = ed.rows[:0]
	if ed.kind == editGrant {
		ed.rows = append(ed.rows, edRow{kind: erWho}, edRow{kind: erScope})
	}
	f := strings.ToLower(strings.TrimSpace(ed.filter.Value()))
	match := func(p delegation.Perm) bool {
		if f == "" {
			return true
		}
		return strings.Contains(strings.ToLower(p.Name), f) || strings.Contains(strings.ToLower(p.Note), f)
	}
	// permission sets first: they are the most specific thing this dataset offers
	if len(ed.sets) > 0 && ed.kind != editSet || ed.kind == editSet && len(ed.sets) > 1 {
		var rows []edRow
		for _, s := range ed.sets {
			if ed.kind == editSet && s == ed.setName {
				continue // a set may not contain itself
			}
			p := delegation.Perm{Name: s, Group: groupSets, Note: "permission set defined on this dataset or an ancestor"}
			if match(p) {
				rows = append(rows, edRow{kind: erPerm, group: groupSets, perm: p})
			}
		}
		if len(rows) > 0 {
			ed.rows = append(ed.rows, edRow{kind: erGroup, group: groupSets})
			ed.rows = append(ed.rows, rows...)
		}
	}
	for _, g := range delegation.GroupOrder {
		var rows []edRow
		for _, p := range delegation.ByGroup(g) {
			if p.NotFreeBSD && !ed.showAll && !ed.perms.Has(p.Name) {
				continue
			}
			if match(p) {
				rows = append(rows, edRow{kind: erPerm, group: g, perm: p})
			}
		}
		if len(rows) > 0 {
			ed.rows = append(ed.rows, edRow{kind: erGroup, group: g})
			ed.rows = append(ed.rows, rows...)
		}
	}
	// selected permissions that are not in the catalogue (newer OpenZFS, or an unknown @set)
	var extra []edRow
	for _, p := range ed.perms {
		if !delegation.Known(p) && !contains(ed.sets, p) {
			pp := delegation.Perm{Name: p, Group: groupOther, Note: "not in the catalogue (accepted only if this OpenZFS version knows it)"}
			if delegation.IsSet(p) {
				pp.Note = "permission set that is not defined on this dataset or an ancestor"
			}
			if match(pp) {
				extra = append(extra, edRow{kind: erPerm, group: groupOther, perm: pp})
			}
		}
	}
	if len(extra) > 0 {
		ed.rows = append(ed.rows, edRow{kind: erGroup, group: groupOther})
		ed.rows = append(ed.rows, extra...)
	}
	ed.rows = append(ed.rows, edRow{kind: erButtons})
	// keep the cursor on the same permission if it is still visible
	if cur != "" {
		for i, r := range ed.rows {
			if r.kind == erPerm && r.perm.Name == cur {
				ed.cursor = i
				return
			}
		}
	}
	if ed.cursor >= len(ed.rows) {
		ed.cursor = len(ed.rows) - 1
	}
	if f != "" && (ed.cursor < 0 || ed.rows[ed.cursor].kind != erPerm) {
		ed.cursor = ed.firstPermRow()
	}
}

const (
	groupSets  = "Permission sets"
	groupOther = "Other"
)

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func (ed *editor) row() edRow { return ed.rows[ed.cursor] }

// Update handles a key; it returns what the root model should do.
func (ed *editor) Update(msg tea.Msg) editorAction {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return actNone
	}
	ed.errMsg = ""
	if ed.typing {
		switch km.String() {
		case "esc":
			ed.filter.Set("")
			ed.typing = false
			ed.rebuild()
		case "enter", "down", "up", "tab":
			ed.typing = false
			if km.String() == "down" || km.String() == "up" {
				return ed.Update(km)
			}
		default:
			// every other key edits the filter, inside it as well as
			// at its end
			if ed.filter.Update(km) {
				ed.rebuild()
			}
		}
		return actNone
	}
	switch km.String() {
	case "esc", "q":
		if !ed.filter.Empty() {
			ed.filter.Set("")
			ed.rebuild()
			return actNone
		}
		return actCancel
	case "ctrl+s", "f10":
		return ed.confirm()
	case "/":
		ed.typing = true
	case "up", "k":
		ed.move(-1)
	case "down", "j":
		ed.move(1)
	case "pgup":
		for i := 0; i < ed.listHeight(); i++ {
			ed.move(-1)
		}
	case "pgdown":
		for i := 0; i < ed.listHeight(); i++ {
			ed.move(1)
		}
	case "tab":
		ed.nextSection(1)
	case "shift+tab":
		ed.nextSection(-1)
	case "home", "g":
		ed.cursor = 0
		if !ed.filter.Empty() {
			ed.cursor = ed.firstPermRow()
		}
	case "end", "G":
		ed.cursor = len(ed.rows) - 1
	case "left", "h":
		ed.horizontal(-1)
	case "right", "l":
		ed.horizontal(1)
	case " ", "enter":
		return ed.activate(km.String() == "enter")
	case "a":
		ed.selectVisible(true)
	case "n":
		ed.selectVisible(false)
	case "p":
		return actPickPreset
	case "x":
		ed.showAll = !ed.showAll
		ed.rebuild()
	}
	return actNone
}

func (ed *editor) move(d int) {
	if n := ed.cursor + d; n >= 0 && n < len(ed.rows) {
		ed.cursor = n
	}
}

// nextSection jumps between who / scope / the permission list / the buttons.
func (ed *editor) nextSection(d int) {
	kindOf := func(i int) int {
		switch ed.rows[i].kind {
		case erWho:
			return 0
		case erScope:
			return 1
		case erButtons:
			return 3
		default:
			return 2
		}
	}
	cur := kindOf(ed.cursor)
	for i := 1; i <= len(ed.rows); i++ {
		j := (ed.cursor + d*i + len(ed.rows)*2) % len(ed.rows)
		if k := kindOf(j); k != cur {
			// land on the first row of that section
			if d > 0 || k == 3 || k == 0 || k == 1 {
				ed.cursor = j
				if d < 0 && k == 2 {
					ed.cursor = ed.firstPermRow()
				}
				return
			}
			ed.cursor = j
			return
		}
	}
}

func (ed *editor) horizontal(d int) {
	switch ed.row().kind {
	case erScope:
		ed.scope = delegation.Scope((int(ed.scope) + d + 3) % 3)
	case erButtons:
		ed.col = (ed.col + d + 2) % 2
	}
}

func (ed *editor) activate(enter bool) editorAction {
	switch r := ed.row(); r.kind {
	case erWho:
		return actPickWho
	case erScope:
		ed.horizontal(1)
	case erGroup:
		// toggle the whole group: all selected → none, otherwise all
		all := true
		var names []string
		for _, x := range ed.rows {
			if x.kind == erPerm && x.group == r.group {
				names = append(names, x.perm.Name)
				if !ed.perms.Has(x.perm.Name) {
					all = false
				}
			}
		}
		if all {
			ed.perms = ed.perms.Without(names...)
		} else {
			ed.perms = ed.perms.With(names...)
		}
	case erPerm:
		if ed.perms.Has(r.perm.Name) {
			ed.perms = ed.perms.Without(r.perm.Name)
		} else {
			ed.perms = ed.perms.With(r.perm.Name)
		}
		if enter {
			ed.move(1)
		}
	case erButtons:
		if ed.col == 0 {
			return ed.confirm()
		}
		return actCancel
	}
	return actNone
}

func (ed *editor) selectVisible(on bool) {
	var names []string
	for _, r := range ed.rows {
		if r.kind == erPerm {
			names = append(names, r.perm.Name)
		}
	}
	if on {
		ed.perms = ed.perms.With(names...)
	} else {
		ed.perms = ed.perms.Without(names...)
	}
}

func (ed *editor) confirm() editorAction {
	if ed.kind == editGrant && ed.who.Kind != delegation.WhoEveryone && ed.who.Name == "" && ed.who.ID < 0 {
		ed.errMsg = "choose a user, a group or everyone first"
		ed.cursor = 0
		return actNone
	}
	if ed.perms.Empty() {
		ed.errMsg = "select at least one permission (or cancel; deleting the entry is d on the main screen)"
		return actNone
	}
	return actOK
}

// ---------------------------------------------------------------- view

// listHeight is the number of rows the list may use: the screen minus the
// title, the info line, an error line if any, the note line and the help line.
func (ed *editor) listHeight() int { return max(3, ed.height-ed.headerLines()-2) }

func (ed *editor) headerLines() int {
	if ed.errMsg != "" {
		return 3
	}
	return 2
}

func (ed *editor) title() string {
	switch ed.kind {
	case editSet:
		return "Permission set " + ed.setName + " on " + ed.dataset
	case editCreate:
		return "Create-time permissions on " + ed.dataset
	default:
		return "Permission entry on " + ed.dataset
	}
}

func (ed *editor) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Width(ed.width).Render(ed.title()) + "\n")
	// summary line
	sum := fmt.Sprintf("%d selected", len(ed.perms))
	if !ed.filter.Empty() || ed.typing {
		f := styleFocus.Render("/")
		if ed.typing {
			f += ed.filter.View()
		} else {
			f += styleFocus.Render(ed.filter.Value())
		}
		sum += "   filter: " + f
	}
	switch ed.kind {
	case editSet:
		b.WriteString(" " + styleLabel.Render("Members of ") + styleValue.Render(ed.setName) + "   " + styleMuted.Render(sum) + "\n")
	case editCreate:
		b.WriteString(" " + styleMuted.Render("Granted locally to the creator of every new descendant file system.   "+sum) + "\n")
	default:
		b.WriteString(" " + styleMuted.Render(sum) + "\n")
	}
	if ed.errMsg != "" {
		b.WriteString(" " + styleErr.Render(ed.errMsg) + "\n")
	}
	// visible window
	h := ed.listHeight()
	start := 0
	if ed.cursor >= h {
		start = ed.cursor - h + 1
	}
	// note of the current permission (shown under the list)
	note := ""
	for i := start; i < len(ed.rows) && i < start+h; i++ {
		r := ed.rows[i]
		sel := i == ed.cursor
		line := ""
		switch r.kind {
		case erWho:
			label := ed.who.String()
			if ed.who.Kind != delegation.WhoEveryone && ed.who.Name == "" && ed.who.ID < 0 {
				label = styleWarn.Render("(choose…)")
			} else {
				label = styleValue.Render(label)
			}
			line = " " + styleLabel.Render(fit("Who:", 12)) + label + styleMuted.Render("   enter to change")
		case erScope:
			var opts []string
			for _, s := range []delegation.Scope{delegation.ScopeBoth, delegation.ScopeLocal, delegation.ScopeDescend} {
				mark := "○ "
				st := styleMuted
				if s == ed.scope {
					mark, st = "◉ ", styleValue
				}
				opts = append(opts, st.Render(mark+s.Describe()))
			}
			line = " " + styleLabel.Render(fit("Applies to:", 12)) + strings.Join(opts, "   ")
			if sel {
				line += styleMuted.Render("   ←/→ or space")
			}
		case erGroup:
			n, total := 0, 0
			for _, x := range ed.rows {
				if x.kind == erPerm && x.group == r.group {
					total++
					if ed.perms.Has(x.perm.Name) {
						n++
					}
				}
			}
			cnt := fmt.Sprintf(" %d/%d ", n, total)
			title := " ── " + r.group + " "
			rule := ed.width - lipgloss.Width(title) - len(cnt) - 1
			if rule < 0 {
				rule = 0
			}
			line = styleHeader.Render(title) + styleMuted.Render(strings.Repeat("─", rule)+cnt)
		case erPerm:
			box := "[ ]"
			if ed.perms.Has(r.perm.Name) {
				box = "[x]"
			}
			name := fit(r.perm.Name, 24)
			if r.perm.NotFreeBSD {
				name = styleMuted.Render(name)
			}
			noteW := ed.width - 32
			line = "   " + box + " " + name + " " + styleMuted.Render(fit(r.perm.Note, noteW))
			if sel {
				note = r.perm.Note
				if len(r.perm.Needs) > 0 {
					note += "  — needs: " + strings.Join(r.perm.Needs, ", ")
				}
				note = r.perm.Name + " (" + r.perm.Kind.String() + "): " + note
			}
		case erButtons:
			ok, cancel := styleButton.Render("OK"), styleButton.Render("Cancel")
			if sel && ed.col == 0 {
				ok = styleButtonOn.Render("OK")
			} else if sel {
				cancel = styleButtonOn.Render("Cancel")
			}
			line = "   " + ok + "  " + cancel
		}
		if sel && r.kind != erButtons {
			line = styleSelected.Width(ed.width).Render(plainLine(r, ed))
		}
		b.WriteString(line + "\n")
	}
	for i := len(ed.rows) - start; i < h; i++ {
		b.WriteString("\n")
	}
	b.WriteString(" " + styleMuted.Render(fit(note, ed.width-2)) + "\n")
	b.WriteString(helpLine("space", "toggle (group: all)", "enter", "toggle+next", "/", "filter", "a/n", "all/none shown", "p", "presets", "x", "non-FreeBSD", "^S", "OK", "esc", "cancel"))
	return b.String()
}

// plainLine renders the row without colours (for the selected-row bar).
func plainLine(r edRow, ed *editor) string {
	switch r.kind {
	case erWho:
		label := ed.who.String()
		if ed.who.Kind != delegation.WhoEveryone && ed.who.Name == "" && ed.who.ID < 0 {
			label = "(choose…)"
		}
		return " " + fit("Who:", 12) + label + "   enter to change"
	case erScope:
		var opts []string
		for _, s := range []delegation.Scope{delegation.ScopeBoth, delegation.ScopeLocal, delegation.ScopeDescend} {
			mark := "○ "
			if s == ed.scope {
				mark = "◉ "
			}
			opts = append(opts, mark+s.Describe())
		}
		return " " + fit("Applies to:", 12) + strings.Join(opts, "   ") + "   ←/→ or space"
	case erGroup:
		return " ── " + r.group + "   (space: select/deselect the whole group)"
	case erPerm:
		box := "[ ]"
		if ed.perms.Has(r.perm.Name) {
			box = "[x]"
		}
		return "   " + box + " " + fit(r.perm.Name, 24) + " " + fit(r.perm.Note, ed.width-32)
	}
	return ""
}
