#!/bin/sh
# Install afl (agent-file-lock) from a GitHub release tarball.
#
#   curl -fsSL https://raw.githubusercontent.com/Mineru98/agent-file-lock/main/install.sh | sh
#
# Environment:
#   AFL_VERSION   tag to install (default: the latest release, e.g. v0.1.4)
#   AFL_BIN_DIR   where to put the binary (default: /usr/local/bin)
#   AFL_NO_SUDO   set to 1 to fail instead of escalating to sudo
set -eu

REPO="Mineru98/agent-file-lock"
BIN_DIR="${AFL_BIN_DIR:-/usr/local/bin}"

die() { printf 'afl-install: %s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*" >&2; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux | darwin) ;;
  *) die "unsupported OS: $os (linux and macOS only; Windows is out of scope)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported architecture: $(uname -m) (amd64 and arm64 only)" ;;
esac

# Resolve the version by following the /releases/latest redirect rather than
# calling the API, which is rate-limited for unauthenticated callers.
version="${AFL_VERSION:-}"
if [ -z "$version" ]; then
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/$REPO/releases/latest") ||
    die "cannot reach github.com to resolve the latest release"
  version=${url##*/}
fi
case "$version" in
  v*) ;;
  *) version="v$version" ;;
esac
num=${version#v}

tarball="afl_${num}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/afl-install.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM

info "downloading $tarball ($version)"
curl -fsL -o "$tmp/$tarball" "$base/$tarball" ||
  die "no such release asset: $base/$tarball"

# Verify against the release's checksums.txt. A mismatch means a corrupted
# download or a tampered asset; either way, do not install it.
if curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"; then
  want=$(awk -v f="$tarball" '$2 == f || $2 == "*"f { print $1 }' "$tmp/checksums.txt")
  if [ -n "$want" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      got=$(sha256sum "$tmp/$tarball" | cut -d' ' -f1)
    elif command -v shasum >/dev/null 2>&1; then
      got=$(shasum -a 256 "$tmp/$tarball" | cut -d' ' -f1)
    else
      got=""
      info "note: no sha256sum or shasum found, skipping checksum verification"
    fi
    [ -z "$got" ] || [ "$got" = "$want" ] ||
      die "checksum mismatch for $tarball (expected $want, got $got)"
  else
    info "note: $tarball is not listed in checksums.txt, skipping verification"
  fi
else
  info "note: checksums.txt is not published for $version, skipping verification"
fi

tar -xzf "$tmp/$tarball" -C "$tmp" afl || die "cannot extract afl from $tarball"

# /usr/local/bin normally needs root; escalate only when it actually does.
if [ -w "$BIN_DIR" ] || { [ ! -e "$BIN_DIR" ] && [ -w "$(dirname "$BIN_DIR")" ]; }; then
  sudo=""
elif [ "${AFL_NO_SUDO:-}" = "1" ]; then
  die "$BIN_DIR is not writable (set AFL_BIN_DIR to a directory you own)"
elif command -v sudo >/dev/null 2>&1; then
  info "$BIN_DIR needs root; using sudo"
  sudo="sudo"
else
  die "$BIN_DIR is not writable and sudo is unavailable (set AFL_BIN_DIR)"
fi

$sudo mkdir -p "$BIN_DIR"
$sudo install -m 0755 "$tmp/afl" "$BIN_DIR/afl"

info "installed $BIN_DIR/afl ($("$BIN_DIR/afl" version 2>/dev/null || echo "$version"))"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) info "warning: $BIN_DIR is not on your PATH" ;;
esac

cat >&2 <<'NEXT'

Next: lock a file, then register the hook with the agent you use.

  sudo afl lock docs/POLICY.md
  afl hook install claude      # or: afl hook install codex

Without the hook the agent only sees "Operation not permitted" and does not
learn that a person decided the file must not change.
NEXT
