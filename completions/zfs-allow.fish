# fish completion for zfs-allow(1). Install as
# /usr/local/share/fish/vendor_completions.d/zfs-allow.fish or ~/.config/fish/completions/zfs-allow.fish.

function __zfs_allow_datasets
    zfs list -H -o name -t filesystem,volume 2>/dev/null
end

function __zfs_allow_who
    __fish_complete_users | sed 's/^/user:/'
    __fish_complete_groups | sed 's/^/group:/'
    printf 'everyone\ncreate-time\n'
    zfs-allow -list 2>/dev/null | awk '/^\t@/ { print $1 }' | sort -u
end

function __zfs_allow_who_only
    __fish_complete_users | sed 's/^/user:/'
    __fish_complete_groups | sed 's/^/group:/'
    printf 'everyone\n'
end

function __zfs_allow_perms
    zfs-allow -catalogue 2>/dev/null | awk '/^  [a-z+]/ { print $1 }'
end

complete -c zfs-allow -o version -d 'Print version and exit'
complete -c zfs-allow -o list -d 'Print the delegations of the dataset and its ancestors'
complete -c zfs-allow -o add -x -a '(__zfs_allow_who)' -d 'Grant -perms to WHO (user:NAME, group:NAME, everyone, @SET, create-time)'
complete -c zfs-allow -o remove -x -a '(__zfs_allow_who)' -d 'Revoke -perms (or everything) from WHO'
complete -c zfs-allow -o perms -x -a '(__fish_complete_list , __zfs_allow_perms)' -d 'Permissions, @SETs and +PRESETs (comma-separated)'
complete -c zfs-allow -o scope -x -a 'both local descendants' -d 'Where the grant applies'
complete -c zfs-allow -o r -d 'With -remove: every descendant too (zfs unallow -r); with -dump: include descendants'
complete -c zfs-allow -o effective -x -a '(__fish_complete_users)' -d 'Effective permissions of USER on the dataset'
complete -c zfs-allow -o where -x -a '(__zfs_allow_who_only)' -d 'Every dataset delegating to WHO'
complete -c zfs-allow -o dump -d 'JSON snapshot of the delegations'
complete -c zfs-allow -o restore -r -d 'Restore a -dump snapshot'
complete -c zfs-allow -o catalogue -d 'Print the permission catalogue and presets'
complete -c zfs-allow -o n -d 'Dry run: print the zfs commands'
complete -c zfs-allow -o y -d 'Apply without asking'
complete -c zfs-allow -o check -d 'Dry run, exit 3 if anything would change'
complete -c zfs-allow -o json -d 'JSON output'
complete -c zfs-allow -o lenient -d 'Accept unknown permission names'
complete -c zfs-allow -f -a '(__zfs_allow_datasets)' -d 'Dataset'
