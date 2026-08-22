package delegation

import (
	"fmt"
	"strings"
)

// Step is one zfs allow / zfs unallow invocation.
type Step struct {
	Args []string // argv after "zfs"
	Desc string   // human description ("grant user bob create,mount on this dataset and its descendants")
}

// Unallow reports whether the step removes permissions.
func (s Step) Unallow() bool { return len(s.Args) > 0 && s.Args[0] == "unallow" }

// String renders the step as a shell command line.
func (s Step) String() string { return CommandLine(s.Args) }

// CommandLine renders "zfs ARGS…" with shell quoting.
func CommandLine(args []string) string {
	parts := []string{"zfs"}
	for _, a := range args {
		parts = append(parts, ShellQuote(a))
	}
	return strings.Join(parts, " ")
}

// Plan is the ordered list of steps that turn one delegation state into another.
type Plan struct {
	Dataset string
	Steps   []Step
}

// Empty reports whether there is nothing to do.
func (p *Plan) Empty() bool { return p == nil || len(p.Steps) == 0 }

// Commands returns the steps as shell command lines.
func (p *Plan) Commands() []string {
	var res []string
	for _, s := range p.Steps {
		res = append(res, s.String())
	}
	return res
}

// Diff computes the zfs commands that turn cur into want. Sets are handled
// first (so grants may reference new sets), then create-time permissions,
// then the grants. Each who gets at most one allow and one unallow per
// scope; a who that loses everything gets a single "zfs unallow WHO".
func Diff(cur, want *Delegations) *Plan {
	ds := want.Dataset
	if ds == "" && cur != nil {
		ds = cur.Dataset
	}
	if cur == nil {
		cur = &Delegations{Dataset: ds}
	}
	p := &Plan{Dataset: ds}
	// sets
	seen := map[string]bool{}
	for _, s := range want.Sets {
		seen[s.Name] = true
		var old PermSet
		if o := cur.Set(s.Name); o != nil {
			old = o.Perms
		}
		if add := s.Perms.Minus(old); !add.Empty() {
			p.add(fmt.Sprintf("define %s: add %s", s.Name, add), "allow", "-s", s.Name, add.String(), ds)
		}
		if rm := old.Minus(s.Perms); !rm.Empty() {
			p.add(fmt.Sprintf("define %s: remove %s", s.Name, rm), "unallow", "-s", s.Name, rm.String(), ds)
		}
	}
	for _, s := range cur.Sets {
		if !seen[s.Name] {
			p.add(fmt.Sprintf("remove the permission set %s", s.Name), "unallow", "-s", s.Name, ds)
		}
	}
	// create-time
	if add := want.Create.Minus(cur.Create); !add.Empty() {
		p.add(fmt.Sprintf("create-time: add %s", add), "allow", "-c", add.String(), ds)
	}
	if rm := cur.Create.Minus(want.Create); !rm.Empty() {
		if want.Create.Empty() {
			p.add("create-time: remove all", "unallow", "-c", ds)
		} else {
			p.add(fmt.Sprintf("create-time: remove %s", rm), "unallow", "-c", rm.String(), ds)
		}
	}
	// grants
	var whos []Who
	for _, g := range cur.Grants {
		whos = append(whos, g.Who)
	}
	for _, g := range want.Grants {
		if cur.Grant(g.Who) == nil {
			whos = append(whos, g.Who)
		}
	}
	for _, w := range whos {
		var c, n Grant
		if g := cur.Grant(w); g != nil {
			c = *g
		}
		if g := want.Grant(w); g != nil {
			n = *g
		}
		if n.Empty() && !c.Empty() {
			p.add(fmt.Sprintf("revoke everything from %s", w), append([]string{"unallow"}, whoArgs(w, ds)...)...)
			continue
		}
		addL, addD := n.Local.Minus(c.Local), n.Descend.Minus(c.Descend)
		rmL, rmD := c.Local.Minus(n.Local), c.Descend.Minus(n.Descend)
		p.grantSteps("unallow", "revoke", w, ds, rmL, rmD)
		p.grantSteps("allow", "grant", w, ds, addL, addD)
	}
	return p
}

