#!/bin/sh
# Run the cache-invalidation model. Exit status is TLC's.
# A violated invariant is a non-zero status. That is a finding, not a crashed tool.
set -eu

cd "$(dirname "$0")"

VERSION=1.7.4
SHA1=bee4a54f3ee3d4afc347c3240ec2d9e93b075104
URL="https://github.com/tlaplus/tlaplus/releases/download/v${VERSION}/tla2tools.jar"

if ! command -v java >/dev/null 2>&1; then
  echo "Java 11 or newer is required to run TLC ${VERSION}." >&2
  exit 1
fi

checksum() {
  if command -v sha1sum >/dev/null 2>&1; then
    sha1sum "$1" | awk '{print $1}'
  else
    shasum -a 1 "$1" | awk '{print $1}'
  fi
}

cache_root="${XDG_CACHE_HOME:-$HOME/.cache}/gleam-tlc/${VERSION}"
jar="${cache_root}/tla2tools.jar"
mkdir -p "$cache_root"

if [ ! -f "$jar" ] || [ "$(checksum "$jar")" != "$SHA1" ]; then
  tmp="${jar}.download"
  curl -fsSL -o "$tmp" "$URL"
  actual="$(checksum "$tmp")"
  if [ "$actual" != "$SHA1" ]; then
    rm -f "$tmp"
    echo "tla2tools.jar ${VERSION} checksum ${actual} != ${SHA1}" >&2
    exit 1
  fi
  mv "$tmp" "$jar"
fi

meta="$(mktemp -d)"
trap 'rm -rf "$meta"' EXIT

java -XX:+UseParallelGC -jar "$jar" \
  -workers 1 \
  -metadir "$meta" \
  -config CacheInvalidation.cfg \
  CacheInvalidation.tla
