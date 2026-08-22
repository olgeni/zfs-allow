package delegation

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
)

// Problem is a preflight finding about a desired delegation state.
type Problem struct {
	Fatal bool   // zfs will refuse the step
	Msg   string // one line
}

func (p Problem) String() string { return p.Msg }

// Env is what the preflight knows about the machine and the operator.
type Env struct {
	Operator  Identity  // who runs the zfs commands
	Effective Effective // the operator's effective permissions on the dataset (for non-root)
	Usermount bool      // vfs.usermount
	Lenient   bool      // accept unknown permission names (newer OpenZFS)
}

// Preflight checks the desired state and the plan that reaches it:
//
//   - permission names that zfs allow will refuse, or that are not in the
//     catalogue (fatal / warning)
//   - @sets that are not defined on the dataset or an ancestor (warning)
//   - zfs-allow(8) dependencies such as "create needs mount", evaluated
//     against what the who will hold on the dataset / its descendants
//   - vfs.usermount=0, which makes every mount-dependent delegation useless
//   - mount points the grantee cannot create sub-mount-points in
//   - what an unprivileged operator cannot do: unallow anyone but
//     themselves, define sets, delegate what they do not hold
func Preflight(want *Listing, plan *Plan, env Env) []Problem {
	var probs []Problem
	warn := func(f string, a ...any) { probs = append(probs, Problem{Msg: fmt.Sprintf(f, a...)}) }
	fatal := func(f string, a ...any) { probs = append(probs, Problem{Fatal: true, Msg: fmt.Sprintf(f, a...)}) }
	d := want.Own
	chain := want.Chain()

	// names
	seenName := map[string]bool{}
	checkNames := func(ctx string, ps PermSet) {
		for _, p := range ps {
			if IsSet(p) {
				if !ValidSetName(p) {
					fatal("%s: %q is not a valid permission set name", ctx, p)
				} else if found := func() bool {
					for _, c := range chain {
						if c != nil && c.Set(p) != nil {
							return true
						}
					}
					return false
				}(); !found && !seenName["set:"+p] {
					seenName["set:"+p] = true
					warn("%s is not defined on %s or an ancestor: it grants nothing until it is", p, d.Dataset)
				}
				continue
			}
			if seenName[p] {
				continue
			}
			seenName[p] = true
			if c, ok := Lookup(p); ok {
				if c.NotFreeBSD {
					fatal("%s: %s is not applicable on FreeBSD (zfs allow refuses it)", ctx, p)
				}
			} else if !env.Lenient {
				fatal("%s: unknown permission %q (not in zfs-allow(8))", ctx, p)
			} else {
				warn("%s: %q is not a known permission; zfs allow accepts it only if this OpenZFS version knows it", ctx, p)
			}
		}
	}
	for _, s := range d.Sets {
		checkNames(s.Name, s.Perms)
	}
	checkNames("create-time", d.Create)
	for _, g := range d.Grants {
		checkNames(g.Who.String(), g.Local.Union(g.Descend))
	}

	// dependencies
	mountNeeded := false
	for _, g := range d.Grants {
		local := WhoEffective(want, g.Who) // what the who gets on this dataset
		var descAnc PermSet                // what descendants inherit from the ancestors
		for _, a := range want.Ancestors {
			if x := a.Grant(g.Who); x != nil {
				descAnc = descAnc.Union(x.Descend)
			}
		}
		desc := Expand(g.Descend.Union(descAnc), chain)
		for _, where := range []struct {
			label string
			have  PermSet
			set   PermSet
		}{{"this dataset", local, Expand(g.Local, chain)}, {"descendants", desc, Expand(g.Descend, chain)}} {
			var missing []string
			for _, p := range where.set {
				c, ok := Lookup(p)
				if !ok {
					continue
				}
				for _, n := range c.Needs {
					if !where.have.Has(n) {
						missing = append(missing, fmt.Sprintf("%s needs %s", p, n))
					}
				}
				if p == "mount" || PermSet(c.Needs).normalize().Has("mount") {
					mountNeeded = true
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				missing = dedupe(missing)
				warn("%s on %s: %s", g.Who, where.label, strings.Join(missing, ", "))
			}
		}
	}
	for _, p := range d.Create {
		if c, ok := Lookup(p); ok {
			for _, n := range c.Needs {
				if !d.Create.Has(n) {
					warn("create-time: %s needs %s (the creator gets only these permissions locally)", p, n)
				}
			}
		}
	}
	if mountNeeded && !env.Usermount {
		warn("vfs.usermount is 0: unprivileged users cannot mount, so mount and everything that needs it (create, destroy, snapshot, rollback, rename, clone, receive) will fail — sysctl vfs.usermount=1")
	}
	if mp := want.Dataset.Mountpoint; want.Dataset.Mounted && mp != "" && mp[0] == '/' {
		for _, g := range d.Grants {
			if g.Who.Kind != WhoUser || g.Who.Name == "" {
				continue
			}
			if !Expand(g.Local, chain).Has("create") {
				continue
			}
			if msg := mountpointProblem(mp, g.Who); msg != "" {
				warn("%s", msg)
			}
		}
	}

	// operator privileges
	if plan != nil && env.Operator.UID != 0 && env.Operator.Name != "" {
		if !env.Effective.Has("allow") {
			for _, s := range plan.Steps {
				if !s.Unallow() {
					fatal("you do not hold the allow permission on %s: zfs allow will be refused (unprivileged delegation needs root or allow)", d.Dataset)
					break
				}
			}
		}
		for _, s := range plan.Steps {
			switch {
			case s.Unallow() && !(len(s.Args) > 2 && s.Args[1] == "-u" && s.Args[2] == env.Operator.Name):
				fatal("%s: an unprivileged user can only unallow their own permissions (root needed)", s.Desc)
			case !s.Unallow() && len(s.Args) > 1 && s.Args[1] == "-s":
				fatal("%s: permission sets can only be defined by root", s.Desc)
			case !s.Unallow():
				// the operator must hold every permission being granted
				perms := Expand(ParsePerms(s.Args[len(s.Args)-2]), chain)
				var missing PermSet
				for _, p := range perms {
					if !IsSet(p) && !env.Effective.Has(p) {
						missing = append(missing, p)
					}
				}
				if len(missing) > 0 && env.Effective.Has("allow") {
					fatal("%s: you do not hold %s on %s yourself, so zfs allow will refuse the whole step", s.Desc, missing, d.Dataset)
				}
			}
		}
	}
	// fatal findings first
	sort.SliceStable(probs, func(i, j int) bool { return probs[i].Fatal && !probs[j].Fatal })
	return probs
}

func dedupe(s []string) []string {
	j := 0
	for i := range s {
		if i == 0 || s[i] != s[i-1] {
			s[j] = s[i]
			j++
		}
	}
	return s[:j]
}

// mountpointProblem reports when user w cannot create directories in mp
// by mode bits alone (an NFSv4 ACL may still allow it — the message says so).
func mountpointProblem(mp string, w Who) string {
	st, err := os.Stat(mp)
	if err != nil {
		return ""
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	id, err := IdentityOf(w.Name)
	if err != nil {
		return ""
	}
	if int(sys.Uid) == id.UID {
		return ""
	}
	mode := st.Mode().Perm()
	if mode&0o002 != 0 {
		return ""
	}
	for _, g := range id.GIDs {
		if g == int(sys.Gid) && mode&0o020 != 0 {
			return ""
		}
	}
	return fmt.Sprintf("%s (uid %d, mode %04o) is not writable by %s, so they cannot create mount points for new file systems under it — chown it, or grant add_subdirectory with an ACL: setfacl -a 0 user:%s:add_subdirectory:allow %s (or facl)",
		mp, sys.Uid, mode, w.Name, w.Name, mp)
}
