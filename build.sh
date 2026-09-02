#!/usr/bin/env bash
set -euo pipefail

# Cross-compile the claude-window-anchor plugin using zig cc.
# cgo -buildmode=c-shared needs a target C toolchain; zig is a single
# self-contained download that works from Windows/macOS/Linux.
#
# Usage:
#   ./build.sh                 # linux amd64 + arm64 + windows amd64 (glibc/mingw)
#   ./build.sh musl            # also build musl (Alpine) variants
#   ./build.sh package         # zip dist artifacts into plugin-store packages + checksums.txt
#   ZIG=${ZIG:-zig} ./build.sh # custom zig binary

VERSION="${VERSION:-0.1.0}"
PLUGIN_ID="claude-window-anchor"
OUT="${OUT:-dist}"
ZIG="${ZIG:-zig}"
MODE="${1:-build}"

find_python() {
  if command -v python >/dev/null 2>&1; then
    echo python
  elif command -v python3 >/dev/null 2>&1; then
    echo python3
  else
    echo "python/python3 not found (needed for 'package')" >&2
    exit 1
  fi
}

if [ "$MODE" = "package" ]; then
  PY="${PY:-$(find_python)}"
  "$PY" - "$PLUGIN_ID" "$VERSION" "$OUT" <<'PYEOF'
import hashlib, os, sys, zipfile

plugin_id, version, out = sys.argv[1], sys.argv[2], sys.argv[3]
targets = [
    ("linux", "amd64", ".so"),
    ("linux", "arm64", ".so"),
    ("windows", "amd64", ".dll"),
]
manifest = []
first_error = None
for goos, goarch, ext in targets:
    lib = os.path.join(out, goos, goarch, plugin_id + ext)
    if not os.path.isfile(lib):
        print(f"error: missing {lib}", file=sys.stderr)
        first_error = True
        continue
    zip_name = f"{plugin_id}_{version}_{goos}_{goarch}.zip"
    with zipfile.ZipFile(zip_name, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.write(lib, arcname=plugin_id + ext)
    digest = hashlib.sha256(open(zip_name, "rb").read()).hexdigest()
    size = os.path.getsize(zip_name)
    manifest.append(f"{digest}  {zip_name}")
    print(f"packed {zip_name} ({size} bytes)")
if first_error:
    sys.exit(1)
with open("checksums.txt", "w", encoding="utf-8") as f:
    f.write("\n".join(manifest) + "\n")
print("wrote checksums.txt")
PYEOF
  exit 0
fi

mkdir -p "$OUT"

build_one() {
  local goos=$1 goarch=$2 zigtarget=$3 ext=$4
  local dir="$OUT/$goos/$goarch"
  mkdir -p "$dir"
  echo ">> building ${goos}/${goarch} (${zigtarget})"
  CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" \
    CC="$ZIG cc -target $zigtarget" \
    CXX="$ZIG c++ -target $zigtarget" \
    go build -buildmode=c-shared -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.pluginVersion=$VERSION" \
      -o "$dir/$PLUGIN_ID$ext" .
  rm -f "$dir/$PLUGIN_ID.h"
}

build_one linux amd64   x86_64-linux-gnu.2.31  .so
build_one linux arm64   aarch64-linux-gnu.2.31 .so
build_one windows amd64 x86_64-windows-gnu    .dll

if [ "$MODE" = "musl" ]; then
  build_one linux amd64 x86_64-linux-musl  .so
  build_one linux arm64 aarch64-linux-musl .so
fi

echo "Done. Artifacts under $OUT:"
find "$OUT" -name "$PLUGIN_ID*" -type f | sort
