# AGENTS.md

Guide for working in the z-api-proxy codebase. Focuses on non-obvious knowledge that isn't immediately apparent from a single file read.

## What This Is

A **Windows system-tray reverse proxy** that sits between Cursor (the AI editor) and the z.ai API. Its sole job is **bidirectional model-name rewriting**: Cursor sends names like `z.ai/glm-5.2`, the proxy rewrites them to `glm-5.2` on the way upstream, then rewrites them back to `z.ai/glm-5.2` in responses so Cursor recognizes them. It is **Windows-only** by design (system tray, `user32.dll` MessageBox, `APPDATA` env var, NSIS installer).

## Commands

```bash
# Build (produces windowless GUI exe — -H windowsgui suppresses the console)
go build -ldflags "-H windowsgui" -o z-api-proxy.exe .
# or just: build.bat

# Vet (there is no linter configured)
go vet ./...

# Go tests — THERE ARE NONE. Do not expect `go test ./...` to do anything.

# Smoke test (integration, requires Python 3 + a running proxy instance)
python test_smoke.py

# Build installer (requires NSIS installed; makensis on PATH)
makensis installer.nsi
```

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
```

- **`systray.Run` blocks** in `main`. The HTTP server runs in a goroutine. The app exits when the user clicks tray → Exit (`systray.Quit`), which triggers graceful server shutdown.
- Concurrency is handled with `sync/atomic` exclusively (`atomic.Pointer[Config]`, `atomic.Bool`, `atomic.Int64`) — **there are no mutexes anywhere**. Preserve this pattern when adding shared state.
- The tray spawns three long-lived goroutines (`updateTooltip`, `updateIcon`, `handleMenu`) that loop forever; `handleMenu` selects on channel clicks.

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
- Icons are embedded at compile time (`//go:embed assets/icon.ico`, `assets/icon-error.ico`).
- `config.go` keys off `%APPDATA%` (falls back to `~/.config` only if unset — effectively Windows-only behavior).

## Logging

Logs go to `%APPDATA%\Z-API-Proxy\proxy.log` (append mode), **not** stdout — there is no console because of `-H windowsgui`. When debugging a deployed build, read that file. A failed log-file open is silently ignored (logging falls back to stderr, which is invisible in a GUI app).

## Conventions

- Package layout: all logic under `internal/`, one package per concern (`config`, `proxy`, `counter`, `tray`). `main.go` is wiring only.
- `go.mod` module path is `z-api-proxy` (no domain prefix); internal imports use `z-api-proxy/internal/...`.
- No dependencies beyond `pelletier/go-toml/v2` (config) and `getlantern/systray` (tray). Avoid adding dependencies unless necessary.
- Style: short receiver names (`p *Proxy`, `m *Manager`, `t *trayApp`, `c *Counter`), exported constructors named `New`.
