#!/usr/bin/env bash
# Installs the latest kubewhy release for your OS/arch into /usr/local/bin.
# Usage: curl -sSL https://kubewhy.didibe.dev | bash
set -euo pipefail

REPO="didiberman/kubewhy"
INSTALL_DIR="${KUBEWHY_INSTALL_DIR:-/usr/local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
esac

version="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')"
if [ -z "$version" ]; then
  echo "Could not determine latest kubewhy release." >&2
  exit 1
fi

archive="kubewhy_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${version}/${archive}"

echo "Installing kubewhy ${version} for ${os}/${arch}..."
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

curl -fsSL "$url" -o "$tmpdir/$archive"
tar -xzf "$tmpdir/$archive" -C "$tmpdir" kubewhy

mkdir -p "$INSTALL_DIR" 2>/dev/null || true

if [ -w "$INSTALL_DIR" ]; then
  mv "$tmpdir/kubewhy" "$INSTALL_DIR/kubewhy"
else
  sudo mv "$tmpdir/kubewhy" "$INSTALL_DIR/kubewhy"
fi

echo "Installed to ${INSTALL_DIR}/kubewhy"
"$INSTALL_DIR/kubewhy" --version
