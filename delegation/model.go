package delegation

import (
	"fmt"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// PermSet is a sorted set of permission names (catalogue names or @sets).
// The zero value is the empty set; all operations return new sets.
type PermSet []string

// ParsePerms parses a comma-separated permission list (whitespace tolerant).
func ParsePerms(s string) PermSet {
	var res PermSet
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			res = append(res, f)
		}
	}
	return res.normalize()
}

func (s PermSet) normalize() PermSet {
	if len(s) == 0 {
		return nil
	}
	out := append(PermSet(nil), s...)
	sort.Strings(out)
	j := 0
	for i := range out {
		if i == 0 || out[i] != out[i-1] {
			out[j] = out[i]
			j++
		}
	}
	return out[:j]
}

// String renders the set as zfs allow wants it: comma-separated, sorted.
func (s PermSet) String() string { return strings.Join(s, ",") }

// Has reports whether the set contains p.
func (s PermSet) Has(p string) bool {
	i := sort.SearchStrings(s, p)
	return i < len(s) && s[i] == p
}

// With returns s plus the given permissions.
func (s PermSet) With(ps ...string) PermSet {
	return append(append(PermSet(nil), s...), ps...).normalize()
}

// Without returns s minus the given permissions.
func (s PermSet) Without(ps ...string) PermSet {
	drop := PermSet(ps).normalize()
	var out PermSet
	for _, p := range s {
		if !drop.Has(p) {
			out = append(out, p)
		}
	}
	return out
}

// Union returns s ∪ t.
func (s PermSet) Union(t PermSet) PermSet { return s.With(t...) }

// Intersect returns s ∩ t.
func (s PermSet) Intersect(t PermSet) PermSet {
	var out PermSet
	for _, p := range s {
		if t.Has(p) {
			out = append(out, p)
		}
	}
	return out
}

// Minus returns s \ t.
func (s PermSet) Minus(t PermSet) PermSet { return s.Without(t...) }

// Equal reports whether the sets have the same members.
func (s PermSet) Equal(t PermSet) bool {
	if len(s) != len(t) {
		return false
	}
	for i := range s {
		if s[i] != t[i] {
			return false
		}
	}
	return true
}

// Empty reports whether the set has no members.
func (s PermSet) Empty() bool { return len(s) == 0 }

// Sets returns the @set references in s.
func (s PermSet) Sets() PermSet {
	var out PermSet
	for _, p := range s {
		if IsSet(p) {
			out = append(out, p)
		}
	}
	return out
}

// WhoKind says whom a grant is for.
type WhoKind int

const (
	WhoUser WhoKind = iota
	WhoGroup
	WhoEveryone
)

func (k WhoKind) String() string {
	switch k {
	case WhoUser:
		return "user"
	case WhoGroup:
		return "group"
	default:
		return "everyone"
	}
}

// Flag is the zfs allow option that selects this kind of who.
func (k WhoKind) Flag() string {
	switch k {
	case WhoUser:
		return "-u"
	case WhoGroup:
		return "-g"
	default:
		return "-e"
	}
}

// Who is the grantee of a delegation: a user, a group or everyone. Users
// and groups are identified by name; when the id does not resolve to a name
// (zfs allow prints "user (unknown: 12345)") Name is empty and ID is set.
type Who struct {
	Kind WhoKind
	Name string
	ID   int // -1 when unknown/irrelevant
}

// Everyone is the who of -e grants.
var Everyone = Who{Kind: WhoEveryone, ID: -1}

// User returns a user who by name.
func User(name string) Who { return Who{Kind: WhoUser, Name: name, ID: -1} }

// Group returns a group who by name.
func Group(name string) Who { return Who{Kind: WhoGroup, Name: name, ID: -1} }

// String renders the who as zfs allow prints it ("user bob", "group wheel",
// "everyone", "user (unknown: 12345)").
func (w Who) String() string {
	switch w.Kind {
	case WhoEveryone:
		return "everyone"
	default:
		return w.Kind.String() + " " + w.Label()
	}
}

