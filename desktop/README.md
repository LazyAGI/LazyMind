# LazyMind Desktop

Desktop mode wraps the existing host-process Local runtime in an Electron shell.

Planned first target:

- macOS arm64 internal `.app` / `.zip`
- bundled Go binaries, process-compose, Caddy, frontend dist, Python 3.11 runtime, and Python venvs
- no bundled model weights

Build entry:

```bash
make desktop-darwin-arm64
```

Expected output:

```text
desktop/dist/mac-arm64/LazyMind.app
desktop/dist/LazyMind-darwin-arm64.zip
```

`desktop/build/` and `desktop/dist/` are per-worktree generated outputs.
The build re-creates the bundled runtime for the current worktree on every run,
but dependency downloads use tool-level user caches such as Go module cache,
uv/pip cache, pnpm store, and Electron/electron-builder cache.
