package delegation

import "sort"

// Source says where an effective permission comes from.
type Source struct {
	Dataset string // the dataset whose delegation grants it
	Who     Who    // the grant it comes from (a group the user is in, everyone, …)
	Via     string // the @set that carried it, if any
	Scope   Scope  // ScopeLocal when granted on the dataset itself, ScopeDescend when inherited from an ancestor
}

// Effective is the permissions an identity holds on a dataset, each with
// every grant that provides it.
type Effective map[string][]Source

// Perms lists the effective permissions, sorted.
func (e Effective) Perms() PermSet {
	var ps PermSet
	for p := range e {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}

// Has reports whether the permission is held.
func (e Effective) Has(p string) bool { return len(e[p]) > 0 }

// EffectiveFor computes what id may do on the dataset described by l: the
// Local sets of the dataset's own grants plus the Descend sets of every
// ancestor's grants, for every who that matches the identity (the user,
// any of their groups, everyone). Create-time permissions are not access
// and are ignored. Root is not special-cased here.
func EffectiveFor(l *Listing, id Identity) Effective {
	eff := Effective{}
	chain := l.Chain()
	add := func(ds string, w Who, ps PermSet, scope Scope) {
		for _, p := range ps {
			if IsSet(p) {
				for _, q := range Expand(PermSet{p}, chain) {
					if !IsSet(q) {
						eff[q] = append(eff[q], Source{ds, w, p, scope})
					}
				}
				continue
			}
			eff[p] = append(eff[p], Source{ds, w, "", scope})
		}
	}
	if l.Own != nil {
		for _, g := range l.Own.Grants {
			if id.Matches(g.Who) {
				add(l.Own.Dataset, g.Who, g.Local, ScopeLocal)
			}
		}
	}
	for _, a := range l.Ancestors {
		for _, g := range a.Grants {
			if id.Matches(g.Who) {
				add(a.Dataset, g.Who, g.Descend, ScopeDescend)
			}
		}
	}
	return eff
}

// WhoEffective computes what a bare who (not an identity: no group
// expansion) gets on l's dataset — used by the preflight to check
// dependencies like "create needs mount" for a grant to a group.
func WhoEffective(l *Listing, w Who) PermSet {
	chain := l.Chain()
	var ps PermSet
	if l.Own != nil {
		if g := l.Own.Grant(w); g != nil {
			ps = ps.Union(g.Local)
		}
		if w.Kind != WhoEveryone {
			if g := l.Own.Grant(Everyone); g != nil {
				ps = ps.Union(g.Local)
			}
		}
	}
	for _, a := range l.Ancestors {
		if g := a.Grant(w); g != nil {
			ps = ps.Union(g.Descend)
		}
		if w.Kind != WhoEveryone {
			if g := a.Grant(Everyone); g != nil {
				ps = ps.Union(g.Descend)
			}
		}
	}
	return Expand(ps, chain)
}
