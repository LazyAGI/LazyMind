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
