package delegation

import (
	"os"
	"strings"
	"testing"
)

func TestParseGolden(t *testing.T) {
	defer fixedResolver()()
	b, err := os.ReadFile("testdata/child.txt")
	if err != nil {
		t.Fatal(err)
	}
	secs, err := Parse(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 {
		t.Fatalf("got %d sections, want 2", len(secs))
	}
	own, anc := Split(secs, "rpool/test/child")
	if own == nil || own.Dataset != "rpool/test/child" {
		t.Fatalf("own = %+v", own)
	}
	if len(anc) != 1 || anc[0].Dataset != "rpool/test" {
		t.Fatalf("ancestors = %+v", anc)
	}
	// child: local olgeni mount,snapshot (create-time perms of the parent
	// granted to the creator), local nobody hold, both (unknown 12345) send
	if g := own.Grant(User("olgeni")); g == nil || g.Local.String() != "mount,snapshot" || !g.Descend.Empty() {
		t.Errorf("olgeni on child: %+v", g)
	}
	if g := own.Grant(User("nobody")); g == nil || g.Local.String() != "hold" || !g.Descend.Empty() {
		t.Errorf("nobody on child: %+v", g)
	}
	if g := own.Grant(Who{Kind: WhoUser, ID: 12345}); g == nil || g.Local.String() != "send" || g.Descend.String() != "send" {
		t.Errorf("12345 on child: %+v", g)
	}
	// parent
	p := anc[0]
	if len(p.Sets) != 1 || p.Sets[0].Name != "@zatest" || p.Sets[0].Perms.String() != "create,mount,snapshot" {
		t.Errorf("sets: %+v", p.Sets)
	}
	if p.Create.String() != "mount,snapshot" {
		t.Errorf("create-time: %v", p.Create)
	}
	if g := p.Grant(Everyone); g == nil || g.Local.String() != "mount,snapshot" || g.Descend.String() != "snapshot" {
		t.Errorf("everyone: %+v", g)
	}
	if g := p.Grant(Group("wheel")); g == nil || !g.Local.Empty() || g.Descend.String() != "release" {
		t.Errorf("wheel: %+v", g)
	}
	if g := p.Grant(Group("operator")); g == nil || g.Local.String() != "bookmark" || g.Descend.String() != "bookmark" {
		t.Errorf("operator: %+v", g)
	}
	if g := p.Grant(User("olgeni")); g == nil || !g.Local.Has("xattr") || !g.Local.Equal(g.Descend) || len(g.Local) != 89 {
		t.Errorf("olgeni on parent: %d perms", len(g.Local))
	}
	// rows come back in zfs allow order
	rows := p.Rows()
	want := []string{
		"everyone Local mount",
		"user nobody Descendent send",
		"group wheel Descendent release",
		"user olgeni Local+Descendent",
		"user (unknown: 12345) Local+Descendent hold,send",
		"group operator Local+Descendent bookmark",
		"everyone Local+Descendent snapshot",
	}
	if len(rows) != len(want) {
		t.Fatalf("rows: %d, want %d", len(rows), len(want))
	}
	for i, r := range rows {
		got := r.Who.String() + " " + r.Scope.String()
		if !strings.HasPrefix(want[i], got) {
			t.Errorf("row %d: %s, want %s", i, got, want[i])
		}
		if !strings.HasSuffix(want[i], r.Perms.String()) && !(r.Who.Name == "olgeni") {
			t.Errorf("row %d perms: %s, want %s", i, r.Perms, want[i])
		}
	}
	// Format reproduces the input sections (modulo the header line)
	text := string(b)
	for _, d := range secs {
		if !strings.Contains(text, d.Format()) {
			t.Errorf("Format of %s does not round-trip:\n%s", d.Dataset, d.Format())
		}
	}
	// FromRows(Rows()) is the identity
	c := p.Clone()
	c.FromRows(p.Rows())
	if !c.Equal(p) {
		t.Errorf("FromRows(Rows()) changed the state:\n%s\n---\n%s", p.Format(), c.Format())
	}
}

func TestParseEmptyAndErrors(t *testing.T) {
	secs, err := Parse("")
	if err != nil || len(secs) != 0 {
		t.Fatalf("empty: %v %v", secs, err)
	}
	if _, err := Parse("Local permissions:\n\tuser x y\n"); err == nil {
		t.Error("body before header accepted")
	}
	if _, err := Parse("---- Permissions on a ----\nWeird permissions:\n"); err == nil {
		t.Error("unknown section accepted")
	}
	if _, err := Parse("---- Permissions on a ----\nLocal permissions:\n\tbogus x y\n"); err == nil {
		t.Error("bad who accepted")
	}
}

func TestPermSet(t *testing.T) {
	s := ParsePerms("mount, create,mount,,snapshot")
	if s.String() != "create,mount,snapshot" {
		t.Fatal(s)
	}
	if !s.Has("mount") || s.Has("send") {
		t.Fatal("Has")
	}
	if s.Without("mount").String() != "create,snapshot" || s.With("allow").String() != "allow,create,mount,snapshot" {
		t.Fatal("With/Without")
	}
	if s.Intersect(ParsePerms("mount,send")).String() != "mount" || s.Minus(ParsePerms("mount")).String() != "create,snapshot" {
		t.Fatal("Intersect/Minus")
	}
	if !ParsePerms("").Empty() || !PermSet(nil).Equal(ParsePerms("")) {
		t.Fatal("empty")
	}
}

func TestWho(t *testing.T) {
	cases := map[string]Who{
		"everyone":   Everyone,
		"user:bob":   User("bob"),
		"u:bob":      User("bob"),
		"bob":        User("bob"),
		"group:whl":  Group("whl"),
		"g:whl":      Group("whl"),
		"user:12345": {Kind: WhoUser, ID: 12345},
	}
	for in, want := range cases {
		got, err := ParseWho(in)
		if err != nil || !got.Same(want) {
			t.Errorf("ParseWho(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "user:", "x:bob"} {
		if _, err := ParseWho(bad); err == nil {
			t.Errorf("ParseWho(%q) accepted", bad)
		}
	}
	if (Who{Kind: WhoUser, ID: 12345}).String() != "user (unknown: 12345)" || (Who{Kind: WhoUser, ID: 12345}).Arg() != "12345" {
		t.Error("unknown rendering")
	}
	if User("bob").Spec() != "user:bob" || Everyone.Spec() != "everyone" {
		t.Error("Spec")
	}
}

func TestExpand(t *testing.T) {
	parent := &Delegations{Dataset: "p", Sets: []Set{{"@base", ParsePerms("mount,snapshot")}}}
	child := &Delegations{Dataset: "p/c", Sets: []Set{{"@more", ParsePerms("@base,send")}, {"@loop", ParsePerms("@loop,hold")}}}
	got := Expand(ParsePerms("@more,create"), []*Delegations{child, parent})
	if got.String() != "create,mount,send,snapshot" {
		t.Fatal(got)
	}
	if Expand(ParsePerms("@loop"), []*Delegations{child}).String() != "hold" {
		t.Fatal("loop")
	}
	if Expand(ParsePerms("@nope"), []*Delegations{child}).String() != "@nope" {
		t.Fatal("unknown set dropped")
	}
}

func TestCatalogue(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Catalogue {
		if seen[p.Name] {
			t.Errorf("duplicate %s", p.Name)
		}
		seen[p.Name] = true
		for _, n := range p.Needs {
			if !Known(n) {
				t.Errorf("%s needs unknown %s", p.Name, n)
			}
		}
		found := false
		for _, g := range GroupOrder {
			if g == p.Group {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: group %q not in GroupOrder", p.Name, p.Group)
		}
	}
	// everything the kernel accepted in the full grant is in the catalogue
	b, _ := os.ReadFile("testdata/child.txt")
	secs, _ := Parse(string(b))
	_, anc := Split(secs, "rpool/test/child")
	for _, p := range anc[0].Grant(User("olgeni")).Local {
		if !Known(p) {
			t.Errorf("kernel permission %s missing from the catalogue", p)
		}
	}
	if !ValidSetName("@a-b.c") || ValidSetName("a") || ValidSetName("@") || ValidSetName("@a b") {
		t.Error("ValidSetName")
	}
	if !ValidPermName("send:raw", false) || ValidPermName("foo", false) || !ValidPermName("foo", true) || ValidPermName("Foo", true) {
		t.Error("ValidPermName")
	}
}

// fixedResolver makes who ordering independent of the machine's passwd/group.
func fixedResolver() func() {
	old := Resolver
	ids := map[string]int{"user:root": 0, "user:olgeni": 1001, "user:nobody": 65534, "user:bob": 1001, "user:amy": 1002,
		"user:gone": 1003, "user:new": 1004, "group:wheel": 0, "group:operator": 5, "group:staff": 20}
	Resolver = func(kind WhoKind, name string) (int, bool) {
		id, ok := ids[kind.String()+":"+name]
		return id, ok
	}
	return func() { Resolver = old }
}
