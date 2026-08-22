package delegation

import (
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	defer fixedResolver()()
	cur := &Delegations{Dataset: "tank/x",
		Sets:   []Set{{"@old", ParsePerms("hold")}, {"@keep", ParsePerms("mount,snapshot")}},
		Create: ParsePerms("mount,snapshot")}
	cur.SetGrant(Grant{User("bob"), ParsePerms("create,mount,snapshot"), ParsePerms("create,mount,snapshot")})
	cur.SetGrant(Grant{Group("wheel"), nil, ParsePerms("release")})
	cur.SetGrant(Grant{Everyone, ParsePerms("mount"), nil})
	cur.SetGrant(Grant{User("gone"), ParsePerms("send"), ParsePerms("send")})

	want := cur.Clone()
	want.SetSet(Set{"@old", nil})                                                                                // remove set
	want.SetSet(Set{"@keep", ParsePerms("mount,snapshot,hold")})                                                 // add to set
	want.SetSet(Set{"@new", ParsePerms("send,send:raw")})                                                        // new set
	want.Create = ParsePerms("mount")                                                                            // remove one create-time perm
	want.SetGrant(Grant{User("bob"), ParsePerms("create,mount,snapshot,destroy"), ParsePerms("mount,snapshot")}) // +destroy local, -create desc
	want.SetGrant(Grant{Group("wheel"), ParsePerms("release"), ParsePerms("release,hold")})                      // release becomes both, +hold desc
	want.SetGrant(Grant{Everyone, nil, nil})                                                                     // revoke all
	want.SetGrant(Grant{User("gone"), nil, nil})
	want.SetGrant(Grant{User("new"), ParsePerms("@new"), ParsePerms("@new")})

	p := Diff(cur, want)
	got := strings.Join(p.Commands(), "\n")
	wantCmds := strings.Join([]string{
		"zfs allow -s @keep hold tank/x",
		"zfs allow -s @new send,send:raw tank/x",
		"zfs unallow -s @old tank/x",
		"zfs unallow -c snapshot tank/x",
		"zfs unallow -d -u bob create tank/x",
		"zfs allow -l -u bob destroy tank/x",
		"zfs unallow -u gone tank/x",
		"zfs allow -l -g wheel release tank/x",
		"zfs allow -d -g wheel hold tank/x",
		"zfs unallow -e tank/x",
		"zfs allow -u new @new tank/x",
	}, "\n")
	if got != wantCmds {
		t.Errorf("plan:\n%s\nwant:\n%s", got, wantCmds)
	}
	if !Diff(cur, cur.Clone()).Empty() {
		t.Error("identical states produce steps")
	}
	// removing all create-time perms is one bare unallow -c
	w2 := cur.Clone()
	w2.Create = nil
	if c := Diff(cur, w2).Commands(); len(c) != 1 || c[0] != "zfs unallow -c tank/x" {
		t.Error(c)
	}
	// unknown id who uses the numeric id
	w3 := cur.Clone()
	w3.SetGrant(Grant{Who{Kind: WhoUser, ID: 12345}, ParsePerms("send"), nil})
	if c := Diff(cur, w3).Commands(); len(c) != 1 || c[0] != "zfs allow -l -u 12345 send tank/x" {
		t.Error(c)
	}
}

func TestApplyEdit(t *testing.T) {
	d := &Delegations{Dataset: "t"}
	d.ApplyEdit(Edit{Who: User("bob"), Scope: ScopeBoth, Perms: ParsePerms("create,mount")})
	d.ApplyEdit(Edit{Who: User("bob"), Scope: ScopeLocal, Perms: ParsePerms("destroy")})
	g := d.Grant(User("bob"))
	if g.Local.String() != "create,destroy,mount" || g.Descend.String() != "create,mount" {
		t.Fatal(g)
	}
	d.ApplyEdit(Edit{Who: User("bob"), Scope: ScopeBoth, Perms: ParsePerms("mount"), Remove: true})
	g = d.Grant(User("bob"))
	if g.Local.String() != "create,destroy" || g.Descend.String() != "create" {
		t.Fatal(g)
	}
	d.ApplyEdit(Edit{Who: User("bob"), Scope: ScopeDescend, Remove: true})
	if g = d.Grant(User("bob")); !g.Descend.Empty() || g.Local.String() != "create,destroy" {
		t.Fatal(g)
	}
	d.ApplyEdit(Edit{Who: User("bob"), Scope: ScopeBoth, Remove: true})
	if d.Grant(User("bob")) != nil {
		t.Fatal("grant not dropped")
	}
}

