// Package ui is the bubbletea front-end of zfs-allow.
package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/olgeni/zfs-allow/delegation"
)

type screen int

const (
	scrPick screen = iota
	scrMain
	scrEditor
	scrForm
	scrPlan
	scrBusy
	scrView   // help, keys, effective, ancestors (viewport)
	scrPicker // filterable list (who, user, preset)
)

type (
	datasetsMsg struct {
		list []delegation.Dataset
		err  error
	}
	loadedMsg struct {
		l   *delegation.Listing
		err error
	}
	principalsMsg struct{ users, groups []delegation.Principal }
	appliedMsg    struct {
		errs []error
		n    int
	}
)

type rowKind int

const (
	rkSet rowKind = iota
	rkCreate
	rkGrant
)

// uiRow is one line of the main table.
type uiRow struct {
	kind rowKind
	set  delegation.Set
	row  delegation.Row
}

// histEntry is one undo step: the working copy and the pending purges.
type histEntry struct {
	cur    *delegation.Delegations
	purges []delegation.Who
}

// Model is the root bubbletea model.
type Model struct {
	scr, prevScr screen
	width        int
	height       int

	// picker
	datasets   []delegation.Dataset
	pickCursor int
	pickOffset int
	pickFilter string
	pickTyping bool
	pickDef    string // dataset to start on (the cwd's)
	fromPicker bool   // the session started on the picker: q/esc on the main screen go back to it

	// dataset
	dataset string
	listing *delegation.Listing
	cur     *delegation.Delegations
	purges  []delegation.Who // pending "zfs unallow -r" (revoke on the dataset and every descendant)
	history []histEntry
	rows    []uiRow
	cursor  int
	offset  int

	// editor
	ed      *editor
	editIdx int // row being edited, -1 for a new one

	// list picker
	pk      *picker
	pkDone  func(m *Model, value string) tea.Cmd
	pkAbort func(m *Model)

	// forms
	form      *form
	formDone  func(m *Model) tea.Cmd
	formAbort func(m *Model)
	formVals  struct {
		s   string
		yes bool
	}

	// viewport screens and plan
	vp      viewport.Model
	vpTitle string
	plan    *delegation.Plan
	probs   []delegation.Problem

	users, groups []delegation.Principal
	env           delegation.Env

	status, errMsg string
	busyMsg        string
	quitting       bool
	fatal          string
}

