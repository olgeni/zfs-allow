# bash completion for zfs-allow(1). Source it from ~/.bashrc or install it as
# /usr/local/share/bash-completion/completions/zfs-allow.

_zfs_allow() {
  local cur prev
  COMPREPLY=()
  if declare -F _get_comp_words_by_ref >/dev/null 2>&1; then
    _get_comp_words_by_ref -n : cur prev
  else
    cur=${COMP_WORDS[COMP_CWORD]}
    prev=${COMP_WORDS[COMP_CWORD-1]}
  fi
  case $prev in
    -scope)
      COMPREPLY=($(compgen -W 'both local descendants' -- "$cur"))
      return ;;
    -add|-remove|-where)
      case $cur in
        user:*)  COMPREPLY=($(compgen -P user: -u -- "${cur#user:}")) ;;
        group:*) COMPREPLY=($(compgen -P group: -g -- "${cur#group:}")) ;;
        *)       local extra='everyone'
                 [ "$prev" != -where ] && extra="$extra create-time $(zfs-allow -list 2>/dev/null | awk '/^\t@/ { print $1 }' | sort -u)"
                 COMPREPLY=($(compgen -W "user: group: $extra" -- "$cur"))
                 compopt -o nospace 2>/dev/null ;;
      esac
      declare -F __ltrim_colon_completions >/dev/null 2>&1 && __ltrim_colon_completions "$cur"
      return ;;
    -perms)
      local last=${cur##*,} head=
      [[ $cur == *,* ]] && head=${cur%,*},
      COMPREPLY=($(compgen -P "$head" -W "$(zfs-allow -catalogue 2>/dev/null | awk '/^  [a-z+]/ { print $1 }')" -- "$last"))
      compopt -o nospace 2>/dev/null
      return ;;
    -effective)
      COMPREPLY=($(compgen -u -- "$cur"))
      return ;;
    -restore)
      COMPREPLY=($(compgen -f -- "$cur"))
      return ;;
  esac
  if [[ $cur == -* ]]; then
    COMPREPLY=($(compgen -W '-version -list -add -remove -perms -scope -r -effective -where -dump -restore -catalogue -n -y -check -json -lenient' -- "$cur"))
    return
  fi
  COMPREPLY=($(compgen -W "$(zfs list -H -o name -t filesystem,volume 2>/dev/null)" -- "$cur"))
}
complete -F _zfs_allow zfs-allow