func TestEffectiveAndPreflight(t *testing.T) {
	parent := &Delegations{Dataset: "tank", Sets: []Set{{"@ops", ParsePerms("mount,snapshot")}}}
	parent.SetGrant(Grant{Group("staff"), ParsePerms("hold"), ParsePerms("@ops,hold")})
	parent.SetGrant(Grant{Everyone, nil, ParsePerms("send")})
	own := &Delegations{Dataset: "tank/home"}
	own.SetGrant(Grant{User("bob"), ParsePerms("create,destroy"), ParsePerms("create")})
	own.SetGrant(Grant{User("amy"), ParsePerms("rollback"), nil})
	l := &Listing{Dataset: Dataset{Name: "tank/home", Type: "filesystem", Mountpoint: "none"}, Own: own, Ancestors: []*Delegations{parent}}

	bob := Identity{Name: "bob", UID: 1001, Groups: []string{"bob", "staff"}, GIDs: []int{1001, 20}}
	eff := EffectiveFor(l, bob)
	if eff.Perms().String() != "create,destroy,hold,mount,send,snapshot" {
		t.Fatalf("effective: %v", eff.Perms())
	}
	if src := eff["mount"]; len(src) != 1 || src[0].Via != "@ops" || src[0].Dataset != "tank" || src[0].Who.Name != "staff" {
		t.Errorf("mount source: %+v", src)
	}
	if len(eff["hold"]) != 1 || eff["hold"][0].Scope != ScopeDescend {
		t.Errorf("hold: %+v", eff["hold"])
	}
	// amy: only her local grant, plus everyone's send from the parent
	amy := Identity{Name: "amy", UID: 1002, Groups: []string{"amy"}, GIDs: []int{1002}}
	if p := EffectiveFor(l, amy).Perms().String(); p != "rollback,send" {
		t.Errorf("amy: %s", p)
	}

	probs := Preflight(l, nil, Env{Usermount: true})
	text := strings.Join(problemStrings(probs), "\n")
	// bob's create on this dataset lacks mount (no group expansion for a bare who)
	if !strings.Contains(text, "user bob on this dataset: create needs mount, destroy needs mount") {
		t.Errorf("missing dependency warning:\n%s", text)
	}
	if !strings.Contains(text, "user bob on descendants: create needs mount") {
		t.Errorf("missing descendant warning:\n%s", text)
	}
	if !strings.Contains(text, "user amy on this dataset: rollback needs mount") {
		t.Errorf("amy:\n%s", text)
	}
	// usermount off adds the sysctl hint
	probs = Preflight(l, nil, Env{Usermount: false})
	if !strings.Contains(strings.Join(problemStrings(probs), "\n"), "vfs.usermount") {
		t.Error("no usermount warning")
	}
	// unknown / FreeBSD-refused / undefined set
	bad := &Delegations{Dataset: "tank/home"}
	bad.SetGrant(Grant{User("bob"), ParsePerms("zoned,bogus,@nope"), nil})
	lb := &Listing{Dataset: l.Dataset, Own: bad}
	text = strings.Join(problemStrings(Preflight(lb, nil, Env{Usermount: true})), "\n")
	for _, want := range []string{"zoned is not applicable on FreeBSD", `unknown permission "bogus"`, "@nope is not defined"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	// unprivileged operator
	plan := Diff(&Delegations{Dataset: "tank/home"}, own)
	env := Env{Operator: bob, Effective: EffectiveFor(l, bob), Usermount: true}
	text = strings.Join(problemStrings(Preflight(l, plan, env)), "\n")
	if !strings.Contains(text, "do not hold the allow permission") {
		t.Errorf("no allow warning:\n%s", text)
	}
	env.Effective["allow"] = []Source{{Dataset: "tank"}}
	text = strings.Join(problemStrings(Preflight(l, plan, env)), "\n")
	if !strings.Contains(text, "grant user amy rollback (this dataset only): you do not hold rollback") {
		t.Errorf("no missing-perm warning:\n%s", text)
	}
	// unallow of someone else needs root; own is fine
	plan = Diff(own, &Delegations{Dataset: "tank/home"})
	text = strings.Join(problemStrings(Preflight(l, plan, env)), "\n")
	if !strings.Contains(text, "revoke everything from user amy: an unprivileged user can only unallow their own") {
		t.Errorf("no unallow warning:\n%s", text)
	}
	if strings.Contains(text, "revoke everything from user bob:") {
		t.Errorf("own unallow flagged:\n%s", text)
	}
}

func problemStrings(ps []Problem) []string {
	var s []string
	for _, p := range ps {
		s = append(s, p.Msg)
	}
	return s
}

func TestRecursive(t *testing.T) {
	defer fixedResolver()()
	cur := &Delegations{Dataset: "tank/x"}
	cur.SetGrant(Grant{User("bob"), ParsePerms("mount"), ParsePerms("mount")})
	cur.SetGrant(Grant{Group("staff"), ParsePerms("hold"), nil})
	want := cur.Clone()
	want.SetGrant(Grant{User("bob"), nil, nil})
	p := Diff(cur, want)
	p.AddRecursive(User("bob"))
	p.AddRecursive(Everyone) // nothing on the dataset itself: prepended
	got := strings.Join(p.Commands(), "\n")
	if got != "zfs unallow -r -e tank/x\nzfs unallow -r -u bob tank/x" {
		t.Fatalf("got:\n%s", got)
	}
	s := RecursiveUnallow(Group("staff"), ScopeDescend, ParsePerms("hold,send"), "tank/x")
	if s.String() != "zfs unallow -r -d -g staff hold,send tank/x" {
		t.Fatal(s)
	}
}
