# Knowledge management MCP implementation ledger

Branch: `dya/knowledge-management-mcp`
Base: `81683cfbea2f0a1cb0c696327abb77e7e412df37`
Worktree: `/Users/dengyuang/.codex/worktrees/knowledge-management-mcp/LazyMind_dev`

The user explicitly authorized this implementation, including ordinary writes without confirmation and deletion/cross-library confirmation only. The primary checkout's unrelated dirty files remain untouched.

| Stage | Current evidence | Deployment acceptance |
| --- | --- | --- |
| P1 contracts/data | Nine internal management tools, external get/search retained; typed requests/results; resource versions/receipts/scene; SQLite/PostgreSQL incremental versus aggregate migrations and down paths pass | Applied to actual isolated 8091; historical resources remain readable |
| P2 shared queries/commands | Global pagination, omitted/root parent distinction, aggregate limits, shared page commands, strict permission/source checks, rollback/conflict tests pass | Live public MCP operations, pagination and actual browser metadata save passed |
| P3 originals | Immutable bytes, four adapters, original persistence/binding, real Work dual-writer owner namespace and MIME regression pass | All four sources verified against actual persisted bytes/documents; processing/deletion checks passed |
| P4 consistency | Per-root move/partial replay, tombstones, publication fence, bounded historical ownership admission, fail-before-commit and lost-response tests pass; resource race tests pass | Measured 121 resources/32 MiB on deployment; live source invisibility/old-reference rejection passed |
| P5 runtime | Five-minute execution credentials, fresh account check before transaction, row locks/live run guard in transaction, confirmation UI/details, durable answered cards and receipt-first replay pass | Actual approve/reject/snapshot/cancel and disabled-account rejection passed; Writer/Image completed |
| P6 consumers | Positive whitelist/empty semantics, native five reads and pinned shims, CLI bytes/roots, structured result/error/metadata/progress support pass | Actual CLI roots and host bridge passed; current PPT materials and pinned legacy Workflow both completed |
| P7 release | Targeted Go/Python/frontend checks and two independent review rounds completed; Core builds on Go 1.25.11 | Backup/deploy, required changed-boundary end-to-end, default release and explicit rollback implemented |

P1–P7 implementation and the required changed-boundary acceptance are complete for isolated 8091. Default management enablement is centralized for public registration, Chat and Work; explicit false remains a rollback switch. Cross-library movement still requires deployment calibration. The 8091 private configuration now retains its measured budget and existing Artifact V2 opt-in (the Artifact V2 global default is unchanged). Ten SQLite backups passed integrity checks before deployment; restoration was not rehearsed. Complete Core/Chat/frontend sources are from this worktree. 8090 was not changed.

Actual public MCP, all four upload sources, MainChat, Work Writer/Image, current PPT materials, pinned legacy Workflow, CLI roots, page editing, confirmation approve/reject/snapshot/cancel, disabled account and historical read compatibility passed. One Research task performed create/list successfully but was cancelled after a later model round stalled; this is retained as a model/runtime limitation rather than reported as a successful full run. PPT acceptance covers the changed material-collection boundary, not a new full presentation export.

## Verified limits and failures

- Go 1.26.1 macOS arm64 linker panics on the full Core binary, including external link mode. Go 1.25.11 builds and runs the main package.
- Five existing project/group tests fail identically at the exact base commit. See `baseline-project-verification.md`; this is not a green full-suite claim.
- Feishu sidecar production startup fixture has a timeout in the full suite; not yet established as a code regression or baseline.
- Homebrew Node references a missing llhttp dylib. Bundled Node runs the frontend targeted tests and TypeScript checks.
- External KB provisioning precedes the Core creation transaction and can leave an external orphan if the Core transaction fails. No unsafe compensating delete was added.
- Cross-library movement remains closed until deployment budgets are supplied following calibration. Progress notifications currently report actual invocation start/end; they do not claim per-file percentage accuracy.

Detailed evidence: `resources-report.md`, `originals-report.md`, `mcp-client-report.md`; root integration logs are under `/tmp/kb-*`.

## Latest integrated verification

- Current Python/LazyLLM tree: 38 files, 704 passed, 0 skipped (three existing deprecation warnings); exact list and command in mcp-client-report.md.
- Current capability, knowledge runtime, document service and migration packages passed. Common catalog check exposed one new spelling mismatch; reuse of the existing message fixed it and full common suite passed.
- CLI coreapi/mcpbridge tests and scoped frontend TypeScript check passed; actual frontend production rebuild and metadata save verified.
- Current focused Go vet (capability/runtime/doc/common) passed. Broadened chat vet reports an unchanged organizer_acceptance_test.go cancel-path warning at lines 189/216; not a new source change and not a clean broad-vet claim.
- Actual HTTP SQLite proxy checkpoint fault reproduced with concurrent document/receipt commit/rollback, then fixed via existing transaction helper; focused tests and race passed. Current 8091 Core includes this fix.
- Checkout tool-state files came from the isolated model-schema diagnostic, not deployed services. They were preserved privately and removed from checkout; actual Research/Writer/Image scopes were verified under isolated runtime.

## Final release verification

- Final deployed list/get reports `ready` for the actual indexed fixture; missing Work version returns `NOT_FOUND` and deleted/moved source tasks are absent from the running list. Browser screenshot refreshed with no stale 99+ processing banner.
- Default/explicit rollback/invalid setting tests, trusted Chat config replacement and Work explicit-empty selection tests pass. No client-supplied system configuration is retained.
- Current LazyLLM dependency is local commit `e05839b005ee0ed8cd119bbc0ed7b209cc44464f` on `dya/knowledge-management-mcp-client`; it must be published before another checkout can fetch the new submodule revision. No remote publication is claimed.
- Test libraries/conversations and dedicated Workflow fixtures remain identifiable by KM-E2E names and the individual reports. No user library was removed for cleanup.

- Final release checks: targeted Core release tests and Chat/Work default/rollback tests passed; focused vet passed. The local runtime manager full suite passed with bundled Node (65.875 s); its initial Homebrew Node run aborted because of the already diagnosed missing dylib. Actual released service returns 11 public knowledge tools, 9 management methods and 5 native KB methods; 8091/8090 HTTP 200, unchanged 8090 Caddy PID 7676.
