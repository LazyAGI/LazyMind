# 8091 deployment preflight — read only

Observed 2026-10-20. No service start/stop, database write, backup, deployment, login creation, or 8090 modification was performed. Runtime configuration contents and credentials are not included.

## Current ownership and isolation

Runtime: `/Users/dengyuang/workspace/lazymind-runtime-8091`; isolated host home: its `host-home/`. The explicit command below returned `overallStatus=ready` and all 15 managed services `running`:

```sh
LAZYMIND_HOST_HOME=/Users/dengyuang/workspace/lazymind-runtime-8091/host-home \
LAZYMIND_RUNTIME_ROOT=/Users/dengyuang/workspace/lazymind-runtime-8091 \
/Users/dengyuang/workspace/LazyMind_dev/local/build/bin/local-runtime-manager status --json \
  --runtime-root /Users/dengyuang/workspace/lazymind-runtime-8091 \
  --repo-root /Users/dengyuang/workspace/LazyMind_dev
```

Current Core PID 17256 runs from primary `backend/core`; Chat PID 17336 has primary `algorithm/lazyllm:algorithm:repo` PYTHONPATH; Auth PID 17290 runs from primary `backend/auth-service`. Caddy PID 17503 serves primary `frontend/dist` at 127.0.0.1:8091. Core is 28001, Chat 28046, proxy 6024, auth 28000. Separate Caddy PID 7676 listens at 127.0.0.1:8090; it was only observed.

Primary and authorized worktree HEAD both resolve to `81683cfbea2f0a1cb0c696327abb77e7e412df37`; this feature resides in the authorized worktree's uncommitted changes. Build and deployment must use the complete worktree `/Users/dengyuang/.codex/worktrees/knowledge-management-mcp/LazyMind_dev`, not current primary executables or dist.

Effective make config uses the last definitions: 8091, isolated host home, ports 28xxx/29xxx, router 38100–38199. Earlier 8090 defaults remain in the same file but are overridden. Do not run bare local-up/down. `runtime-isolated.mk` overrides up/down recipes to omit the global assistant bridge. For deployment use both Makefiles and the explicit private config; all normal local-up prerequisites remain active.

## Minimum consistent backup set

All following relative paths are under the runtime root. Ten actual SQLite databases were found; each was opened read-only to inspect page_count/page_size and journal mode, without checkpointing. Their logical backup total is approximately **175.2 MiB** (native backup size may vary slightly as state changes). The SQLite directory's allocated disk use including WAL files was about 200.2 MiB; copying its live main files alone is not a valid backup.

| SQLite database | Logical bytes | Journal |
| --- | ---: | --- |
| data/stores/sqlite/auth/authservice.db | 303104 | delete |
| data/stores/sqlite/auth-state/auth_state.db | 49152 | wal |
| data/stores/sqlite/core/core.db | 25337856 | wal |
| data/stores/sqlite/core-state/core_state.db | 144334848 | wal |
| data/stores/sqlite/lazyllm/app.db | 1179648 | wal |
| data/stores/sqlite/scan/scan_control_plane.db | 139264 | wal |
| data/stores/sqlite/scan-state/scan_state.db | 6852608 | wal |
| data/stores/sqlite/channel-gateway/channel-gateway.db | 159744 | wal |
| data/homes/lazymind/sqlite/segment-store.db | 2793472 | wal |
| data/homes/lazyllm/.lazyllm_filesystem_queue.db | 2564096 | delete |

`core.db` is directly affected by new migrations and backfill. Auth code changes and local login/session validation touch auth/auth-state. Historical visibility reads readonly `lazyllm/app.db`, but upload/search/delete acceptance will mutate it, the segment store and Milvus, so they belong in the same rollback set. The SQLite server maps core/lazyllm/segments to these exact paths. Milvus PID 17304 holds the isolated runtime `data/stores/milvus/lazymind-v3/LOCK`. No 8090 shared data path was observed for these stores.

Preserve these non-SQLite source/asset directories after quiescing 8091 writers:

