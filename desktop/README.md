# LazyMind Desktop

Desktop mode wraps the existing host-process Local runtime in an Electron shell. Local remains a source-checkout runtime; Desktop is the distributable form.

中文操作流程：[Mac 本地构建 → 验证 → 上传 ModelScope → 安装测试](INSTALL.zh-CN.md)。包含测试 ZIP / Developer ID DMG 的选择、同次构建组件的定位及上传后的校验命令。

## Build matrix

| Platform | Local | Desktop |
|----------|-------|---------|
| macOS Intel x64 | `make local-up` / `make local-down` | `make desktop-darwin-x64` / `make desktop-darwin-x64-dmg` (native Intel validation pending) |
| macOS arm64 | `make local-up` / `make local-down` | `make desktop-darwin-arm64` (internal ZIP) / `make desktop-darwin-arm64-dmg` (signed DMG) |
| Windows x64 | `make local-win-up` / `make local-win-down` | `make desktop-windows-x64` (portable ZIP) / `make desktop-windows-x64-installer` (installer) |

Desktop packages bundle the Go services, process-compose, Caddy, Pandoc 3.11, the compiled frontend, Python 3.11 runtime, auth/algorithm venvs, LazyLLM, and shared Local dependencies. Default builds provide RAG/Milvus dependencies as a separately installable component and fetch published workflow examples during warmup. Model weights are not bundled.

Local macOS builds generate and verify a RAG ZIP/catalog for the current resolved dependencies by default, matching CI. Publish that paired component before users download it. For an ad-hoc ARM64 build, `LAZYMIND_DESKTOP_REBUILD_PYTHON_COMPONENTS=false` explicitly reuses the pinned published bundle; its exact dependency and component-boundary checks must still pass. Developer ID builds always rebuild to retain matching signatures.

F4 statistics and plotting run natively on Windows 10/11 using an AppContainer and a Job Object; Docker and WSL are not required. Each execution has its own private scratch directory and AppContainer profile, receives no service credentials or network capabilities, and is limited to one process, 1 GiB of committed memory, and the configured execution timeout. A custom read-only capability grants access to the bundled Python runtime and its installed libraries, including shared `.pth` dependencies; service data and source paths from `PYTHONPATH` are not granted. Runtime ACLs must permit AppContainer reads (user-owned installations are prepared automatically). Failure to set up isolation stops execution. Linux deployments continue to use the existing service-container sandbox. Run `python -m pytest tests/algorithm/chat/workflows/test_paper_execution.py` on native Windows to check computation, SVG/PNG/PDF exports, access restrictions and cleanup; these tests are collected by the Windows algorithm CI job.

On Windows, the parent checks scratch and AppContainer private storage every 50 ms and once after exit: 16 MiB per file, 64 MiB combined, and at most 1024 directory entries. Exceeding a budget aborts the job and cleans up its private storage. These are monitored budgets that may briefly overshoot between checks, not filesystem-enforced quotas; Linux retains its existing file-size rlimit.

On macOS, statistics, plotting and lightweight experiments run in a fresh Python process with Seatbelt applied before the worker executes. The policy permits reads of Python libraries and system fonts and writes to the job's private scratch directory. Network access and child-process creation are denied. The child receives a clean environment and CPU, file-size and descriptor limits. The parent enforces the execution timeout and checks resident memory and scratch storage every 50 ms and after exit. The monitored budgets are 1 GiB of resident memory, 16 MiB per file, 64 MiB of scratch storage and 1024 directory entries, with possible brief overshoot between checks. Isolation setup failure stops the job. Run `python -m pytest tests/algorithm/chat/workflows/test_paper_execution.py tests/algorithm/chat/workflows/test_paper_execution_macos.py` on macOS to verify computation, image exports, isolation and cleanup.

Pandoc release URLs and SHA-256 pins live in `desktop/dependencies/pandoc.json`. Desktop builds reuse verified archives from `desktop/cache/dependencies/pandoc`, try `LAZYMIND_PANDOC_MIRROR_URL` first when configured, then the project-controlled mainland mirror list, and finally the official upstream release. The packaged algorithm services receive the absolute binary path through `LAZYMIND_PANDOC_PATH`; end users do not download or configure Pandoc.

