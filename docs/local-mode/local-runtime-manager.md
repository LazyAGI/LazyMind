# Local Runtime Manager v1

## 1. Intent and v1 scope

Local Runtime Manager v1 provides developer-facing Makefile entrypoints to run the
new local runtime control plane from this repository.

The current implementation is intentionally docker-stack-only:

- `lazymind-local` manages one process: `docker-stack`.
- `docker-stack` executes:
  - `docker compose -f docker-compose.yml -f local/docker-compose.local.yml ...`
- No Electron mode, packaging workflow, or service de-containerization changes are included in v1.

Local mode remains explicit and does not change Cloud defaults.

## 2. Non-goals

- Replacing Cloud/Server mode behavior.
- Changing `docker-compose.yml` for local runtime compatibility.
- Adding cross-platform Electron process supervision or native dialogs.
- Altering product APIs or public service contracts.
- Guaranteeing Windows or packaged runtime behavior in this version.

## 3. Configuration and profile defaults

- `LAZYMIND_LOCAL_PROFILE` (Make variable, default `linux-browser`)
- `LAZYMIND_LOCAL_BIN` (Make variable, default `local/local-runtime-manager/lazymind-local`)
- `LAZYMIND_LOCAL_DIAGNOSTICS_OUTPUT` (Make variable, default `.lazymind-local/diagnostics/runtime.zip`)
- `LAZYMIND_LOCAL_GOCACHE` (Make variable, default `.codex-gocache/go-build` under the repo)

Profile arguments are passed through the same variable in local-runtime targets.

## 4. Startup path

The local runtime path is:

1. `make local-runtime-up`
2. `make local-runtime-build` compiles `local/local-runtime-manager/lazymind-local`.
3. `lazymind-local up --profile $(LAZYMIND_LOCAL_PROFILE)` starts the local profile.
4. Local services are managed under overlays via `local/docker-compose.local.yml`.

The Makefile does not alter `make up`/`make down`; local runtime only runs when one
of the new `local-runtime-*` targets is invoked explicitly.

## 5. Paths

Runtime artifacts are written under:

- `.lazymind-local/state`
- `.lazymind-local/logs`
- `.lazymind-local/run`
- `.lazymind-local/generated`
- `.lazymind-local/data`
- `.lazymind-local/cache`
- `.lazymind-local/diagnostics`

The binary at `local/local-runtime-manager/lazymind-local` is optional to build and is
ignored by git.

## 6. Cloud compatibility impact

- Cloud mode remains default (`make up` / `make down` unchanged).
- Base `docker-compose.yml` is not modified by this work.
- No cloud stack components are removed.
- Local runtime remains an explicit, developer-invoked path.

## 7. Commands

- `make local-runtime-build` -> `go build -o lazymind-local .` in `local/local-runtime-manager`
- `make local-runtime-up` -> build then `lazymind-local up --profile $(LAZYMIND_LOCAL_PROFILE)`
- `make local-runtime-down` -> `lazymind-local down --profile $(LAZYMIND_LOCAL_PROFILE)`
- `make local-runtime-status` -> `lazymind-local status --json --profile $(LAZYMIND_LOCAL_PROFILE)`
- `make local-runtime-doctor` -> build then `lazymind-local doctor --profile $(LAZYMIND_LOCAL_PROFILE)`
- `make local-runtime-diagnostics` -> `lazymind-local export-diagnostics --profile $(LAZYMIND_LOCAL_PROFILE) --output .lazymind-local/diagnostics/runtime.zip`

Direct CLI invocation examples:

```bash
local/local-runtime-manager/lazymind-local up --profile linux-browser
local/local-runtime-manager/lazymind-local down --profile linux-browser
local/local-runtime-manager/lazymind-local status --json
local/local-runtime-manager/lazymind-local logs --service docker-stack --tail 200
local/local-runtime-manager/lazymind-local doctor
local/local-runtime-manager/lazymind-local export-diagnostics --output ./.lazymind-local/diagnostics/runtime.zip
```

## 8. Test and acceptance steps

- Confirm CLI behavior:
  - `cd local/local-runtime-manager && go test ./...`
- Confirm local overlay composition shape:
  - `docker compose -f docker-compose.yml -f local/docker-compose.local.yml config --services`
- Confirm local diagnostic entrypoint:
  - `make local-runtime-doctor` (requires process-compose installed)

## 9. Known limitations

- v1 only orchestrates local mode through `docker-stack`; no mixed host-process profile is in scope.
- This documentation does not include Electron, installer packaging, or Windows-first behavior.