// New creates the model; dataset "" starts on the picker with pickDef selected.
func New(dataset, pickDef string) *Model {
	m := &Model{dataset: dataset, pickDef: pickDef, editIdx: -1, width: 80, height: 24}
	m.vp = viewport.New(80, 20)
	if dataset == "" {
		m.scr = scrPick
		m.fromPicker = true
	} else {
		m.scr = scrMain
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{loadPrincipals, m.loadEnv()}
	if m.dataset == "" {
		cmds = append(cmds, loadDatasets)
	} else {
		cmds = append(cmds, m.load())
	}
	return tea.Batch(cmds...)
}

func loadDatasets() tea.Msg {
	l, err := delegation.Datasets()
	return datasetsMsg{l, err}
}

func loadPrincipals() tea.Msg {
	return principalsMsg{delegation.Users(), delegation.Groups()}
}

func (m *Model) loadEnv() tea.Cmd {
	return func() tea.Msg {
		return envMsg{m.computeEnv()}
	}
}

type envMsg struct{ env delegation.Env }

func (m *Model) computeEnv() delegation.Env {
	env := delegation.Env{}
	if id, err := delegation.CurrentIdentity(); err == nil {
		env.Operator = id
	}
	env.Usermount, _ = delegation.Usermount()
	return env
}

func (m *Model) load() tea.Cmd {
	ds := m.dataset
	return func() tea.Msg {
		l, err := delegation.Load(ds)
		return loadedMsg{l, err}
	}
}

func (m *Model) setStatus(s string) { m.status, m.errMsg = s, "" }
func (m *Model) setError(s string)  { m.errMsg, m.status = s, "" }

func (m *Model) modified() bool {
	return m.listing != nil && (!m.cur.Equal(m.listing.Own) || len(m.purges) > 0)
}

func (m *Model) push() {
	m.history = append(m.history, histEntry{m.cur.Clone(), append([]delegation.Who(nil), m.purges...)})
	if len(m.history) > 100 {
		m.history = m.history[1:]
	}
}

// rebuildRows recomputes the table from the working copy.
func (m *Model) rebuildRows() {
	m.rows = m.rows[:0]
	if m.cur == nil {
		return
	}
	for _, s := range m.cur.Sets {
		m.rows = append(m.rows, uiRow{kind: rkSet, set: s})
	}
	if !m.cur.Create.Empty() {
		m.rows = append(m.rows, uiRow{kind: rkCreate})
	}
	for _, r := range m.cur.Rows() {
		m.rows = append(m.rows, uiRow{kind: rkGrant, row: r})
	}
	m.clampCursor()
}

func (m *Model) listHeight() int { return max(3, m.height-10) }

func (m *Model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// availableSets lists the @sets defined on the dataset (working copy) or an ancestor.
func (m *Model) availableSets() []string {
	seen := map[string]bool{}
	var res []string
	for _, d := range append([]*delegation.Delegations{m.cur}, m.listing.Ancestors...) {
		if d == nil {
			continue
		}
		for _, s := range d.Sets {
			if !seen[s.Name] {
				seen[s.Name] = true
				res = append(res, s.Name)
			}
		}
	}
	sort.Strings(res)
	return res
}

// workingListing is the listing with the working copy as the dataset's own
// delegations (for effective / preflight what-ifs).
func (m *Model) workingListing() *delegation.Listing {
	return &delegation.Listing{Dataset: m.listing.Dataset, Own: m.cur, Ancestors: m.listing.Ancestors}
}

// openPicker shows a filterable list; done gets the chosen value.
func (m *Model) openPicker(title, desc string, items []pickItem, initial string, done func(m *Model, value string) tea.Cmd, abort func(m *Model)) tea.Cmd {
	m.pk = newPicker(title, desc, items, initial, m.width, m.height)
	m.pkDone, m.pkAbort = done, abort
	m.prevScr, m.scr = m.scr, scrPicker
	return nil
}

// whoItems lists everyone, the manual entry, the users and the groups.
func (m *Model) whoItems() []pickItem {
	items := []pickItem{
		{"everyone                   — every user (zfs allow -e)", "everyone"},
		{"» type a principal manually (user:NAME, group:NAME, user:UID, group:GID)…", manualWho},
	}
	for _, u := range m.users {
		items = append(items, pickItem{fmt.Sprintf("user:%-20s (uid %d)", u.Name, u.ID), "user:" + u.Name})
	}
	for _, g := range m.groups {
		items = append(items, pickItem{fmt.Sprintf("group:%-19s (gid %d)", g.Name, g.ID), "group:" + g.Name})
	}
	return items
}

func (m *Model) userItems() []pickItem {
	var items []pickItem
	for _, u := range m.users {
		items = append(items, pickItem{fmt.Sprintf("%-20s (uid %d)", u.Name, u.ID), u.Name})
	}
	return items
}

func presetItems() []pickItem {
	var items []pickItem
	for _, p := range delegation.Presets {
		items = append(items, pickItem{fmt.Sprintf("%-20s %s", p.Name, p.Desc), p.Name})
	}
	return items
}

func (m *Model) openForm(f *form, done func(m *Model) tea.Cmd, abort func(m *Model)) tea.Cmd {
	m.form, m.formDone, m.formAbort = f, done, abort
	m.form.resize(min(m.width, 110))
	m.prevScr, m.scr = m.scr, scrForm
	return m.form.Init()
}

// ---------------------------------------------------------------- update

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.vp.Width, m.vp.Height = msg.Width, max(3, msg.Height-3)
		if m.ed != nil {
			m.ed.setSize(msg.Width, msg.Height)
		}
		if m.pk != nil {
			m.pk.setSize(msg.Width, msg.Height)
		}
		if m.form != nil {
			m.form.resize(min(msg.Width, 110))
		}
		m.clampCursor()
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
	case envMsg:
		m.env = msg.env
		if m.listing != nil && m.env.Operator.Name != "" {
			m.env.Effective = delegation.EffectiveFor(m.listing, m.env.Operator)
		}
		return m, nil
	case principalsMsg:
		m.users, m.groups = msg.users, msg.groups
		return m, nil
	case datasetsMsg:
		if msg.err != nil {
			m.fatal = msg.err.Error()
			m.quitting = true
			return m, tea.Quit
		}
		m.datasets = msg.list
		m.pickCursor = 0
		for i, d := range msg.list {
			if d.Name == m.pickDef {
				m.pickCursor = i
			}
		}
		m.clampPick()
		return m, nil
	case loadedMsg:
		if msg.err != nil {
			if m.listing == nil {
				m.fatal = msg.err.Error()
				m.quitting = true
				return m, tea.Quit
			}
			m.scr = scrMain
			m.setError(msg.err.Error())
			return m, nil
		}
		m.listing = msg.l
		m.dataset = msg.l.Dataset.Name
		m.cur = msg.l.Own.Clone()
		m.history, m.purges = nil, nil
		m.rebuildRows()
		if m.scr == scrBusy {
			m.scr = scrMain
		}
		if m.env.Operator.Name != "" {
			m.env.Effective = delegation.EffectiveFor(m.listing, m.env.Operator)
		}
		return m, nil
	case appliedMsg:
		if len(msg.errs) > 0 {
			var b strings.Builder
			for _, e := range msg.errs {
				b.WriteString(e.Error() + "\n")
			}
			m.showView("Errors", styleErr.Render(fmt.Sprintf("%d of %d command(s) failed:", len(msg.errs), msg.n))+"\n\n"+b.String()+
				"\n"+styleMuted.Render("The state below is reloaded from the kernel."))
			m.prevScr = scrMain
		} else {
			m.scr = scrMain
			m.setStatus(fmt.Sprintf("Applied %d command(s)", msg.n))
		}
		return m, m.load()
	}

	switch m.scr {
	case scrPick:
		return m.updatePick(msg)
	case scrMain:
		return m.updateMain(msg)
	case scrEditor:
		return m.updateEditor(msg)
	case scrPicker:
		switch m.pk.Update(msg) {
		case pickDone:
			m.scr = m.prevScr
			return m, m.pkDone(m, m.pk.Value())
		case pickCancel:
			m.scr = m.prevScr
			if m.pkAbort != nil {
				m.pkAbort(m)
			}
		}
		return m, nil
	case scrForm:
		return m.updateForm(msg)
	case scrPlan:
		return m.updatePlan(msg)
	case scrView:
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "esc", "q", "?", "h", "i", "E", "enter":
				m.scr = m.prevScr
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case scrBusy:
		return m, nil
	}
	return m, nil
}

