package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/olgeni/zfs-allow/delegation"
)

type cliOptions struct {
	list      bool
	add       string
	remove    string
	perms     string
	scope     string
	effective string
	where     string
	catalogue bool
	dryRun    bool
	yes       bool
	check     bool
	json      bool
	lenient   bool
}

func (o cliOptions) nonInteractive() bool {
	return o.list || o.add != "" || o.remove != "" || o.effective != "" || o.where != "" || o.catalogue || o.dryRun || o.check
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "zfs-allow: "+format+"\n", a...)
	return 1
}

// runCLI executes the non-interactive modes and returns the exit status.
func runCLI(dataset string, o cliOptions) int {
	switch {
	case o.catalogue:
		return runCatalogue(o)
	case o.where != "":
		return runWhere(o)
	}
	l, err := delegation.Load(dataset)
	if err != nil {
		return fail("%v", err)
	}
	switch {
	case o.list:
		return runList(l, o)
	case o.effective != "":
		return runEffective(l, o)
	case o.add != "" || o.remove != "":
		return runEdit(l, o)
	}
	return fail("nothing to do (-list, -add, -remove, -effective, -where or -catalogue)")
}

// parsePermsArg expands +PRESET tokens and validates the names.
func parsePermsArg(s string, lenient bool) (delegation.PermSet, error) {
	var ps delegation.PermSet
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if strings.HasPrefix(tok, "+") {
			p, ok := delegation.PresetByName(tok[1:])
			if !ok {
				return nil, fmt.Errorf("unknown preset %q (zfs-allow -catalogue lists them)", tok[1:])
			}
			ps = ps.Union(p.Perms)
			continue
		}
		if !delegation.ValidPermName(tok, lenient) {
			if c, ok := delegation.Lookup(tok); ok && c.NotFreeBSD {
				return nil, fmt.Errorf("%s is not applicable on FreeBSD", tok)
			}
			return nil, fmt.Errorf("unknown permission %q (zfs-allow -catalogue lists them; -lenient passes it through)", tok)
		}
		ps = ps.With(tok)
	}
	return ps, nil
}

func runEdit(l *delegation.Listing, o cliOptions) int {
	spec, remove := o.add, false
	if o.remove != "" {
		if o.add != "" {
			return fail("-add and -remove are exclusive")
		}
		spec, remove = o.remove, true
	}
	var perms delegation.PermSet
	if o.perms != "" {
		var err error
		if perms, err = parsePermsArg(o.perms, o.lenient); err != nil {
			return fail("%v", err)
		}
	}
	if !remove && perms.Empty() {
		return fail("-add needs -perms")
	}
	scope, err := delegation.ParseScope(o.scope)
	if err != nil {
		return fail("%v", err)
	}
	want := l.Own.Clone()
	switch {
	case spec == "create-time" || spec == "-c":
		switch {
		case remove && perms.Empty():
			want.Create = nil
		case remove:
			want.Create = want.Create.Minus(perms)
		default:
			want.Create = want.Create.Union(perms)
		}
	case delegation.IsSet(spec):
		if !delegation.ValidSetName(spec) {
			return fail("invalid permission set name %q", spec)
		}
		var cur delegation.PermSet
		if s := want.Set(spec); s != nil {
			cur = s.Perms
		}
		switch {
		case remove && perms.Empty():
			cur = nil
		case remove:
			cur = cur.Minus(perms)
		default:
			cur = cur.Union(perms)
		}
		want.SetSet(delegation.Set{Name: spec, Perms: cur})
	default:
		who, err := delegation.ParseWho(spec)
		if err != nil {
			return fail("%v", err)
		}
		want.ApplyEdit(delegation.Edit{Who: who, Scope: scope, Perms: perms, Remove: remove})
	}
	plan := delegation.Diff(l.Own, want)
	env := cliEnv(l, o)
	wl := &delegation.Listing{Dataset: l.Dataset, Own: want, Ancestors: l.Ancestors}
	probs := delegation.Preflight(wl, plan, env)
	if o.check {
		if plan.Empty() {
			return 0
		}
		return 3
	}
	if o.json && o.dryRun {
		return printJSON(jsonPlan(plan, probs))
	}
	if plan.Empty() {
		fmt.Println("Nothing to do.")
		return 0
	}
	for _, s := range plan.Steps {
		fmt.Println(s)
	}
	for _, p := range probs {
		tag := "note"
		if p.Fatal {
			tag = "warning"
		}
		fmt.Fprintf(os.Stderr, "%s: %s\n", tag, p.Msg)
	}
	if o.dryRun {
		return 0
	}
	if !o.yes {
		fmt.Fprintf(os.Stderr, "Run %d command(s) on %s? [y/N] ", len(plan.Steps), l.Dataset.Name)
		ans, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Fprintln(os.Stderr, "Declined.")
			return 2
		}
	}
	errs := plan.Execute(nil)
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "zfs-allow:", e)
	}
	if len(errs) > 0 {
		return 1
	}
	fmt.Fprintf(os.Stderr, "Applied %d command(s).\n", len(plan.Steps))
	return 0
}

