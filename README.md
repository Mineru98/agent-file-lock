# agent-file-lock (`afl`)

`afl` pins files that a coding agent (or anyone running as your user) must
never modify. It uses the kernel's **immutable flag** — `chattr +i` on Linux,
`chflags schg` on macOS — so the lock cannot be undone without root, unlike
`chmod`, which the same user can simply reverse. Locked files also cannot be
deleted or renamed, which defeats the "write to temp file then rename" trick
editors and agents use.

Single static Go binary. No runtime dependencies (stdlib only).

| Level | Mechanism | Same user can undo? | Blocks delete/rename? |
|---|---|---|---|
| `strong` (default) | Linux `FS_IMMUTABLE_FL`, macOS `SF_IMMUTABLE` | **No** (root only) | **Yes** |
| `user` | `chmod a-w` (+ macOS `UF_IMMUTABLE`) | Yes | No (Linux) / Yes (macOS) |

## Install

```sh
# macOS (Homebrew cask, installs shell completions too)
brew install Mineru98/tap/afl

# any platform with a Go toolchain
go install github.com/Mineru98/agent-file-lock/cmd/afl@latest

# from source
make build && cp bin/afl /usr/local/bin/
```

Prebuilt tarballs for linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 are
attached to every [release](https://github.com/Mineru98/agent-file-lock/releases).

## Usage

```
afl lock   <path>...                  lock files (needs sudo for strong)
afl lock   -R <dir>                   every regular file beneath <dir>
afl lock   -f afl.yaml                everything listed in the config
afl unlock <path>... | -R <dir> | -f afl.yaml
afl status [-R] <path>... | -f afl.yaml
afl check  -f afl.yaml                exit 1 if anything drifted (CI / pre-commit; no root)
afl run    -f afl.yaml -- <cmd...>    unlock, run <cmd>, then always re-lock
afl doctor [<path>]                   OS, privileges, filesystem support, WSL detection
afl completion bash|zsh|fish
```

```sh
sudo afl lock docs/POLICY.md
echo x >> docs/POLICY.md      # → Operation not permitted
rm docs/POLICY.md             # → Operation not permitted (even with sudo, until unlocked)
sudo afl unlock docs/POLICY.md
```

Flags: `-f/--config`, `-R/--recursive`, `--include-dirs`, `--dir-only`,
`--level strong|user`, `--exclude <glob>` (repeatable, `**` supported),
`--follow-symlinks`, `-n/--dry-run`, `--fail-fast`, `--json`, `-q/--quiet`,
`--elevate` (re-exec via sudo).

Rules worth knowing:

- A directory needs `-R` (or `--dir-only`); `afl lock docs` alone is refused with a hint.
- `-R` targets regular files only. Add `--include-dirs` to lock directory inodes too, which also blocks creating new files inside.
- Symlinks are skipped unless `--follow-symlinks`.
- Every change is re-read and verified; a filesystem that silently ignores the flag is reported as a failure.
- Already-locked / already-unlocked targets are no-ops (exit 0).
- If every requested entry had to be skipped (for example a protected file was replaced by a symlink), `lock` exits 1 and `check` reports drift instead of a hollow success.
- `unlock` after a `user`-level lock restores `u+w` only; group/other write bits removed by the lock are not restored (afl keeps no record of the original mode).
- All mutations go through a file descriptor opened with `O_NOFOLLOW`, so the final path component cannot be swapped for a symlink between inspection and change. Parent directories are still resolved by name; keep protected trees inside directories the agent cannot rename.

Exit codes: `0` ok · `1` partial failure or `check` drift · `2` usage · `3` insufficient privileges · `4` unsupported filesystem.

## Config file

`afl.yaml` (or `afl.json` with the same schema). Relative paths resolve against the file's directory.

```yaml
version: 1
level: strong
exclude:
  - "**/*.tmp"
paths:
  - docs/POLICY.md
  - path: docs/specs
    recursive: true
    include_dirs: false
  - path: README.md
    level: user
```

The YAML parser is a deliberate subset (mappings, sequences, quoted/plain scalars,
comments). Anchors, flow style (`{}`/`[]`), block scalars (`|`/`>`) and tags are
rejected with a line number — use JSON if you need them. See
[`afl.yaml.example`](afl.yaml.example).

## Working with git

Locked files that git tracks make `git pull` / `checkout` fail when upstream
changes them. That is the point. To update:

```sh
sudo afl run -f afl.yaml -- git pull
```

`afl run` unlocks the protected set, runs the command, and re-locks afterwards
regardless of how the command ends (its exit code is passed through; a failed
re-lock turns it into exit 1 with a loud warning). Under `sudo` the command is
run as the invoking user (`SUDO_UID`/`SUDO_GID`), so `git pull` or an editor does
not create root-owned files; pass `--as-root` to keep root. The manual form still
works if you prefer it:

```sh
sudo afl unlock -f afl.yaml && git pull && sudo afl lock -f afl.yaml
```

Pre-commit / CI: `afl check -f afl.yaml` needs no root and exits 1 on drift.
Pair the OS lock with your agent's own deny rules; the flag is the last line of defence, not the first.

## Platform notes

**Linux** — needs `CAP_LINUX_IMMUTABLE` (root has it; Docker's default cap set
does **not**: `docker run --cap-add LINUX_IMMUTABLE`). Supported on ext2/3/4,
xfs, btrfs, f2fs, jfs. Not on NFS, SMB, FAT/exFAT, overlayfs, FUSE, or 9p.
`afl doctor` reads `/proc/self/mountinfo` and tells you before anything is touched.

**macOS** — `strong` = `schg`, root required to set and clear. `user` adds `uchg`.

**WSL** — WSL2's own filesystem (ext4, e.g. under `~`) works like Linux.
`/mnt/c` and other DrvFs mounts are 9p and cannot hold the flag; `afl` exits 4
and tells you to move the files into the Linux filesystem (or fall back to
`--level user`, which on DrvFs only works with `metadata` enabled in `/etc/wsl.conf`).

**Windows native** is out of scope.

## Shell completion

```sh
# bash (3.2+ compatible)
afl completion bash > ~/.local/share/bash-completion/completions/afl
# zsh
afl completion zsh > "${fpath[1]}/_afl"        # e.g. /usr/local/share/zsh/site-functions/_afl
# fish
afl completion fish > ~/.config/fish/completions/afl.fish
```

Homebrew: `$(brew --prefix)/etc/bash_completion.d/afl`, `$(brew --prefix)/share/zsh/site-functions/_afl`.

## Development

```sh
make test          # unit tests, no privileges needed (user-level round trip runs for real)
make test-root     # sudo: real schg / chattr +i round trip on this host
make test-linux    # Docker with --cap-add LINUX_IMMUTABLE
make vet           # cross-OS/arch vet (linux amd64/arm64/386, freebsd, openbsd)
```

Note: the `afl` prefix is shared with the AFL fuzzer (`afl-fuzz`, `afl-gcc`).
They do not conflict, but `afl<TAB>` will list both if you have it installed.

## License

MIT
