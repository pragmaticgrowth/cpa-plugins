# Design spec — `cpa` : CLIProxyAPI Plugin Builder (Claude Code plugin)

**Date:** 2026-07-19
**Status:** approved (design), pending spec review
**Repo:** `pragmaticgrowth/cpa-plugins` (public marketplace repo; working dir `~/cpa-plugins`)
**Upstream pinned:** CLIProxyAPI **v7.2.88** (`93d74a890a44802f656d7f39a573916b2611896e`), Docs `6002ea0dc24afa6efbd7b8f8462ff2085da85b18`

---

## 1. Goal & non-goals

**Goal.** Ship an installable Claude Code plugin that turns any Claude Code session into an expert **CLIProxyAPI native-plugin builder** — full awareness of the C-ABI plugin system, the ~15 capabilities, the request lifecycle, and a verified build/install/wire/test workflow, delivered as skills + slash commands + a subagent.

**Success criteria.**
- `/plugin marketplace add pragmaticgrowth/cpa-plugins` → `/plugin install cpa@cpa-plugins` works.
- After install, asking "build a CLIProxyAPI plugin that X" auto-loads the awareness skills and produces a correct native Go plugin skeleton.
- `/cpa:scaffold` output **compiles to a valid `c-shared` dylib exporting `cliproxy_plugin_init`** (dogfood-verified this session).
- Knowledge is verifiable (vendored real upstream artifacts) and refreshable (`refresh-upstream.sh`, pinned SHA).
- `claude plugin validate` passes (or manual equivalent if CLI lacks the subcommand).

**Non-goals (this session).**
- Building real production provider/translator plugins — those come in following sessions *on top of* this setup.
- Multi-language scaffolding beyond Go (C/Rust are covered in reference docs, not scaffolded). Go is the primary, best-supported path.
- A live CPA integration test requiring a running server (the `/cpa:test` command supports it, but this session verifies the build+exports, not a running-instance round trip).

---

## 2. Distribution model

Marketplace repo hosts one plugin now (`cpa`), with room for more later.

```
/plugin marketplace add pragmaticgrowth/cpa-plugins
/plugin install cpa@cpa-plugins
# commands then available as /cpa:scaffold, /cpa:build, /cpa:validate, /cpa:wire, /cpa:test
```

Plugin `name: "cpa"` (⇒ terse command namespace `/cpa:*`), `displayName: "CLIProxyAPI Plugin Builder"` for discovery.

---

## 3. Repository layout

```
cpa-plugins/                                  # git repo root (== working dir)
  .claude-plugin/marketplace.json             # marketplace catalog
  plugins/cpa/
    .claude-plugin/plugin.json                # manifest (name: cpa)
    skills/
      cpa-plugin-overview/SKILL.md            # + references/lifecycle.md, capability-decision-guide.md
      cpa-plugin-abi/SKILL.md                 # + references/abi-contract.md, rpc-methods.md, host-callbacks.md, memory-model.md
      cpa-capabilities/SKILL.md               # + references/<one file per capability group>.md
      cpa-build-and-wire/SKILL.md             # + references/build.md, discovery-and-install.md, config-wiring.md, verify.md
      cpa-go-plugin-authoring/SKILL.md        # + references/skeleton.md, real-world-patterns.md
    commands/
      scaffold.md  build.md  validate.md  wire.md  test.md
    agents/
      cpa-plugin-author.md
    references/upstream/                       # VENDORED ground-truth (pinned v7.2.88)
      pluginapi-types.go  pluginabi-types.go
      examples/{simple,codex-service-tier,claude-web-search-router}/...
      docs-plugin/*.md
      VERSION.txt                              # upstream SHA + tag + date + file manifest
    templates/go-plugin/                       # scaffold source (see §7)
      main.go.tmpl  go.mod.tmpl  Makefile  config.snippet.yaml  README.md  .gitignore
    scripts/refresh-upstream.sh
    README.md
    assets/logo.svg                            # copy of public/logo.svg for the plugin
  .claude/
    settings.json                              # EXTEND existing (keep gopls-lsp@claude-plugins-official)
  docs/superpowers/specs/2026-07-19-...md      # this spec
  public/logo.svg                              # existing (user-provided) project logo
  CLAUDE.md                                    # repo dev guide
  README.md  LICENSE  .gitignore
```

