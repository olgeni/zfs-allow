// Package delegation models ZFS delegated administration (zfs allow /
// zfs unallow): the permission catalogue, the delegations of a dataset as
// printed by "zfs allow", the commands that turn one state into another,
// and the effective permissions of a user.
package delegation

import (
	"sort"
	"strings"
)

// Kind is the kind of a permission, as in the NAME/TYPE table of zfs-allow(8).
type Kind int

const (
	KindSubcommand Kind = iota
	KindOther
	KindProperty
)

func (k Kind) String() string {
	switch k {
	case KindSubcommand:
		return "subcommand"
	case KindOther:
		return "other"
	default:
		return "property"
	}
}

// Perm describes one delegatable permission.
type Perm struct {
	Name  string
	Kind  Kind
	Group string   // catalogue group, for the picker
	Note  string   // what it allows / what it needs, from zfs-allow(8)
	Needs []string // permissions that must also be delegated for this one to be usable (same dataset)
	// NotFreeBSD marks permissions zfs allow refuses on FreeBSD
	// ("operation not applicable to datasets of this type").
	NotFreeBSD bool
}

// Catalogue groups, in display order.
const (
	GroupLifecycle  = "Datasets & snapshots"
	GroupSend       = "Send & receive"
	GroupShare      = "Sharing"
	GroupDelegate   = "Delegation"
	GroupEncryption = "Encryption"
	GroupQuota      = "Quotas & space accounting"
	GroupMount      = "Mounting & visibility"
	GroupAccess     = "Access control & attributes"
	GroupStorage    = "Storage & performance"
	GroupNaming     = "Names & compatibility"
	GroupUserProp   = "User properties"
)

// GroupOrder is the display order of the catalogue groups.
var GroupOrder = []string{GroupLifecycle, GroupSend, GroupShare, GroupDelegate, GroupEncryption,
	GroupQuota, GroupMount, GroupAccess, GroupStorage, GroupNaming, GroupUserProp}

func sub(name, group, note string, needs ...string) Perm {
	return Perm{Name: name, Kind: KindSubcommand, Group: group, Note: note, Needs: needs}
}
func other(name, group, note string, needs ...string) Perm {
	return Perm{Name: name, Kind: KindOther, Group: group, Note: note, Needs: needs}
}
func prop(name, group, note string) Perm {
	return Perm{Name: name, Kind: KindProperty, Group: group, Note: note}
}

