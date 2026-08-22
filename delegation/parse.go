package delegation

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Parse reads the output of "zfs allow DATASET", which lists the dataset's
// own delegations followed by one section per ancestor that delegates
// anything (datasets without delegations are omitted, including the
// requested one). The sections are returned in that order.
func Parse(text string) ([]*Delegations, error) {
	var (
		res   []*Delegations
		cur   *Delegations
		sect  string
		lineN int
	)
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		lineN++
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "---- Permissions on ") {
			name := strings.TrimPrefix(line, "---- Permissions on ")
			if i := strings.Index(name, " ----"); i >= 0 {
				name = name[:i]
			}
			name = strings.TrimRight(name, "- ")
			cur = &Delegations{Dataset: name}
			res = append(res, cur)
			sect = ""
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("zfs allow output, line %d: %q before any dataset header", lineN, line)
		}
		if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
			switch strings.TrimSuffix(strings.TrimSpace(line), ":") {
			case "Permission sets", "Create time permissions", "Local permissions", "Descendent permissions", "Local+Descendent permissions":
				sect = strings.TrimSuffix(strings.TrimSpace(line), ":")
			default:
				return nil, fmt.Errorf("zfs allow output, line %d: unknown section %q", lineN, line)
			}
			continue
		}
		body := strings.TrimSpace(line)
		switch sect {
		case "Permission sets":
			name, perms, ok := strings.Cut(body, " ")
			if !ok || !IsSet(name) {
				return nil, fmt.Errorf("zfs allow output, line %d: bad permission set %q", lineN, body)
			}
			cur.SetSet(Set{name, ParsePerms(perms)})
		case "Create time permissions":
			cur.Create = cur.Create.Union(ParsePerms(body))
		case "Local permissions", "Descendent permissions", "Local+Descendent permissions":
			who, perms, err := parseWhoLine(body)
			if err != nil {
				return nil, fmt.Errorf("zfs allow output, line %d: %v", lineN, err)
			}
			g := Grant{Who: who}
			if old := cur.Grant(who); old != nil {
				g = *old
			}
			if sect != "Descendent permissions" {
				g.Local = g.Local.Union(perms)
			}
			if sect != "Local permissions" {
				g.Descend = g.Descend.Union(perms)
			}
			cur.SetGrant(g)
		default:
			return nil, fmt.Errorf("zfs allow output, line %d: %q outside any section", lineN, body)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

// parseWhoLine parses "user NAME perms", "user (unknown: ID) perms",
// "group NAME perms" or "everyone perms".
func parseWhoLine(s string) (Who, PermSet, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return Who{}, nil, fmt.Errorf("empty entry")
	}
	if f[0] == "everyone" {
		if len(f) != 2 {
			return Who{}, nil, fmt.Errorf("bad entry %q", s)
		}
		return Everyone, ParsePerms(f[1]), nil
	}
	var kind WhoKind
	switch f[0] {
	case "user":
		kind = WhoUser
	case "group":
		kind = WhoGroup
	default:
		return Who{}, nil, fmt.Errorf("bad entry %q", s)
	}
	if len(f) == 3 {
		return Who{Kind: kind, Name: f[1], ID: -1}, ParsePerms(f[2]), nil
	}
	// user (unknown: 12345) perms
	if len(f) == 4 && f[1] == "(unknown:" && strings.HasSuffix(f[2], ")") {
		id, err := strconv.Atoi(strings.TrimSuffix(f[2], ")"))
		if err == nil {
			return Who{Kind: kind, ID: id}, ParsePerms(f[3]), nil
		}
	}
	return Who{}, nil, fmt.Errorf("bad entry %q", s)
}

// Split separates the sections into the requested dataset's own delegations
// (nil if it has none) and the ancestors', nearest first.
func Split(secs []*Delegations, dataset string) (own *Delegations, ancestors []*Delegations) {
	for _, s := range secs {
		if s.Dataset == dataset {
			own = s
		} else {
			ancestors = append(ancestors, s)
		}
	}
	return own, ancestors
}

// Format renders the delegations the way zfs allow prints them (without the
// dataset header); useful for tests and -n output.
func (d *Delegations) Format() string {
	if d.Empty() {
		return ""
	}
	var b strings.Builder
	if len(d.Sets) > 0 {
		b.WriteString("Permission sets:\n")
		for _, s := range d.Sets {
			fmt.Fprintf(&b, "\t%s %s\n", s.Name, s.Perms)
		}
	}
	if !d.Create.Empty() {
		fmt.Fprintf(&b, "Create time permissions:\n\t%s\n", d.Create)
	}
	for _, sc := range []Scope{ScopeLocal, ScopeDescend, ScopeBoth} {
		first := true
		for _, r := range d.Rows() {
			if r.Scope != sc {
				continue
			}
			if first {
				fmt.Fprintf(&b, "%s permissions:\n", sc)
				first = false
			}
			fmt.Fprintf(&b, "\t%s %s\n", r.Who, r.Perms)
		}
	}
	return b.String()
}

// Header is the "---- Permissions on X ----…" line zfs allow prints.
func (d *Delegations) Header() string {
	h := "---- Permissions on " + d.Dataset + " "
	for len(h) < 70 {
		h += "-"
	}
	return h
}
