# Windows Codex integration: recovery and acceptance

Pull the current PR branch and rebuild LazyMind through the normal Windows build
workflow. No separate hotfix package is required. Existing shared runtime helpers
are immutable copies: rebuilding LazyMind alone does not replace an already
activated helper. Disconnect the old integration, then explicitly reconnect using
the rebuilt version when ready to test. Keep the recovered Windows machine in
native mode until that test is scheduled.

## Confirmed Windows findings

The Windows report confirms MSIX discovery (`f28fa113`) and manifest-selected
Desktop entrypoints (`8b41d526`). Both `ChatGPT.exe` and `Codex.exe` may exist;
`AppxManifest.xml` identifies the actual Desktop entrypoint. The report also
confirms a local plugin compatibility fix reached installation/activation, but
not working shared session control.

After a Store update removed the previous package directory, the installed
launcher still referenced its old Node, CLI and Desktop paths. Launching the old
Node failed before Codex could read its startup policy. The organization-settings
warning in this incident therefore must not be treated solely as a network error.

## Source changes and limits

- A native Windows launcher resolves the original bundled CLI from the running
  Desktop process and its manifest. It does not use an unrelated CLI on PATH.
- Auxiliary CLI requests retain their original arguments and run without Node.
  Shared app-server startup checks the saved Desktop path, runtime files and CLI
  version first. Stale or unsupported shared configuration delegates this launch
  to the current original CLI and records that reconnection is required. It does
  not automatically approve a new version for shared operation.
- This delegation is a decision before shared startup. Failed or disconnected
  shared conversations are never replayed against another backend.
- A known `cmd.exe /d /s /c call` desktop-plugin wrapper is normalized to the
  bundled Node and plugin `server.mjs`. Arbitrary shell commands are rejected.
- CUA may use bundled Node or a complete runtime under Codex's managed local-data
  directory. Its entrypoint must belong to that same managed runtime. Neither a
  PATH Node nor an entrypoint from a different runtime is accepted.
- Settings distinguish unconfigured, waiting for restart, connected, failed and
  invalidated states. A registration file alone is not a connected channel.
  Connected means a verified backend listener and live Desktop adapter transport;
  it is not proof that a particular Continue/Stop command has been consumed.
- Disconnect restores generated launcher paths to the validated current bundled
  CLI, rather than a removed package path. A separate native operation can restore
  saved user environment values without launching Node or Codex.

## Emergency environment restoration

Prefer the normal LazyMind disconnect action. If Codex cannot start and normal
disconnect is blocked, first exit the Codex error dialog and preserve the local
changes and runtime logs. Use the **newly built** LazyMind CLI for this operation;
old installed helpers do not include it. Run PowerShell as the same Windows user:

```powershell
$connector = Read-Host 'Full path to the newly built lazymind.exe'
if (-not (Test-Path -LiteralPath $connector -PathType Leaf)) { throw 'CLI not found' }
$profilePath = if ($env:CODEX_HOME) { $env:CODEX_HOME } else { Join-Path $env:USERPROFILE '.codex' }
$registrationPath = Join-Path $profilePath 'lazymind-shared-server.json'
$registration = Get-Content -LiteralPath $registrationPath -Raw | ConvertFrom-Json
@{ path = $registration.installation } | ConvertTo-Json -Compress |
    & $connector internal codex-runtime restore-windows-startup
if ($LASTEXITCODE -ne 0) { throw 'Recovery failed; preserve the error and snapshot' }
```

This restores the four saved user environment values and their registry types,
refuses unrelated changes, and retains the activation snapshot, registration,
plugins and conversation data. It marks disconnection in progress. Finish normal
LazyMind disconnect afterward to restore generated config paths and remove the
registration. If registration was already renamed during manual recovery, do not
re-enable it merely to run this command; the supplied `path` can instead refer to
the original installation's `runtime.json` beside its retained `activation.json`.

An existing Explorer/terminal process may retain its previous environment. Verify
launch from a fresh process; the new launcher also honors the disconnection marker.
Do not terminate Codex automatically or remove its user profile/authentication data.

## Windows acceptance (still required)

Use the same Windows account and LazyMind data-isolation environment as the
reported machine. Do not hardcode a username, package version or drive.

1. Record the commit and `Get-AppxPackage -Name OpenAI.Codex` results. Confirm the
   manifest-selected Desktop and bundled CLI exist. Check traditional installation,
   explicit binding, CLI-only, missing package and failed-query cases separately.
2. Verify both the known desktop-plugin wrapper and managed CUA runtime prepare
   successfully. Run `pnpm test:platform` with `LAZYMIND_RUNTIME_HELPER` pointing to
   the newly built CLI, under ordinary and elevated execution. Private files must
   remain owned by the individual user; do not weaken the owner checks.
3. Enable while Codex is already running. Expect configured/waiting for restart,
   not a claimed connected channel. Quit normally and launch from the original icon.
4. Verify native startup policy loading, a live shared backend and connected
   Desktop transport. Test Continue/Stop in existing and new sessions. Record
   acceptance, consumption and effect on the intended session, not just `pending`.
5. Quit and reopen. Verify backend reuse and working desktop/browser tools.
6. On a disposable test installation, exercise a Store update that removes old
   package paths. Verify original-icon startup uses the new original CLI and
   LazyMind reports invalidated shared configuration. Do not remove WindowsApps
   directories manually. Test unsupported new versions without enabling shared mode.
7. Verify disconnect after upgrade restores user environment values and generated
   paths, and original-icon startup succeeds. Test the independent recovery
   operation with Node unavailable and ensure snapshots remain available for retry.
8. Inject backend startup/connection failure, verify the failure state, then verify
   recovery. A failed in-flight request must not be replayed.

Capture only relevant errors from `adapter.jsonl`, `coordinator.jsonl` and
`daemon.log` in `runtime.json`'s `runtimeDir`. Do not publish authentication files,
capability tokens, complete plugin environments or conversation content.

macOS logic tests and Windows cross-compilation do not establish Windows end-to-end
success. In particular, MSIX updates, environment inheritance, original-icon
launches and actual Continue/Stop effects still require Windows execution.
