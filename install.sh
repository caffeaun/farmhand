#!/bin/sh
# FarmHand installer bootstrap.
#
# Detects this host's OS/arch, downloads the matching binary from the latest
# (or pinned) GitHub release, places it at /usr/local/bin/farmhand, and execs
# `farmhand setup` to finish the configuration.
#
# Usage:
#   curl -fL https://github.com/caffeaun/farmhand/releases/latest/download/install.sh | sh
#
#   # or pin a version:
#   FARMHAND_VERSION=v0.6.3 curl -fL .../install.sh | sh
#
#   # any flags after `--` are passed through to `farmhand setup`:
#   curl -fL .../install.sh | sh -s -- --yes --port 8080
set -eu

REPO="caffeaun/farmhand"
BIN_DEST="/usr/local/bin/farmhand"
VERSION="${FARMHAND_VERSION:-latest}"

# OS detection
case "$(uname -s)" in
  Linux)  OS="linux"  ;;
  Darwin) OS="darwin" ;;
  *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

# Arch detection
case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

ASSET="farmhand-${OS}-${ARCH}"

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

# Need a tmpdir we can write to; mktemp -d portable across BSD and GNU.
TMPDIR_LOCAL="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_LOCAL"' EXIT

echo "downloading $URL"
if command -v curl >/dev/null 2>&1; then
  curl -fL --progress-bar -o "${TMPDIR_LOCAL}/farmhand" "$URL"
elif command -v wget >/dev/null 2>&1; then
  wget --show-progress -O "${TMPDIR_LOCAL}/farmhand" "$URL"
else
  echo "need curl or wget to download release asset" >&2
  exit 1
fi

chmod +x "${TMPDIR_LOCAL}/farmhand"

# Quick sanity check
"${TMPDIR_LOCAL}/farmhand" --version

# Install into PATH
if [ "$(id -u)" -eq 0 ]; then
  mv "${TMPDIR_LOCAL}/farmhand" "$BIN_DEST"
else
  echo "installing $BIN_DEST (requires sudo)"
  sudo mv "${TMPDIR_LOCAL}/farmhand" "$BIN_DEST"
fi
echo "installed $BIN_DEST"

# Configure: hand off to `farmhand setup`. Pass through any extra args.
#
# When this script is invoked via `curl ... | sh`, stdin is the curl pipe —
# every prompt would read EOF and silently accept defaults. Redirect stdin
# from /dev/tty so the user actually sees and answers the prompts. If
# /dev/tty is unavailable (CI / Docker without a TTY), let stdin pass
# through and trust the caller to pass --yes.
TTY_REDIRECT=""
if [ -r /dev/tty ]; then
  TTY_REDIRECT="< /dev/tty"
fi

echo "running farmhand setup $*"
if [ "$(id -u)" -eq 0 ]; then
  eval exec "$BIN_DEST" setup "$@" "$TTY_REDIRECT"
else
  eval exec sudo "$BIN_DEST" setup "$@" "$TTY_REDIRECT"
fi