// ---------------------------------------------------------------- picker

func (m *Model) pickVisible() []int {
	f := strings.ToLower(m.pickFilter)
	var idx []int
	for i, d := range m.datasets {
		if f == "" || strings.Contains(strings.ToLower(d.Name), f) {
			idx = append(idx, i)
		}
	}
	return idx
}

func (m *Model) clampPick() {
	vis := m.pickVisible()
	if m.pickCursor >= len(vis) {
		m.pickCursor = len(vis) - 1
	}
	if m.pickCursor < 0 {
		m.pickCursor = 0
	}
	h := max(3, m.height-6)
	if m.pickCursor < m.pickOffset {
		m.pickOffset = m.pickCursor
	}
	if m.pickCursor >= m.pickOffset+h {
		m.pickOffset = m.pickCursor - h + 1
	}
}

func (m *Model) updatePick(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.pickTyping {
		switch k.String() {
		case "esc":
			m.pickFilter, m.pickTyping = "", false
		case "enter", "up", "down":
			m.pickTyping = false
			if k.String() != "enter" {
				return m.updatePick(msg)
			}
			return m.pickEnter()
		case "backspace":
			if m.pickFilter != "" {
				m.pickFilter = m.pickFilter[:len(m.pickFilter)-1]
			}
		default:
			if k.Type == tea.KeyRunes {
				m.pickFilter += k.String()
				m.pickCursor = 0
			}
		}
		m.clampPick()
		return m, nil
	}
	switch k.String() {
	case "esc", "q":
		// clear the filter first; then back to the open dataset (when the
		// session started there) or quit
		if m.pickFilter != "" {
			m.pickFilter = ""
			m.clampPick()
			return m, nil
		}
		if m.listing != nil && !m.fromPicker {
			m.scr = scrMain
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "/":
		m.pickTyping = true
	case "up", "k":
		m.pickCursor--
	case "down", "j":
		m.pickCursor++
	case "pgup":
		m.pickCursor -= max(3, m.height-6)
	case "pgdown":
		m.pickCursor += max(3, m.height-6)
	case "home", "g":
		m.pickCursor = 0
	case "end", "G":
		m.pickCursor = len(m.pickVisible()) - 1
	case "enter":
		return m.pickEnter()
	case "?":
		m.showView("Help", helpText)
	}
	m.clampPick()
	return m, nil
}

func (m *Model) pickEnter() (tea.Model, tea.Cmd) {
	vis := m.pickVisible()
	if len(vis) == 0 {
		return m, nil
	}
	d := m.datasets[vis[m.pickCursor]]
	m.dataset = d.Name
	m.cursor, m.offset = 0, 0
	m.scr, m.busyMsg = scrBusy, "Reading delegations of "+d.Name+"…"
	return m, m.load()
}

func (m *Model) pickView() string {
	var b strings.Builder
	b.WriteString(styleTitle.Width(m.width).Render("zfs-allow — choose a dataset") + "\n")
	if m.datasets == nil {
		return b.String() + "\n  Listing datasets…\n"
	}
	f := ""
	if m.pickFilter != "" || m.pickTyping {
		f = "   filter: " + styleFocus.Render("/"+m.pickFilter)
		if m.pickTyping {
			f += styleFocus.Render("▏")
		}
	}
	b.WriteString(" " + styleMuted.Render(fmt.Sprintf("%d datasets (file systems and volumes)", len(m.datasets))) + f + "\n")
	vis := m.pickVisible()
	// the name column is as wide as the widest (indented) name, the mount
	// point gets the rest
	wName := 20
	for _, i := range vis {
		d := m.datasets[i]
		n := len([]rune(d.Name))
		if m.pickFilter == "" && d.Depth() > 0 {
			n = 2*d.Depth() + 2 + len([]rune(d.Name[strings.LastIndexByte(d.Name, '/')+1:]))
		}
		wName = max(wName, n)
	}
	wName = min(wName, max(20, m.width-2-11-24))
	wMount := max(10, m.width-wName-2-11-1)
	b.WriteString(styleHeader.Render(" "+fit("Dataset", wName)+" "+fit("Type", 10)+" "+fit("Mount point", wMount)) + "\n")
	h := max(3, m.height-6)
	for i := m.pickOffset; i < len(vis) && i < m.pickOffset+h; i++ {
		d := m.datasets[vis[i]]
		indent := strings.Repeat("  ", d.Depth())
		if m.pickFilter != "" {
			indent = ""
		}
		name := indent + d.Name
		if m.pickFilter == "" && d.Depth() > 0 {
			name = indent + "└ " + d.Name[strings.LastIndexByte(d.Name, '/')+1:]
		}
		typ := d.Type
		mp := d.Mountpoint
		if d.IsVolume() {
			typ, mp = "volume", "—"
		} else if d.Mounted {
			mp = d.Mountpoint
		} else if d.Mountpoint != "none" && d.Mountpoint != "legacy" {
			mp = d.Mountpoint + " (not mounted)"
		}
		if d.Name == m.pickDef {
			mp += " ●"
		}
		line := " " + fit(name, wName) + " " + fit(typ, 10) + " " + fit(mp, wMount)
		if i == m.pickCursor {
			line = styleSelected.Width(m.width).Render(line)
		} else if d.Name == m.pickDef {
			line = styleFocus.Render(line)
		}
		b.WriteString(line + "\n")
	}
	for i := len(vis) - m.pickOffset; i < h; i++ {
		b.WriteString("\n")
	}
	def := ""
	if m.pickDef != "" {
		def = "● = the dataset of the current directory   "
	}
	back := []string{"q/esc", "quit"}
	if m.listing != nil && !m.fromPicker {
		back = []string{"q/esc", "back to " + m.dataset}
	}
	b.WriteString(" " + styleMuted.Render(def) + helpLine(append([]string{"enter", "open", "/", "filter", "?", "help"}, back...)...))
	return b.String()
}

// ---------------------------------------------------------------- main

func (m *Model) updateMain(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok || m.listing == nil {
		return m, nil
	}
	switch k.String() {
	case "q", "esc", "backspace":
		// back: to the dataset list when the session started there, else quit
		if m.modified() {
			return m, m.openForm(confirmForm("Discard unsaved changes?", "The delegations have pending edits that were not applied.", &m.formVals.yes),
				func(m *Model) tea.Cmd {
					if m.formVals.yes {
						if m.fromPicker {
							m.cur = m.listing.Own.Clone()
							m.history, m.purges = nil, nil
							m.rebuildRows()
							return m.toPicker()
						}
						m.quitting = true
						return tea.Quit
					}
					m.scr = scrMain
					return nil
				}, nil)
		}
		if m.fromPicker {
			return m, m.toPicker()
		}
		m.quitting = true
		return m, tea.Quit
	case "?", "f1":
		m.showView("Help", helpText)
	case "h":
		m.showView("Keys", keymapView(m.width))
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "pgup":
		m.cursor -= m.listHeight()
	case "pgdown":
		m.cursor += m.listHeight()
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.rows) - 1
	case "enter", "e":
		if len(m.rows) > 0 {
			return m, m.editRow(m.cursor)
		}
	case "a":
		m.formVals.s = "grant"
		return m, m.openForm(addKindForm(&m.formVals.s), func(m *Model) tea.Cmd {
			switch m.formVals.s {
			case "create":
				m.editIdx = -1
				for i, r := range m.rows {
					if r.kind == rkCreate {
						m.editIdx = i
					}
				}
				m.ed = newEditor(editCreate, m.dataset, delegation.Who{}, 0, m.cur.Create, m.availableSets(), m.width, m.height)
				m.scr = scrEditor
				return nil
			case "set":
				m.formVals.s = "@"
				return m.openForm(setNameForm(&m.formVals.s, m.availableSets()), func(m *Model) tea.Cmd {
					name := strings.TrimSpace(m.formVals.s)
					if !strings.HasPrefix(name, "@") {
						name = "@" + name
					}
					m.editIdx = -1
					m.ed = newEditor(editSet, m.dataset, delegation.Who{}, 0, nil, m.availableSets(), m.width, m.height)
					m.ed.setName = name
					m.ed.rebuild()
					m.scr = scrEditor
					return nil
				}, func(m *Model) { m.scr = scrMain })
			default:
				m.editIdx = -1
				m.ed = newEditor(editGrant, m.dataset, delegation.Who{Kind: delegation.WhoUser, ID: -1}, delegation.ScopeBoth, nil, m.availableSets(), m.width, m.height)
				m.scr = scrEditor
				return m.pickWho()
			}
		}, func(m *Model) { m.scr = scrMain })
	case "d", "delete":
		if len(m.rows) == 0 {
			return m, nil
		}
		m.push()
		r := m.rows[m.cursor]
		switch r.kind {
		case rkSet:
			m.cur.SetSet(delegation.Set{Name: r.set.Name})
			m.setStatus("Removed " + r.set.Name + " (users of the set lose its permissions)")
		case rkCreate:
			m.cur.Create = nil
			m.setStatus("Removed the create-time permissions")
		default:
			rows := m.cur.Rows()
			rows = append(rows[:m.cursor-m.grantOffset()], rows[m.cursor-m.grantOffset()+1:]...)
			m.cur.FromRows(rows)
			m.setStatus(fmt.Sprintf("Removed %s (%s)", r.row.Who, strings.ToLower(r.row.Scope.Describe())))
		}
		m.rebuildRows()
	case "R":
		if len(m.rows) == 0 || m.rows[m.cursor].kind != rkGrant {
			m.setError("R revokes a user/group/everyone entry here and on every descendant (zfs unallow -r)")
			return m, nil
		}
		w := m.rows[m.cursor].row.Who
		m.formVals.yes = false
		return m, m.openForm(confirmForm("Revoke everything from "+w.String()+" on "+m.dataset+" and every descendant?",
			"zfs unallow -r removes the entry here and on every dataset below; it is planned now and runs when you apply (A).", &m.formVals.yes),
			func(m *Model) tea.Cmd {
				m.scr = scrMain
				if !m.formVals.yes {
					return nil
				}
				m.push()
				m.cur.SetGrant(delegation.Grant{Who: w})
				found := false
				for _, p := range m.purges {
					if p.Same(w) {
						found = true
					}
				}
				if !found {
					m.purges = append(m.purges, w)
				}
				m.rebuildRows()
				m.setStatus(fmt.Sprintf("Revocation of %s on the whole subtree pending — press A to apply", w))
				return nil
			}, func(m *Model) { m.scr = scrMain })
	case "u":
		if n := len(m.history); n > 0 {
			m.cur, m.purges = m.history[n-1].cur, m.history[n-1].purges
			m.history = m.history[:n-1]
			m.rebuildRows()
			m.setStatus("Undone")
		} else {
			m.setStatus("Nothing to undo")
		}
	case "r":
		m.scr, m.busyMsg = scrBusy, "Reloading…"
		return m, m.load()
	case "A":
		return m, m.startApply()
	case "E":
		return m, m.openPicker("Effective permissions of", "What this user may do here, through their own entries, their groups, everyone and the ancestors.",
			m.userItems(), m.env.Operator.Name, func(m *Model, v string) tea.Cmd {
				m.showEffective(v)
				return nil
			}, nil)
	case "i":
		m.showView("Ancestors", m.ancestorsText())
	case "D":
		if m.modified() {
			m.setError("apply or undo the pending edits before switching dataset")
			return m, nil
		}
		return m, m.toPicker()
	}
	m.clampCursor()
	return m, nil
}

