# opencode-go — CLIProxyAPI provider plugin

Adds **OpenCode Go** (`https://opencode.ai/zen/go/v1`) to CLIProxyAPI as a
provider. Its 22 low-cost coding models are served through CLIProxyAPI's
OpenAI-/Claude-/Gemini-compatible surfaces.

## How it works

- Declares two capabilities: `auth_provider` (turns an `opencode-go.json`
  credential into a routing record) and `model_provider` (lists the catalogue,
  discovered live from `GET /models` with a baked-in fallback).
- Declares **no** executor: CLIProxyAPI's built-in OpenAI-compatible executor
  binds to the `opencode-go` provider key and does the `Bearer POST
  /chat/completions`. All 22 models are reachable through that one endpoint.

## Build

    make build            # CGO_ENABLED=1 go build -buildmode=c-shared -o opencode-go.dylib .
    make test             # unit tests (internal/core)

## Install

Copy the artifact into the host's plugin dir under GOOS/GOARCH:

    mkdir -p <plugins.dir>/$(go env GOOS)/$(go env GOARCH)
    cp opencode-go.dylib <plugins.dir>/$(go env GOOS)/$(go env GOARCH)/

## Credential

Create `<AuthDir>/opencode-go.json` (default AuthDir: `~/.cli-proxy-api`). Do
**not** commit it. Fill the key from your environment:

    OPENCODE_API_KEY=sk-... \
      jq -n --arg k "$OPENCODE_API_KEY" \
      '{type:"opencode-go", api_key:$k, base_url:"https://opencode.ai/zen/go/v1"}' \
      > ~/.cli-proxy-api/opencode-go.json

## Wire

Merge `config.snippet.yaml` into your CLIProxyAPI `config.yaml`, then restart
the host. Verify:

    curl -s localhost:<port>/v0/management/plugins       # opencode-go registered
    curl -s localhost:<port>/v1/models | grep opencode   # 22 models
    curl -s localhost:<port>/v1/chat/completions -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}'
