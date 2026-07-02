# z-api-proxy

A lightweight Windows system-tray reverse proxy that bridges [Cursor](https://cursor.com) and the [z.ai](https://z.ai) API. It performs **bidirectional model-name rewriting** so Cursor can use z.ai models seamlessly.

Cursor sends model names like `z.ai/glm-5.2`, but the z.ai API expects `glm-5.2`. This proxy rewrites the name on the way out, then rewrites it back in responses so Cursor recognizes the model. That's all it does — fast, simple, and transparent.

## Features

- **System tray app** — lives in your notification area, no console window
- **Bidirectional model rewriting** — request and response bodies, including SSE streams
- **Hot-reloadable config** — edit settings without restarting (picked up within 5 seconds)
- **Native ARM64 + x64** — runs natively on both Windows architectures, no emulation
- **Start with Windows** — optional autostart toggle in the tray menu (on by default)
- **Connection testing** — built-in upstream reachability check via tray menu
- **Live status** — tray icon and tooltip show handled/rejected counts and error state

## Installation

### Download the installer

1. Grab the latest `z-api-proxy-setup.exe` from [releases](https://github.com/zeevrussak/z-api-proxy/releases)
2. Run it — the installer auto-detects your architecture (x64 or ARM64) and installs the matching native binary
3. The proxy launches automatically after install and appears in your system tray

### Build from source

Requirements:
- **Go 1.25+**
- **[WiX v4+](https://wixtoolset.org/)** (`dotnet tool install -g wix`) for MSI installers
- **[NSIS](https://nsis.sourceforge.io/)** (optional, for the combined exe installer)

```bash
# Full release build: compiles both architectures, builds MSIs + NSIS installer
# Artifacts are placed in releases/
build.bat

# Or build a single binary manually for your architecture
go build -ldflags "-H windowsgui -X main.version=dev" -o z-api-proxy.exe .
```

Release artifacts in `releases/`:

| File | Description |
|------|-------------|
| `z-api-proxy-{VERSION}-amd64.msi` | MSI installer for x64 Windows |
| `z-api-proxy-{VERSION}-arm64.msi` | MSI installer for ARM64 Windows |
| `z-api-proxy-{VERSION}-setup.exe` | Combined NSIS installer (auto-detects architecture) |

## Configuration

On first launch, a default config is created at:

```
%APPDATA%\Z-API-Proxy\config.toml
```

You can edit it via the tray menu (**right-click → Configure...**) or manually. Changes are picked up automatically within ~5 seconds — no restart needed.

```toml
[server]
# Local listen address. Set this as the custom OpenAI base URL in Cursor.
# In Cursor: Settings → Models → OpenAI API Base URL = http://127.0.0.1:8787/v1
listen = "127.0.0.1:8787"

[upstream]
# z.ai API base URL
base_url = "https://api.z.ai/api/paas/v4"

# API key for z.ai. Leave empty to pass through from Cursor.
api_key = ""

# Model name mappings.
# "from" = model name as sent by Cursor
# "to"   = model name as expected by z.ai upstream
[[models]]
from = "z.ai/glm-5.2"
to = "glm-5.2"

[[models]]
from = "z.ai/glm-4.6"
to = "glm-4.6"
```

### Setting up Cursor

> **Critical**: You **must** use a custom API key in Cursor. If you use
> Cursor's default (subscription) mode, requests route through Cursor's
> servers, which block private network addresses (`127.0.0.1`) with the
> error *"Access to private networks is forbidden."* A custom key forces
> Cursor to send requests directly from your machine to the local proxy.

1. Start z-api-proxy (it appears in your system tray)
2. In Cursor: **Settings → Models → OpenAI API Key** — enter your z.ai API key
3. In Cursor: **Settings → Models → OpenAI API Base URL**
4. Set it to `http://127.0.0.1:8787/v1`
5. Make sure the model name matches a `[[models]].from` entry in your config (e.g. `z.ai/glm-5.2`)

Alternatively, set the z.ai API key in `config.toml` under `[upstream].api_key`
and leave Cursor's key field empty — the proxy will inject it.

## Tray Menu

| Item | Description |
|------|-------------|
| **Configure...** | Opens `config.toml` in Notepad for editing |
| **Test Connection** | Pings the upstream `/models` endpoint and reports status |
| **Start with Windows** | Toggle autostart at login (on by default) |
| **Exit** | Quit the proxy |

The tray icon turns red when the proxy encounters upstream errors, and the tooltip shows live handled/rejected request counts.

## How It Works

```
Cursor  ──HTTP──>  z-api-proxy (127.0.0.1:8787)  ──HTTPS──>  z.ai API
                   rewrites z.ai/glm-5.2 → glm-5.2         (api.z.ai)
                   rewrites glm-5.2 → z.ai/glm-5.2
```

- Rewrites the `"model"` field in request bodies before forwarding upstream
- Rewrites the `"model"` and `"id"` fields in response bodies (both regular JSON and SSE streams) on the way back
- Unmapped model names pass through unchanged
- If `api_key` is set in config, it overrides the `Authorization` header; otherwise Cursor's key is passed through

## Architecture

```
main.go          Entry point — wiring, HTTP server, embeds tray icons
internal/
├── config/      TOML config with hot-reload via mtime polling (atomic.Pointer)
├── proxy/       Reverse proxy with bidirectional model-name rewriting
├── counter/     Atomic request counters (handled/rejected)
└── tray/        System tray UI, Windows autostart, native dialogs
```

## Requirements

- **Windows 10/11** (x64 or ARM64)
- For building: **Go 1.25+**, optionally NSIS for the installer

## License

[Creative Commons Attribution 4.0 International (CC BY 4.0)](https://creativecommons.org/licenses/by/4.0/) — see [LICENSE](LICENSE).
