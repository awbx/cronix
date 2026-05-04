#!/usr/bin/env sh
# Install the cronix CLI from a GitHub release.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/awbx/cronix/main/install.sh | sh
#
# Pin a version or change the install directory:
#   curl -fsSL .../install.sh | CRONIX_VERSION=v0.4.0 INSTALL_DIR=/usr/local/bin sh
set -eu

REPO="awbx/cronix"
BIN="cronix"
VERSION="${CRONIX_VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

err() { printf 'cronix install: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || err "missing required command: $1"; }

need uname
need curl
need tar
need mktemp

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux|darwin) ;;
  *) err "unsupported OS: $os (only linux and darwin are supported)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) err "unsupported architecture: $arch" ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' \
    | head -n1)"
  [ -n "$VERSION" ] || err "could not resolve latest version (is ${REPO} public?)"
fi

VER_NO_V="${VERSION#v}"
ARCHIVE="${BIN}_${VER_NO_V}_${os}_${arch}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}"

tmp="$(mktemp -d 2>/dev/null || mktemp -d -t cronix)"
trap 'rm -rf "$tmp"' EXIT

printf 'Downloading %s %s/%s...\n' "$VERSION" "$os" "$arch"
curl -fsSL "${URL}/${ARCHIVE}"     -o "${tmp}/${ARCHIVE}"     || err "failed to download ${ARCHIVE}"
curl -fsSL "${URL}/checksums.txt" -o "${tmp}/checksums.txt" || err "failed to download checksums.txt"

expected="$(grep "  ${ARCHIVE}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
[ -n "$expected" ] || err "no checksum entry for ${ARCHIVE}"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmp}/${ARCHIVE}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${tmp}/${ARCHIVE}" | awk '{print $1}')"
else
  err "neither sha256sum nor shasum is installed"
fi
[ "$expected" = "$actual" ] || err "checksum mismatch (expected $expected, got $actual)"

tar -xzf "${tmp}/${ARCHIVE}" -C "$tmp"
[ -f "${tmp}/${BIN}" ] || err "binary ${BIN} not found in archive"

mkdir -p "$INSTALL_DIR" || err "cannot create ${INSTALL_DIR}"
mv "${tmp}/${BIN}" "${INSTALL_DIR}/${BIN}" || err "cannot write to ${INSTALL_DIR} (try sudo or set INSTALL_DIR)"
chmod +x "${INSTALL_DIR}/${BIN}"

printf 'Installed %s %s to %s\n' "$BIN" "$VERSION" "${INSTALL_DIR}/${BIN}"

case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *) printf '\nNote: %s is not on your PATH. Add it via:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" "$INSTALL_DIR" ;;
esac
