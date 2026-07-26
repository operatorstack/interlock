#!/bin/sh
# interlock installer — rendered by distribution/render.mjs, do not edit by hand.
#
#   curl -fsSL https://get.operatorstack.systems/interlock | sh
#
# Toolchain-free: downloads a prebuilt, checksum-verified binary from OperatorStack's
# GCP Artifact Registry, fronted by get.operatorstack.systems. All-GCP, no GitHub Releases, no npm.
#
# Env overrides:
#   INTERLOCK_VERSION       pin an exact version (e.g. v0.3.1); default: latest channel
#   INTERLOCK_INSTALL_DIR   install location; default: /usr/local/bin or ~/.local/bin
set -eu

BINARY="interlock"
GET_HOST="get.operatorstack.systems"
SMOKE_CMD="doctor"
VERSION="${INTERLOCK_VERSION:-}"
INSTALL_DIR="${INTERLOCK_INSTALL_DIR:-}"

say()  { printf '  %s\n' "$1"; }
die()  { printf 'error: %s\n' "$1" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- resolve OS/arch -----------------------------------------------------------
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in
  linux|darwin) ;;
  *) die "unsupported OS: $os (use install.ps1 on Windows)";;
esac
case "$arch" in
  x86_64|amd64) arch=amd64;;
  arm64|aarch64) arch=arm64;;
  *) die "unsupported architecture: $arch";;
esac

# --- downloader ----------------------------------------------------------------
if have curl; then dl() { curl -fsSL "$1"; }; dlf() { curl -fsSL "$1" -o "$2"; }
elif have wget; then dl() { wget -qO- "$1"; }; dlf() { wget -qO "$2" "$1"; }
else die "need curl or wget"; fi

# --- resolve version (latest channel unless pinned) ----------------------------
if [ -z "$VERSION" ]; then
  VERSION=$(dl "https://${GET_HOST}/${BINARY}/latest" | sed -n 's/.*"version"[ ]*:[ ]*"\([^"]*\)".*/\1/p')
  [ -n "$VERSION" ] || die "could not resolve latest version from https://${GET_HOST}/${BINARY}/latest"
fi
say "installing ${BINARY} ${VERSION} (${os}/${arch})"

# --- download + checksum-verify ------------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
archive="${BINARY}_${VERSION}_${os}_${arch}.tar.gz"
base="https://${GET_HOST}/${BINARY}/dl/${VERSION}"
dlf "${base}/${archive}" "${tmp}/${archive}" || die "download failed: ${base}/${archive}"
dlf "${base}/checksums.txt" "${tmp}/checksums.txt" || die "checksums download failed"

want=$(grep " ${archive}\$" "${tmp}/checksums.txt" | awk '{print $1}')
[ -n "$want" ] || die "no checksum listed for ${archive}"
if have sha256sum; then got=$(sha256sum "${tmp}/${archive}" | awk '{print $1}')
elif have shasum; then got=$(shasum -a 256 "${tmp}/${archive}" | awk '{print $1}')
else die "need sha256sum or shasum"; fi
[ "$want" = "$got" ] || die "checksum mismatch (expected $want, got $got) — refusing to install"
say "checksum verified"

# --- extract + install ---------------------------------------------------------
tar -xzf "${tmp}/${archive}" -C "$tmp" || die "extract failed"
[ -f "${tmp}/${BINARY}" ] || die "binary ${BINARY} not found in archive"
chmod +x "${tmp}/${BINARY}"

if [ -z "$INSTALL_DIR" ]; then
  if [ -w /usr/local/bin ] 2>/dev/null; then INSTALL_DIR=/usr/local/bin
  elif have sudo; then INSTALL_DIR=/usr/local/bin; SUDO=sudo
  else INSTALL_DIR="${HOME}/.local/bin"; mkdir -p "$INSTALL_DIR"; fi
fi
${SUDO:-} mv "${tmp}/${BINARY}" "${INSTALL_DIR}/${BINARY}" || die "install to ${INSTALL_DIR} failed"
say "installed to ${INSTALL_DIR}/${BINARY}"

case ":$PATH:" in *":${INSTALL_DIR}:"*) ;; *) say "note: add ${INSTALL_DIR} to your PATH";; esac

# --- smoke test ----------------------------------------------------------------
if [ -n "$SMOKE_CMD" ] && have "${INSTALL_DIR}/${BINARY}"; then
  say "running: ${BINARY} ${SMOKE_CMD}"
  "${INSTALL_DIR}/${BINARY}" ${SMOKE_CMD} || say "(${BINARY} ${SMOKE_CMD} reported issues — see above)"
fi
say "done. run '${BINARY} --help' to get started."
