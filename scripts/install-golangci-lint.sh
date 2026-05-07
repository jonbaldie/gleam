#!/bin/sh

set -eu

VERSION="1.64.8"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
  darwin|linux)
    ;;
  *)
    printf 'unsupported OS: %s\n' "$OS" >&2
    exit 1
    ;;
esac

case "$ARCH" in
  x86_64)
    ARCH="amd64"
    ;;
  arm64|aarch64)
    ARCH="arm64"
    ;;
  *)
    printf 'unsupported architecture: %s\n' "$ARCH" >&2
    exit 1
    ;;
esac

ROOT_DIR=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
BIN_DIR=${BIN_DIR:-"$ROOT_DIR/bin"}
TMP_DIR=$(mktemp -d)
ARCHIVE="golangci-lint-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/golangci/golangci-lint/releases/download/v${VERSION}/${ARCHIVE}"

cleanup() {
  rm -rf "$TMP_DIR"
}

trap cleanup EXIT INT TERM

mkdir -p "$BIN_DIR"
curl -sSfL "$URL" -o "$TMP_DIR/$ARCHIVE"
tar -xzf "$TMP_DIR/$ARCHIVE" -C "$TMP_DIR"
install "$TMP_DIR/golangci-lint-${VERSION}-${OS}-${ARCH}/golangci-lint" "$BIN_DIR/golangci-lint"

printf 'installed %s\n' "$BIN_DIR/golangci-lint"
