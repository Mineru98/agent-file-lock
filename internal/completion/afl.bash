# bash completion for afl — compatible with bash 3.2 (macOS default).
# Install: afl completion bash > ~/.local/share/bash-completion/completions/afl
_afl() {
  local cur prev
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  COMPREPLY=()

  case "$prev" in
    -f|--config)
      COMPREPLY=( $(compgen -f -X '!*.@(yaml|yml|json)' -- "$cur") $(compgen -d -- "$cur") )
      return 0 ;;
    completion)
      COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
      return 0 ;;
    --level)
      COMPREPLY=( $(compgen -W "strong user" -- "$cur") )
      return 0 ;;
    --exclude)
      return 0 ;;
  esac

  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "lock unlock status check doctor completion version help" -- "$cur") )
    return 0
  fi

  case "$cur" in
    -*)
      COMPREPLY=( $(compgen -W "-f --config -R --recursive -n --dry-run --level --include-dirs --dir-only --exclude --follow-symlinks --fail-fast --json -q --quiet --elevate -h --help -v --version" -- "$cur") )
      return 0 ;;
  esac
  # fall through to default path completion (-o default)
  return 0
}
complete -o default -F _afl afl