// Catalogue lists every permission zfs-allow(8) knows (FreeBSD 15 / OpenZFS 2.4),
// in catalogue-group order.
var Catalogue = []Perm{
	// Datasets & snapshots
	sub("create", GroupLifecycle, "create file systems and volumes; needs mount, and refreservation for non-sparse volumes", "mount"),
	sub("destroy", GroupLifecycle, "destroy datasets and snapshots; needs mount", "mount"),
	sub("rename", GroupLifecycle, "rename datasets; needs mount and create in the new parent", "mount", "create"),
	sub("mount", GroupLifecycle, "mount and unmount file systems (on FreeBSD also needs vfs.usermount=1 and a writable mount point)"),
	sub("snapshot", GroupLifecycle, "take snapshots; needs mount", "mount"),
	sub("rollback", GroupLifecycle, "roll back to a snapshot; needs mount", "mount"),
	sub("clone", GroupLifecycle, "clone snapshots; needs create and mount in the origin file system", "create", "mount"),
	sub("promote", GroupLifecycle, "promote clones; needs mount and promote in the origin file system", "mount"),
	sub("bookmark", GroupLifecycle, "create bookmarks"),
	sub("hold", GroupLifecycle, "add user holds to snapshots"),
	sub("release", GroupLifecycle, "release user holds (which may destroy the snapshot)"),
	sub("diff", GroupLifecycle, "zfs diff: look up paths by object number and take the snapshots it needs"),
	// Send & receive
	sub("send", GroupSend, "send replication streams"),
	sub("send:raw", GroupSend, "send raw streams only (encrypted datasets never leave decrypted)"),
	sub("receive", GroupSend, "receive streams, including receive -F; needs mount and create", "mount", "create"),
	other("receive:append", GroupSend, "receive streams without -F (no forced rollback); needs mount and create", "mount", "create"),
	// Sharing
	sub("share", GroupShare, "share file systems over NFS or SMB"),
	prop("sharenfs", GroupShare, "NFS sharing options"),
	prop("sharesmb", GroupShare, "SMB sharing options"),
	// Delegation
	sub("allow", GroupDelegate, "delegate permissions the user holds themselves (zfs allow); unallow of other users still needs root"),
	// Encryption
	sub("load-key", GroupEncryption, "load and unload encryption keys"),
	sub("change-key", GroupEncryption, "change encryption keys"),
	prop("encryption", GroupEncryption, "encryption algorithm (set at creation)"),
	prop("keyformat", GroupEncryption, "key format (raw, hex, passphrase)"),
	prop("keylocation", GroupEncryption, "where the key comes from (prompt, file://…)"),
	prop("pbkdf2iters", GroupEncryption, "PBKDF2 iterations for passphrase keys"),
	// Quotas & space
	prop("quota", GroupQuota, "quota including descendants and snapshots"),
	prop("refquota", GroupQuota, "quota on the dataset's own data"),
	prop("reservation", GroupQuota, "guaranteed space including descendants"),
	prop("refreservation", GroupQuota, "guaranteed space for the dataset itself"),
	prop("filesystem_limit", GroupQuota, "maximum number of descendant file systems"),
	prop("snapshot_limit", GroupQuota, "maximum number of snapshots"),
	other("userquota", GroupQuota, "set/read any userquota@… property"),
	other("userobjquota", GroupQuota, "set/read any userobjquota@… property"),
	other("userused", GroupQuota, "read any userused@… property"),
	other("userobjused", GroupQuota, "read any userobjused@… property"),
	other("groupquota", GroupQuota, "set/read any groupquota@… property"),
	other("groupobjquota", GroupQuota, "set/read any groupobjquota@… property"),
	other("groupused", GroupQuota, "read any groupused@… property"),
	other("groupobjused", GroupQuota, "read any groupobjused@… property"),
	other("projectquota", GroupQuota, "set/read any projectquota@… property"),
	other("projectobjquota", GroupQuota, "set/read any projectobjquota@… property"),
	other("projectused", GroupQuota, "read any projectused@… property"),
	other("projectobjused", GroupQuota, "read any projectobjused@… property"),
	prop("defaultuserquota", GroupQuota, "default user quota"),
	prop("defaultuserobjquota", GroupQuota, "default user object quota"),
	prop("defaultgroupquota", GroupQuota, "default group quota"),
	prop("defaultgroupobjquota", GroupQuota, "default group object quota"),
	prop("defaultprojectquota", GroupQuota, "default project quota"),
	prop("defaultprojectobjquota", GroupQuota, "default project object quota"),
	// Mounting & visibility
	prop("mountpoint", GroupMount, "mount point"),
	prop("canmount", GroupMount, "whether the file system can be mounted (on, off, noauto)"),
	prop("overlay", GroupMount, "allow mounting over a non-empty directory"),
	prop("readonly", GroupMount, "read-only"),
	prop("snapdir", GroupMount, "visibility of the .zfs directory"),
	prop("snapdev", GroupMount, "visibility of volume snapshot devices"),
	prop("volmode", GroupMount, "how volumes are exposed (geom, dev, none)"),
	prop("jailed", GroupMount, "dataset managed from within a jail (FreeBSD)"),
	{Name: "zoned", Kind: KindProperty, Group: GroupMount, Note: "Solaris/Linux zones — not applicable on FreeBSD", NotFreeBSD: true},
	// Access control & attributes
	prop("aclinherit", GroupAccess, "ACL inheritance (discard, noallow, restricted, passthrough, passthrough-x)"),
	prop("aclmode", GroupAccess, "what chmod does to the ACL (discard, groupmask, passthrough, restricted)"),
	prop("acltype", GroupAccess, "ACL type (off, nfsv4, posix)"),
	prop("atime", GroupAccess, "update access times"),
	prop("relatime", GroupAccess, "relaxed access time updates"),
	prop("exec", GroupAccess, "allow executing files"),
	prop("setuid", GroupAccess, "honour set-uid bits"),
	prop("devices", GroupAccess, "allow device nodes"),
	prop("nbmand", GroupAccess, "non-blocking mandatory locking"),
	prop("xattr", GroupAccess, "extended attributes (on, off, sa)"),
	prop("vscan", GroupAccess, "virus scanning"),
	{Name: "mlslabel", Kind: KindProperty, Group: GroupAccess, Note: "Solaris Trusted Extensions label — not applicable on FreeBSD", NotFreeBSD: true},
	prop("context", GroupAccess, "SELinux context"),
	prop("fscontext", GroupAccess, "SELinux file system context"),
	prop("defcontext", GroupAccess, "SELinux default context"),
	prop("rootcontext", GroupAccess, "SELinux root context"),
	// Storage & performance
	prop("compression", GroupStorage, "compression algorithm"),
	prop("checksum", GroupStorage, "checksum algorithm"),
	prop("dedup", GroupStorage, "deduplication"),
	prop("copies", GroupStorage, "number of data copies"),
	prop("recordsize", GroupStorage, "record size"),
	prop("dnodesize", GroupStorage, "dnode size"),
	prop("special_small_blocks", GroupStorage, "small-block threshold for the special vdev"),
	prop("logbias", GroupStorage, "ZIL bias (latency, throughput)"),
	prop("sync", GroupStorage, "synchronous write behaviour"),
	prop("primarycache", GroupStorage, "what the ARC caches"),
	prop("secondarycache", GroupStorage, "what the L2ARC caches"),
	prop("redundant_metadata", GroupStorage, "metadata redundancy"),
	prop("prefetch", GroupStorage, "prefetch behaviour"),
	prop("direct", GroupStorage, "direct I/O behaviour"),
	prop("volsize", GroupStorage, "volume size"),
	prop("volblocksize", GroupStorage, "volume block size (set at creation)"),
	// Names & compatibility
	prop("casesensitivity", GroupNaming, "case sensitivity (set at creation)"),
	prop("normalization", GroupNaming, "Unicode normalization (set at creation)"),
	prop("utf8only", GroupNaming, "reject non-UTF-8 names (set at creation)"),
	prop("longname", GroupNaming, "allow file names longer than 255 bytes"),
	prop("version", GroupNaming, "on-disk file system version"),
	// User properties
	other("userprop", GroupUserProp, "set any user property (module:name=value)"),
}