// toPicker shows the dataset list with the cursor on the current dataset.
func (m *Model) toPicker() tea.Cmd {
	m.pickDef = m.dataset
	m.pickFilter, m.pickTyping = "", false
	m.scr = scrPick
	if m.datasets == nil {
		return loadDatasets
	}
	for i, d := range m.datasets {
		if d.Name == m.dataset {
			m.pickCursor = i
		}
	}
	m.clampPick()
	return nil
}

// grantOffset is the index of the first grant row in m.rows.
func (m *Model) grantOffset() int {
	n := 0
	for _, r := range m.rows {
		if r.kind != rkGrant {
			n++
		}
	}
	return n
}

func (m *Model) editRow(i int) tea.Cmd {
	r := m.rows[i]
	m.editIdx = i
	switch r.kind {
	case rkSet:
		m.ed = newEditor(editSet, m.dataset, delegation.Who{}, 0, r.set.Perms, m.availableSets(), m.width, m.height)
		m.ed.setName = r.set.Name
		m.ed.rebuild()
	case rkCreate:
		m.ed = newEditor(editCreate, m.dataset, delegation.Who{}, 0, m.cur.Create, m.availableSets(), m.width, m.height)
	default:
		m.ed = newEditor(editGrant, m.dataset, r.row.Who, r.row.Scope, r.row.Perms, m.availableSets(), m.width, m.height)
	}
	m.scr = scrEditor
	return nil
}

