# fish completion for afl.
# Install: afl completion fish > ~/.config/fish/completions/afl.fish

set -l cmds lock unlock status check run hook doctor completion version help

complete -c afl -f
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a lock -d 'Lock files (immutable by default)'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a unlock -d 'Remove locks'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a status -d 'Show lock state'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a check -d 'Compare config against actual state'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a run -d 'Unlock, run a command, then re-lock'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a hook -d 'Refuse an agent edit to a locked path and explain why'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a doctor -d 'Diagnose platform and filesystem support'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a completion -d 'Print shell completion script'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a version -d 'Print version'
complete -c afl -n "not __fish_seen_subcommand_from $cmds" -a help -d 'Show help'

set -l ops lock unlock status check run
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
complete -c afl -n "__fish_seen_subcommand_from lock unlock" -l guard-parents -d 'Make ancestors append-only (default)'
complete -c afl -n "__fish_seen_subcommand_from lock unlock" -l no-guard-parents -d 'Leave ancestors alone'
complete -c afl -n "__fish_seen_subcommand_from lock unlock" -l guard-root -r -F -d 'How far up the guard walks'
complete -c afl -n "__fish_seen_subcommand_from status" -s a -l all -d 'Do not skip build directories'
complete -c afl -n "__fish_seen_subcommand_from status" -l depth -x -d 'Limit scan depth'
complete -c afl -n "__fish_seen_subcommand_from run" -l as-root -d 'Keep root for the command'
complete -c afl -n "__fish_seen_subcommand_from hook" -F
complete -c afl -n "__fish_seen_subcommand_from hook; and not __fish_seen_subcommand_from install uninstall print check" -a 'install uninstall print check'
complete -c afl -n "__fish_seen_subcommand_from install uninstall print" -a 'claude-code codex generic'
complete -c afl -n "__fish_seen_subcommand_from hook" -l format -x -a 'auto json exit-code' -d 'Response dialect'
complete -c afl -n "__fish_seen_subcommand_from hook" -l strict -d 'Deny unclassifiable commands naming a locked path'
complete -c afl -n "__fish_seen_subcommand_from hook" -l all -d 'Every known harness'
complete -c afl -n "__fish_seen_subcommand_from hook" -l global -d 'User-level config'
complete -c afl -n "__fish_seen_subcommand_from doctor" -F
complete -c afl -n "__fish_seen_subcommand_from doctor" -l json -d 'Machine-readable output'
complete -c afl -n "__fish_seen_subcommand_from completion" -x -a 'bash zsh fish'