// grantSteps emits one step per scope bucket: the permissions in both
// buckets without -l/-d, the remainders with -l / -d.
func (p *Plan) grantSteps(verb, word string, w Who, ds string, local, desc PermSet) {
	both := local.Intersect(desc)
	type bucket struct {
		scope Scope
		perms PermSet
	}
	for _, b := range []bucket{{ScopeBoth, both}, {ScopeLocal, local.Minus(both)}, {ScopeDescend, desc.Minus(both)}} {
		if b.perms.Empty() {
			continue
		}
		args := append([]string{verb}, b.scope.Flags()...)
		args = append(args, whoArgs(w, b.perms.String(), ds)...)
		p.add(fmt.Sprintf("%s %s %s (%s)", word, w, b.perms, strings.ToLower(b.scope.Describe())), args...)
	}
}

// whoArgs renders "-u NAME", "-g NAME" or "-e" followed by rest.
func whoArgs(w Who, rest ...string) []string {
	if w.Kind == WhoEveryone {
		return append([]string{"-e"}, rest...)
	}
	return append([]string{w.Kind.Flag(), w.Arg()}, rest...)
}

func (p *Plan) add(desc string, args ...string) {
	p.Steps = append(p.Steps, Step{Args: args, Desc: desc})
}

// RecursiveUnallow is "zfs unallow -r": it removes the permissions (all of
// them when perms is empty) of w on the dataset and every descendant.
func RecursiveUnallow(w Who, scope Scope, perms PermSet, ds string) Step {
	args := append([]string{"unallow", "-r"}, scope.Flags()...)
	rest := []string{ds}
	if !perms.Empty() {
		rest = []string{perms.String(), ds}
	}
	args = append(args, whoArgs(w, rest...)...)
	what := "everything"
	if !perms.Empty() {
		what = perms.String()
	}
	return Step{Args: args, Desc: fmt.Sprintf("revoke %s from %s on %s and every descendant", what, w, ds)}
}

// AddRecursive turns the plan's "revoke everything from w" step into a
// recursive one (zfs unallow -r), or prepends one when w has nothing on the
// dataset itself.
func (p *Plan) AddRecursive(w Who) {
	bare := CommandLine(append([]string{"unallow"}, whoArgs(w, p.Dataset)...))
	for i, s := range p.Steps {
		if s.String() == bare {
			p.Steps[i] = RecursiveUnallow(w, ScopeBoth, nil, p.Dataset)
			return
		}
	}
	p.Steps = append([]Step{RecursiveUnallow(w, ScopeBoth, nil, p.Dataset)}, p.Steps...)
}

// Execute runs the steps in order, reporting progress; it does not stop at
// the first failure (later steps are independent) and returns all errors.
func (p *Plan) Execute(progress func(done, total int)) []error {
	var errs []error
	for i, s := range p.Steps {
		if _, err := Run(s.Args...); err != nil {
			errs = append(errs, fmt.Errorf("%s: %v", s, err))
		}
		if progress != nil {
			progress(i+1, len(p.Steps))
		}
	}
	return errs
}

// Apply applies want on top of cur in place (used by the CLI's -add/-remove
// to build the desired state); it is Diff's inverse for the row model.
type Edit struct {
	Who    Who
	Scope  Scope
	Perms  PermSet
	Remove bool // remove instead of add; empty Perms = everything in that scope
}

// ApplyEdit changes d according to e.
func (d *Delegations) ApplyEdit(e Edit) {
	g := Grant{Who: e.Who}
	if old := d.Grant(e.Who); old != nil {
		g = *old
	}
	switch {
	case e.Remove && e.Perms.Empty():
		if e.Scope != ScopeDescend {
			g.Local = nil
		}
		if e.Scope != ScopeLocal {
			g.Descend = nil
		}
	case e.Remove:
		if e.Scope != ScopeDescend {
			g.Local = g.Local.Minus(e.Perms)
		}
		if e.Scope != ScopeLocal {
			g.Descend = g.Descend.Minus(e.Perms)
		}
	default:
		if e.Scope != ScopeDescend {
			g.Local = g.Local.Union(e.Perms)
		}
		if e.Scope != ScopeLocal {
			g.Descend = g.Descend.Union(e.Perms)
		}
	}
	d.SetGrant(g)
}
