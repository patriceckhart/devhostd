#!/usr/bin/env bash
#
# devhostd installer for macOS and Linux.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/patriceckhart/devhostd/main/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/patriceckhart/devhostd/main/install.sh | bash -s -- v0.0.1 ~/bin
#
# Positional arguments:
#   $1  Release tag, such as v0.0.1. Defaults to latest.
#   $2  Install directory. Defaults to the first writable directory in
#       /usr/local/bin, $HOME/.local/bin, or $HOME/bin.
#
# Environment overrides:
#   DEVHOSTD_VERSION  Same as $1.
#   DEVHOSTD_PREFIX   Same as $2.
#   GITHUB_TOKEN      Token with contents:read access for a private repository.

set -euo pipefail

OWNER="patriceckhart"
REPO="devhostd"
BINARY="devhostd"

VERSION="${1:-${DEVHOSTD_VERSION:-latest}}"
PREFIX="${2:-${DEVHOSTD_PREFIX:-}}"

msg() { printf '==> %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

CURL_AUTH=()
if [ -n "${GITHUB_TOKEN:-}" ]; then
  CURL_AUTH=(-H "Authorization: Bearer $GITHUB_TOKEN")
fi

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  MINGW*|MSYS*|CYGWIN*) die "Windows detected; download the Windows release archive from GitHub" ;;
  *) die "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "${CURL_AUTH[@]+"${CURL_AUTH[@]}"}" \
    "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" \
    | sed -nE 's/.*"tag_name": *"([^"]+)".*/\1/p' | head -n1)
  [ -n "$VERSION" ] || die "could not resolve the latest release"
fi

case "$VERSION" in
  v*) ;;
  *) VERSION="v$VERSION" ;;
esac
VERSION_NUMBER="${VERSION#v}"

pick_prefix() {
  local candidates=()
  if [ -n "$PREFIX" ]; then
    printf '%s\n' "$PREFIX"
    return
  fi
  candidates+=("/usr/local/bin")
  if [ -n "${HOME:-}" ]; then
    candidates+=("$HOME/.local/bin" "$HOME/bin")
  fi
  for directory in "${candidates[@]}"; do
    if [ -d "$directory" ] && [ -w "$directory" ]; then
      printf '%s\n' "$directory"
      return
    fi
  done
  if [ -n "${HOME:-}" ]; then
    mkdir -p "$HOME/.local/bin"
    printf '%s\n' "$HOME/.local/bin"
    return
  fi
  die "no writable install directory found; pass one as the second argument"
}

PREFIX=$(pick_prefix)
mkdir -p "$PREFIX"

ARCHIVE="${BINARY}_${VERSION_NUMBER}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}"
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

msg "downloading $ARCHIVE"
curl -fsSL "${CURL_AUTH[@]+"${CURL_AUTH[@]}"}" \
  -o "$TEMP_DIR/$ARCHIVE" "$BASE_URL/$ARCHIVE" \
  || die "download failed: $BASE_URL/$ARCHIVE"

msg "downloading checksums"
curl -fsSL "${CURL_AUTH[@]+"${CURL_AUTH[@]}"}" \
  -o "$TEMP_DIR/checksums.txt" "$BASE_URL/checksums.txt" \
  || die "download failed: $BASE_URL/checksums.txt"

expected=$(grep " ${ARCHIVE}\$" "$TEMP_DIR/checksums.txt" | awk '{print $1}' || true)
[ -n "$expected" ] || die "checksums.txt does not contain $ARCHIVE"

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$TEMP_DIR/$ARCHIVE" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$TEMP_DIR/$ARCHIVE" | awk '{print $1}')
else
  die "sha256sum or shasum is required"
fi
[ "$expected" = "$actual" ] || die "checksum verification failed"

msg "extracting $ARCHIVE"
tar -xzf "$TEMP_DIR/$ARCHIVE" -C "$TEMP_DIR"
[ -f "$TEMP_DIR/$BINARY" ] || die "archive does not contain $BINARY"

msg "installing $PREFIX/$BINARY"
install -m 0755 "$TEMP_DIR/$BINARY" "$PREFIX/$BINARY" 2>/dev/null \
  || { cp "$TEMP_DIR/$BINARY" "$PREFIX/$BINARY" && chmod 0755 "$PREFIX/$BINARY"; }

case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *)
    warn "$PREFIX is not on PATH"
    warn "add this to your shell configuration: export PATH=\"$PREFIX:\$PATH\""
    ;;
esac

msg "installed $($PREFIX/$BINARY version)"
msg "run: devhostd help"