| Relative path | Allocated estimate | Why |
| --- | ---: | --- |
| data/core/uploads | 56.7 MiB | originals, generated media, workflow artifacts/workspaces and completed temporary uploads |
| data/subagent | 27.4 MiB | unique workspaces and artifact blobs; Core actual artifact root is data/subagent/artifact-blobs |
| data/homes/lazymind/agent_workspace | 39.8 MiB | existing agent workspace outputs; not classified as disposable cache |
| data/stores/milvus | 8.6 MiB | retrieval index; stop its writer before filesystem snapshot |
| data/scan | 10.1 MiB | scanner/file-watcher state |
| data/stores/scan | 0.6 MiB | additional watcher state |

Small configuration/credential files to preserve privately: `runtime-8091.make.env`, `runtime-8091.env`, `runtime-isolated.mk`, `launch-args.json` (combined 10,843 bytes); `config/`; `generated/`; `state/runtime-state.json`; `run/processes.json` as rollback provenance only; `data/channel-gateway/master.key`; `data/assistant/credentials.json` and connector-host-id; `host-home/.lazymind/` (40 KiB, including local connector credentials). Do not print their contents or commit them. Runtime PID/lock files are not restored as live process state.

The conservative minimal set above is approximately **319 MiB**, before small config/credential overhead. Reserve **400 MiB per rollback point**. Existing `backups/` (~923 MiB) and `private-backups/` (~521 MiB) are excluded; do not copy or clean them as part of this task. Keep the latest two validated rollback points, and never remove an unvalidated/explicitly retained one automatically.

Suggested destination: a timestamped private directory under `/Users/dengyuang/.local/share/lazymind-backups/8091/` (outside repo, non-cloud; directory mode 0700, files 0600). Record source HEAD plus uncommitted diff provenance, runtime root, DB list, sizes, included/excluded files, integrity results and restoration command/order. No destination was created by this preflight.

### Native SQLite procedure for the deployer

After closing new 8091 ingress and allowing ongoing work to drain, use the explicitly scoped runtime manager to quiesce its writers. Use SQLite Backup API for each database even when services are stopped. Native backup gives per-database consistency; quiescing all 8091 writers is what aligns the ten files and filesystem assets into one application rollback point. Do not claim an application-wide consistent backup from ten independent live backups.

Executable Python pattern for each relative DB path in the table (to run later, not executed here):

```python
import sqlite3
from pathlib import Path
runtime = Path('/Users/dengyuang/workspace/lazymind-runtime-8091')
backup = Path('/Users/dengyuang/.local/share/lazymind-backups/8091/REPLACE_WITH_TIMESTAMP')
# db_paths is exactly the ten paths listed above.
for relative in db_paths:
    source = runtime / relative
    target = backup / relative
    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with sqlite3.connect(source.as_uri() + '?mode=ro', uri=True) as src:
        with sqlite3.connect(target) as dst:
            src.backup(dst, pages=256, sleep=0.05)
            assert dst.execute('PRAGMA integrity_check').fetchone() == ('ok',)
    target.chmod(0o600)
```

Then copy only listed non-DB directories/configs. Do not copy live SQLite -wal/-shm beside native backups. On rollback, stop only 8091, preserve failed deployment evidence, restore the whole matched snapshot (DBs + original/index assets + config), remove only corresponding stopped DB WAL/SHM leftovers, and restart the previously verified code/config. Never overlay restored DB main files onto an active SQLite server.

## Activity at inspection time

- No pending/running SubAgent, async job, resource-update task, workflow outbox, learning preanalysis, or external-agent run was found.
- Readonly waiting parser queue was empty; five document-service tasks were SUCCESS; Core parse tasks were six SUCCEEDED and five legacy unset.
- One historical chat history says generating (last update 2026-09-17), but its runtime hash expired; **unexpired generating runtime states = 0**.
- One task-center workflow_run says running, but has finished_at 2026-10-14 and no active step; preserve it, do not reset it during deployment.
- One workflow session is waiting since 2026-10-15; its steps are only failed/succeeded, with no running step. Keep the waiting state intact.
- These observations justify a drain check, not a permanent guarantee; repeat the active-state/queue check immediately before shutdown.

