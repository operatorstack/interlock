#!/bin/sh
# Interlock installer (macOS / Linux) — no Go toolchain required.
#
#   curl -fsSL https://raw.githubusercontent.com/operatorstack/interlock/main/install.sh | sh
#
# It detects your OS/arch, downloads a pinned prebuilt release, verifies the
# SHA-256 checksum, installs the `interlock` binary, then runs `interlock doctor`
# and the repository-policy demo. It fails closed on a checksum mismatch.
#
# Environment overrides:
#   INTERLOCK_VERSION      release tag to install (default: latest)
#   INTERLOCK_INSTALL_DIR  install directory (default: /usr/local/bin, else ~/.local/bin)
set -eu

REPO="operatorstack/interlock"
BINARY="interlock"

info() { printf 'interlock-install: %s\n' "$1" >&2; }
die() { printf 'interlock-install: error: %s\n' "$1" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "required tool not found: $1"; }

# --- detect platform ------------------------------------------------------
os=$(uname -s)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS: $os (Windows: use install.ps1)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $arch" ;;
esac

need uname
need tar
if command -v curl >/dev/null 2>&1; then
  dl() { curl -fsSL "$1" -o "$2"; }
  fetch() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  dl() { wget -qO "$2" "$1"; }
  fetch() { wget -qO - "$1"; }
else
  die "need curl or wget"
fi

# sha256 tool differs across platforms.
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  die "need sha256sum or shasum"
fi

# --- resolve version ------------------------------------------------------
version="${INTERLOCK_VERSION:-}"
if [ -z "$version" ]; then
  info "resolving latest release"
  version=$(fetch "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name":' | head -n1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
  [ -n "$version" ] || die "could not resolve latest release tag"
fi
info "installing ${BINARY} ${version} (${os}/${arch})"

archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/download/${version}"

# --- download + verify ----------------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

dl "${base}/${archive}" "${tmp}/${archive}" || die "downloading ${archive}"
dl "${base}/checksums.txt" "${tmp}/checksums.txt" || die "downloading checksums.txt"

want=$(grep " ${archive}\$" "${tmp}/checksums.txt" | awk '{print $1}' | head -n1)
[ -n "$want" ] || die "no checksum for ${archive} in checksums.txt"
got=$(sha256 "${tmp}/${archive}")
if [ "$want" != "$got" ]; then
  die "checksum mismatch for ${archive} (want ${want}, got ${got})"
fi
info "checksum verified"

tar -xzf "${tmp}/${archive}" -C "$tmp"
[ -f "${tmp}/${BINARY}" ] || die "archive did not contain ${BINARY}"
chmod +x "${tmp}/${BINARY}"

# --- install --------------------------------------------------------------
dir="${INTERLOCK_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ] 2>/dev/null || { [ "$(id -u)" = 0 ] && [ -d /usr/local/bin ]; }; then
    dir=/usr/local/bin
  else
    dir="${HOME}/.local/bin"
  fi
fi
mkdir -p "$dir"

if [ -w "$dir" ]; then
  mv "${tmp}/${BINARY}" "${dir}/${BINARY}"
elif command -v sudo >/dev/null 2>&1; then
  info "elevating with sudo to write ${dir}"
  sudo mv "${tmp}/${BINARY}" "${dir}/${BINARY}"
else
  die "cannot write ${dir}; set INTERLOCK_INSTALL_DIR to a writable path"
fi
info "installed ${dir}/${BINARY}"

case ":${PATH}:" in
  *":${dir}:"*) ;;
  *) info "note: ${dir} is not on your PATH; add it to use \`${BINARY}\` directly" ;;
esac

# --- prove it works -------------------------------------------------------
bin="${dir}/${BINARY}"
echo >&2
"$bin" doctor
echo >&2
"$bin" demo repository-policy
echo >&2
info "done — author your own policy with \`${BINARY} init\`"
