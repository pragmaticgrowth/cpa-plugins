# __PLUGIN_ID__

A native [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) plugin (scaffolded with `/cpa:scaffold`).

## Prerequisites
- Go **1.26+** and a C compiler (CGO). On macOS: Xcode command-line tools.
- The CLIProxyAPI host must be built **with cgo** to load plugins.

## Build
```bash
make build          # -> __PLUGIN_ID__.<dylib|so|dll>
make validate       # confirm cliproxy_plugin_init is exported
```
Or directly:
```bash
CGO_ENABLED=1 go build -buildmode=c-shared -o __PLUGIN_ID__.dylib .
```

## Install
Copy the artifact to your host's plugins directory, under the platform path:
```
<plugins.dir>/<GOOS>/<GOARCH>/__PLUGIN_ID__.<ext>
```
```bash
make install PLUGINS_DIR=/path/to/cliproxy/plugins
```

## Enable
Merge `config.snippet.yaml` into your `config.yaml` (`plugins.enabled: true` and `plugins.configs.__PLUGIN_ID__`), then reload/restart the host.

## Verify
```bash
curl -s -H "Authorization: Bearer $MGMT_KEY" \
  http://127.0.0.1:<mgmt-port>/v0/management/plugins | jq '.[] | select(.id=="__PLUGIN_ID__")'
# expect: registered: true, effective_enabled: true
```

## Develop
Edit `main.go`:
- Set identity in `exampleRegistration()` (Name/Version/Author/GitHubRepository are all **required**).
- Flip capability flags in `registrationCapability{...}` and implement the matching `handleMethod` case.
- See the `cpa-capabilities` and `cpa-go-plugin-authoring` skills for each capability's interface.
