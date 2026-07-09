# LazyMind Desktop

Desktop mode wraps the existing host-process Local runtime in an Electron shell.

Planned first target:

- macOS arm64 internal `.app` / `.zip`
- bundled Go binaries, process-compose, Caddy, frontend dist, Python 3.11 runtime, and Python venvs
- no bundled model weights
- no Apple Developer ID signing or notarization

Build entry:

```bash
make desktop-darwin-arm64
```

Expected output:

```text
desktop/dist/mac-arm64/LazyMind.app
desktop/dist/LazyMind-darwin-arm64.zip
desktop/dist/SHA256SUMS.txt
desktop/dist/LazyMind-darwin-arm64.build.json
```

The default build applies an ad-hoc code signature before zipping:

```bash
LAZYMIND_DESKTOP_SIGNING_MODE=adhoc make desktop-darwin-arm64
```

Use `LAZYMIND_DESKTOP_SIGNING_MODE=none` only when debugging signing issues.
Ad-hoc signing is only for internal testing. It is not Developer ID signing and
does not satisfy Apple notarization, so first-run Gatekeeper approval is still
expected on another Mac.

Tester instructions live in [RUN_ON_MAC.md](RUN_ON_MAC.md). Send testers the
zip and `SHA256SUMS.txt`; the build manifest is for release/debug records.

`desktop/build/` and `desktop/dist/` are per-worktree generated outputs.
The build re-creates the bundled runtime for the current worktree on every run,
but dependency downloads use tool-level user caches such as Go module cache,
uv/pip cache, pnpm store, and Electron/electron-builder cache.