// Label is the name, or "(unknown: ID)" for unresolved ids, or "everyone".
func (w Who) Label() string {
	if w.Kind == WhoEveryone {
		return "everyone"
	}
	if w.Name == "" {
		return fmt.Sprintf("(unknown: %d)", w.ID)
	}
	return w.Name
}

// Arg is what to pass to zfs allow after the -u/-g flag: the name, or the
// numeric id for unresolved ids.
func (w Who) Arg() string {
	if w.Name == "" {
		return strconv.Itoa(w.ID)
	}
	return w.Name
}

// Spec renders the who in the CLI's compact syntax: user:NAME, group:NAME,
// everyone (user:ID for unresolved ids).
func (w Who) Spec() string {
	if w.Kind == WhoEveryone {
		return "everyone"
	}
	return w.Kind.String() + ":" + w.Arg()
}

// Same reports whether two whos denote the same grantee.
func (w Who) Same(o Who) bool {
	if w.Kind != o.Kind {
		return false
	}
	if w.Kind == WhoEveryone {
		return true
	}
	if w.Name != "" || o.Name != "" {
		return w.Name == o.Name
	}
	return w.ID == o.ID
}

// ParseWho parses user:NAME, u:NAME, group:NAME, g:NAME, everyone, or a
// bare name (a user). Numeric names are ids.
func ParseWho(s string) (Who, error) {
	s = strings.TrimSpace(s)
	if s == "everyone" || s == "-e" {
		return Everyone, nil
	}
	kind := WhoUser
	name := s
	if i := strings.IndexByte(s, ':'); i >= 0 {
		switch s[:i] {
		case "user", "u":
			kind = WhoUser
		case "group", "g":
			kind = WhoGroup
		default:
			return Who{}, fmt.Errorf("invalid principal %q (use user:NAME, group:NAME or everyone)", s)
		}
		name = s[i+1:]
	}
	if name == "" {
		return Who{}, fmt.Errorf("invalid principal %q: empty name", s)
	}
	w := Who{Kind: kind, Name: name, ID: -1}
	if id, err := strconv.Atoi(name); err == nil {
		w.Name, w.ID = "", id
	}
	return w, nil
}

// less orders whos the way zfs allow prints them: users, then groups, then
// everyone; within a kind by the *string* form of the numeric id (the
// order of the ZAP keys "ul$1001" < "ul$12345" < "ul$65534"), falling back
// to the name when an id is not known.
func (w Who) less(o Who) bool {
	if w.Kind != o.Kind {
		return w.Kind < o.Kind
	}
	if w.ID >= 0 && o.ID >= 0 {
		a, b := strconv.Itoa(w.ID), strconv.Itoa(o.ID)
		if a != b {
			return a < b
		}
	}
	if (w.ID >= 0) != (o.ID >= 0) {
		return w.ID >= 0
	}
	return w.Name < o.Name
}

// Resolver maps a user or group name to its id, for ordering; the default
// asks the system (with a cache). Tests may replace it.
var Resolver = func(kind WhoKind, name string) (int, bool) {
	resolveMu.Lock()
	defer resolveMu.Unlock()
	key := kind.String() + ":" + name
	if id, ok := resolveCache[key]; ok {
		return id, id >= 0
	}
	id := -1
	if kind == WhoUser {
		if u, err := user.Lookup(name); err == nil {
			id, _ = strconv.Atoi(u.Uid)
		}
	} else if g, err := user.LookupGroup(name); err == nil {
		id, _ = strconv.Atoi(g.Gid)
	}
	resolveCache[key] = id
	return id, id >= 0
}

var (
	resolveMu    sync.Mutex
	resolveCache = map[string]int{}
)

// Resolved returns w with the id filled in from the name when possible.
func (w Who) Resolved() Who {
	if w.Kind == WhoEveryone || w.Name == "" || w.ID >= 0 {
		return w
	}
	if id, ok := Resolver(w.Kind, w.Name); ok {
		w.ID = id
	}
	return w
}

// Grant is everything delegated to one who on one dataset: the permissions
// that apply to the dataset itself (Local) and the ones that apply to its
// descendants (Descend). "zfs allow" prints their intersection as
// Local+Descendent and the remainders as Local / Descendent.
type Grant struct {
	Who     Who
	Local   PermSet
	Descend PermSet
}

