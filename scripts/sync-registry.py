#!/usr/bin/env python3
"""Sync registry.json artifact hashes to a plugin's published GitHub Release.

Usage:
    python3 scripts/sync-registry.py <plugin-id> <release-tag>
    # e.g.
    python3 scripts/sync-registry.py opencode-go opencode-go-v0.1.0

For every release asset named "<plugin>-<goos>-<goarch>.<ext>", it downloads the
file, recomputes sha256 + size, and rewrites the matching artifact's sha256/size
in registry.json **in place** (surgical — existing formatting is preserved).

Requires the `gh` CLI (authenticated) for listing release assets. Run this after
a version bump once CI has uploaded the new artifacts, then review + commit the
registry.json diff. Assets are treated as immutable per version, so re-running on
an unchanged release is a no-op.
"""
import hashlib
import re
import subprocess
import sys
import urllib.request

REPO = "pragmaticgrowth/cpa-plugins"
REGISTRY = "registry.json"


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__)
        return 2
    plugin, tag = sys.argv[1], sys.argv[2]

    names = subprocess.check_output(
        ["gh", "release", "view", tag, "-R", REPO, "--json", "assets",
         "--jq", ".assets[].name"]
    ).decode().split()

    text = open(REGISTRY).read()
    changed = 0
    for name in names:
        if not name.startswith(plugin + "-"):
            continue
        url = f"https://github.com/{REPO}/releases/download/{tag}/{name}"
        data = urllib.request.urlopen(url).read()  # noqa: S310 (trusted host)
        sha, size = hashlib.sha256(data).hexdigest(), len(data)
        pat = re.compile(
            r'("url": "' + re.escape(url) + r'",\s*\n\s*"sha256": ")[0-9a-f]{64}'
            r'(",\s*\n\s*"size": )\d+'
        )
        text, n = pat.subn(lambda m: m.group(1) + sha + m.group(2) + str(size), text)
        changed += n
        status = "updated" if n else "URL NOT IN registry.json (add the artifact block manually)"
        print(f"  {name}: {status}  sha256={sha[:12]}… size={size}")

    open(REGISTRY, "w").write(text)
    print(f"done — {changed} artifact(s) synced for {plugin} @ {tag}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
