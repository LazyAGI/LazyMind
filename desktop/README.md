# LazyMind Desktop

Desktop mode wraps the existing host-process Local runtime in an Electron shell. Local remains a source-checkout runtime; Desktop is the distributable form.

## Build matrix

| Platform | Local | Desktop |
|----------|-------|---------|
| macOS arm64 | `make local-up` / `make local-down` | `make desktop-darwin-arm64` (internal ZIP) / `make desktop-darwin-arm64-dmg` (signed DMG) |
| Windows x64 | `make local-win-up` / `make local-win-down` | `make desktop-windows-x64` (portable ZIP) / `make desktop-windows-x64-installer` (installer) |

Desktop packages bundle the Go services, process-compose, Caddy, the compiled frontend, Python 3.11 runtime, auth/algorithm venvs, LazyLLM, Milvus Lite 3, and the Local dependency overlay. Model weights are not bundled.

Platform-maintained Skill directories and installable Skill links are declared together in `skills/builtin-sources.yaml`; curated experiences keep their schema, locales, and images under `skills/featured/<id>/`. Desktop builds package or download every source into the same locked ZIP catalog under `resources/runtime/builtin-skills`, and compile the curated catalog plus content-hashed assets under `resources/runtime/featured-skills`. Bundled Caddy serves those assets through `/showcase-assets/` on both macOS and Windows. Release builds use the lock in frozen mode; users only unpack a Skill into their personal revision store when they click Install or Try.

The frontend dependency tree is installed while building, but raw `frontend/node_modules` is not distributed. Vite compiles browser dependencies into `frontend/dist`, and Desktop serves that static output through bundled Caddy.

## Outputs

macOS:

```text
desktop/dist/mac-arm64/LazyMind.app
desktop/dist/LazyMind-darwin-arm64.zip
desktop/dist/LazyMind-macos-arm64.dmg
```

Windows:

```text
desktop/dist/win-unpacked/             # complete unpacked Electron application
desktop/dist/LazyMind-windows-x64-yyyyMMdd-HHmmss-<commit>.zip  # portable distribution with build time and short Git commit
desktop/dist/LazyMind-windows-x64-installer-<version>-yyyyMMdd-HHmmss-<commit>.exe  # assisted per-user installer
```

`LazyMind.exe` is the entry point inside `win-unpacked`; the directory also contains Electron DLLs/locales and `resources/runtime` with all LazyMind services and Python dependencies.

## macOS signed DMG

The local distribution build requires a `Developer ID Application` identity in
the login keychain:

```bash
make desktop-darwin-arm64-dmg
```

`electron-builder` discovers the local Developer ID identity automatically.
The local target signs the app and DMG but does not submit them to Apple.

For CI, provide `CSC_LINK`, `CSC_KEY_PASSWORD`, and the Apple notarization
credentials. `.github/workflows/macos-installer.yml` maps those values from
`MAC_CSC_LINK`, `MAC_CSC_KEY_PASSWORD`, `APPLE_ID`,
`APPLE_APP_SPECIFIC_PASSWORD`, and `APPLE_TEAM_ID` repository secrets.
Release builds submit a ZIP first, staple the accepted app ticket when
available, then package and submit the DMG. A ZIP timeout falls through to DMG
packaging; a DMG timeout uploads the pending DMG and submission record for the
manual finalizer.

A DMG drag-install cannot run a post-install script. To provide the same cache
and runtime preparation as the Windows NSIS installer, the packaged macOS app
runs the shared offline installer warmup once on first launch for each app
version. A failed warmup is not marked complete and is retried on the next
launch.

Windows Desktop supports Windows 10/11 x64, runs as the current user, and does not require MinGW, administrator rights, or Developer Mode. Installer builds are unsigned unless standard electron-builder signing variables such as `CSC_LINK` are supplied.

The assisted installer supports in-place upgrades and blocks downgrades. It offers two installation types:

- **Simple installation (default):** installs the bundled Python environment as a single archive, avoiding installation-time expansion and the long per-file firewall/antivirus scan. Python is expanded automatically on first launch.
- **Full installation:** expands Python and warms the bundled Python, Node, and local services before setup completes, matching the previous installer behavior.

Silent installs default to simple mode and accept `--simple-install` or `--full-install` explicitly. On a fresh or repair install, existing `%LOCALAPPDATA%\LazyMind` data can be retained (the default) or cleared. Upgrades always retain it. The uninstaller similarly defaults to removing the program only and can optionally clear Local AppData. Neither workflow reads, deletes, or moves `%USERPROFILE%\Documents\LazyMind`.

## Trusted local mode

Desktop packages restrict agent file writes to the per-conversation workspace and disable local command tools by default. For a trusted, single-user package, set `LAZYMIND_TRUSTED_LOCAL_MODE=true` when building:

```powershell
$env:LAZYMIND_TRUSTED_LOCAL_MODE = 'true'
make desktop-windows-x64-installer
```

The build records the feature in the packaged runtime manifest, so the installed app keeps the setting without requiring the user to configure an environment variable. In this mode, agents may read and write user-requested absolute host paths and can use LazyLLM's local command tool. Existing overwrite, delete, move, and dangerous-command approval checks still apply. Do not enable this mode in packages distributed to untrusted or multi-user environments.

For source-based Local runs, setting the same environment variable before `make local-win-up` or `make local-up` enables the mode for that process without changing a desktop package.

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