// Empty reports whether the grant carries no permission at all.
func (g Grant) Empty() bool { return g.Local.Empty() && g.Descend.Empty() }

// Both, LocalOnly and DescendOnly split the grant as zfs allow prints it.
func (g Grant) Both() PermSet        { return g.Local.Intersect(g.Descend) }
func (g Grant) LocalOnly() PermSet   { return g.Local.Minus(g.Descend) }
func (g Grant) DescendOnly() PermSet { return g.Descend.Minus(g.Local) }

// Set is a named permission set (@name) defined on a dataset.
type Set struct {
	Name  string // including the @
	Perms PermSet
}

// Delegations is the delegation state of one dataset.
type Delegations struct {
	Dataset string
	Sets    []Set   // sorted by name
	Create  PermSet // create-time permissions (granted locally to the creator of new descendants)
	Grants  []Grant // one per who, in zfs allow order
}

// Empty reports whether the dataset delegates nothing at all.
func (d *Delegations) Empty() bool {
	return d == nil || len(d.Sets) == 0 && d.Create.Empty() && len(d.Grants) == 0
}

// Clone returns a deep copy.
func (d *Delegations) Clone() *Delegations {
	if d == nil {
		return nil
	}
	c := &Delegations{Dataset: d.Dataset, Create: d.Create.With()}
	for _, s := range d.Sets {
		c.Sets = append(c.Sets, Set{s.Name, s.Perms.With()})
	}
	for _, g := range d.Grants {
		c.Grants = append(c.Grants, Grant{g.Who, g.Local.With(), g.Descend.With()})
	}
	return c
}

// Grant returns the grant for w, or nil.
func (d *Delegations) Grant(w Who) *Grant {
	for i := range d.Grants {
		if d.Grants[i].Who.Same(w) {
			return &d.Grants[i]
		}
	}
	return nil
}

// SetGrant stores g (replacing the grant for the same who), dropping it if
// empty, and keeps the order canonical.
func (d *Delegations) SetGrant(g Grant) {
	out := d.Grants[:0]
	for _, o := range d.Grants {
		if !o.Who.Same(g.Who) {
			out = append(out, o)
		}
	}
	d.Grants = out
	if !g.Empty() {
		d.Grants = append(d.Grants, Grant{g.Who.Resolved(), g.Local.normalize(), g.Descend.normalize()})
	}
	d.sortGrants()
}

func (d *Delegations) sortGrants() {
	sort.SliceStable(d.Grants, func(i, j int) bool { return d.Grants[i].Who.less(d.Grants[j].Who) })
}

// Set returns the permission set called name, or nil.
func (d *Delegations) Set(name string) *Set {
	for i := range d.Sets {
		if d.Sets[i].Name == name {
			return &d.Sets[i]
		}
	}
	return nil
}

// SetSet stores s (replacing a set with the same name; an empty set removes it).
func (d *Delegations) SetSet(s Set) {
	out := d.Sets[:0]
	for _, o := range d.Sets {
		if o.Name != s.Name {
			out = append(out, o)
		}
	}
	d.Sets = out
	if !s.Perms.Empty() {
		d.Sets = append(d.Sets, Set{s.Name, s.Perms.normalize()})
	}
	sort.Slice(d.Sets, func(i, j int) bool { return d.Sets[i].Name < d.Sets[j].Name })
}

// Equal reports whether two states are identical.
func (d *Delegations) Equal(o *Delegations) bool {
	if d.Empty() && o.Empty() {
		return true
	}
	if d == nil || o == nil || len(d.Sets) != len(o.Sets) || len(d.Grants) != len(o.Grants) || !d.Create.Equal(o.Create) {
		return false
	}
	for i := range d.Sets {
		if d.Sets[i].Name != o.Sets[i].Name || !d.Sets[i].Perms.Equal(o.Sets[i].Perms) {
			return false
		}
	}
	for i := range d.Grants {
		if !d.Grants[i].Who.Same(o.Grants[i].Who) || !d.Grants[i].Local.Equal(o.Grants[i].Local) || !d.Grants[i].Descend.Equal(o.Grants[i].Descend) {
			return false
		}
	}
	return true
}