---

## 4. Skills (progressive disclosure)

Each `SKILL.md` = YAML frontmatter (`name`, `description` = trigger) + a compact, verified body that front-loads the essentials and points to `references/*.md` for depth. Content is authored from the 11-section research (in `scratchpad/research/`, folded in) and cross-checked against the vendored real artifacts.

| Skill | `description` trigger (when Claude loads it) | Body covers | references/ |
|---|---|---|---|
| **cpa-plugin-overview** | building/understanding a CLIProxyAPI plugin; "what capability do I need" | what CPA is; the request lifecycle with hook points; a decision guide mapping intent → capability; how the pieces fit | `lifecycle.md` (full pipeline+mermaid), `capability-decision-guide.md` |
| **cpa-plugin-abi** | the C ABI / entrypoint / JSON-RPC / host callbacks | `cliproxy_plugin_init`, ABI v1, buffer struct, `{ok,result,error}` envelope, register/reconfigure/shutdown, ~35 RPC method constants, memory ownership | `abi-contract.md`, `rpc-methods.md`, `host-callbacks.md`, `memory-model.md` |
| **cpa-capabilities** | choosing/implementing any of the ~15 capabilities | catalog: per capability → purpose, interface methods, config keys, when-to-use, which example | grouped: `providers-auth-exec.md`, `translate-normalize-intercept.md`, `ops-scheduler-usage-cli-mgmt.md` |
| **cpa-build-and-wire** | build/install/enable/verify a plugin | verified Go `c-shared` build; `plugins/<goos>/<goarch>/<id>.<ext>` discovery; `config.yaml` `plugins.*`; management-API verify | `build.md`, `discovery-and-install.md`, `config-wiring.md`, `verify.md` |
| **cpa-go-plugin-authoring** | writing the Go plugin code | canonical annotated skeleton (entrypoint, register, capability dispatch); real-world patterns (config parse, host callbacks, streaming, model routing) | `skeleton.md`, `real-world-patterns.md` |

---

## 5. Commands

All are thin, deterministic slash commands (Markdown w/ frontmatter `description`, `argument-hint`). They orchestrate via Bash + the skills.

- **`/cpa:scaffold <name> [--caps a,b,c] [--dir .]`** — copy `templates/go-plugin/`, substitute `<name>`/module path, stub the requested capabilities' methods. Emits next-step hints (build/wire/test). Default caps: `simple` (register-only) if none given.
- **`/cpa:build [path]`** — run `CGO_ENABLED=1 go build -buildmode=c-shared -o <id>.<ext> .` for the host OS; report artifact path + size.
- **`/cpa:validate [artifact]`** — `nm`/`go` check the dylib exports `cliproxy_plugin_init` (+ `cliproxyPluginCall/Free/Shutdown`); confirm ABI v1 constant and required `Metadata` (Name/Version/Author/GitHubRepository) are set in source.
- **`/cpa:wire [id]`** — print the `plugins.configs.<id>` YAML snippet + the exact `plugins/<goos>/<goarch>/<id>.<ext>` install path; optionally copy the artifact there.
- **`/cpa:test [id]`** — build+install, then `GET /v0/management/plugins` (needs running CPA + mgmt key via env) → confirm `registered:true`, `effective_enabled:true`. Degrades gracefully to build+validate if no server.

---

## 6. Subagent — `cpa-plugin-author`

`agents/cpa-plugin-author.md`. Frontmatter: `name`, `description` (delegate full-plugin authoring from a spec), `model: inherit` (respect session model), tools limited to file+bash+the cpa skills. Behavior: read the capability skills/references, scaffold, implement the requested capabilities against the real interfaces, build, and self-validate the exports before returning.

---

## 7. Templates — `templates/go-plugin/`

