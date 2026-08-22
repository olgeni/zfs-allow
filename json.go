package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/olgeni/zfs-allow/delegation"
)

type jsonWho struct {
	Kind string `json:"kind"`           // user, group, everyone
	Name string `json:"name,omitempty"` // empty when the id does not resolve
	ID   *int   `json:"id,omitempty"`
}

func toJSONWho(w delegation.Who) jsonWho {
	j := jsonWho{Kind: w.Kind.String(), Name: w.Name}
	if w.ID >= 0 && w.Kind != delegation.WhoEveryone {
		id := w.ID
		j.ID = &id
	}
	return j
}

type jsonGrant struct {
	Who     jsonWho  `json:"who"`
	Local   []string `json:"local"`   // on the dataset itself
	Descend []string `json:"descend"` // on its descendants
}

type jsonSet struct {
	Name  string   `json:"name"`
	Perms []string `json:"perms"`
}

type jsonDelegations struct {
	Dataset string      `json:"dataset"`
	Sets    []jsonSet   `json:"sets"`
	Create  []string    `json:"create_time"`
	Grants  []jsonGrant `json:"grants"`
}

func toJSONDelegations(d *delegation.Delegations) jsonDelegations {
	j := jsonDelegations{Dataset: d.Dataset, Sets: []jsonSet{}, Create: strs(d.Create), Grants: []jsonGrant{}}
	for _, s := range d.Sets {
		j.Sets = append(j.Sets, jsonSet{s.Name, strs(s.Perms)})
	}
	for _, g := range d.Grants {
		j.Grants = append(j.Grants, jsonGrant{toJSONWho(g.Who), strs(g.Local), strs(g.Descend)})
	}
	return j
}

func strs(ps delegation.PermSet) []string {
	if ps == nil {
		return []string{}
	}
	return []string(ps)
}

type jsonListingDoc struct {
	Dataset    string            `json:"dataset"`
	Type       string            `json:"type"`
	Mountpoint string            `json:"mountpoint"`
	Mounted    bool              `json:"mounted"`
	Own        jsonDelegations   `json:"own"`
	Ancestors  []jsonDelegations `json:"ancestors"` // nearest first, only those that delegate
}

func jsonListing(l *delegation.Listing) jsonListingDoc {
	doc := jsonListingDoc{Dataset: l.Dataset.Name, Type: l.Dataset.Type, Mountpoint: l.Dataset.Mountpoint, Mounted: l.Dataset.Mounted,
		Own: toJSONDelegations(l.Own), Ancestors: []jsonDelegations{}}
	for _, a := range l.Ancestors {
		doc.Ancestors = append(doc.Ancestors, toJSONDelegations(a))
	}
	return doc
}

type jsonSource struct {
	Dataset string  `json:"dataset"`
	Who     jsonWho `json:"who"`
	Via     string  `json:"via,omitempty"`
	Scope   string  `json:"scope"` // local (granted on the dataset) or descend (inherited from an ancestor)
}

type jsonEffectiveDoc struct {
	Dataset     string                  `json:"dataset"`
	User        string                  `json:"user"`
	UID         int                     `json:"uid"`
	Groups      []string                `json:"groups"`
	Root        bool                    `json:"root"`
	Permissions map[string][]jsonSource `json:"permissions"`
}

func jsonEffective(l *delegation.Listing, id delegation.Identity, eff delegation.Effective) jsonEffectiveDoc {
	doc := jsonEffectiveDoc{Dataset: l.Dataset.Name, User: id.Name, UID: id.UID, Groups: id.Groups, Root: id.UID == 0, Permissions: map[string][]jsonSource{}}
	for p, srcs := range eff {
		for _, s := range srcs {
			sc := "local"
			if s.Scope == delegation.ScopeDescend {
				sc = "descend"
			}
			doc.Permissions[p] = append(doc.Permissions[p], jsonSource{s.Dataset, toJSONWho(s.Who), s.Via, sc})
		}
	}
	return doc
}

type jsonWhereDoc struct {
	Who      jsonWho        `json:"who"`
	Datasets []jsonWhereHit `json:"datasets"`
}

type jsonWhereHit struct {
	Dataset string   `json:"dataset"`
	Local   []string `json:"local"`
	Descend []string `json:"descend"`
}

func jsonWhere(who delegation.Who, hits []*whereHit) jsonWhereDoc {
	doc := jsonWhereDoc{Who: toJSONWho(who), Datasets: []jsonWhereHit{}}
	for _, h := range hits {
		doc.Datasets = append(doc.Datasets, jsonWhereHit{h.Dataset, strs(h.Grant.Local), strs(h.Grant.Descend)})
	}
	return doc
}

type jsonPlanDoc struct {
	Dataset  string        `json:"dataset"`
	Commands []jsonCommand `json:"commands"`
	Problems []jsonProblem `json:"problems"`
}

type jsonCommand struct {
	Args    []string `json:"args"` // zfs arguments
	Command string   `json:"command"`
	Desc    string   `json:"description"`
}

type jsonProblem struct {
	Fatal bool   `json:"fatal"`
	Msg   string `json:"message"`
}

func jsonPlan(p *delegation.Plan, probs []delegation.Problem) jsonPlanDoc {
	doc := jsonPlanDoc{Dataset: p.Dataset, Commands: []jsonCommand{}, Problems: []jsonProblem{}}
	for _, s := range p.Steps {
		doc.Commands = append(doc.Commands, jsonCommand{s.Args, s.String(), s.Desc})
	}
	for _, pr := range probs {
		doc.Problems = append(doc.Problems, jsonProblem{pr.Fatal, pr.Msg})
	}
	return doc
}

type jsonPerm struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Group      string   `json:"group"`
	Note       string   `json:"note"`
	Needs      []string `json:"needs"`
	NotFreeBSD bool     `json:"not_freebsd,omitempty"`
}

type jsonPreset struct {
	Name  string   `json:"name"`
	Desc  string   `json:"description"`
	Perms []string `json:"perms"`
}

type jsonCatalogueDoc struct {
	Groups      []string     `json:"groups"`
	Permissions []jsonPerm   `json:"permissions"`
	Presets     []jsonPreset `json:"presets"`
}

func jsonCatalogue() jsonCatalogueDoc {
	doc := jsonCatalogueDoc{Groups: delegation.GroupOrder, Permissions: []jsonPerm{}, Presets: []jsonPreset{}}
	for _, p := range delegation.Catalogue {
		needs := p.Needs
		if needs == nil {
			needs = []string{}
		}
		doc.Permissions = append(doc.Permissions, jsonPerm{p.Name, p.Kind.String(), p.Group, p.Note, needs, p.NotFreeBSD})
	}
	for _, p := range delegation.Presets {
		doc.Presets = append(doc.Presets, jsonPreset{p.Name, p.Desc, strs(p.Perms)})
	}
	return doc
}

func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "zfs-allow:", err)
		return 1
	}
	return 0
}
