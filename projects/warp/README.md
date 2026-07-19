# warp — CLIProxyAPI native provider plugin

Makes **Warp AI** a first-class provider inside [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI),
so you can spend your **Warp subscription credits** through any entry protocol the
host exposes (OpenAI `/v1/chat/completions`, Anthropic `/v1/messages`, Gemini, …).
The plugin maps `chat-completions ⟷ warp.multi_agent.v1` protobuf and streams over
HTTP/2 to `app.warp.dev/ai/multi-agent`.

Declares four capabilities: `auth_provider`, `executor` (format `chat-completions`
in/out, model scope `oauth`), `model_registrar`, and `command_line_plugin`.

## License & status

- **License: AGPL-3.0** — this plugin imports Warp's AGPL-3.0 protobuf bindings
  (`github.com/warpdotdev/warp-proto-apis`), so the combined work is AGPL-3.0.
- **Unofficial, unsupported integration.** It impersonates the Warp desktop client
  to consume your subscription. There is no sanctioned Warp API. Use at your own
  risk; Warp may change or block it at any time.

## Build

```sh
make build        # CGO_ENABLED=1 go build -buildmode=c-shared -o warp.dylib .
nm -gU warp.dylib | grep cliproxy_plugin_init
make test         # go test ./internal/core/...
```

## Install & wire

1. Copy `warp.<dylib|so|dll>` into the host's `plugins/<GOOS>/<GOARCH>/` (or `plugins/`).
2. Merge `config.snippet.yaml` into the host `config.yaml`.
3. Restart the host. `GET /v0/management/plugins` should list `warp` with the four
   capabilities; `GET /v1/models` should show `warp/claude-4-sonnet`, etc.

### Configuration keys (`plugins.configs.warp`)

| key | default | meaning |
| --- | --- | --- |
| `enabled` | `true` | enable the plugin |
| `priority` | `10` | plugin priority |
| `use_warp_credits` | `true` | sets `allow_use_of_warp_credits`; leave BYOK keys empty |
| `model_prefix` | `warp/` | prefix for registered model IDs (avoids collisions) |
| `client_version` | `v0.2025.08.06.08.12.stable_02` | `x-warp-client-version` header |
| `os_category` / `os_name` / `os_version` | `Windows` / `Windows` / `11 (26100)` | `x-warp-os-*` header triad |

**Client-version staleness:** Warp may reject a stale `x-warp-client-version`. If
requests start failing with client-version errors, update `client_version` to the
value the current Warp desktop client sends and reload.

## Login (`--warp-login`)

Log into the Warp **app** in your browser as normal (this is the browser login);
the plugin imports the credential the app stored:

```sh
cli-proxy-api --warp-login                       # reads the macOS Keychain (dev.warp.Warp-Stable)
cli-proxy-api --warp-login --warp-refresh-token <token>   # paste a Firebase refresh token instead
```

This does one Firebase token refresh to obtain the initial access JWT and writes a
`warp-*.json` auth record into the host auth dir (default `~/.cli-proxy-api`). The
host schedules subsequent refreshes automatically.

## v1 limitations

- **Text chat only.** No tool/function calling, no image/multimodal input, no MCP.
- **String message content only** (no content-part arrays).
- **Stateless** — full history is sent inline each call; no `conversation_id`
  persistence across proxy requests.
- **`count_tokens` is a rough estimate** (chars/4), not exact.
- Keychain import requires the Warp app installed + logged in on macOS; on other
  platforms use `--warp-refresh-token`.

See `docs/superpowers/specs/2026-07-19-warp-provider-plugin-design.md` for the full design.
