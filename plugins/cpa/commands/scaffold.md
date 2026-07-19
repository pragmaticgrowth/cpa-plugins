---
description: Scaffold a new CLIProxyAPI native Go plugin project from the built-in template
argument-hint: <plugin-id> [--caps cap1,cap2] [--dir <path>]
---

Scaffold a new CLIProxyAPI native plugin project.

Arguments: `$ARGUMENTS`
- First token = `<plugin-id>` (kebab-case; becomes the config key, filename, and `Metadata.Name`).
- Optional `--caps a,b,c` = capabilities to advertise (e.g. `request-normalizer,model-router,executor`). Default: `request-normalizer` (a safe no-op passthrough).
- Optional `--dir <path>` = target directory (default: `./<plugin-id>`).

Steps:
1. Parse `<plugin-id>`, `--caps`, and `--dir` from the arguments. If no plugin-id was given, ask for one and stop.
2. Copy the template into the target dir and rename the `.tmpl` files:
   ```bash
   ID="<plugin-id>"; DIR="<dir or ./$ID>"; MOD="github.com/<owner>/$ID"
   mkdir -p "$DIR"
   cp "${CLAUDE_PLUGIN_ROOT}/templates/go-plugin/main.go.tmpl"  "$DIR/main.go"
   cp "${CLAUDE_PLUGIN_ROOT}/templates/go-plugin/go.mod.tmpl"   "$DIR/go.mod"
   cp "${CLAUDE_PLUGIN_ROOT}/templates/go-plugin/Makefile"      "$DIR/Makefile"
   cp "${CLAUDE_PLUGIN_ROOT}/templates/go-plugin/config.snippet.yaml" "$DIR/config.snippet.yaml"
   cp "${CLAUDE_PLUGIN_ROOT}/templates/go-plugin/README.md"     "$DIR/README.md"
   cp "${CLAUDE_PLUGIN_ROOT}/templates/go-plugin/.gitignore"    "$DIR/.gitignore"
   ```
3. Substitute placeholders (ask the user for author/repo if unknown, or infer from `gh`):
   ```bash
   sed -i '' -e "s|__PLUGIN_ID__|$ID|g" -e "s|__AUTHOR__|<author>|g" -e "s|__REPO_URL__|<repo url>|g" "$DIR/main.go" "$DIR/Makefile" "$DIR/config.snippet.yaml" "$DIR/README.md"
   sed -i '' -e "s|__MODULE_PATH__|$MOD|g" "$DIR/go.mod"
   ```
   (On Linux use `sed -i` without the `''`.)
4. If `--caps` were requested beyond the default, use the **cpa-capabilities** and **cpa-go-plugin-authoring** skills to: flip the corresponding flags in `registrationCapability{...}`, add the required fields (e.g. `ExecutorInputFormats`/`ExecutorOutputFormats` for `executor`), and add the matching `handleMethod` case with a real stub. Cite the exact interface from `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go`.
5. Verify it still builds: `cd "$DIR" && CGO_ENABLED=1 go build -buildmode=c-shared -o "$ID.dylib" . && nm -gU "$ID.dylib" | grep cliproxy_plugin_init` (`.so` on Linux, `.dll` on Windows).
6. Tell the user the next steps: implement the capability logic, then `/cpa:build`, `/cpa:wire`, `/cpa:test`.

Load the **cpa-plugin-overview** skill first if you're unsure which capabilities the user's goal needs.
