# AGENTS.md

Guide for working in the z-api-proxy codebase. Focuses on non-obvious knowledge that isn't immediately apparent from a single file read.

## What This Is

A **Windows system-tray reverse proxy** that sits between Cursor (the AI editor) and the z.ai API. Its sole job is **bidirectional model-name rewriting**: Cursor sends names like `z.ai/glm-5.2`, the proxy rewrites them to `glm-5.2` on the way upstream, then rewrites them back to `z.ai/glm-5.2` in responses so Cursor recognizes them. It is **Windows-only** by design (system tray, `user32.dll` MessageBox, `APPDATA` env var, NSIS installer).

## Commands

```bash
# Full release: Go binaries (amd64+arm64) + MSIs + NSIS installer → releases/
# Reads VERSION file, injects version into binary via ldflags
build.bat

# Vet (there is no linter configured)
go vet ./...

# Smoke test (integration, requires Python 3 + a running proxy instance)
python test_smoke.py

# Build MSI per-arch (requires WiX v4+ as dotnet tool: dotnet tool install -g wix)
wix build installer.wxs -arch x64 -d MsiVersion=1.0.0 -d DisplayVersion=1.0.0-alpha \
    -d BinPath=build/amd64/z-api-proxy.exe -d UpgradeCode=18CAB0AD-AF9E-4C0B-AD01-99EF83004F7C \
    -o releases/z-api-proxy-1.0.0-alpha-amd64.msi
```

The app ships as **native binaries for both amd64 and arm64**. The NSIS installer detects the host architecture at install time via `PROCESSOR_ARCHITEW6432`. The MSIs are per-architecture. Both produce Start Menu shortcuts, ARP registry entries, and launch after install.

**Version management**: the `VERSION` file (e.g. `1.0.0-alpha`) is the single source of truth. `build.bat` reads it and injects it into the Go binary (`-X main.version=...`), MSIs (display name + numeric version), and NSIS installer. MSI ProductVersion strips the pre-release suffix (`1.0.0-alpha` → numeric `1.0.0`).

**Release artifacts** land in `releases/` (gitignored): `z-api-proxy-{VERSION}-amd64.msi`, `z-api-proxy-{VERSION}-arm64.msi`, `z-api-proxy-{VERSION}-setup.exe` (NSIS).

The smoke test is **not** a unit test. It starts a mock upstream in-process (Python `http.server`), assumes the real proxy is already running on `127.0.0.1:8787`, and asserts the forward/reverse rewriting works end-to-end against a config that maps `z.ai/glm-5.2` → `glm-5.2`. If you change rewriting logic, run the proxy first, then the smoke test.

## Configuration — The #1 Gotcha

**The `config.toml` in the repo root is a template, NOT the file the app reads.**

At runtime the app loads config from `%APPDATA%\Z-API-Proxy\config.toml` (see `config.DefaultConfigPath()` / `AppConfigDir()`). On first launch, if that file is missing, `config.CreateDefault` writes a hardcoded default to that location. The repo `config.toml` is purely a reference copy.

Consequences:
- Editing the repo `config.toml` has **no effect** on a running app.
- The tray "Configure..." menu item opens the **APPDATA** path in Notepad, not the repo path.
- Config changes are picked up via mtime polling (5s interval) in `Manager.watch` — no restart needed, and the proxy calls `manager.Get()` on every request to read the latest.

## Architecture & Control Flow

```
main.go  ──> config.Manager (hot-reload, atomic.Pointer)
        ──> counter.Counter (atomic int64 handled/rejected)
        ──> proxy.Proxy  (implements http.Handler)
        ──> http.Server  (runs in goroutine)
        ──> tray.Run()   (BLOCKING — main loop lives here)
        ──> tunnel.Manager  (cloudflared subprocess, mutex-protected)
        ──> updater          (GitHub release checker + MSI installer)
```

