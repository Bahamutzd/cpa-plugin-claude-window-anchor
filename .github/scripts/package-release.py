#!/usr/bin/env python3
"""Package built plugin libraries into plugin-store release zips + checksums.txt.

Reads libraries from <dist>/<goos>/<goarch>/<plugin-id>.<ext> (the layout
produced by build.sh and by the GitHub Actions build matrix), writes one zip
per platform at the archive root plus a checksums.txt in sha256sum format.

Usage:
  python package-release.py --plugin-id claude-window-anchor --version 0.1.0 --dist dist
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


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plugin-id", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--dist", required=True, help="directory with <goos>/<goarch>/ layout")
    args = parser.parse_args()

    missing = False
    entries = []
    for goos, goarch, ext in TARGETS:
        lib = os.path.join(args.dist, goos, goarch, args.plugin_id + ext)
        if not os.path.isfile(lib):
            print(f"error: missing {lib}", file=sys.stderr)
            missing = True
            continue
        zip_name = f"{args.plugin_id}_{args.version}_{goos}_{goarch}.zip"
        with zipfile.ZipFile(zip_name, "w", zipfile.ZIP_DEFLATED) as archive:
            archive.write(lib, arcname=args.plugin_id + ext)
        digest = hashlib.sha256(open(zip_name, "rb").read()).hexdigest()
        compact = f"{digest}  {zip_name}"
        entries.append(compact)
        print(f"packed {zip_name} ({os.path.getsize(zip_name)} bytes)")

    if missing:
        return 1
    with open("checksums.txt", "w", encoding="utf-8") as f:
        f.write("\n".join(entries) + "\n")
    print("wrote checksums.txt")
    return 0


if __name__ == "__main__":
    sys.exit(main())
