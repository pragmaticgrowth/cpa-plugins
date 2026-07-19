#!/usr/bin/env bash
# Refresh the vendored CLIProxyAPI ground-truth in references/upstream/ from upstream.
#
# Usage:
#   scripts/refresh-upstream.sh [REF]
#   CPA_UPSTREAM_REF=v7.2.90 scripts/refresh-upstream.sh
#
# REF defaults to the currently pinned tag below. Re-run, then review the git diff
# and update the skills/references if the ABI or capability set changed.
set -euo pipefail

PINNED_REF="v7.2.88"
REF="${1:-${CPA_UPSTREAM_REF:-$PINNED_REF}}"

CODE_REPO="https://github.com/router-for-me/CLIProxyAPI.git"
DOCS_REPO="https://github.com/router-for-me/CLIProxyAPIDocs.git"

# Resolve paths relative to this script (references/upstream is a sibling of scripts/).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEST="$PLUGIN_ROOT/references/upstream"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "==> cloning CLIProxyAPI @ $REF"
git clone --depth 1 --branch "$REF" "$CODE_REPO" "$WORK/code" 2>/dev/null \
  || git clone "$CODE_REPO" "$WORK/code"  # fall back to default branch if REF isn't a branch/tag
git -C "$WORK/code" checkout "$REF" 2>/dev/null || true
echo "==> cloning CLIProxyAPIDocs (default branch)"
git clone --depth 1 "$DOCS_REPO" "$WORK/docs"

CODE_SHA="$(git -C "$WORK/code" rev-parse HEAD)"
CODE_TAG="$(git -C "$WORK/code" describe --tags --always 2>/dev/null || echo "$REF")"
DOCS_SHA="$(git -C "$WORK/docs" rev-parse HEAD)"

echo "==> copying curated artifacts -> references/upstream/"
mkdir -p "$DEST/examples" "$DEST/docs-plugin"
cp "$WORK/code/sdk/pluginapi/types.go" "$DEST/pluginapi-types.go"
cp "$WORK/code/sdk/pluginabi/types.go" "$DEST/pluginabi-types.go"

# Vendor Go for every example plugin, plus C/Rust for `simple` (the canonical minimal one).
rm -rf "$DEST/examples"; mkdir -p "$DEST/examples"
for dir in "$WORK/code/examples/plugin"/*/; do
  ex="$(basename "$dir")"
  [ -d "$dir/go" ] || continue
  mkdir -p "$DEST/examples/$ex"
  cp -R "$dir/go" "$DEST/examples/$ex/"
  [ -f "$dir/README.md" ] && cp "$dir/README.md" "$DEST/examples/$ex/"
done
for lang in c rust; do
  [ -d "$WORK/code/examples/plugin/simple/$lang" ] && cp -R "$WORK/code/examples/plugin/simple/$lang" "$DEST/examples/simple/"
done

rm -f "$DEST/docs-plugin"/*.md
cp "$WORK/docs/docs/en/plugin/"*.md "$DEST/docs-plugin/"

cat > "$DEST/VERSION.txt" <<EOF
CLIProxyAPI upstream — pinned reference snapshot
================================================
Repo:        $CODE_REPO
Tag:         $CODE_TAG
Commit:      $CODE_SHA
Docs repo:   $DOCS_REPO
Docs commit: $DOCS_SHA
Snapshot:    $(date -u +%Y-%m-%d)

Vendored files (curated ground-truth; regenerate with scripts/refresh-upstream.sh):
  pluginapi-types.go            <- sdk/pluginapi/types.go
  pluginabi-types.go            <- sdk/pluginabi/types.go
  examples/*/go                 <- examples/plugin/*/go (all capability examples)
  examples/simple/{c,rust}      <- examples/plugin/simple/{c,rust}
  docs-plugin/*.md              <- CLIProxyAPIDocs docs/en/plugin/*.md
EOF

echo "==> done. Pinned to $CODE_TAG ($CODE_SHA)."
echo "    Review: git -C \"$PLUGIN_ROOT\" diff -- references/upstream"
echo "    If the ABI/capabilities changed, update the skills and templates/go-plugin/go.mod.tmpl (require version)."
