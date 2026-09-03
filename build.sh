#!/usr/bin/env bash
set -euo pipefail

# Build claude-window-anchor as a native cgo shared library.
#
# cgo -buildmode=c-shared needs a C toolchain for the target platform.
# CI (release.yml) builds the full linux/windows matrix on native host
# runners; this script covers local development on the current platform.
#
# Usage:
#   ./build.sh                  # build for the current platform (e.g. windows/amd64 with mingw gcc)
#   ./build.sh package          # zip dist artifacts into plugin-store packages + checksums.txt
#   CGO_CC=<cc> ./build.sh      # pick the C compiler explicitly

VERSION="${VERSION:-0.2.2}"
PLUGIN_ID="claude-window-anchor"
OUT="${OUT:-dist}"
MODE="${1:-build}"

# C toolchain: honor CGO_CC, else fall back to the mingw gcc that ships with
# Git for Windows' MSYS2, else plain gcc.
find_cc() {
  if [ -n "${CGO_CC:-}" ]; then
    echo "$CGO_CC"
  elif command -v gcc >/dev/null 2>&1; then
    echo gcc
  elif [ -x /d/mingw64/bin/gcc ]; then
    echo /d/mingw64/bin/gcc
  else
    echo "no C compiler found (set CGO_CC or install gcc)" >&2
    exit 1
  fi
}

if [ "$MODE" = "package" ]; then
  if [ ! -d "$OUT" ]; then
    echo "error: $OUT missing; run ./build.sh first" >&2
    exit 1
  fi
  if command -v python3 >/dev/null 2>&1 && python3 -c "import zipfile" >/dev/null 2>&1; then
    PY=python3
  elif command -v python >/dev/null 2>&1 && python -c "import zipfile" >/dev/null 2>&1; then
    PY=python
  else
    echo "python/python3 with zipfile support not found (needed for 'package')" >&2
    exit 1
  fi
  "$PY" .github/scripts/package-release.py \
    --plugin-id "$PLUGIN_ID" --version "$VERSION" --dist "$OUT"
  exit 0
fi

GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"
case "$GOOS" in
  windows) EXT=".dll" ;;
  darwin) EXT=".dylib" ;;
  *) EXT=".so" ;;
esac

CC="$(find_cc)"
OUT_DIR="$OUT/$GOOS/$GOARCH"
mkdir -p "$OUT_DIR"
echo ">> building ${GOOS}/${GOARCH} (CC=$CC)"
CGO_ENABLED=1 GOOS="$GOOS" GOARCH="$GOARCH" CC="$CC" \
  go build -buildmode=c-shared -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.pluginVersion=$VERSION" \
    -o "$OUT_DIR/$PLUGIN_ID$EXT" .
rm -f "$OUT_DIR/$PLUGIN_ID.h"

echo "Done. Artifact: $OUT_DIR/$PLUGIN_ID$EXT"