var catalogueByName = func() map[string]*Perm {
	m := make(map[string]*Perm, len(Catalogue))
	for i := range Catalogue {
		m[Catalogue[i].Name] = &Catalogue[i]
	}
	return m
}()

// Lookup returns the catalogue entry for name.
func Lookup(name string) (Perm, bool) {
	p, ok := catalogueByName[name]
	if !ok {
		return Perm{}, false
	}
	return *p, true
}

// Known reports whether name is a catalogue permission.
func Known(name string) bool { _, ok := catalogueByName[name]; return ok }

// IsSet reports whether name is a permission-set reference (@name).
func IsSet(name string) bool { return strings.HasPrefix(name, "@") }

// ValidPermName reports whether name could be given to zfs allow: a catalogue
// permission, or a permission set name. Unknown names are accepted when
// lenient is set (newer OpenZFS versions may know more permissions).
func ValidPermName(name string, lenient bool) bool {
	if IsSet(name) {
		return ValidSetName(name)
	}
	if Known(name) {
		return true
	}
	return lenient && permNameRe(name)
}

func permNameRe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == ':' || r == '-') {
			return false
		}
	}
	return true
}

// ValidSetName reports whether s is a valid permission set name: @ followed
// by a dataset-component name, at most 64 characters in total.
func ValidSetName(s string) bool {
	if !strings.HasPrefix(s, "@") || len(s) < 2 || len(s) > 64 {
		return false
	}
	for _, r := range s[1:] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' || r == ':') {
			return false
		}
	}
	return true
}

// ByGroup returns the catalogue permissions of one group, in catalogue order.
func ByGroup(group string) []Perm {
	var res []Perm
	for _, p := range Catalogue {
		if p.Group == group {
			res = append(res, p)
		}
	}
	return res
}

// Presets are named bundles of permissions for common delegation tasks.
type Preset struct {
	Name  string
	Desc  string
	Perms PermSet
}

// Presets lists the built-in bundles, in display order.
var Presets = []Preset{
	{"snapshots", "take, hold, release and destroy snapshots, roll back", ParsePerms("snapshot,hold,release,destroy,rollback,mount,bookmark,diff")},
	{"backup", "send streams and take the snapshots a backup needs", ParsePerms("send,snapshot,hold,release,bookmark,mount")},
	{"replication-target", "receive streams into this subtree", ParsePerms("receive,create,mount,destroy,rollback,hold,release,snapshot,userprop")},
	{"datasets", "create, destroy, rename, clone and mount datasets", ParsePerms("create,destroy,rename,clone,promote,mount,snapshot")},
	{"properties", "tune the common properties", ParsePerms("compression,atime,relatime,recordsize,quota,refquota,reservation,refreservation,mountpoint,canmount,readonly,sharenfs,userprop,exec,setuid,snapdir")},
	{"quotas", "set quotas and reservations, read usage", ParsePerms("quota,refquota,reservation,refreservation,userquota,userobjquota,userused,userobjused,groupquota,groupobjquota,groupused,groupobjused,projectquota,projectobjquota,projectused,projectobjused")},
	{"everything", "every permission zfs allow accepts on FreeBSD", allFreeBSD()},
}

func allFreeBSD() PermSet {
	var s PermSet
	for _, p := range Catalogue {
		if !p.NotFreeBSD {
			s = append(s, p.Name)
		}
	}
	sort.Strings(s)
	return s
}

// PresetByName returns the preset called name.
func PresetByName(name string) (Preset, bool) {
	for _, p := range Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}