func (m *Model) pickWho() tea.Cmd {
	initial := m.ed.who.Spec()
	if m.ed.who.Kind != delegation.WhoEveryone && m.ed.who.Name == "" && m.ed.who.ID < 0 {
		initial = ""
	}
	return m.openPicker("Delegate to", "A user gets the permissions directly; a group gives them to all its members.", m.whoItems(), initial, func(m *Model, v string) tea.Cmd {
		if v == manualWho {
			m.formVals.s = ""
			return m.openForm(manualWhoForm(&m.formVals.s), func(m *Model) tea.Cmd {
				w, err := delegation.ParseWho(m.formVals.s)
				if err == nil {
					m.ed.SetWho(w)
				}
				m.scr = scrEditor
				return nil
			}, func(m *Model) { m.scr = scrEditor })
		}
		if w, err := delegation.ParseWho(v); err == nil {
			m.ed.SetWho(w)
		}
		m.scr = scrEditor
		return nil
	}, func(m *Model) {
		m.scr = scrEditor
		if m.editIdx < 0 && m.ed.who.Kind != delegation.WhoEveryone && m.ed.who.Name == "" && m.ed.who.ID < 0 {
			// new entry and no grantee chosen: nothing to edit
			m.scr = scrMain
		}
	})
}

func (m *Model) updateEditor(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.ed.Update(msg) {
	case actCancel:
		m.scr = scrMain
	case actPickWho:
		return m, m.pickWho()
	case actPickPreset:
		return m, m.openPicker("Preset", "Replaces the current selection with the bundle; tick more afterwards if needed.", presetItems(), "", func(m *Model, v string) tea.Cmd {
			if p, ok := delegation.PresetByName(v); ok {
				m.ed.ApplyPreset(p.Perms)
			}
			m.scr = scrEditor
			return nil
		}, nil)
	case actOK:
		m.push()
		switch m.ed.kind {
		case editSet:
			m.cur.SetSet(delegation.Set{Name: m.ed.setName, Perms: m.ed.perms})
		case editCreate:
			m.cur.Create = m.ed.perms.With()
		default:
			rows := m.cur.Rows()
			nr := delegation.Row{Who: m.ed.who, Scope: m.ed.scope, Perms: m.ed.perms}
			if m.editIdx >= 0 && m.rows[m.editIdx].kind == rkGrant {
				rows[m.editIdx-m.grantOffset()] = nr
			} else {
				rows = append(rows, nr)
			}
			m.cur.FromRows(rows)
		}
		m.rebuildRows()
		// put the cursor on the edited thing
		for i, r := range m.rows {
			switch m.ed.kind {
			case editSet:
				if r.kind == rkSet && r.set.Name == m.ed.setName {
					m.cursor = i
				}
			case editCreate:
				if r.kind == rkCreate {
					m.cursor = i
				}
			default:
				if r.kind == rkGrant && r.row.Who.Same(m.ed.who) && r.row.Scope == m.ed.scope {
					m.cursor = i
				}
			}
		}
		m.clampCursor()
		m.scr = scrMain
	}
	return m, nil
}

