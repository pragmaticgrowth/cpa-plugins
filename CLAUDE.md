# cpa-plugins

A Claude Code **marketplace repo**. Its one plugin, `cpa` ("CLIProxyAPI Plugin Builder"), makes Claude Code an expert at building **CLIProxyAPI native plugins** — shared libraries (`.dylib`/`.so`/`.dll`, from Go/C/Rust) loaded in-process via the C ABI.

## What this repo is (and isn't)
- **Is:** a Claude Code plugin + marketplace. The product is the `cpa` plugin in `plugins/cpa/`.
- **Also hosts real plugins:** `projects/<plugin-id>/` holds actual CLIProxyAPI
  provider/translator plugins built with this tooling (owner decision 2026-07-19 —
  all plugins live in-repo, not in split consuming projects). First one:
  `projects/opencode-go/` (OpenCode Go provider). `templates/go-plugin/` remains
  the scaffold.

## Structure
- `.claude-plugin/marketplace.json` — catalog (single plugin `cpa`).
- `plugins/cpa/.claude-plugin/plugin.json` — manifest; `name: cpa` ⇒ commands are `/cpa:*`.
- `plugins/cpa/skills/<name>/SKILL.md` (+ `references/`) — the "awareness" (5 skills).
- `plugins/cpa/commands/*.md` — `/cpa:scaffold|build|validate|wire|test`.
- `plugins/cpa/agents/cpa-plugin-author.md` — authoring subagent.
- `plugins/cpa/templates/go-plugin/` — buildable Go scaffold. Placeholders (`__PLUGIN_ID__`, `__MODULE_PATH__`, `__AUTHOR__`, `__REPO_URL__`) appear **only inside string literals**, so the template compiles as-is.
- `plugins/cpa/references/upstream/` — vendored ground-truth, pinned to upstream **v7.2.88**; regenerate with `scripts/refresh-upstream.sh`. Don't hand-edit.
- `docs/superpowers/specs/` — the design spec.
- `projects/<plugin-id>/` — real, buildable CLIProxyAPI plugins (own `go.mod`,
  c-shared build). `projects/opencode-go/` is the OpenCode Go provider.

## Build toolchain (for building CLIProxyAPI plugins)
- **Go 1.26+** (`/opt/homebrew/bin/go`) + CGO (Apple clang). Verified command:
  `CGO_ENABLED=1 go build -buildmode=c-shared -o <id>.dylib .` → exports `cliproxy_plugin_init`.
- Standalone plugins resolve the SDK from the published module: `require github.com/router-for-me/CLIProxyAPI/v7 v7.2.88` (no `replace` needed — verified).
- `gopls` LSP is enabled via `.claude/settings.json`.

## Conventions
- Skills: frontmatter is exactly `name` + `description` (trigger starting "Use when…"). Body front-loads essentials, defers depth to `references/*.md`. Cite vendored truth as `${CLAUDE_PLUGIN_ROOT}/references/upstream/...`.
- Use **real upstream symbols only** — verify against `plugins/cpa/references/upstream/pluginapi-types.go` and `pluginabi-types.go`.
- Keep the pinned version consistent across `templates/go-plugin/go.mod.tmpl`, `references/upstream/VERSION.txt`, and skill mentions.

## Common tasks
- **Add/edit a skill:** create `plugins/cpa/skills/<name>/SKILL.md`; `claude plugin validate .`; `/reload-plugins`.
- **Refresh upstream:** `plugins/cpa/scripts/refresh-upstream.sh [REF]`, then review `git diff -- plugins/cpa/references/upstream` and update skills/templates if the ABI or capability set changed.
- **Dogfood locally:** `/plugin marketplace add .` → `/plugin install cpa@cpa-plugins` (or `claude --plugin-dir plugins/cpa`) → `/reload-plugins`.
- **Validate the plugin:** this Claude Code build has **no `claude plugin validate`**. Instead: `claude plugin marketplace add .` → `claude plugin install cpa@cpa-plugins` → `claude plugin details cpa@cpa-plugins` (component inventory + token cost). Commands are counted as "skills" in that inventory.
- **Verify the scaffold builds:** scaffold into a temp dir, `CGO_ENABLED=1 go build -buildmode=c-shared -o x.dylib .`, then `nm -gU x.dylib | grep cliproxy_plugin_init`.

## Publish
Public repo `pragmaticgrowth/cpa-plugins`. Users: `/plugin marketplace add pragmaticgrowth/cpa-plugins` then `/plugin install cpa@cpa-plugins`.

## Upstream
CLIProxyAPI (MIT) — https://github.com/router-for-me/CLIProxyAPI, pinned **v7.2.88**. Docs: CLIProxyAPIDocs.