// Scope is where a grant applies, as selected by zfs allow -l / -d / neither.
type Scope int

const (
	ScopeBoth    Scope = iota // this dataset and its descendants (no flag)
	ScopeLocal                // this dataset only (-l)
	ScopeDescend              // descendants only (-d)
)

func (s Scope) String() string {
	switch s {
	case ScopeLocal:
		return "Local"
	case ScopeDescend:
		return "Descendent"
	default:
		return "Local+Descendent"
	}
}

// Describe is the long form used in the editor.
func (s Scope) Describe() string {
	switch s {
	case ScopeLocal:
		return "This dataset only"
	case ScopeDescend:
		return "Descendants only"
	default:
		return "This dataset and its descendants"
	}
}

// Flags are the zfs allow/unallow options selecting the scope.
func (s Scope) Flags() []string {
	switch s {
	case ScopeLocal:
		return []string{"-l"}
	case ScopeDescend:
		return []string{"-d"}
	default:
		return nil
	}
}

// ParseScope parses local|l|this, descend|descendent|descendants|d, both|all|"".
func ParseScope(s string) (Scope, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "both", "all", "local+descendent", "ld", "dl":
		return ScopeBoth, nil
	case "local", "l", "this":
		return ScopeLocal, nil
	case "descend", "descendent", "descendant", "descendents", "descendants", "d":
		return ScopeDescend, nil
	}
	return 0, fmt.Errorf("invalid scope %q (local, descendants or both)", s)
}

// Row is one line of the "zfs allow" display: a who, a scope and the
// permissions — the unit the UI edits. Grants are split into rows exactly
// like zfs allow prints them (Local, Descendent, Local+Descendent).
type Row struct {
	Who   Who
	Scope Scope
	Perms PermSet
}

// Rows splits the grants into display rows, in zfs allow order: Local,
// Descendent, Local+Descendent sections, each in who order.
func (d *Delegations) Rows() []Row {
	if d == nil {
		return nil
	}
	var rows []Row
	for _, sc := range []Scope{ScopeLocal, ScopeDescend, ScopeBoth} {
		for _, g := range d.Grants {
			var ps PermSet
			switch sc {
			case ScopeLocal:
				ps = g.LocalOnly()
			case ScopeDescend:
				ps = g.DescendOnly()
			default:
				ps = g.Both()
			}
			if !ps.Empty() {
				rows = append(rows, Row{g.Who, sc, ps})
			}
		}
	}
	return rows
}

// FromRows rebuilds the grants from display rows (sets and create-time
// permissions are kept). Overlapping rows are merged.
func (d *Delegations) FromRows(rows []Row) {
	d.Grants = nil
	for _, r := range rows {
		g := Grant{Who: r.Who}
		if old := d.Grant(r.Who); old != nil {
			g = *old
		}
		switch r.Scope {
		case ScopeLocal:
			g.Local = g.Local.Union(r.Perms)
		case ScopeDescend:
			g.Descend = g.Descend.Union(r.Perms)
		default:
			g.Local = g.Local.Union(r.Perms)
			g.Descend = g.Descend.Union(r.Perms)
		}
		d.SetGrant(g)
	}
}

// Expand resolves @set references in ps using the sets of d and of the
// ancestors (nearest first), recursively. Unknown sets are kept as-is so
// the caller can report them.
func Expand(ps PermSet, chain []*Delegations) PermSet {
	var out PermSet
	var walk func(PermSet, int)
	seen := map[string]bool{}
	walk = func(s PermSet, depth int) {
		for _, p := range s {
			if !IsSet(p) {
				out = append(out, p)
				continue
			}
			if seen[p] || depth > 16 {
				continue
			}
			seen[p] = true
			found := false
			for _, d := range chain {
				if d == nil {
					continue
				}
				if st := d.Set(p); st != nil {
					walk(st.Perms, depth+1)
					found = true
					break
				}
			}
			if !found {
				out = append(out, p)
			}
		}
	}
	walk(ps, 0)
	return out.normalize()
}
