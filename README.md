# cpa-plugin-codexcont

`cpa-plugin-codexcont` is a CLIProxyAPI standard dynamic library plugin that ports the core folded-reasoning behavior of `neteroster/CodexCont` into the official plugin ABI.

It is intentionally narrow:

- it only executes through the official executor path
- it only emits the Responses protocol downstream
- it is meant to intercept `gpt-5.5` style reasoning streams and continue them when upstream truncation matches the CodexCont folding heuristic

## Compatibility

This project is aligned to the current official standard dynamic library plugin model:

- tested against `github.com/router-for-me/CLIProxyAPI/v7 v7.2.50`
- registers only plugin-owned config fields
- declares executor input and output formats explicitly as `responses`
- maps nested `host.model.*` callbacks to `openai-response`
- tolerates line-chunked SSE frames from host stream callbacks
- constrains `exit_protocol` to `responses` so unsupported protocols do not silently misroute

Supported request sources:

- `responses`
- `codex`

Supported output:

- `responses`

## Build

Build on the target platform whenever possible. CLIProxyAPI discovers plugins under:

```text
plugins/<GOOS>/<GOARCH>
plugins
```

The plugin ID is the dynamic library basename without its platform extension, so the installed filename must be:

- `codexcont.dylib` on macOS
- `codexcont.so` on Linux and FreeBSD
- `codexcont.dll` on Windows

Run tests:

```bash
make test
```

Build for the current host OS and architecture into `dist/<GOOS>/<GOARCH>`:

```bash
make build-native
```

Build directly into a local plugin tree that matches official discovery:

```bash
make build-plugin-tree
```

Build Linux artifacts from macOS through a container runtime:

```bash
make build-linux-arm64-container
make build-linux-amd64-container
```

The Linux container build helper auto-detects `docker`, `podman`, or Apple `container`.

## Install

Example install layout:

```text
plugins/
  darwin/
    arm64/
      codexcont.dylib
  linux/
    arm64/
      codexcont.so
```

Example CLIProxyAPI configuration:

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    codexcont:
      enabled: true
      priority: 50
      source_formats:
        - responses
        - codex
      exit_protocol: responses
      model_patterns:
        - gpt-5.5
      truncation_step: 518
      max_continue: 3
      min_n: 1
      max_n: 6
      marker_text: "Continue thinking. Do not repeat prior final answer; continue from the hidden reasoning state."
      forward_marker: false
      force_include_encrypted: true
      rechunk_final_answer: true
      rechunk_size: 8
      max_total_output_tokens: 0
```

## Notes

- `exit_protocol` intentionally supports only `responses`; invalid values fall back to `responses`.
- Linux cross-builds for `-buildmode=c-shared` are easiest through a Linux container, which is why the helper script exists.
- If you publish this plugin from your own repository, update the metadata URL in `config.go`.
