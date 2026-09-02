#!/usr/bin/env python3
"""Package a built plugin library into a plugin-store release zip.

Mode 1 (CI): package one library into a single zip.
  python package-release.py --library dist/linux/amd64/claude-window-anchor.so \
      --archive claude-window-anchor_0.1.0_linux_amd64.zip

Mode 2 (local): scan <dist>/<goos>/<goarch>/ for all platform outputs and zip
  every one that exists (missing platforms are skipped with a warning).
  python package-release.py --plugin-id claude-window-anchor \
      --version 0.1.0 --dist dist
"""
import argparse
import hashlib
import os
import sys
import zipfile

TARGETS = [
    ("linux", "amd64", ".so"),
    ("linux", "arm64", ".so"),
    ("windows", "amd64", ".dll"),
]


def write_zip(library: str, zip_name: str) -> int:
    with zipfile.ZipFile(zip_name, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.write(library, arcname=os.path.basename(library))
    digest = hashlib.sha256(open(zip_name, "rb").read()).hexdigest()
    print(f"packed {zip_name} ({os.path.getsize(zip_name)} bytes) sha256={digest}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--library", help="single library file to package")
    parser.add_argument("--archive", help="output zip name (with --library)")
    parser.add_argument("--plugin-id", help="plugin id (with --dist)")
    parser.add_argument("--version", help="version (with --dist)")
    parser.add_argument("--dist", help="directory with <goos>/<goarch>/ layout (with --plugin-id)")
    args = parser.parse_args()

    if args.library:
        if not args.archive:
            print("error: --library requires --archive", file=sys.stderr)
            return 1
        if not os.path.isfile(args.library):
            print(f"error: missing {args.library}", file=sys.stderr)
            return 1
        return write_zip(args.library, args.archive)

    if args.plugin_id and args.version and args.dist:
        any_packed = False
        for goos, goarch, ext in TARGETS:
            lib = os.path.join(args.dist, goos, goarch, args.plugin_id + ext)
            if not os.path.isfile(lib):
                print(f"warning: missing {lib} (skipped)", file=sys.stderr)
                continue
            zip_name = f"{args.plugin_id}_{args.version}_{goos}_{goarch}.zip"
            write_zip(lib, zip_name)
            any_packed = True
        if not any_packed:
            print(f"error: no plugin libraries found under {args.dist}", file=sys.stderr)
            return 1
        return 0

    parser.print_help()
    return 1


if __name__ == "__main__":
    sys.exit(main())