func (m *Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
		m.scr = m.prevScr
		if m.formAbort != nil {
			m.formAbort(m)
		}
		return m, nil
	}
	f, cmd := m.form.Update(msg)
	if hf, ok := f.(*huh.Form); ok {
		m.form.Form = hf
	}
	switch m.form.State {
	case huh.StateCompleted:
		m.scr = m.prevScr
		return m, m.formDone(m)
	case huh.StateAborted:
		m.scr = m.prevScr
		if m.formAbort != nil {
			m.formAbort(m)
		}
		return m, nil
	}
	return m, cmd
}

// ---------------------------------------------------------------- plan

func (m *Model) startApply() tea.Cmd {
	if !m.modified() {
		m.setStatus("No pending changes")
		return nil
	}
	m.plan = delegation.Diff(m.listing.Own, m.cur)
	for _, w := range m.purges {
		m.plan.AddRecursive(w)
	}
	m.probs = delegation.Preflight(m.workingListing(), m.plan, m.env)
	m.vp.SetContent(m.planText())
	m.vp.GotoTop()
	m.prevScr, m.scr = scrMain, scrPlan
	return nil
}

func (m *Model) updatePlan(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "n", "q":
			m.scr = scrMain
			return m, nil
		case "enter", "y":
			plan := m.plan
			n := len(plan.Steps)
			m.scr, m.busyMsg = scrBusy, fmt.Sprintf("Running %d zfs command(s)…", n)
			return m, func() tea.Msg {
				return appliedMsg{plan.Execute(nil), n}
			}
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *Model) planText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d command(s) on %s\n\n", styleHeader.Render("Plan:"), len(m.plan.Steps), m.dataset)
	for i, s := range m.plan.Steps {
		fmt.Fprintf(&b, "%s %s\n      %s\n", styleFocus.Render(fmt.Sprintf("[%d/%d]", i+1, len(m.plan.Steps))), s, styleMuted.Render(s.Desc))
	}
	if len(m.probs) > 0 {
		b.WriteString("\n")
		fatal := 0
		for _, p := range m.probs {
			if p.Fatal {
				fatal++
			}
		}
		if fatal > 0 {
			b.WriteString(styleErr.Render(fmt.Sprintf("⚠ %d problem(s) that will make zfs refuse a command:", fatal)) + "\n")
		} else {
			b.WriteString(styleWarn.Render("Pre-flight notes:") + "\n")
		}
		for _, p := range m.probs {
			st := styleWarn
			if p.Fatal {
				st = styleErr
			}
			b.WriteString(st.Render("  • ") + wrapText(p.Msg, m.width-6, "    ") + "\n")
		}
	}
	b.WriteString("\n" + styleMuted.Render("Resulting delegations:") + "\n")
	if f := m.cur.Format(); f != "" {
		for _, line := range strings.Split(strings.TrimRight(f, "\n"), "\n") {
			b.WriteString("  " + styleMuted.Render(line) + "\n")
		}
	} else {
		b.WriteString("  " + styleMuted.Render("(none)") + "\n")
	}
	return b.String()
}

// wrapText wraps s to width, indenting continuation lines.
func wrapText(s string, width int, indent string) string {
	if width < 20 {
		return s
	}
	words := strings.Fields(s)
	var b strings.Builder
	line := 0
	for i, w := range words {
		if i > 0 && line+1+len([]rune(w)) > width {
			b.WriteString("\n" + indent)
			line = len(indent)
		} else if i > 0 {
			b.WriteString(" ")
			line++
		}
		b.WriteString(w)
		line += len([]rune(w))
	}
	return b.String()
}

// ---------------------------------------------------------------- views

func (m *Model) showView(title, text string) {
	m.vpTitle = title
	m.vp.SetContent(text)
	m.vp.GotoTop()
	if m.scr != scrView {
		m.prevScr = m.scr
	}
	m.scr = scrView
}

