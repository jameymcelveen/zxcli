#!/bin/sh
# Installs zx on macOS or Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/jameymcelveen/zxcli/main/scripts/install.sh | sh
#
# Options (environment variables):
#   ZX_VERSION      a release tag such as v0.1.0 (default: the latest release)
#   ZX_INSTALL_DIR  where to put the binary (default: /usr/local/bin if you can
#                   write there, otherwise ~/.local/bin)
set -eu

REPO="jameymcelveen/zxcli"
VERSION="${ZX_VERSION:-latest}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux | darwin) ;;
  *) echo "zx: this script covers macOS and Linux. On Windows use install.ps1." >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "zx: no build for CPU type $arch" >&2; exit 1 ;;
esac

asset="zx_${os}_${arch}.tar.gz"
if [ "$VERSION" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading $asset ($VERSION)"
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

want=$(awk -v f="$asset" '$2 == f {print $1}' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
  got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "zx: checksum mismatch for $asset, so nothing was installed" >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"

dir="${ZX_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"
install -m 755 "$tmp/zx" "$dir/zx"

echo "installed $("$dir/zx" version)"
echo "location  $dir/zx"
case ":$PATH:" in
  *":$dir:"*) echo "run: zx" ;;
  *) echo "note: $dir is not on your PATH yet. Add it, then run: zx" ;;
esac
