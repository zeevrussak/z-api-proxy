# z-api-proxy

[![License: CC BY 4.0](https://img.shields.io/badge/License-CC%20BY%204.0-blue.svg)](https://creativecommons.org/licenses/by/4.0/)
[![Platform: Windows](https://img.shields.io/badge/Platform-Windows%20x64%20%7C%20ARM64-blue)](#)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8)](#)

**A Windows system-tray reverse proxy for using [z.ai](https://z.ai) GLM models (GLM-4.6, GLM-5.2) with [Cursor](https://cursor.com) — with built-in public tunnel, auto-update, and one-click setup.**

Cursor sends model names like `z.ai/glm-5.2`, but the z.ai API expects `glm-5.2`. This proxy transparently rewrites model names in both directions — requests and responses, including SSE streams — so Cursor and z.ai can talk to each other without any configuration hacks.

## Why You Need This

Cursor's cloud servers block requests to `localhost` / `127.0.0.1` ("Access to private networks is forbidden"). z-api-proxy solves this two ways:

1. **Public Tunnel (built-in)** — One click in the tray creates a public HTTPS URL via Cloudflare Quick Tunnel. No account needed.
2. **Direct pass-through** — When using a custom API key, Cursor sends requests directly from your machine, bypassing its cloud servers entirely.

## Features

- **Bidirectional model rewriting** — request and response bodies, including SSE streams
- **Built-in public tunnel** — one-click Cloudflare Quick Tunnel (no account, no signup)
- **Auto-update** — checks GitHub for new releases on startup, one-click install
- **Hot-reloadable config** — edit settings without restarting (picked up within 5 seconds)
- **Native ARM64 + x64** — runs natively on both Windows architectures, no emulation
- **MSI + NSIS installers** — per-architecture MSI packages and a combined auto-detecting installer
- **Start with Windows** — optional autostart toggle in the tray menu (on by default)
- **Copy Base URL** — one-click clipboard copy of the proxy URL for Cursor settings
- **Connection testing** — built-in upstream reachability check
- **Live status** — tray icon and tooltip show handled/rejected counts and error state

## Quick Start

### Install

1. Download the latest installer from [Releases](https://github.com/zeevrussak/z-api-proxy/releases):
   - **`z-api-proxy-win-*-amd64.msi`** for x64 Windows
   - **`z-api-proxy-win-*-arm64.msi`** for ARM64 Windows (Surface Pro X, Snapdragon laptops)
   - **`z-api-proxy-win-*-setup.exe`** — combined installer (auto-detects architecture)
2. Run the installer — the proxy starts automatically and appears in your system tray

### Configure Cursor

1. Start z-api-proxy (system tray icon)
2. **Right-click the tray icon → "Start Public Tunnel"**
3. Wait a few seconds — a dialog shows your public URL (e.g. `https://random.trycloudflare.com`)
4. In Cursor: **Settings → Models**
   - Set **OpenAI API Key** to your z.ai API key
   - Enable **Override OpenAI Base URL**
   - Set URL to **`https://random.trycloudflare.com/v1`** (the tunnel URL + `/v1`)
5. Set the model name to `z.ai/glm-5.2` or `z.ai/glm-4.6` (matching your config)

> **Alternative (no tunnel):** If you use a custom API key in Cursor, requests go directly from your machine. Set the base URL to `http://127.0.0.1:8787/v1` directly. Use **Copy Base URL** in the tray for one-click clipboard copy.

## Configuration

On first launch, a default config is created at:

```
%APPDATA%\Z-API-Proxy\config.toml
```

Edit it via the tray menu (**right-click → Configure...**) or manually. Changes are picked up automatically within ~5 seconds — no restart needed.

```toml
[server]
# Local listen address
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

## Tray Menu

| Item | Description |
|------|-------------|
| **Configure...** | Opens `config.toml` in Notepad for editing |
| **Test Connection** | Pings the upstream `/models` endpoint and reports status |
| **Copy Base URL** | Copies `http://127.0.0.1:8787/v1` to clipboard for Cursor |
| **Start Public Tunnel** | Creates a public HTTPS URL via Cloudflare Quick Tunnel. Auto-starts on next launch when enabled |
| **Copy Tunnel URL** | Copies the active tunnel URL (with `/v1`) to clipboard |
| **Contact Developer** | Opens your mail client to send feedback |
| **Start with Windows** | Toggle autostart at login (on by default) |
| **Update Available!** | Shows when a new release exists — click to download and install |
| **Exit** | Quit the proxy |

The tray icon turns red when the proxy encounters upstream errors, and the tooltip shows live handled/rejected request counts.

## How It Works

```
Cursor  ──HTTPS──>  Cloudflare Tunnel  ──>  z-api-proxy (127.0.0.1:8787)  ──HTTPS──>  z.ai API
                    (public URL)             rewrites z.ai/glm-5.2 → glm-5.2         (api.z.ai)
                                             rewrites glm-5.2 → z.ai/glm-5.2
```

- Rewrites the `"model"` field in request bodies before forwarding upstream
- Rewrites the `"model"` and `"id"` fields in response bodies (both regular JSON and SSE streams) on the way back
- Unmapped model names pass through unchanged
- If `api_key` is set in config, it overrides the `Authorization` header; otherwise Cursor's key is passed through

### Public Tunnel Details

The tunnel uses [cloudflared](https://github.com/cloudflare/cloudflared) (Apache 2.0 license). On first use, the proxy downloads `cloudflared.exe` (~50 MB) to `%APPDATA%\Z-API-Proxy\` and caches it. The tunnel creates a random `*.trycloudflare.com` URL that forwards to your local proxy. No Cloudflare account, API token, or configuration is needed.

**Note:** Quick Tunnel URLs are ephemeral — they change each time the tunnel restarts. For a stable URL, consider running your own Cloudflare Named Tunnel.

**Auto-start:** Once you enable the tunnel via the tray menu, it automatically starts on every app launch. The preference is stored in `%APPDATA%\Z-API-Proxy\tunnel.pref`. Disable it by clicking **Stop Public Tunnel**.

## Build from Source

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

# Run tests
go test ./...
```

Release artifacts in `releases/`:

| File | Description |
|------|-------------|
| `z-api-proxy-win-{VERSION}-amd64.msi` | MSI installer for x64 Windows |
| `z-api-proxy-win-{VERSION}-arm64.msi` | MSI installer for ARM64 Windows |
| `z-api-proxy-win-{VERSION}-setup.exe` | Combined NSIS installer (auto-detects architecture) |

## Architecture

```
main.go              Entry point — wiring, HTTP server, embeds tray icons
                    Version injected via ldflags (-X main.version=...)
internal/
├── config/          TOML config with hot-reload via mtime polling (atomic.Pointer)
├── proxy/           Reverse proxy with bidirectional model-name rewriting
├── counter/         Atomic request counters (handled/rejected)
├── tray/            System tray UI, Windows autostart, native dialogs
├── tunnel/          Cloudflare Quick Tunnel manager (cloudflared subprocess)
└── updater/         GitHub release checker + MSI auto-updater
```

## Requirements

- **Windows 10/11** (x64 or ARM64)
- For building: **Go 1.25+**, optionally WiX and NSIS for installers

## License

[Creative Commons Attribution 4.0 International (CC BY 4.0)](https://creativecommons.org/licenses/by/4.0/) — see [LICENSE](LICENSE).

## Links

- **Source:** [github.com/zeevrussak/z-api-proxy](https://github.com/zeevrussak/z-api-proxy)
- **z.ai API:** [api.z.ai](https://api.z.ai)
- **Cursor:** [cursor.com](https://cursor.com)
- **cloudflared:** [github.com/cloudflare/cloudflared](https://github.com/cloudflare/cloudflared) (Apache 2.0)
