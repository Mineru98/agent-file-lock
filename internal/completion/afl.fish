# fish completion for afl.
# Install: afl completion fish > ~/.config/fish/completions/afl.fish

set -l cmds lock unlock status check doctor completion version help

complete -c afl -f
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a lock -d 'Lock files (immutable by default)'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a unlock -d 'Remove locks'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a status -d 'Show lock state'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a check -d 'Compare config against actual state'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a doctor -d 'Diagnose platform and filesystem support'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a completion -d 'Print shell completion script'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a version -d 'Print version'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a help -d 'Show help'

set -l ops lock unlock status check
complete -c afl -n "__fish_seen_subcommand_from $ops" -F
complete -c afl -n "__fish_seen_subcommand_from $ops" -s f -l config -r -F -d 'Config file (yaml/json)'
complete -c afl -n "__fish_seen_subcommand_from $ops" -s R -l recursive -d 'Recurse into directories'
complete -c afl -n "__fish_seen_subcommand_from $ops" -s n -l dry-run -d 'Print the plan only'
complete -c afl -n "__fish_seen_subcommand_from $ops" -l level -x -a 'strong user' -d 'Lock level'
complete -c afl -n "__fish_seen_subcommand_from $ops" -l include-dirs -d 'Also lock directory inodes'
complete -c afl -n "__fish_seen_subcommand_from $ops" -l dir-only -d 'Lock the directory inode only'
complete -c afl -n "__fish_seen_subcommand_from $ops" -l exclude -x -d 'Glob to skip'
complete -c afl -n "__fish_seen_subcommand_from $ops" -l follow-symlinks -d 'Lock symlink targets'
complete -c afl -n "__fish_seen_subcommand_from $ops" -l fail-fast -d 'Stop at first failure'
complete -c afl -n "__fish_seen_subcommand_from $ops" -l json -d 'Machine-readable output'
complete -c afl -n "__fish_seen_subcommand_from $ops" -s q -l quiet -d 'Only print failures'
complete -c afl -n "__fish_seen_subcommand_from $ops" -l elevate -d 'Re-exec via sudo'
complete -c afl -n "__fish_seen_subcommand_from doctor" -F
complete -c afl -n "__fish_seen_subcommand_from doctor" -l json -d 'Machine-readable output'
complete -c afl -n "__fish_seen_subcommand_from completion" -x -a 'bash zsh fish'
