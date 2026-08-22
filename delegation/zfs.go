package delegation

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ZFSCommand is the zfs(8) binary; tests may point it elsewhere.
var ZFSCommand = "zfs"

// Run runs zfs with args and returns its standard output. On failure the
// error carries zfs's message (without the "cannot …" noise trimmed).
func Run(args ...string) (string, error) {
	cmd := exec.Command(ZFSCommand, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		if errors.Is(err, exec.ErrNotFound) {
			msg = "zfs command not found"
		}
		return out.String(), fmt.Errorf("%s", msg)
	}
	return out.String(), nil
}

// Dataset is a file system or volume as listed by zfs list.
type Dataset struct {
	Name       string
	Type       string // filesystem or volume
	Mountpoint string // "-" for volumes, "none"/"legacy" or a path
	Mounted    bool
}

// IsVolume reports whether the dataset is a zvol.
func (d Dataset) IsVolume() bool { return d.Type == "volume" }

// Depth is the number of / in the name (pool = 0).
func (d Dataset) Depth() int { return strings.Count(d.Name, "/") }

// Parent is the parent dataset name ("" for a pool).
func (d Dataset) Parent() string { return ParentOf(d.Name) }

// ParentOf returns the parent dataset name ("" for a pool).
func ParentOf(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[:i]
	}
	return ""
}

// Ancestors returns the ancestor dataset names, nearest first.
func Ancestors(name string) []string {
	var res []string
	for p := ParentOf(name); p != ""; p = ParentOf(p) {
		res = append(res, p)
	}
	return res
}

// Datasets lists every file system and volume, sorted by name.
func Datasets() ([]Dataset, error) {
	out, err := Run("list", "-H", "-p", "-o", "name,type,mountpoint,mounted", "-t", "filesystem,volume", "-s", "name")
	if err != nil {
		return nil, err
	}
	var res []Dataset
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 4 {
			continue
		}
		res = append(res, Dataset{Name: f[0], Type: f[1], Mountpoint: f[2], Mounted: f[3] == "yes"})
	}
	return res, nil
}

// Info returns the dataset's type and mount point, or an error if it does
// not exist (or is a snapshot).
func Info(name string) (Dataset, error) {
	out, err := Run("list", "-H", "-p", "-o", "name,type,mountpoint,mounted", "-t", "filesystem,volume", name)
	if err != nil {
		return Dataset{}, err
	}
	f := strings.Split(strings.TrimSpace(out), "\t")
	if len(f) < 4 {
		return Dataset{}, fmt.Errorf("%s: not a file system or volume", name)
	}
	return Dataset{Name: f[0], Type: f[1], Mountpoint: f[2], Mounted: f[3] == "yes"}, nil
}

// Listing is what the tool works on: a dataset, its own delegations and
// those of its ancestors (nearest first, only the ones that delegate).
type Listing struct {
	Dataset   Dataset
	Own       *Delegations   // never nil; Empty() when nothing is delegated
	Ancestors []*Delegations // nearest first
}

// Chain is own + ancestors, for Expand.
func (l *Listing) Chain() []*Delegations { return append([]*Delegations{l.Own}, l.Ancestors...) }

// Load reads the delegations of a dataset with "zfs allow".
func Load(name string) (*Listing, error) {
	ds, err := Info(name)
	if err != nil {
		return nil, err
	}
	out, err := Run("allow", name)
	if err != nil {
		return nil, err
	}
	secs, err := Parse(out)
	if err != nil {
		return nil, err
	}
	own, anc := Split(secs, name)
	if own == nil {
		own = &Delegations{Dataset: name}
	}
	// keep the ancestors nearest first (zfs prints them that way, but be safe)
	sort.SliceStable(anc, func(i, j int) bool { return len(anc[i].Dataset) > len(anc[j].Dataset) })
	return &Listing{Dataset: ds, Own: own, Ancestors: anc}, nil
}

// DatasetForPath returns the dataset the path lives on ("" if it is not on
// ZFS). It statfs's the path, so the path must exist.
func DatasetForPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var st unix.Statfs_t
	if err := unix.Statfs(abs, &st); err != nil {
		return "", err
	}
	if cstr(st.Fstypename[:]) != "zfs" {
		return "", nil
	}
	return cstr(st.Mntfromname[:]), nil
}

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// Usermount reports the value of vfs.usermount, which gates unprivileged
// mounting (and therefore create/destroy/snapshot/… delegations) on FreeBSD.
func Usermount() (bool, error) {
	v, err := unix.SysctlUint32("vfs.usermount")
	if err != nil {
		return false, err
	}
	return v != 0, nil
}

// Principal is a user or group known to the system.
type Principal struct {
	Name string
	ID   int
}

// Users returns the system's users (getent, falling back to /etc/passwd).
func Users() []Principal { return enumerate("passwd", "/etc/passwd") }

// Groups returns the system's groups.
func Groups() []Principal { return enumerate("group", "/etc/group") }

func enumerate(db, file string) []Principal {
	var text string
	if out, err := exec.Command("getent", db).Output(); err == nil {
		text = string(out)
	} else if b, err := os.ReadFile(file); err == nil {
		text = string(b)
	}
	seen := map[string]bool{}
	var res []Principal
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			continue
		}
		f := strings.Split(line, ":")
		if len(f) < 3 || seen[f[0]] {
			continue
		}
		id, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		seen[f[0]] = true
		res = append(res, Principal{Name: f[0], ID: id})
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Name < res[j].Name })
	return res
}

// Identity is a user with their groups, for effective-permission questions.
type Identity struct {
	Name   string
	UID    int
	Groups []string // group names, primary first
	GIDs   []int
}

// IdentityOf looks up a user by name or numeric uid.
func IdentityOf(spec string) (Identity, error) {
	var u *user.User
	var err error
	if _, convErr := strconv.Atoi(spec); convErr == nil {
		u, err = user.LookupId(spec)
	} else {
		u, err = user.Lookup(spec)
	}
	if err != nil {
		return Identity{}, err
	}
	return identityOf(u)
}

// CurrentIdentity is the identity of the process owner.
func CurrentIdentity() (Identity, error) {
	u, err := user.Current()
	if err != nil {
		return Identity{}, err
	}
	return identityOf(u)
}

func identityOf(u *user.User) (Identity, error) {
	id := Identity{Name: u.Username}
	id.UID, _ = strconv.Atoi(u.Uid)
	gids, err := u.GroupIds()
	if err != nil {
		// fall back to the primary group
		gids = []string{u.Gid}
	}
	// primary first
	ordered := []string{u.Gid}
	for _, g := range gids {
		if g != u.Gid {
			ordered = append(ordered, g)
		}
	}
	for _, g := range ordered {
		n, _ := strconv.Atoi(g)
		id.GIDs = append(id.GIDs, n)
		if gr, err := user.LookupGroupId(g); err == nil {
			id.Groups = append(id.Groups, gr.Name)
		} else {
			id.Groups = append(id.Groups, g)
		}
	}
	return id, nil
}

// Matches reports whether a grant to w applies to the identity.
func (id Identity) Matches(w Who) bool {
	switch w.Kind {
	case WhoEveryone:
		return true
	case WhoUser:
		if w.Name != "" {
			return w.Name == id.Name
		}
		return w.ID == id.UID
	default:
		for i, g := range id.Groups {
			if w.Name != "" && g == w.Name || w.Name == "" && i < len(id.GIDs) && id.GIDs[i] == w.ID {
				return true
			}
		}
		return false
	}
}

// ShellQuote quotes s for a POSIX shell if needed.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./-_", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
