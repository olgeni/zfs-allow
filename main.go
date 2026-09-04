// Command zfs-allow is a terminal UI and scripting front-end for ZFS
// delegated administration (zfs allow / zfs unallow).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/olgeni/zfs-allow/delegation"
	"github.com/olgeni/zfs-allow/ui"
)

const version = "1.0.4"

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: zfs-allow [dataset]                                  (interactive)
       zfs-allow -list [-json] [dataset]                       (like zfs allow, plus the ancestors)
       zfs-allow -add WHO -perms P,… [-scope S] [-n|-y|-check] [dataset]
       zfs-allow -remove WHO [-perms P,…] [-scope S] [-n|-y|-check] [dataset]
       zfs-allow -remove WHO -r [-perms P,…] [-scope S] [-n|-y] [dataset]   (here and every descendant)
       zfs-allow -effective USER [-json] [dataset]             (what USER may do there)
       zfs-allow -where WHO [-json]                            (every dataset delegating to WHO)
       zfs-allow -dump [-r] [dataset] > F | zfs-allow -restore F [-n|-y|-check]   (snapshot / restore)
       zfs-allow -catalogue [-json]                            (every permission, with descriptions)

WHO is user:NAME, group:NAME, everyone, @SET (a permission set definition) or
create-time. P is a permission from the catalogue, an @SET, or +PRESET
(zfs-allow -catalogue lists both). S is both (default), local or descendants.
The dataset defaults to the one the current directory is on. Exit status: 0,
1 error, 2 declined, 3 changes pending (-check).

`)
		flag.PrintDefaults()
	}
	showVersion := flag.Bool("version", false, "print version and exit")
	var o cliOptions
	flag.BoolVar(&o.list, "list", false, "non-interactive: print the delegations of the dataset and of its ancestors")
	flag.StringVar(&o.add, "add", "", "non-interactive: grant -perms to WHO (user:NAME, group:NAME, everyone, @SET to define a set, create-time)")
	flag.StringVar(&o.remove, "remove", "", "non-interactive: revoke -perms (or everything) from WHO")
	flag.StringVar(&o.perms, "perms", "", "with -add/-remove: comma-separated permissions, @SETs and +PRESETs")
	flag.StringVar(&o.scope, "scope", "both", "with -add/-remove: both (this dataset and descendants, zfs allow without -l/-d), local (-l) or descendants (-d)")
	flag.StringVar(&o.effective, "effective", "", "non-interactive: print the effective permissions of USER on the dataset, with their sources")
	flag.StringVar(&o.where, "where", "", "non-interactive: list every dataset that delegates anything to WHO (user:NAME, group:NAME, everyone)")
	flag.BoolVar(&o.recursive, "r", false, "with -remove: also on every descendant (zfs unallow -r); with -dump: include the descendants")
	flag.BoolVar(&o.dump, "dump", false, "non-interactive: print a JSON snapshot of the dataset's delegations (with -r: of every descendant too); -restore reads it")
	flag.StringVar(&o.restore, "restore", "", "non-interactive: bring the datasets recorded in the -dump snapshot FILE back to it (asks unless -y; -n previews; -check exits 3 if anything differs)")
	flag.BoolVar(&o.catalogue, "catalogue", false, "non-interactive: print the permission catalogue and the presets")
	flag.BoolVar(&o.dryRun, "n", false, "with -add/-remove: print the zfs commands and change nothing")
	flag.BoolVar(&o.yes, "y", false, "with -add/-remove: apply without asking")
	flag.BoolVar(&o.check, "check", false, "with -add/-remove: dry run that exits 3 if anything would change, 0 if nothing would")
	flag.BoolVar(&o.json, "json", false, "non-interactive: JSON output for -list, -effective, -where, -catalogue and -n")
	flag.BoolVar(&o.lenient, "lenient", false, "accept permission names that are not in the catalogue (newer OpenZFS)")
	flag.Parse()
	if *showVersion {
		fmt.Println("zfs-allow", version)
		return
	}
	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "zfs-allow: at most one dataset")
		os.Exit(1)
	}
	dataset := flag.Arg(0)
	if dataset != "" {
		ds, err := resolveDataset(dataset)
		if err != nil {
			fmt.Fprintln(os.Stderr, "zfs-allow:", err)
			os.Exit(1)
		}
		dataset = ds
	}
	cwdDS, _ := cwdDataset()
	if o.nonInteractive() {
		if dataset == "" && !o.catalogue && o.where == "" && o.restore == "" {
			if cwdDS == "" {
				fmt.Fprintln(os.Stderr, "zfs-allow: the current directory is not on ZFS; name a dataset")
				os.Exit(1)
			}
			dataset = cwdDS
		}
		os.Exit(runCLI(dataset, o))
	}
	if err := ui.Run(dataset, cwdDS); err != nil {
		fmt.Fprintln(os.Stderr, "zfs-allow:", err)
		os.Exit(1)
	}
}

// resolveDataset turns the command-line argument, a dataset name or a path
// on a ZFS file system, into the dataset's name. zfs(8) itself takes a path
// only when it contains a slash ("." and "dir" are read as dataset names:
// "self reference", "dataset does not exist"), so paths are resolved here
// with statfs; a path zfs does accept is normalised to the dataset name too.
func resolveDataset(arg string) (string, error) {
	d, err := delegation.Info(arg)
	if err == nil {
		return d.Name, nil
	}
	if _, serr := os.Stat(arg); serr != nil {
		return "", err
	}
	ds, perr := delegation.DatasetForPath(arg)
	if perr != nil {
		return "", perr
	}
	if ds == "" {
		return "", fmt.Errorf("%s: not on a ZFS file system", arg)
	}
	if d, err = delegation.Info(ds); err != nil {
		return "", err
	}
	return d.Name, nil
}

func cwdDataset() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return delegation.DatasetForPath(wd)
}
