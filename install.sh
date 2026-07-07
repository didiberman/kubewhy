#!/usr/bin/env bash
# Installs the latest kubewhy release for your OS/arch into a directory you
# already own -- never asks for sudo/admin access.
# Usage: curl -sSL https://kubewhy.didibe.dev | bash
set -euo pipefail

REPO="didiberman/kubewhy"
INSTALL_DIR="${KUBEWHY_INSTALL_DIR:-$HOME/.local/bin}"

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

mkdir -p "$INSTALL_DIR"
mv "$tmpdir/kubewhy" "$INSTALL_DIR/kubewhy"
chmod +x "$INSTALL_DIR/kubewhy"

echo "Installed to ${INSTALL_DIR}/kubewhy"
"$INSTALL_DIR/kubewhy" --version

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "${INSTALL_DIR} isn't on your PATH yet. Add this to your shell rc file (~/.bashrc, ~/.zshrc):"
    echo ""
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    echo ""
    ;;
esac