A minimal but real, **buildable** Go plugin:
- `main.go.tmpl` — the full C-ABI preamble (cgo block), exported `cliproxy_plugin_init`, `cliproxyPluginCall/Free/Shutdown`, a `plugin.register` returning `schema_version` + `Metadata` + `capabilities`, a `plugin.reconfigure` handler, and clearly marked `// CAPABILITY: <name>` stub blocks toggled by scaffold.
- `go.mod.tmpl` — `module <path>`; `require github.com/router-for-me/CLIProxyAPI/v7 v7.2.88` (published module, **no** local replace).
- `Makefile` — `build` (c-shared per OS), `install` (to `plugins/<goos>/<goarch>/`), `clean`.
- `config.snippet.yaml` — ready `plugins:`/`plugins.configs.<id>` block.
- `README.md`, `.gitignore`.

**Dogfood gate:** the scaffold output MUST compile to a valid dylib exporting `cliproxy_plugin_init` before we publish. Note: the published `v7.2.88` module must resolve via `go mod download`; if the published module path/version isn't `go get`-able standalone, fall back to documenting the `replace` directive against a local checkout and record that limitation.

---

## 8. Vendored upstream & refresh

`references/upstream/` holds real artifacts copied from the pinned clone:
- `pluginapi-types.go`, `pluginabi-types.go` (the exact contract).
- `examples/{simple,codex-service-tier,claude-web-search-router}/` (Go sources + READMEs).
- `docs-plugin/*.md` (all 22 capability docs).
- `VERSION.txt` — upstream tag/SHA/date + a manifest of copied files.

`scripts/refresh-upstream.sh` — clone upstream at `$CPA_UPSTREAM_REF` (default the pinned tag), copy the curated set, rewrite `VERSION.txt`. Idempotent; prints a diff summary.

---

## 9. Dev workspace setup (dogfooding this repo)

- **`.claude/settings.json`** — EXTEND the existing file (keep `enabledPlugins: gopls-lsp@claude-plugins-official`). Add: `permissions.allow` for `Bash(go build*)`, `Bash(go test*)`, `Bash(go mod*)`, `Bash(gh *)`, `Bash(nm *)`, `Bash(cmake*)`; add the local marketplace to `extraKnownMarketplaces` and self-enable `cpa@cpa-plugins` for in-repo dogfooding.
- **`CLAUDE.md`** — repo dev guide (< 200 lines): what this repo is, structure map, how to add/edit a skill, how to refresh upstream, how to test the scaffold, publish flow. Then run `/claude-md-management:claude-md-improver` to audit it (explicit user request).

---

## 10. Build / verify / publish plan (this session)

1. Scaffold the repo tree; write manifest + marketplace + all skills/commands/agent + templates + refresh script + README/LICENSE.
2. Populate `references/upstream/` from the pinned clone; write `VERSION.txt`.
3. **Dogfood:** `/cpa:scaffold sample --caps request-normalizer` → `go build -buildmode=c-shared` → `nm` confirms `cliproxy_plugin_init`. Fix template until green.
4. `claude plugin validate .` (or manual: JSON parses, paths exist, no traversal).
5. `/claude-md-management:claude-md-improver` on CLAUDE.md.
6. Commit on a feature branch; create public `pragmaticgrowth/cpa-plugins` via `gh`; push.

---

## 11. Risks & open items

- **Published-module buildability.** `go get github.com/router-for-me/CLIProxyAPI/v7@v7.2.88` must work for standalone plugins. If not, document the local-`replace` fallback (§7).
- **`claude plugin validate` availability** depends on CLI version; have a manual checklist fallback.
- **Command namespace** is fixed to plugin `name` (`cpa`) — no separate alias; accepted.
- **gh org create permission** — confirm the token can create public repos in `pragmaticgrowth` (scopes present: `repo`, `read:org`).
- **Knowledge drift** — mitigated by pinned SHA + refresh script; skills note the pinned version.

---

## 12. Content source map

Authored skills/references draw from `scratchpad/research/`: `01-architecture` → overview/lifecycle; `02-plugin-abi` → abi; `03-plugin-host-lifecycle` → build-and-wire/discovery; `04/05/06-caps-*` → capabilities; `07/08-examples-go-*` → go-authoring + templates; `09-build-config` (verified) → build-and-wire; `10-abi-c-rust` → abi/reference C-Rust notes; `00-claude-code-plugins` → repo/manifest/marketplace structure.
