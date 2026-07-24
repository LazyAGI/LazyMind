# LazyMind Desktop

Desktop mode wraps the existing host-process Local runtime in an Electron shell. Local remains a source-checkout runtime; Desktop is the distributable form.

## Build matrix

| Platform | Local | Desktop |
|----------|-------|---------|
| macOS arm64 | `make local-up` / `make local-down` | `make desktop-darwin-arm64` |
| Windows x64 | `make local-win-up` / `make local-win-down` | `make desktop-windows-x64` |

Desktop packages bundle the Go services, process-compose, Caddy, the compiled frontend, Python 3.11 runtime, auth/algorithm venvs, LazyLLM, Milvus Lite 3, and the Local dependency overlay. Model weights are not bundled.

The frontend dependency tree is installed while building, but raw `frontend/node_modules` is not distributed. Vite compiles browser dependencies into `frontend/dist`, and Desktop serves that static output through bundled Caddy.

## Outputs

macOS:

```text
desktop/dist/mac-arm64/LazyMind.app
desktop/dist/LazyMind-darwin-arm64.zip
```

Windows:

```text
desktop/dist/win-unpacked/             # complete unpacked Electron application
desktop/dist/LazyMind-windows-x64-yyyyMMdd-HHmmss-<commit>.zip  # portable distribution with build time and short Git commit
```

`LazyMind.exe` is the entry point inside `win-unpacked`; the directory also contains Electron DLLs/locales and `resources/runtime` with all LazyMind services and Python dependencies.

Windows Desktop supports Windows 10/11 x64, runs as the current user, and does not require MinGW, administrator rights, or Developer Mode. The first release is unsigned and has no installer or automatic updater.

## Runtime behavior

Desktop binds only to `127.0.0.1`. It retains the normal Local/Desktop auto-login flow through `/_local/admin-session`, while LAN auto-login remains disabled.

Local and Desktop share the platform LazyMind data directory so knowledge bases remain available when switching modes, but they cannot run concurrently. Stop Local before opening Desktop and close Desktop before starting Local. Electron also enforces a single Desktop instance.

On Windows, all Desktop-generated files live under `%LOCALAPPDATA%\LazyMind`:

```text
%LOCALAPPDATA%\LazyMind\data             # SQLite, Milvus, uploads, and service data
%LOCALAPPDATA%\LazyMind\Desktop          # Electron/Chromium profile and browser caches
%LOCALAPPDATA%\LazyMind\Logs\desktop     # Electron startup and diagnostic logs
%LOCALAPPDATA%\LazyMind\Logs\crash-dumps # Electron crash reports
```

Desktop does not read, migrate, or remove any legacy Electron profile outside this root. The Windows local document source is `%USERPROFILE%\Documents\LazyMind`; Desktop creates it at runtime startup and the file watcher scans it recursively.

`desktop/build/<target>/runtime` and `desktop/dist` are generated outputs. Each build recreates its target runtime; dependency downloads continue to use the normal Go, uv/pip, pnpm, Electron, and electron-builder user caches.
