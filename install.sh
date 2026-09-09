#!/bin/sh

set -eu

REPO="agent-fox-dev/agent-fox"
# The three tools share one release, one interface and one version. Installing
# one of them and not the others leaves a shell where `fix` cannot be reached
# from the issue `issue` just filed.
TOOLS="${TOOLS:-spec issue fix}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "Error: unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

case "$OS" in
  darwin|linux) ;;
  *)
    echo "Error: unsupported platform: $OS" >&2
    exit 1
    ;;
esac

VERSION="${VERSION:-latest}"

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL -o "$1" "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$1" "$2"; }
else
  echo "Error: curl or wget is required" >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"

for tool in $TOOLS; do
  ASSET="${tool}-${OS}-${ARCH}"
  if [ "$VERSION" = "latest" ]; then
    URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
  else
    URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
  fi

  echo "Downloading ${ASSET}..."
  TMP="$(mktemp)"
  # shellcheck disable=SC2064  # TMP is expanded now on purpose
  trap "rm -f '$TMP'" EXIT

  fetch "$TMP" "$URL"
  chmod +x "$TMP"
  mv "$TMP" "${INSTALL_DIR}/${tool}"
  trap - EXIT

  echo "Installed ${tool} to ${INSTALL_DIR}/${tool}"
done