Release history samples remain separate ModelScope ZIP assets, described by `desktop/history-injection-package.json`. By default, Windows/macOS builds stage only their URL, size and SHA-256 into the runtime manifest. Installer/first-launch warmup downloads and verifies the archive after Python preparation, caches it under the user runtime, extracts its `history-injection/` subtree, and then starts Core to import conversations and artifacts. Matching installed samples are reused without network access. Download failures preserve existing samples, allow normal startup and retry at the next runtime launch. The signed application directory is never modified. Set `LAZYMIND_DESKTOP_DEFER_HISTORY=false` (Actions: `defer_history=false`) to restore the bundled archive for offline distribution; publishing the sample ZIP and updating its metadata remains unchanged.

## Fast Desktop development

To verify a slim Python environment and its downloaded RAG bundle without building an installer, run `scripts/verify-python-components.py` with the staged algorithm Python. It checks base imports, ZIP integrity, overlay imports, and Milvus persistence across a restart using temporary data. See [Mac component verification](INSTALL.zh-CN.md#4-上传前验证本地组件) and [Windows build and verification](INSTALL.zh-CN.md#windows-本地构建与复测) for commands.

Release RAG publication uses ModelScope tag catalogs: Mac Developer ID builds sign native components with the bundled Python's Team ID before creating the ZIP. CI requires `MODELSCOPE_API_TOKEN` (dataset write access); `LAZYMIND_MS_COMPONENT_REPO` defaults to `CarlosShaoting/lazymind-cst`. ZIP and checksums upload before the online JSON, followed by a real download verification. Clients resolve the tag catalog at installation time and retain SHA/size/compatibility checks and offline restart receipts. See [RAG release instructions](INSTALL.zh-CN.md#actions-自动发布-rag).

Installer GitHub Artifacts become downloadable before independent MS/HF mirror jobs. Only installers go into `Realease/` (intentional spelling); existing RAG paths are unchanged. Configure `HF_TOKEN` and optionally `LAZYMIND_HF_COMPONENT_REPO` (default `LazyAGI/LazyMind`) for the HF installer mirror. Installation tests use paired build artifacts, not pending cloud uploads; first-time online RAG installation still requires successful RAG publication.

Use the source Electron shell for browser/UI development instead of rebuilding an installer:

```bash
make local-up
make desktop-dev
```

`desktop-dev` starts a Desktop-mode Vite renderer on `127.0.0.1:5173`, proxies API and Browser WebSocket traffic to the existing Local Runtime on `127.0.0.1:8090`, and launches Electron directly from `desktop/electron`. React/CSS changes use Vite HMR. Changes to `desktop/electron/src/*.js` automatically restart only Electron.

Stop the development shell without stopping Local Runtime:

```bash
make desktop-dev-down
```

Logs are written under `local/build/desktop-dev/`. Override the defaults with `LAZYMIND_DESKTOP_DEV_PORT` and `LAZYMIND_DESKTOP_EXTERNAL_RUNTIME_URL`. Both renderer and runtime URLs are restricted to loopback hosts because the renderer receives the privileged Desktop preload bridge. Browser actions default to the Chromium engine already included in Electron. Users can also select their installed Microsoft Edge under Settings → System tools → Dependencies → LazyMind Browser. Desktop connects automatically and opens dedicated windows with persistent website sessions; no Chrome/Edge extension setup is required. Edge uses a separate profile and a private CDP pipe, with no additional runtime dependency or browser download. Docker and Local can still use the external Chrome/Edge extension.

Run the dedicated browser connection and adapter tests with `node --test desktop/electron/tests/*.test.js` from the repository root.

Platform-maintained Skill directories and installable Skill links are declared together in `skills/builtin-sources.yaml`; curated experiences keep their schema, locales, and images under `skills/featured/<id>/`. Desktop packages the locked Skill catalog, complete `SKILL.md` previews, and patch assets under `resources/runtime/builtin-skills`, plus the curated catalog and content-hashed assets under `resources/runtime/featured-skills`. Remote Skill ZIPs are downloaded and verified when the user clicks Install or Try; the installer does not include them. Bundled Caddy serves featured assets through `/showcase-assets/` on both macOS and Windows. A Skill already installed in the personal revision store remains usable offline.

The frontend dependency tree is installed while building, but raw `frontend/node_modules` is not distributed. Vite compiles browser dependencies into `frontend/dist`, and Desktop serves that static output through bundled Caddy.

## Outputs

### Desktop update build and publication

Update configuration is public JSON supplied at build time, either by the file path
`LAZYMIND_DESKTOP_UPDATE_CONFIG` or by `LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON`, never both.
There is no built-in personal download source. The helper and native packaging metadata
consume the same input. Builds without either input keep automatic updates disabled.
`version`, `platform` and `arch` are derived from the build; supplied values must match.
`releaseId` may be omitted to derive it from platform, architecture and version (a `+`
in build metadata becomes `_`). Explicit release IDs remain supported.

Example public configuration (replace the placeholders before building):

```json
{
  "trustDomain": "your-desktop-release",
  "audience": "official",
  "channel": "stable",
  "mode": "official",
  "signingIdentity": "YOUR_APPLE_TEAM_ID_OR_WINDOWS_PUBLISHER",
  "updaterProtocol": 1,
  "policyURL": "https://modelscope.cn/datasets/OWNER/REPO/resolve/master/updates/stable.json",
  "allowedOrigins": ["https://modelscope.cn"],
  "publicKeys": {"release-key": "BASE64_RAW_32_BYTE_ED25519_PUBLIC_KEY"}
}
```

`allowedOrigins` pins every download redirect as well as initial URLs. Verify real
large-file downloads and add only the exact required HTTPS CDN origins before release.
Existing installations retain their embedded source and keys until a compatible update
replaces them. Keep private signing keys and upload tokens out of this JSON and bundles.

Both installer workflows have an `enable_updates` input (default `false`). Configure:

| Build variable | Value |
| --- | --- |
| `LAZYMIND_MAC_UPDATE_CONFIG_JSON` / `LAZYMIND_WINDOWS_UPDATE_CONFIG_JSON` | Platform-specific public JSON above |
| `LAZYMIND_UPDATE_BASE_URL` | HTTPS dataset prefix, e.g. `https://modelscope.cn/datasets/OWNER/REPO/resolve/master/updates/` |
| `LAZYMIND_MAC_UPDATE_MINIMUM_SYSTEM_VERSION` / `LAZYMIND_WINDOWS_UPDATE_MINIMUM_SYSTEM_VERSION` | Supported OS minimum as `major.minor.patch` |
| `LAZYMIND_DESKTOP_UPDATES_ENABLED` | Optional `true` to enable update artifact generation in the tag release workflow |
| `LAZYMIND_DESKTOP_UPDATE_PUBLICATION_ENABLED` | Set to `true` to publish signed tag builds and manually dispatched update-enabled installer builds |
| `LAZYMIND_UPDATE_SOURCE_BUILD_RUN_ID` | Optional successful build run containing the currently published source artifacts. CI can recover the latest compatible source from the signed public policy when artifacts have expired. |

macOS `signing_mode` accepts `auto`, `developer-id`, or `adhoc`. `auto` signs/notarizes
requested tags and uses ad-hoc signing for branches, irrespective of repository name.
A fork tag therefore needs the same signing secrets or an explicit manual `adhoc` build;
missing credentials never silently downgrade a requested signed build. Existing `mac_dev`
environment protections still apply. Windows uses `WIN_CSC_LINK` and
`WIN_CSC_KEY_PASSWORD`; enabled update builds require a valid Authenticode installer.
Official automatic updates require these native signatures. The manual macOS
Installer also offers `enable_unsigned_update_test` for an in-place v2 ad-hoc
test. See [the fork test setup](#in-place-macos-ad-hoc-update-test).

Enabled builds export `desktop-update-macos-<run-id>` or
`desktop-update-windows-<run-id>` containing one directory per release: the actual ZIP/EXE,
`feed.json`, the exact built `components.json`, and `release.json` with identity, hashes,
sizes and the runtime manifest digest. macOS ZIP metadata must match the built app.
The build artifact is also the input to the ModelScope publication job. Publish the
release-specific Python assets through the corresponding platform's RAG workflow.

To test Windows A → B, enable automatic publication and use **Windows Desktop
Installer** with `enable_updates` checked. Publish A and install its signed EXE.
Then run the same workflow for a higher version B. For v2, CI verifies the native
upgrade and publishes B's package, component catalog and signed policy to ModelScope.
A checks that policy through its normal **Check for updates** action. A publication
with a verified predecessor also starts a read-only job on a fresh native runner.
It installs A, creates a knowledge base, uses the installed app to download and
install B, and checks that B can reopen that same knowledge base. The run
attaches the result as `desktop-installed-upgrade-<platform>-<run-id>`. The Windows
workflow takes its baseline from the configured source run or from the signed public
history for Windows. The macOS installer can publish its own signed releases in
the same way. For a Mac without Developer ID, use the ad-hoc test round below.

Each platform can publish its first A independently. When its previous release is
already on ModelScope, CI verifies the public policy, downloads that release's
package and component catalog and checks their hashes before extending its history.
A public 404 permits the first channel publication. A transport or signature error
stops the publication.
The separate **Publish desktop update policy** manual workflow also accepts an explicit
plan with successful build run IDs from the current repository. Configure its
`desktop_updates` environment with approval/branch protection appropriate to the publisher:

| Setting | Kind |
| --- | --- |
| `LAZYMIND_UPDATE_POLICY_PRIVATE_KEY` (PKCS8 Ed25519 PEM) | Secret |
| `MODELSCOPE_UPDATE_TOKEN` (write access to the target Dataset) | Secret |
| `LAZYMIND_UPDATE_POLICY_KEY_ID` (must match the embedded public key) | Variable |
| `LAZYMIND_UPDATE_MODELSCOPE_REPO` (`OWNER/REPO`) | Variable |

Pass comma-separated build run IDs and a JSON plan. The first publication can contain
only A with no edges. Subsequent plans retain the supported history. Example v1 manual plan:

```json
{
  "policySequence": 2,
  "issuedAt": "REPLACE_WITH_CURRENT_RFC3339_UTC_TIME",
  "expiresAt": "REPLACE_WITH_FUTURE_RFC3339_UTC_TIME",
  "releaseIds": ["darwin-arm64-1.0.0", "darwin-arm64-1.1.0"],
  "edges": [{
    "sourceReleaseId": "darwin-arm64-1.0.0",
    "targetReleaseId": "darwin-arm64-1.1.0",
    "priority": 0,
    "dataProfile": "REPLACE_WITH_VERIFIED_SAME_V1_PROFILE",
    "requiredComponents": {}
  }],
  "revoked": []
}
```

For a manual v2 plan, include the successful build runs for its releases.
Each upgrade or repair needs the native report for that source and target
from the **target build run**, in its macOS or Windows evidence artifact.
The workflow resolves the report paths before signing. The offline signer
still accepts local report paths when you invoke it directly.

A data profile is measured from the actual supported runtime state, not inferred from
version numbers or a build manifest. Authorize an edge only after verifying compatible
data and components. Different profiles, downgrades, same-version edges, different native
signers/platforms and unimplemented migrations are rejected. Client-side component
preflight remains authoritative for each installed component selection.

Offline commands are `go run ./cmd/update-publisher sign` / `verify` from the runtime
manager module (use `--help` for flags). The signer checks its output using the existing
client policy contract and against every selected release's embedded public configuration.
The publisher needs only the signed policy and public config, never the private key.
Historical policy verification allows expired policies solely to check sequence ordering;
new policies must be current. Retries cannot overwrite different immutable files or
replace a newer policy with an older/equivocal one.

The upload script preflights the complete local file set and existing remote files,
uploads missing artifacts, downloads and checks their SHA-256, then rechecks the old
policy before publishing the new signed policy last and reading it back. A failed final
readback is a failed publication, not proof of rollback. Reconcile remote state before
retrying. The workflow serializes publishing; use one publishing repository/workflow per
dataset/channel. The ModelScope API is not treated as a cross-publisher compare-and-swap
transaction. Never overwrite an artifact referenced by a published policy.

Unit checks (no real uploads/downloads):

```bash
node --test desktop/scripts/update-build-config.test.mjs desktop/scripts/prepare-update-release.test.mjs desktop/scripts/update-publication-workflow.test.mjs
python3 -m unittest discover -s desktop/scripts -p test_publish_desktop_update.py
python3 -m unittest discover -s desktop/scripts -p test_update_publication_transport.py
# In local/local-runtime-manager:
go test ./internal/desktopupdate ./cmd/update-publisher -run 'TestPublication|TestAssemble|TestSignCommand'
```

Real signing/notarization, ModelScope download redirects and A→B installation are release
acceptance checks, not established by these unit tests.

### Windows A → B with a dedicated test certificate

Use **Windows Desktop Installer** on the fork. Both packages install and appear as **LazyMind**. Run this on a dedicated Windows test account or VM with a clean installation. The test build uses the normal Authenticode and signed-policy checks. It has its own publisher certificate, Ed25519 public keys, trust domain and ModelScope prefix.

Create one self-signed RSA code-signing certificate for the test channel on a Windows signing workstation. Retain that same key pair for A and B. For example, use PowerShell in a private directory:

```powershell
$cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject "CN=Desktop QA Publisher" -KeyAlgorithm RSA -KeyLength 3072 -HashAlgorithm SHA256 -KeyExportPolicy Exportable -CertStoreLocation Cert:\CurrentUser\My
Export-Certificate -Cert $cert -FilePath windows-qa.cer | Out-Null
Export-PfxCertificate -Cert $cert -FilePath windows-qa.pfx -Password (Read-Host -AsSecureString 'Choose PFX password') | Out-Null
```

Configure the fork under **Settings → Secrets and variables → Actions**:

| Repository setting | Value |
| --- | --- |
| Secret `WINDOWS_TEST_CSC_LINK` | Single-line Base64 of the PFX containing the test private key |
| Secret `WINDOWS_TEST_CSC_KEY_PASSWORD` | PFX password |
| Variable `LAZYMIND_WINDOWS_TEST_CERT_DER_BASE64` | Single-line Base64 of the **public DER .cer** |
| Variable `LAZYMIND_WINDOWS_TEST_UPDATE_REPO` | Writable ModelScope dataset in `OWNER/REPO` form |
| Variable `LAZYMIND_WINDOWS_TEST_UPDATE_CONFIG_JSON` | Public v2 configuration below |
| Variable `LAZYMIND_WINDOWS_UPDATE_MINIMUM_SYSTEM_VERSION` | Lowest supported Windows version, e.g. `10.0.19045` |

In the **desktop_updates_test** environment store secrets `WINDOWS_TEST_UPDATE_POLICY_PRIVATE_KEY` (PKCS#8 Ed25519 PEM) and `MODELSCOPE_UPDATE_TOKEN` (dataset write access), and variable `LAZYMIND_WINDOWS_TEST_UPDATE_POLICY_KEY_ID` matching the public key ID. Windows and macOS test publications reuse the upload token from this environment. Windows keeps its own certificate, policy signing key and publication prefix. Only the signing job receives the PFX.

Example public JSON for a fresh round; replace the dataset, public key and signer before use:

```json
{
  "trustDomain": "windows-test-trial-1",
  "audience": "official",
  "channel": "canary",
  "mode": "official",
  "signingIdentity": "Desktop QA Publisher",
  "updaterProtocol": 2,
  "policyURL": "https://modelscope.cn/datasets/OWNER/REPO/resolve/master/updates/windows-test/trial-1/policy.json",
  "allowedOrigins": ["https://modelscope.cn"],
  "publicKeys": {"qa-1": "BASE64_RAW_32_BYTE_ED25519_TEST_PUBLIC_KEY"}
}
```

Use the same configuration for A and B. Include the exact HTTPS CDN origins used by the dataset for redirects. The policy, EXE, feed, component catalog and RAG bundles use the test round's prefix. Choose a fresh round by changing the folder and trust domain together. Ordinary and official builds keep their existing release settings.

1. On the designated test machine, compare the public certificate fingerprint with the signing workstation's copy. Once per certificate, import the public `.cer` into the test user's trusted roots: `Import-Certificate -FilePath .\windows-qa.cer -CertStoreLocation Cert:\CurrentUser\Root`. The disposable GitHub-hosted runners use their machine root store to avoid an interactive trust prompt. Never distribute the PFX to installation or verification machines.
2. In **Run workflow**, select the desired branch, leave `git_ref` empty, select `enable_test_signed_updates` only and enter the A version (e.g. `0.5.301`) in `test_version`. Wait for test publication. Install the resulting LazyMind EXE in the test account.
3. Run Windows Desktop Installer again with the same settings and a higher version (e.g. `0.5.302`). Open **Check for updates** in A. For the first publication CI records that no predecessor exists. For B the installed-upgrade job installs the verified A on a fresh Windows runner, creates a knowledge base, uses A's own updater to install B, then opens the same knowledge base. Its evidence is the `desktop-installed-upgrade-windows-<run-id>` artifact.
4. To run a normal signed publication, use `enable_updates` with the production settings instead. Neither Windows mode needs a macOS build.

### In-place macOS ad-hoc update test

Configure the `desktop_updates_test` environment with its own `MODELSCOPE_UPDATE_TOKEN`,
`LAZYMIND_UPDATE_POLICY_PRIVATE_KEY`, and `LAZYMIND_UPDATE_POLICY_KEY_ID`. The test App
uses the corresponding public key. Set `LAZYMIND_MAC_UNSIGNED_UPDATE_TEST_CONFIG_JSON`
to a public v2 configuration with `mode` set to `unsigned-test`, a non-official audience,
`channel` set to `personal-test`, and `signingIdentity` set to `adhoc`. Omit the version,
release ID, platform and architecture. Pin the HTTPS ModelScope and CDN origins used
by the test policy, installer and components. Set `LAZYMIND_UNSIGNED_UPDATE_TEST_BASE_URL`
under the dataset's `/resolve/master/updates/unsigned-test/` and set
`LAZYMIND_MAC_UNSIGNED_UPDATE_TEST_MINIMUM_SYSTEM_VERSION` to the minimum macOS version
for this test, using `major.minor.patch`.

In **macOS Desktop Installer**, enable `enable_unsigned_update_test`, leave
`enable_updates` unchecked, and enter the version for build A. Choose **new**
for `test_round` and leave `source_build_run_id` empty. CI creates an
`in-place/round-<A-run-id>/` directory under the test base and prints the
policy URL and trust domain in the build summary.

To test an existing 0.5 installation, open the old app, create an identifiable
knowledge base or document, record its data location, and quit. Unzip A's
`package.zip` and replace that copy of `LazyMind.app` in `~/Applications` or
a writable `/Applications`. Open A from that same location and confirm the old
data and settings are available. A uses the ordinary Desktop data directories
and honors the existing runtime and SQLite directory settings.

To build B, choose **continue**, enter A's successful run ID and set a higher
`test_version`. After publication, check for updates in A and install B. Verify
the app reopened at the same path and can read and write the same data. To build
C, continue from B's successful run ID. Keep the test keys, origins and identity
settings stable throughout the round.

If the latest successful source artifact is unavailable, choose **continue** and
set `source_build_run_id` to `published:<first-run-id>` for this in-place round.
The workflow verifies its public policy and source artifacts. Historical
`LazyMind Test.app` builds (including 0.5.225–0.5.228) use a different layout
and trust domain. Start a new round to switch to `LazyMind.app`.

### Component compatibility during updates

For v2 updates, Desktop carries the user's RAG and Cluster choices across the
application replacement. The main app can open while a selected component is
offline or needs a different Python build. Core retries each selected download
in the background, checks the archive against the new release's catalog and
tests imports with its packaged Python. Desktop waits for idle services before
restarting the runtime to load the verified revision. A component the user
never installed stays uninstalled. When a release has no matching component
descriptor, Desktop remembers the choice for a later release.

Automatic installation checks the target's immutable `components` catalog against
the runtime's actual installed-component selection before native preparation and
again after tasks drain, before stopping services. The artifact must be copied
from the target bundle and its size/SHA pinned by the signed release policy.
`same-v1` cannot replace or drop an installed component identity: compatible
same-tag repacks can keep the selected revision, while unsupported tag/base/ABI
changes are blocked with the current application still running. Component/data
migration is not implemented by this check. Existing clients must first receive
an updater with this check through a compatible same-tag bridge release; do not
publish unsupported cross-tag edges to older clients.

Starting with a source client that supports v1 component handoff, the last idle
check is followed by a one-time receipt for the signed target and the installed
component selections. Target startup checks that receipt before retaining those
selections. For v1 targets, manual version changes and older source clients
without a receipt use the v1 component cleanup path.

If the source app closes during v1 preparation, reopen it to continue or
postpone the update. The runtime checks the saved attempt and the previous
process identity before allowing either action. Once it has authorized the
native installer, reopening the source app cannot start another installation.

When repairing an interrupted macOS v2 update, LazyMind can ask you to restart
your computer if an older transaction has no conclusive installer-process
information. Restart macOS, reopen the supported repair version, and select
**Verify this version**. The app checks the boot session and signed repair
permission again before resuming.

If a different app version was copied over an update that had already reached
activation, the startup message identifies the version awaiting verification.
Use that target's supported installer to resume, or contact support. Keep the
transaction record and the user data. Early recovery diagnostics are appended
to `desktop-update-recovery.log` in the desktop logs directory, including the
installed version, recovery step and reason. A later successful startup does
not overwrite this log.

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

The Feishu CLI version and platform archive/license checksums are maintained together in `backend/core/providerconnection/feishu-cli-release.json`. macOS, Windows, Docker, and the Go runtime read this same manifest; update the version and its checksums together when upgrading the CLI.

CLI-authenticated document tools also use `backend/feishu-credential-helper`, which pins the same official CLI library version. Desktop builds and the Sidecar image include the helper and its SHA256 file. Core reads `LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_PATH` and `LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_SHA256`; the Sidecar reads the checksum from `LAZYMIND_FEISHU_CLI_CREDENTIAL_HELPER_SHA256_FILE` instead. Missing or invalid helper configuration leaves existing OAuth and CLI scanning available, while CLI document tools refuse to export a credential.

The helper verifies the native CLI user's identity before handing a short-lived user access token to existing internal document tools. Refresh tokens and app secrets stay with the CLI. Sidecar token responses are encrypted and bound to the signed request. Read-only CLI connections require reauthorization with the document write scopes before Writer can publish. Windows helper builds are checked separately from native authentication: the existing Windows CLI filesystem-permission compatibility issue still requires a native fix and verification.

## Cloud release origin

Set `LAZYMIND_CLOUD_BASE_URL` while building a Desktop package to embed its trusted Cloud HTTPS origin in `resources/runtime/manifest.json`:

```bash
LAZYMIND_CLOUD_BASE_URL=https://cloud.example.com make desktop-darwin-arm64
```

The value must be an HTTPS origin without a path, query, fragment, or user information. A packaged Desktop uses the embedded Manifest value and does not allow a process environment variable to replace it. Source development remains configurable through `LAZYMIND_CLOUD_BASE_URL`; omitting the variable while building produces a Local-only package without Cloud navigation.

An internal test package can forward the fixed Notion OAuth callback from `https://localhost:8443` to its embedded Cloud origin without an SSH tunnel:

```bash
LAZYMIND_DESKTOP_BUILD_AUDIENCE=internal \
LAZYMIND_CLOUD_BASE_URL=https://cloud.internal.example:5027 \
LAZYMIND_CLOUD_OAUTH_CALLBACK_MODE=localhost-relay \
LAZYMIND_CLOUD_OAUTH_CALLBACK_PORT=8443 \
make desktop-darwin-arm64
```

The relay starts lazily when the user begins managed Provider OAuth, so a port conflict cannot prevent Desktop or its local features from starting. It is a byte-only TCP forwarder bound to `127.0.0.1`; TLS remains end-to-end between the browser and Cloud. The test Cloud certificate therefore needs both its configured Cloud hostname and `DNS:localhost` SANs, and every test device must trust only the corresponding lab CA certificate. The CA private key is never packaged. Production is the default build audience and fails closed if `localhost-relay` is requested; production packages use `direct` with the final Cloud HTTPS domain.

## macOS signed DMG

DMG artifacts use UDZO (zlib), electron-builder 24's default compressed DMG
format. The installed app contents and internal ZIP builds are unchanged.

The local distribution build requires a `Developer ID Application` identity in
the login keychain:

```bash
make desktop-darwin-arm64-dmg
```

`electron-builder` discovers the local Developer ID identity automatically.
The local target signs the app and DMG but does not submit them to Apple.

Recorder privacy permissions depend on the signed helper's identity. Production
packages validate both `ai.lazymind.desktop` and `ai.lazymind.recorder` after final
signing and again inside the release DMG. Both must use Developer ID signatures
from the expected Apple Team and retain Team/bundle-bound designated requirements;
ad-hoc or binary-hash-bound identities fail the release check. CI takes the
expected Team from `APPLE_TEAM_ID`. Local Developer ID builds may set
`LAZYMIND_MACOS_TEAM_ID` to pin it explicitly.

Keep the same signing Team and bundle identifiers across releases. Switching
from an ad-hoc test package to a certificate-signed release can require one new
authorization. Ad-hoc test helpers identify themselves by their binary hash, so
changing their source may require renewed Screen Recording and Input Monitoring
permissions even if System Settings still shows an older grant. The helper cache
preserves unchanged signed artifacts; it cannot preserve identity after an ad-hoc
helper changes. Validate authorization continuity on a native Mac with two
successive certificate-signed builds before shipping signing-related changes.

Official tag builds require CI signing and notarization
credentials. `.github/workflows/macos-installer.yml` maps them from
`MAC_CSC_LINK`, `MAC_CSC_KEY_PASSWORD`, `APPLE_ID`,
`APPLE_APP_SPECIFIC_PASSWORD`, and `APPLE_TEAM_ID` environment secrets. They
submit a ZIP first, staple the accepted app ticket, then package, submit, and
staple the DMG. Either notarization stage fails after its bounded timeout.

When `signing_mode` is `auto`, tag builds use Developer ID in both the upstream
repository and forks. Manual development builds can select `adhoc` to produce an
unnotarized DMG. The in-place ad-hoc update option tests v2 upgrades with the existing Desktop data.

Desktop release tags containing a prerelease suffix, such as `v0.3.0-a0` or
`v0.3.0-rc.1`, run the complete platform build and installer test workflows but
do not create a GitHub Release. Stable tags such as `v0.3.0` create a draft
GitHub Release after both platforms pass.

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

Runtime shutdown and stale-process cleanup use saved process start identities and inherited runtime ownership labels. A program's location under the build, resources, or data directory alone does not authorize termination. External Agent hosts own their `lazymind mcp proxy` processes; runtime cleanup leaves these Connectors and their children running. Owned descendants are recorded before ordered shutdown and terminated individually, so detached workers can be recovered without signalling an Agent's process group. Older leftovers whose ownership cannot be established are skipped with a diagnostic.

Desktop binds only to `127.0.0.1`. It retains the normal Local/Desktop auto-login flow through `/_local/admin-session`, while LAN auto-login remains disabled.

Local and Desktop share the platform LazyMind data directory so knowledge bases remain available when switching modes, but they cannot run concurrently. Stop Local before opening Desktop and close Desktop before starting Local. Electron also enforces a single Desktop instance.

Desktop stages the PPT export scripts into a content-addressed directory under
the user runtime's `deps/editable-ppt/export-runtime` before linking Node
dependencies. Startup and dependency preparation must not add links or files to
the signed `.app` resources. A staging failure disables that exporter entry point
instead of falling back to writing into the installed application.

Local and Desktop also share the existing device key for auth-service cloud credentials. The identity stays in `~/Library/Application Support/lazymind-desktop/credential-device.json` on macOS and `%LOCALAPPDATA%\LazyMind\Desktop\credential-device.json` on Windows. Empty `LAZYMIND_AUTH_CLOUD_SECRET_KEY` and the old development default select this device key; existing credentials encrypted with the old default remain readable. Explicit custom keys are preserved and must be configured consistently in both modes. Keep the identity file when retaining the database. An invalid identity stops auth-service startup instead of generating a replacement key.

On Windows, all Desktop-generated files live under `%LOCALAPPDATA%\LazyMind`:

```text
%LOCALAPPDATA%\LazyMind\data             # SQLite, Milvus, uploads, and service data
%LOCALAPPDATA%\LazyMind\Desktop          # Electron/Chromium profile and browser caches
%LOCALAPPDATA%\LazyMind\Logs\desktop     # Electron startup and diagnostic logs
%LOCALAPPDATA%\LazyMind\Logs\crash-dumps # Electron crash reports
```

Desktop does not read, migrate, or remove any legacy Electron profile outside this root. The Windows local document source is `%USERPROFILE%\Documents\LazyMind`; Desktop creates it at runtime startup and the file watcher scans it recursively.

Local-folder discovery asks for consent before each parent-location selection and keeps broad search locations in Electron's profile only. Discovery uses a bounded, directory-only scan, skips the platform Desktop, Documents, Downloads, and media folders, and does not pass broad locations to the local runtime. When the user connects one or more folders, Desktop persists only the exact selected folders and updates file-watcher allowed roots dynamically without restarting the runtime. Existing `Documents\LazyMind` bindings keep their original virtual-root mapping.

`desktop/build/<target>/runtime` and `desktop/dist` are generated outputs. Each build recreates its target runtime; dependency downloads continue to use the normal Go, uv/pip, pnpm, Electron, and electron-builder user caches.