- **`systray.Run` blocks** in `main`. The HTTP server runs in a goroutine. The app exits when the user clicks tray → Exit (`systray.Quit`), which triggers graceful server shutdown.
- Concurrency is handled with `sync/atomic` for config/proxy/counter; the tunnel package uses a `sync.Mutex` (manages a subprocess with multi-step start/stop). Preserve the appropriate pattern per package.
- The tray spawns goroutines: `updateTooltip`, `updateIcon`, `handleMenu`, and `checkForUpdates` (one-shot on startup).

### Request flow (`proxy.ServeHTTP`)

1. Read full request body → forward-rewrite model name → forward upstream.
2. **Path stripping**: `/v1` prefix is stripped before forwarding, because the upstream `base_url` already ends in `/api/paas/v4`.
3. Hop-by-hop headers are dropped (`hopHeaders` list).
4. If `upstream.api_key` is set, it overrides the `Authorization` header; otherwise the client's key passes through.
5. Response branch: `text/event-stream` → line-by-line SSE rewrite + flush; anything else → buffered regular rewrite with recalculated `Content-Length`.
6. Error state (`hasErr` atomic) flips on and is reflected in the tray icon.

## Rewriting Implementation — Byte-Level, Not JSON

Model rewriting is done with `bytes.ReplaceAll` on **string fragments**, not via JSON (un)marshalling. This is a deliberate performance choice but has consequences:

- It matches `"model":"X"` and `"model": "X"` (both with/without space after colon).
- Response rewriting (`rewriteModelFields`) also rewrites the `"id"` field, trying both `:` and `: ` separators.
- SSE lines are only rewritten if they start with `data:` (after trim) and do **not** contain `[DONE]`.
- Unmapped models pass through unchanged — this is tested explicitly in the smoke test.

If you add a new field that needs rewriting, extend the field list in `rewriteModelFields`. If you change to real JSON parsing, be aware SSE responses are JSON-per-line and the smoke test relies on the current contract.

## Windows-Specific Code

- `tray.go` calls `user32.dll` `MessageBoxW` directly via `syscall`/`unsafe` for native dialogs (test connection, errors). Do not port this to other platforms without replacing it.
- Icons are embedded at compile time (`//go:embed assets/icon.ico`, `assets/icon-error.ico`). High-resolution versions (256x256 max) are generated from `scripts/gen_icons.py`.
- `config.go` keys off `%APPDATA%` (falls back to `~/.config` only if unset — effectively Windows-only behavior).
- `tunnel.go` downloads and caches `cloudflared.exe` in `%APPDATA%\Z-API-Proxy\`. The download URL picks `amd64` or `arm64` based on `runtime.GOARCH`.
- `updater.go` downloads MSIs from GitHub releases and launches `msiexec /i <path>` — the WiX `MajorUpgrade` element handles replacing the old version.
- Clipboard operations use `powershell Set-Clipboard` via `exec.Command`.

## Logging

Logs go to `%APPDATA%\Z-API-Proxy\proxy.log` (append mode), **not** stdout — there is no console because of `-H windowsgui`. When debugging a deployed build, read that file. A failed log-file open is silently ignored (logging falls back to stderr, which is invisible in a GUI app).

## Conventions

- Package layout: all logic under `internal/`, one package per concern (`config`, `proxy`, `counter`, `tray`, `tunnel`, `updater`). `main.go` is wiring only.
- `go.mod` module path is `z-api-proxy` (no domain prefix); internal imports use `z-api-proxy/internal/...`.
- Dependencies: `pelletier/go-toml/v2` (config), `getlantern/systray` (tray), `golang.org/x/sys` (registry). Avoid adding dependencies unless necessary.
- Style: short receiver names (`p *Proxy`, `m *Manager`, `t *trayApp`, `c *Counter`), exported constructors named `New`.
- Version is managed via the `VERSION` file and injected at build time with `-X main.version=...`.
- Release artifacts use `win` in their names (e.g. `z-api-proxy-win-1.0.0-alpha-amd64.msi`).
- Tests: `go test ./...` runs unit tests in `config` and `updater`. The Python `test_smoke.py` is a separate integration test.
