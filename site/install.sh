#!/bin/sh
# ccbs installer — Claude Code better status
#   curl -fsSL https://heyjeay.github.io/ccbs/install.sh | sh
#
# Downloads the latest release for your OS/CPU from GitHub, checks its
# SHA-256 against the release's checksums.txt, and installs it to
# ~/.local/bin (override with CCBS_INSTALL_DIR). No sudo needed.
set -eu

REPO="${CCBS_REPO:-heyjeay/ccbs}"
DIR="${CCBS_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${CCBS_VERSION:-latest}"

say() { printf '\033[1;32m›\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mccbs:\033[0m %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS $(uname -s) — on Windows use: irm https://heyjeay.github.io/ccbs/install.ps1 | iex" ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported CPU $(uname -m)" ;;
esac

if [ "$VERSION" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi
base="${CCBS_BASE_URL:-$base}"   # for mirrors / testing
file="ccbs_${os}_${arch}.tar.gz"

fetch() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -q "$1" -O "$2"
  else die "need curl or wget"; fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

say "downloading $file"
fetch "$base/$file" "$tmp/$file" || die "download failed: $base/$file"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "couldn't fetch checksums.txt"

want="$(grep " $file\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
[ -n "$want" ] || die "no checksum listed for $file"
if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/$file" | cut -d' ' -f1)"
else got="$(shasum -a 256 "$tmp/$file" | cut -d' ' -f1)"; fi
[ "$want" = "$got" ] || die "checksum mismatch for $file"
say "checksum ok"

tar -xzf "$tmp/$file" -C "$tmp"
mkdir -p "$DIR"
install -m 0755 "$tmp/ccbs" "$DIR/ccbs" 2>/dev/null || { cp "$tmp/ccbs" "$DIR/ccbs"; chmod 0755 "$DIR/ccbs"; }
say "installed $("$DIR/ccbs" --version) → $DIR/ccbs"

case ":$PATH:" in
  *":$DIR:"*) say "run: ccbs" ;;
  *) say "add $DIR to your PATH, e.g.:"
     printf '    echo '\''export PATH="%s:$PATH"'\'' >> ~/.%src && exec $SHELL\n' "$DIR" "$(basename "${SHELL:-sh}")" ;;
esac