func (m *Model) showEffective(name string) {
	id, err := delegation.IdentityOf(name)
	if err != nil {
		m.scr = scrMain
		m.setError(err.Error())
		return
	}
	m.showView("Effective permissions", effectiveText(m.workingListing(), id, m.modified(), m.width))
}

// effectiveText renders what id may do on the dataset, grouped by catalogue group.
func effectiveText(l *delegation.Listing, id delegation.Identity, pending bool, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (uid %d, groups %s) on %s\n", styleHeader.Render("Effective permissions of"), id.Name, id.UID, strings.Join(id.Groups, ","), l.Dataset.Name)
	if pending {
		b.WriteString(styleWarn.Render("  (computed from the edited, not yet applied state)") + "\n")
	}
	if id.UID == 0 {
		b.WriteString("\n  root is never restricted by delegations: everything is allowed.\n")
		return b.String()
	}
	eff := delegation.EffectiveFor(l, id)
	if len(eff) == 0 {
		b.WriteString("\n  nothing — no entry for the user, their groups or everyone here or on an ancestor.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  %d permission(s)\n", len(eff))
	groups := append([]string{}, delegation.GroupOrder...)
	groups = append(groups, groupOther)
	for _, g := range groups {
		first := true
		for _, p := range eff.Perms() {
			pg := groupOther
			if c, ok := delegation.Lookup(p); ok {
				pg = c.Group
			}
			if pg != g {
				continue
			}
			if first {
				b.WriteString("\n" + styleHeader.Render(" "+g) + "\n")
				first = false
			}
			var srcs []string
			for _, s := range eff[p] {
				how := "on " + s.Dataset
				if s.Scope == delegation.ScopeDescend {
					how = "from " + s.Dataset
				}
				if s.Via != "" {
					how += " via " + s.Via
				}
				srcs = append(srcs, s.Who.String()+" "+how)
			}
			fmt.Fprintf(&b, "   %s %s\n", fit(p, 24), styleMuted.Render(fit(strings.Join(srcs, "; "), width-30)))
		}
	}
	return b.String()
}

func (m *Model) ancestorsText() string {
	var b strings.Builder
	if len(m.listing.Ancestors) == 0 {
		return "No ancestor of " + m.dataset + " delegates anything.\n"
	}
	b.WriteString("Delegations of the ancestors (nearest first). Their Descendent and\nLocal+Descendent entries also apply to " + m.dataset + ";\nLocal entries do not. Edit them by opening that dataset (D).\n")
	for _, a := range m.listing.Ancestors {
		b.WriteString("\n" + styleHeader.Render(a.Header()) + "\n")
		for _, line := range strings.Split(strings.TrimRight(a.Format(), "\n"), "\n") {
			if strings.HasPrefix(line, "\t") {
				b.WriteString("    " + line[1:] + "\n")
			} else {
				b.WriteString(styleLabel.Render(line) + "\n")
			}
		}
	}
	return b.String()
}

func (m *Model) View() string {
	if m.quitting {
		if m.fatal != "" {
			return styleErr.Render("zfs-allow: "+m.fatal) + "\n"
		}
		return ""
	}
	switch m.scr {
	case scrPick:
		return m.pickView()
	case scrEditor:
		return m.ed.View()
	case scrPicker:
		return m.pk.View()
	case scrForm:
		return m.frame(m.formTitle(), m.form.View(), "")
	case scrPlan:
		return m.frame("Apply changes to "+m.dataset, m.vp.View(), helpLine("y/enter", "run the commands", "esc", "back", "↑/↓", "scroll"))
	case scrView:
		return m.frame(m.vpTitle, m.vp.View(), helpLine("esc", "back", "↑/↓", "scroll"))
	case scrBusy:
		return m.frame("zfs-allow", "\n  "+m.busyMsg+"\n", "")
	}
	return m.mainView()
}

func (m *Model) formTitle() string {
	if m.listing != nil {
		return "zfs-allow — " + m.dataset
	}
	return "zfs-allow"
}

func (m *Model) frame(title, body, help string) string {
	return styleTitle.Width(m.width).Render(title) + "\n" + body + "\n" + help
}

func (m *Model) mainView() string {
	if m.listing == nil {
		return styleTitle.Width(m.width).Render("zfs-allow") + "\n\n  Loading " + m.dataset + "…\n"
	}
	var b strings.Builder
	b.WriteString(styleTitle.Width(m.width).Render("Delegated permissions on "+m.dataset) + "\n")
	d := m.listing.Dataset
	info := d.Type
	if d.IsVolume() {
		info = "volume"
	} else if d.Mounted {
		info += ", mounted at " + d.Mountpoint
	} else {
		info += ", not mounted (mountpoint " + d.Mountpoint + ")"
	}
	b.WriteString(styleLabel.Render(" Dataset: ") + styleValue.Render(d.Name) + styleMuted.Render("   ("+info+")") + "\n")
	anc := "no ancestor delegates anything"
	if n := len(m.listing.Ancestors); n > 0 {
		names := make([]string, 0, n)
		for _, a := range m.listing.Ancestors {
			names = append(names, a.Dataset)
		}
		anc = fmt.Sprintf("also inherits from %s (i to view)", strings.Join(names, ", "))
	}
	op := "root"
	if m.env.Operator.UID != 0 {
		op = m.env.Operator.Name + " (unprivileged: may only delegate what it holds, and revoke only its own)"
		if !m.env.Effective.Has("allow") && m.env.Operator.Name != "" {
			op = m.env.Operator.Name + " (unprivileged and without the allow permission here: read-only)"
		}
	}
	b.WriteString(styleLabel.Render(" Scope:   ") + fitLeft(anc, m.width-10) + "\n")
	b.WriteString(styleLabel.Render(" You:     ") + op + "\n")
	if !m.env.Usermount && m.env.Operator.UID != 0 || !m.env.Usermount {
		b.WriteString(" " + styleWarn.Render("vfs.usermount=0: delegated mount/create/destroy/snapshot… will fail for unprivileged users") + "\n")
	} else {
		b.WriteString("\n")
	}
	hdr := " Delegations"
	if m.modified() {
		hdr += styleWarn.Render("  ● modified — press A to apply")
	}
	if len(m.purges) > 0 {
		var names []string
		for _, w := range m.purges {
			names = append(names, w.String())
		}
		hdr += styleWarn.Render("  (subtree revocation pending: " + strings.Join(names, ", ") + ")")
	}
	b.WriteString(hdr + "\n")
	wIdx, wKind, wWho, wScope := 3, 12, 24, 20
	wPerms := m.width - wIdx - wKind - wWho - wScope - 6
	if wPerms < 20 {
		wWho = max(12, wWho+wPerms-20)
		wPerms = m.width - wIdx - wKind - wWho - wScope - 6
	}
	b.WriteString(styleHeader.Render(" "+fit("#", wIdx)+" "+fit("Kind", wKind)+" "+fit("Who", wWho)+" "+fit("Applies to", wScope)+" "+fit("Permissions", wPerms)) + "\n")
	h := m.listHeight()
	for i := m.offset; i < len(m.rows) && i < m.offset+h; i++ {
		r := m.rows[i]
		var kind, who, scope string
		var perms delegation.PermSet
		switch r.kind {
		case rkSet:
			kind, who, scope, perms = "set", r.set.Name, "here + descendants", r.set.Perms
		case rkCreate:
			kind, who, scope, perms = "create-time", "(creator)", "new descendants", m.cur.Create
		default:
			kind, who, scope, perms = r.row.Who.Kind.String(), r.row.Who.Label(), r.row.Scope.Describe(), r.row.Perms
			switch r.row.Scope {
			case delegation.ScopeBoth:
				scope = "here + descendants"
			case delegation.ScopeLocal:
				scope = "this dataset only"
			default:
				scope = "descendants only"
			}
		}
		ptxt := perms.String()
		if len(perms) > 6 {
			ptxt = fmt.Sprintf("%d: %s", len(perms), ptxt)
		}
		line := " " + fit(fmt.Sprint(i), wIdx) + " " + fit(kind, wKind) + " " + fit(who, wWho) + " " + fit(scope, wScope) + " " + fit(ptxt, wPerms)
		if i == m.cursor {
			line = styleSelected.Width(m.width).Render(line)
		} else if r.kind == rkSet {
			line = styleFocus.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if len(m.rows) == 0 {
		b.WriteString(styleMuted.Render("   (nothing is delegated on this dataset — a to add)") + "\n")
	}
	for i := max(len(m.rows)-m.offset, 1); i < h; i++ {
		b.WriteString("\n")
	}
	// detail of the current row
	detail := ""
	if m.cursor < len(m.rows) {
		r := m.rows[m.cursor]
		switch r.kind {
		case rkSet:
			detail = r.set.Name + " = " + r.set.Perms.String()
		case rkCreate:
			detail = "create-time: " + m.cur.Create.String()
		default:
			detail = r.row.Who.String() + ", " + strings.ToLower(r.row.Scope.Describe()) + ": " + r.row.Perms.String()
		}
	}
	b.WriteString(" " + styleMuted.Render(fit(detail, m.width-2)) + "\n")
	status := m.status
	if m.errMsg != "" {
		status = styleErr.Render(m.errMsg)
	} else if status != "" {
		status = styleOK.Render(status)
	}
	if status != "" {
		b.WriteString(" " + status + "\n")
	} else {
		b.WriteString(helpLine("enter", "edit", "a", "add", "d", "delete", "A", "apply", "u", "undo", "R", "revoke below", "E", "effective", "i", "ancestors", "D", "datasets", "?", "help", "q/esc", m.backLabel()) + "\n")
	}
	return b.String()
}

func (m *Model) backLabel() string {
	if m.fromPicker {
		return "back"
	}
	return "quit"
}

// Run starts the TUI. dataset "" opens the picker with pickDef preselected.
func Run(dataset, pickDef string) error {
	m := New(dataset, pickDef)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return err
	}
	if fm, ok := final.(*Model); ok && fm.fatal != "" {
		fmt.Fprintln(os.Stderr, "zfs-allow:", fm.fatal)
		os.Exit(1)
	}
	return nil
}