## Build dependencies and source provenance

Authorized worktree currently has no `local/build` tree. It does have its own frontend/node_modules directory. Primary has dependency venvs under local/build/deps/python and a frontend virtual store; no path-bearing editable .pth files were found in those venvs. Primary local/build/runtimes/node is empty. Reuse package/download/build caches, not primary business executables, frontend/dist, or full build-root symlinks.

Isolated host-home cache inventory: Go build ~650 MiB; uv ~1.15 GiB; pip ~122 MiB; node/corepack ~21 MiB. Runtime manager goToolEnv selects this host home's Go build/module cache. Python install machinery uses uv and requirements from paths.RepoRoot. Chat prepends that RepoRoot's algorithm/lazyllm and algorithm to PYTHONPATH. Frontend prepares its own build-root node_modules store, runs frozen-lockfile pnpm install, then pnpm build. Core always runs go build -buildvcs=false into its selected build root in local profile. Thus source-isolated rebuild can reuse external package caches without copying old business output.

**Build blocker observed:** `/opt/homebrew/bin/node --version` exits by signal (-6); previous test output identifies missing libllhttp.9.3.dylib. No working node@20/node@22 candidate was present in the checked standard paths. `go`, `pnpm`, `uv` resolve under /opt/homebrew/bin, but pnpm and local-pandoc's node script need a functioning Node first. Do not begin downtime until an appropriate working Node is selected and worktree builds can complete. No dependency installation or configuration change was performed here.

Before switching, build the worktree manager/CLI/Core/frontend and stage its Python dependencies with the runtime's private environment. Ensure the resolved build root is worktree/local/build and new Core cwd, Chat PYTHONPATH and frontend Caddy root all identify the worktree. The custom local-up override only removes bridge side effects; it does not skip these builds.

## Login and acceptance route evidence

Local proxy implements **POST /_local/admin-session**, loopback-only unless explicitly configured otherwise. GET requests to both `http://127.0.0.1:8091/_local/admin-session` and `http://127.0.0.1:6024/_local/admin-session` returned 405, confirming route availability without issuing a session. The endpoint may refresh/bootstrap credentials; call it only during authorized acceptance, keep token in process memory or a private 0600 file, and never log it. Existing runtime test-session files were not read.

For native ownership filtering, actual running Chat environment sets `LAZYMIND_CORE_API_URL=http://127.0.0.1:28001`. `core_api_client.py` sends current X-User-Id and internal service token directly there. Core handleAPI's permission array is catalog metadata for gateway RBAC, not an in-process account permission gate; direct Core request runs dataset/source READ authorization in ResolveResourceDataset. Therefore local native qa.read calls do not pass through the document.read gateway route. If another deployment points Core API URL at the public proxy instead, that assumption is invalid and must be tested.

Using the running Chat's headers privately, direct Core GET /datasets returned 200. POST /documents:filterActive with empty items returned 404 because the running old Core does not contain this feature. No token was printed. After deployment, verify it returns a well-formed items result, then test active and deleted document pairs through native semantic/keyword/parent/window/image/citation paths; a health check alone is insufficient.

Follow-up code correction: native headers have no tenant, so the worktree now resolves legitimate internal requests through Auth's current `identity/internal` endpoint, shared with the management runtime. Nondefault tenant list/get/original read/filter and failure/cross-tenant rejection passed real-header HTTP + SQLite regressions; this is not yet deployed acceptance. Include a nondefault-tenant account in acceptance and verify Auth and Core come from this worktree, since old Auth lacks this current-identity endpoint.

Recommended acceptance sequence: verify 8090 unchanged; verify all new process/build/source paths and isolated DBs; local login; MCP tool enumeration and positive whitelist; create test library/folder; multipart local original upload and independent bindings; native original read/search; metadata and same-library move; delete/cross-library move confirmation including reject/accept; stale in-flight deletion visibility and restart persistence; Work main/subagent artifact import; verify waiting historical workflow unchanged; verify 8090 listener/health again. Preserve all acceptance IDs and results in a private report, with no credentials.
