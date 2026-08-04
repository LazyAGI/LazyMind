# Phase-one LazyMind acceptance report

Status: complete. Authority: PRs #492–#502 plus the PR11 acceptance change.

## Twelve executable acceptance clauses

| # | Evidence |
|---|---|
| 1 | Attempt Context schemas and sanitization tests reject model configuration and Host secrets. |
| 2 | `workflow/dispatch_test.go` proves production queued dispatch never calls the fixed algorithm endpoint. |
| 3 | Workflow Client boundary tests confine HTTP and persistence compatibility access to named adapters. |
| 4 | Runtime golden fixtures plus `workflow/attempt` FakeExecutor tests cover serial, parallel, retry, rewind, resume, races, and Artifact facts. |
| 5 | Workflow manager/Driver regressions and frontend Vitest cover approval, stop, handoff, Driver, synthetic turn, SSE, and Panel behavior. |
| 6 | Authoring v1 tests prove fixed Skill snapshots, deterministic diagnostics, and model-free publication. |
| 7 | `baseline-manifest.json` binds every golden scenario to a real production test symbol; contract tests execute them. |
| 8 | `python3 scripts/check_workflow_naming.py` rejects public legacy names; ORM/rolling tests cover physical mappings. |
| 9 | `workflow/attempt` tests cover lease expiry, fencing, reclaim, idempotency, and terminal competition. |
| 10 | Frontend projection/Event Stream tests cover snapshot, patch, replay, resync, gaps, progress, and unknown versions without polling. |
| 11 | Workflow Client/File Adapter tests pin attachments to resource revision/hash and sanitize Attempt Context. |
| 12 | Migration contract tests cover expand-only schema, old binary writes, capability fallback, and unbackfilled rows. |

## Required commands

All passed on the PR11 integration tree:

```text
cd backend/core && go test ./... -count=1
PYTHONPATH=algorithm:algorithm/lazyllm python3 -m pytest algorithm/tests/chat/workflows algorithm/tests/test_workflow_*.py algorithm/tests/chat/test_sensitive_filter_skip.py -q
npm --prefix tests/frontend test
pnpm --dir frontend run build
python3 scripts/check_workflow_naming.py
make lint
```

## Compatibility usage and deletion ledger

Compatibility paths remain observable and rollback-safe; none is deleted without a
zero-use observation window. The counters are `workflow_legacy_client_hits`,
`workflow_legacy_db_hits`, `workflow_legacy_route_hits`,
`legacy_policy_hits` and legacy dispatch hits.
The test baseline is zero for every default-on path; rollback tests deliberately
increment the associated counter.

| Compatibility path | Default | Removal condition |
|---|---|---|
| Python legacy Workflow client/DB adapter | off | production counters remain zero for the declared observation window |
| legacy API route aliases | off/flagged | route counter zero and all clients advertise `workflow.v1` |
| legacy duplicated policy prompt | off | policy comparison stable and `legacy_policy_hits` zero |
| fixed SubAgent endpoint and `plugin_run_outbox` | rollback only | queued dispatch healthy and legacy dispatch hits zero |

Deletion is intentionally deferred until those runtime conditions are observed;
the authoritative writers and all new traffic already use Workflow v1.