func cliEnv(l *delegation.Listing, o cliOptions) delegation.Env {
	env := delegation.Env{Lenient: o.lenient}
	if id, err := delegation.CurrentIdentity(); err == nil {
		env.Operator = id
		env.Effective = delegation.EffectiveFor(l, id)
	}
	env.Usermount, _ = delegation.Usermount()
	return env
}

func runList(l *delegation.Listing, o cliOptions) int {
	if o.json {
		return printJSON(jsonListing(l))
	}
	printListing(l)
	return 0
}

func printListing(l *delegation.Listing) {
	for i, d := range l.Chain() {
		if d.Empty() && i > 0 {
			continue
		}
		fmt.Println(d.Header())
		if d.Empty() {
			fmt.Println("\t(nothing delegated)")
			continue
		}
		fmt.Print(d.Format())
	}
}

func runEffective(l *delegation.Listing, o cliOptions) int {
	id, err := delegation.IdentityOf(o.effective)
	if err != nil {
		return fail("%v", err)
	}
	eff := delegation.EffectiveFor(l, id)
	if o.json {
		return printJSON(jsonEffective(l, id, eff))
	}
	if id.UID == 0 {
		fmt.Printf("%s is root: delegations do not restrict root.\n", id.Name)
		return 0
	}
	fmt.Printf("%s (uid %d, groups %s) on %s: %d permission(s)\n", id.Name, id.UID, strings.Join(id.Groups, ","), l.Dataset.Name, len(eff))
	for _, p := range eff.Perms() {
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
		fmt.Printf("%-24s %s\n", p, strings.Join(srcs, "; "))
	}
	return 0
}

// whereHit is one dataset delegating something to the who.
type whereHit struct {
	Dataset string
	Grant   delegation.Grant
}

func runWhere(o cliOptions) int {
	who, err := delegation.ParseWho(o.where)
	if err != nil {
		return fail("%v", err)
	}
	list, err := delegation.Datasets()
	if err != nil {
		return fail("%v", err)
	}
	hits := make([]*whereHit, len(list))
	var errs []string
	var mu sync.Mutex
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, d := range list {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, err := delegation.Run("allow", name)
			if err != nil {
				mu.Lock()
				errs = append(errs, name+": "+err.Error())
				mu.Unlock()
				return
			}
			secs, err := delegation.Parse(out)
			if err != nil {
				mu.Lock()
				errs = append(errs, name+": "+err.Error())
				mu.Unlock()
				return
			}
			own, _ := delegation.Split(secs, name)
			if own != nil {
				if g := own.Grant(who); g != nil {
					hits[i] = &whereHit{name, *g}
				}
			}
		}(i, d.Name)
	}
	wg.Wait()
	var found []*whereHit
	for _, h := range hits {
		if h != nil {
			found = append(found, h)
		}
	}
	sort.Strings(errs)
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "zfs-allow: warning:", e)
	}
	if o.json {
		return printJSON(jsonWhere(who, found))
	}
	for _, h := range found {
		g := h.Grant
		var parts []string
		if b := g.Both(); !b.Empty() {
			parts = append(parts, "local+descendent: "+b.String())
		}
		if l := g.LocalOnly(); !l.Empty() {
			parts = append(parts, "local: "+l.String())
		}
		if d := g.DescendOnly(); !d.Empty() {
			parts = append(parts, "descendent: "+d.String())
		}
		fmt.Printf("%s\t%s\n", h.Dataset, strings.Join(parts, "; "))
	}
	return 0
}

func runCatalogue(o cliOptions) int {
	if o.json {
		return printJSON(jsonCatalogue())
	}
	for _, g := range delegation.GroupOrder {
		fmt.Println(g)
		for _, p := range delegation.ByGroup(g) {
			note := p.Note
			if p.NotFreeBSD {
				note = "(refused on FreeBSD) " + note
			}
			fmt.Printf("  %-24s %-11s %s\n", p.Name, p.Kind, note)
		}
	}
	fmt.Println("Presets (+NAME in -perms)")
	for _, p := range delegation.Presets {
		fmt.Printf("  %-24s %s\n    %s\n", "+"+p.Name, p.Desc, p.Perms)
	}
	return 0
}
