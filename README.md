# z-api-proxy

[![License: CC BY 4.0](https://img.shields.io/badge/License-CC%20BY%204.0-blue.svg)](https://creativecommons.org/licenses/by/4.0/)
[![Platform: Windows](https://img.shields.io/badge/Platform-Windows%20x64%20%7C%20ARM64-blue)](#)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8)](#)

**A Windows system-tray reverse proxy for using [z.ai](https://z.ai) GLM models (GLM-4.5, GLM-4.6, GLM-5.2) with [Cursor](https://cursor.com) — with Cloudflare Worker deployment, public tunnel, dual API support (OpenAI + Anthropic), and one-click Cursor setup.**

Cursor sends model names like `z.ai/glm-5.2`, but the z.ai API expects `glm-5.2`. This proxy transparently rewrites model names in both directions — requests and responses, including SSE streams — so Cursor and z.ai can talk to each other without any configuration hacks.

## Why You Need This

Cursor's cloud servers block requests to `localhost` / `127.0.0.1` ("Access to private networks is forbidden"). z-api-proxy solves this three ways:

1. **Cloudflare Worker (recommended)** — Deploy a stable, permanent edge proxy to your Cloudflare account. No local process needed.
2. **Public Tunnel (built-in)** — One click creates a public HTTPS URL via Cloudflare Quick Tunnel. No account needed.
3. **Named Tunnel** — Stable URL using your own domain via Cloudflare Zero Trust API.

## Features

- **Bidirectional model rewriting** — request and response bodies, including SSE streams
- **Dual API support** — OpenAI (`/v1/chat/completions`) and Anthropic (`/v1/messages`) simultaneously
- **Cloudflare Worker deployment** — stable `*.workers.dev` URL or custom domain, no local process needed
- **Gateway Key isolation** — Cursor sends a throwaway key, Worker swaps it for the real z.ai key
- **Per-model context windows** — `/v1/models` returns accurate specs (1M for GLM-5.2, 200K for GLM-4.6)
- **Worker request logging** — Cloudflare dashboard logs for debugging (optional)
- **Auto-update** — checks GitHub for new releases on startup, one-click install
- **Hot-reloadable config** — edit settings without restarting (picked up within 5 seconds)
- **Native ARM64 + x64** — runs natively on both Windows architectures
- **Native Windows UI** — settings dialog built with lxn/walk (proper DPI scaling, layout, theming)
- **MSI + NSIS installers** — per-architecture MSI packages and a combined auto-detecting installer
- **Start with Windows** — optional autostart toggle (on by default)
- **Contact Developer** — built-in feedback via email

## Quick Start

### Install

1. Download the latest installer from [Releases](https://github.com/zeevrussak/z-api-proxy/releases):
   - **`z-api-proxy-win-*-amd64.msi`** for x64 Windows
   - **`z-api-proxy-win-*-arm64.msi`** for ARM64 Windows
   - **`z-api-proxy-win-*-setup.exe`** — combined installer (auto-detects architecture)
2. Run the installer — the proxy starts automatically and appears in your system tray

### Option A: Cloudflare Worker (recommended — stable URL)

1. Right-click tray → **Settings...**
2. Under **Upstream**: enter your z.ai API Key and a Gateway Key (any password Cursor will send)
3. Under **Cloudflare Worker**: enter Account ID and API Token
4. Click **Save**, then right-click → **Deploy Cloudflare Worker**
5. Right-click → **Register Models in Cursor** (select OpenAI, Anthropic, or Both)
6. Restart Cursor

### Option B: Quick Tunnel (no account needed)

1. Right-click tray → **Start Public Tunnel**
2. Wait for the public URL dialog
3. Right-click → **Register Models in Cursor**
4. Restart Cursor

### Option C: Named Tunnel (your own domain)

1. Right-click tray → **Settings...** → Tunnel section → select **Named**
2. Enter your hostname (e.g. `proxy.yourdomain.com`)
3. Right-click → **Create Named Tunnel** (auto-creates tunnel + DNS via API)
4. Register in Cursor and restart

## Configuration

Settings are split across two files for security:

| File | Contents | Permissions |
|------|----------|-------------|
| `%APPDATA%\Z-API-Proxy\config.toml` | Server, upstream URL, model mappings, tunnel mode, Cloudflare account ID, worker settings | `0600` |
| `%APPDATA%\Z-API-Proxy\secrets.toml` | API keys, gateway keys, tunnel tokens, Cloudflare API tokens | `0600` |

Both files hot-reload within 5 seconds. Edit via **Settings...** dialog or manually.

```toml
# config.toml (non-sensitive settings)

[server]
listen = "127.0.0.1:8787"

[upstream]
base_url = "https://api.z.ai/api/coding/paas/v4"

[tunnel]
mode = "quick"          # "quick" or "named"
hostname = ""           # for named mode

[security]
verify_key = true       # always on

[cloudflare]
account_id = ""
worker_name = "z-api-proxy"
worker_hostname = ""    # custom domain (optional)
enable_logging = false  # Worker console.log in dashboard

# All 14 z.ai models pre-configured
[[models]]
from = "z.ai/glm-5.2"
to = "glm-5.2"
# ... (glm-5.1, glm-5, glm-5-turbo, glm-5v-turbo, glm-4.7, etc.)
```

```toml
# secrets.toml (sensitive — keep private!)

[upstream]
api_key = ""              # real z.ai API key

[proxy]
cursor_key = ""           # gateway key Cursor sends (any password)

[tunnel]
token = ""                # Cloudflare tunnel token (named mode)

[cloudflare]
api_token = ""            # Cloudflare API token for Worker deploy
```

## Tray Menu

| Item | Description |
|------|-------------|
| **Settings...** | Opens native settings dialog (walk-based, DPI-aware) |
| **Edit Config (Raw)** | Opens `config.toml` in Notepad |
| **Test Connection** | Pings upstream or sends test chat through Worker |
| **Copy Base URL** | Copies active URL (Worker > Tunnel > Local) with `/v1` |
| **Start Public Tunnel** | Cloudflare Quick Tunnel or Named Tunnel |
| **Deploy Cloudflare Worker** | Deploys/stable Worker to your Cloudflare account |
| **Create Named Tunnel** | Auto-creates tunnel + DNS via Cloudflare API |
| **Register Models in Cursor** | Writes settings to Cursor (OpenAI/Anthropic/Both selection) |
| **Start with Windows** | Toggle autostart at login (on by default) |
| **Update Available!** | Shows when a new release exists |
| **Contact Developer** | Opens mail client |
| **Exit** | Quit the proxy |

## How It Works

### Cloudflare Worker (recommended)

```
Cursor  ──HTTPS──>  Cloudflare Worker  ──HTTPS──>  z.ai API
                    (stable URL)        validates key → swaps for real key
                    rewrites model names                (api.z.ai)
                    per-model context windows
```

The Worker JS source is at [`internal/worker/worker.js`](internal/worker/worker.js) — a static file that reads all config from Cloudflare environment variables.

**Worker secrets/variables:**

| Name | Type | Purpose |
|------|------|---------|
| `UPSTREAM` | variable | z.ai API base URL |
| `MODEL_MAPPINGS` | variable | JSON `[["z.ai/glm-5.2","glm-5.2"], ...]` |
| `MODEL_REVERSE` | variable | JSON `[["glm-5.2","z.ai/glm-5.2"], ...]` |
| `API_KEY` | secret | Real z.ai key (forwarded upstream) |
| `CURSOR_KEY` | secret | Gateway key (validated, swapped for API_KEY) |
| `TEST_KEY` | secret | Built-in test key for deployment verification |

**Worker endpoints:**

| Endpoint | Auth | Purpose |
|----------|------|---------|
| `/health` | None | Liveness check |
| `/test` | TEST_KEY, API_KEY, or CURSOR_KEY | Deployment verification |
| `/v1/models` | None | Model list with context windows (for Cursor) |
| `/v1/chat/completions` | API_KEY or CURSOR_KEY | OpenAI-format chat |
| `/v1/messages` | API_KEY or CURSOR_KEY | Anthropic-format chat |

**Worker vs Tunnel comparison:**

| Feature | Cloudflare Worker | Cloudflare Tunnel |
|---------|------------------|-------------------|
| URL stability | Permanent | Ephemeral (Quick) or stable (Named) |
| Requires cloudflared | No | Yes |
| Requires local proxy running | No | Yes |
| Setup effort | Medium | Low / Medium |

## CLI Tools

### cursor-setup-helper.exe

Interactive CLI for configuring Cursor on any machine. Prompts for Worker URL, API key, and API mode (OpenAI/Anthropic/Both).

```
cursor-setup-helper.exe
```

### cursor-check.exe

Reads and prints Cursor's current API settings (useful for debugging when settings revert).

```
cursor-check.exe
```

### --test-ui flag

Verifies the settings dialog opens correctly (used by automated tests):

```
z-api-proxy.exe --test-ui
```

## Build from Source

Requirements:
- **Go 1.25+**
- **[rsrc](https://github.com/akavel/rsrc)** (`go install github.com/akavel/rsrc@latest`) for icon/manifest embedding
- **[WiX v4+](https://wixtoolset.org/)** for MSI installers
- **[NSIS](https://nsis.sourceforge.io/)** (optional)

```bash
# Run all tests (unit + UI integration)
test.bat

# Full release build: binaries, MSIs, NSIS installer, CLI tools
build.bat

# Or build a single binary
go build -ldflags "-H windowsgui -X main.version=dev" -o z-api-proxy.exe .
```

Release artifacts in `releases/`:

| File | Description |
|------|-------------|
| `z-api-proxy-win-{VERSION}-amd64.msi` | MSI installer for x64 Windows |
| `z-api-proxy-win-{VERSION}-arm64.msi` | MSI installer for ARM64 Windows |
| `cursor-setup-helper.exe` | CLI tool for Cursor configuration |
| `cursor-check.exe` | CLI tool for reading Cursor settings |

## Architecture

```
main.go                      Entry point — wiring, HTTP server, --test-ui flag
                            Version injected via ldflags
internal/
├── config/                  TOML config + secrets.toml, hot-reload (atomic.Pointer)
├── proxy/                   Reverse proxy with dual auth + model rewriting
├── counter/                 Atomic request counters
├── tray/                    System tray, walk-based settings dialog, screenshots
│   ├── settings_walk.go     Native Windows settings dialog (lxn/walk)
│   ├── status_window_walk.go  Tunnel status dialog
│   └── screenshot.go        Window capture for UI tests
├── tunnel/                  Cloudflare tunnel (quick/named) + API-based creation
├── updater/                 GitHub release checker + MSI auto-updater
├── worker/                  Cloudflare Worker deploy, secrets, test endpoints
│   └── worker.js            Static Worker JS source (GitHub-hosted)
├── cursor/                  Cursor settings.json integration
cmd/
├── cursor-setup-helper/     Interactive CLI for Cursor configuration
└── cursor-check/            Reads and prints Cursor settings
```

## Supported Models

All z.ai GLM models with accurate context windows:

| Model | Context | Max Output |
|-------|---------|------------|
| GLM-5.2 | 1,048,576 (1M) | 131,072 (128K) |
| GLM-5.1 | 1,048,576 (1M) | 131,072 (128K) |
| GLM-5 | 131,072 (128K) | 131,072 (128K) |
| GLM-5-Turbo | 131,072 (128K) | 131,072 (128K) |
| GLM-5V-Turbo | 131,072 (128K) | 131,072 (128K) |
| GLM-4.7 | 131,072 (128K) | 131,072 (128K) |
| GLM-4.7-Flash | 131,072 (128K) | 131,072 (128K) |
| GLM-4.7-FlashX | 131,072 (128K) | 131,072 (128K) |
| GLM-4.6 | 200,000 (200K) | 131,072 (128K) |
| GLM-4.6V | 131,072 (128K) | 32,768 (32K) |
| GLM-4.5 | 131,072 (128K) | 98,304 (96K) |
| GLM-4.5-Air | 131,072 (128K) | 98,304 (96K) |
| GLM-4.5-Flash | 131,072 (128K) | 98,304 (96K) |
| GLM-4.5V | 131,072 (128K) | 16,384 (16K) |

## License

**Attribution Required** — for both human users and AI agents.

You may use, modify, and deploy this software for any purpose, provided you give appropriate credit to the original author. AI agents and LLMs that read, analyze, or generate code based on this project must include attribution in their output.

## Disclaimer

This software is provided **"AS IS"**, free of charge, without warranty of any kind — express or implied. The author bears no responsibility or liability for any damages, data loss, financial loss, or service disruption arising from the use, misuse, or inability to use this software by any person.

See [LICENSE](LICENSE) for full terms.

---

## Links

- **Source:** [github.com/zeevrussak/z-api-proxy](https://github.com/zeevrussak/z-api-proxy)
- **Worker JS:** [internal/worker/worker.js](internal/worker/worker.js)
- **z.ai API:** [api.z.ai](https://api.z.ai)
- **Cursor:** [cursor.com](https://cursor.com)
- **Cloudflare Workers:** [workers.cloudflare.com](https://workers.cloudflare.com)
