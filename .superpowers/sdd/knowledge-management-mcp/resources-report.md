# Core resource commands implementation report

Worktree: `/Users/dengyuang/.codex/worktrees/knowledge-management-mcp/LazyMind_dev`.
Scope: P1/P2/P4 Core resource commands, page command reuse, scene/version/receipt schema. No deployment, application restart, commit, or primary-checkout edits.

## Shared contracts

`doc.NewResourceService(ResourceServiceDeps)` takes an injected GORM database, optional strict source policy/provision functions, `OriginalService`, and bounded execution budget. It exposes:

- `CreateKnowledge(ctx, op, CreateKnowledgeRequest)`
- `CreateFolders(ctx, op, CreateFoldersRequest)`
- `UpdateMetadata(ctx, op, UpdateMetadataRequest)`
- `Move(ctx, op, MoveResourcesRequest)`
- `Delete(ctx, op, DeleteResourcesRequest)`
- `Upload(ctx, op, []ImportOriginalRequest)`
- `Snapshot(ctx, op, []ResourceRef)` for destructive source snapshots.
- `SnapshotMove(ctx, op, MoveResourcesRequest)` binds the full source trees and exact destination library/folder, without expanding the destination library.
- `HasCommittedMove(ctx, op, request)` validates matching durable results before runtime confirmation replay; a header with no committed/unconfirmed child does not qualify.
- `ReplayOperation(ctx, op, action, canonicalInput)` and `ResourceInputDigest(canonicalInput)` allow upload retry before network/local acquisition regenerates file references.

`ResourceRef` identifies kind and library plus folder/document ID. `ResourceOperationContext` contains trusted caller, operation key, optional canonical input digest, expected snapshot, optional library scope, and `ValidateExecution(ctx, tx)` plus `ValidateBeforeCommit(ctx)` callbacks. Nil library scope is unrestricted; an empty non-nil scope allows no existing library. `ValidateBeforeCommit` refreshes external account status after preparation, before opening the transaction. `ValidateExecution` runs inside each mutation transaction before the receipt read/resource write. Batch headers also use this callback. Runtime supplies the actual run/attempt locks and validation.

Snapshots also return preview resource names/full logical paths and exact move destination, using the source rows and bounded ancestor reads.

Results contain action/status, success/failure/skipped/unconfirmed counts and per-resource identities, prior identity, old-to-new mapping, parent, processing/cleanup status, entry URL and structured error. Operation receipts are keyed by tenant/user/operation key, bind action plus canonical input hash, and contain real committed IDs.

`ResolveResourceDataset(ctx, tx, caller, id, action)` and `PrepareResourceAuthorization(ctx, caller, requests)` are shared with `OriginalService`. ACL rows are read through the injected transaction; owner checks short-circuit group resolution. External source policy and group membership are prepared before opening the transaction. WRITE implies UPLOAD, matching existing ACL semantics. Tenant bounds and library scope remain enforced. Whole-library deletion currently requires owner; this deployment has no separate explicit administrator-delete grant policy.

## Behavior and invariants

- Knowledge creation preserves page algorithm/parser/options contract and optional explicit page ID; scene defaults to general. New library owner and existing dataset_maintainer grants are committed in the same transaction, shared by MCP and the page. The name is validated and duplicate conflicts are explicit.
- Folder paths are relative. Existing same-name entries conflict rather than silently merge. Root is represented by an empty parent ID.
- Metadata changes enforce kind-specific name/description/scene/tags allowlists. Omitted tags are unchanged; empty tags clear. Missing resources never turn PATCH into create.
- Same-library moves preserve IDs and use conditional dataset/document versions. Self/descendant cycles and destination conflicts reject. Page name/tags plus parent updates share one transaction and metadata validation helper.
- Cross-library moves prepare immutable originals outside transactions, allocate new folder/document IDs, bind originals and processing task metadata inside the root transaction, then logically remove the source. Empty folders and complete ID mappings are retained. Processing submission and source cleanup happen after commit.
- Independent roots commit separately. A bounded durable header freezes normalized root membership and original parameter digest before a root disappears. Stable child keys include parent key hash/root index and bind the full original batch digest. Each successful child receipt is in its resource transaction. Failed roots leave their entire source tree intact. A final parent receipt summarizes partial results. Header/child receipts support recovery after interruption before the parent summary. Reusing the operation key with changed inputs, including a shorter retry, conflicts before new effects.
- Delete logically hides resources and fences processing state in the transaction; task history is retained. Cleanup runs after commit, with submitted/failed status distinct from resource state.
- A lost commit acknowledgement triggers an independent bounded receipt lookup. A saved receipt returns the real IDs. If no outcome can be confirmed, the result/error says UNCONFIRMED and includes candidate resources and verify-before-retry guidance. Prepared original copies are retained in this case, never deleted as if rollback were certain.
- Subtree/ancestor enumeration and copy preparation have explicit limits and return OPERATION_TOO_LARGE details. Cross-library execution is disabled unless deployment budget is explicitly marked calibrated.

## Schema and migration coverage

ORM additions: dataset scene/resource_version; document resource_version; resource_operation_receipts.

New unmerged dev migration `20261020034257_knowledge_resource_commands` has PostgreSQL and SQLite up/down paths. Existing v0_3 aggregate was updated without renaming or adding an aggregate. The separate `20261020130000_document_ownership_admission` dev migration supplied by the originals worker is consolidated into the same aggregate up/down; schema equality also covers its two new tables. Both dialects include version triggers covering legacy document inserts, meaningful metadata/location changes and deletes. Redundant timestamp/statistic writes do not endlessly advance versions; explicit CAS increments do not double increment the document version. SQLite recursive triggers were exercised.

Migration tests compare aggregate versus complete dev paths, retain existing data, test receipt uniqueness/defaults/version behavior, and validate down restoration. PostgreSQL verification used an ephemeral `postgres:16-alpine` container with no application/runtime mounts; it was removed after verification. No 8090 or 8091 runtime service was used.

## Validation

- `go -C backend/core test ./doc ./acl ./common -count=1` passed after the partial-root and commit-ambiguity changes.
- Focused tests cover original preservation/full tree mapping, ancestor deduplication, name conflict concurrency, metadata rollback on failed move, empty-root parent, stale descendant snapshots, missing-original root rollback, independent-root partial/replay/changed-input conflict, transaction failure with no receipt, strict pre-transaction source policy, tenant/scope/field permissions, whole-library owner protection, execution guard timing, and lost commit acknowledgement.
- Commit-ambiguity tests wrap a real SQL transaction: commit then return unexpected EOF (receipt reconciliation); rollback then return unexpected EOF (unconfirmed, never false definitive success).
- PostgreSQL and SQLite migration tests passed again after ownership-admission integration: `MIGRATION_TEST_POSTGRES_DSN=... go -C backend/core test ./migrations -run TestResourceCommands -count=1 -v` (both dialects executed, no skip). Ephemeral Postgres was removed.
- Final `go -C backend/core test ./doc ./acl ./common -count=1` passed (doc 3.397s, acl 0.416s, common 1.715s).
- Final `go -C backend/core test -race ./doc -run TestResource -count=1` passed (3.246s).
- Final fixture coverage additionally validates preview names/ancestor paths, a header without committed children cannot bypass confirmation, creator membership in the creation transaction, fresh account rejection before transaction entry, and normalized parent/child selection budget.
- `git diff --check` passed for the resource, ACL, ORM and migration paths.

## Remaining deployment/acceptance boundaries

- Cross-library budget calibration and real 8091 end-to-end acceptance are outstanding; the production gate remains closed by default. Fixture values are not claimed as calibrated operational limits.
- External algorithm KB provisioning precedes the Core create transaction. If provisioning succeeds but Core commit definitively fails, an external orphan may remain. No speculative deletion is issued because a deterministic same-key concurrent create could own that KB. This requires a scoped reconciliation policy before claiming no-orphan lifecycle guarantees.
- Rich page dataset edits (algorithm/parser/cover options) retain their specialized existing path with write permission and version CAS. Legacy document type/file metadata remains outside MCP's public allowlist and is a separate page update step when combined with ordinary metadata. The public MCP command paths do not expose those fields.
- Cleanup status `submitted` records the existing cleanup handoff, not an independently verified physical search-index garbage-collection completion. Failed cleanup is retained in the result/receipt; this work does not introduce an asynchronous retry platform.
- Account/run lifecycle after potentially long preparation depends on the supplied fresh-account and transaction execution guards; Core local ACL and resource versions are rechecked in the resource transaction, while the external source-policy snapshot cannot be made transactionally atomic with a remote service.
- Bounded ancestor/statistic traversal can reject a large same-library operation too; the implementation does not silently launch unbounded scans.

## Follow-up review: confirmed move snapshots and deadline reserve

Fixed the review P1 in the independent-root wrapper. Each root now carries the durable header's source-root and destination digests into `moveAtomic`; the actual transaction checks them before any resource CAS/write. Its freshly prepared snapshot remains an additional check and cannot replace the confirmed header scope.

A second reproduced window existed in initial confirmation handling: an independent `SnapshotMove` check followed by a new destination capture could rebind execution to a changed destination. Confirmation comparison now uses the exact source trees and target library/folder rows captured for preparation/header construction. The root transaction also compares the captured complete target rows to the rows it is about to use.

Regression hooks mutate state after the wrapper's checks but before the atomic mover captures its initial tree. The old code moved newly added descendants, renamed roots and a changed destination; all three now return CONFLICT and keep the changed root at its source. Additional single-root and two-root tests reject a destination change during initial preflight capture. The independent runtime reviewer rechecked the transaction constraints and confirmed the P1 is removed.

Cross-library preparation reserves three seconds from a request deadline for commit/receipt handling. Both the estimate and actual preparation context use `min(MaxPrepareDuration, deadlineRemaining - 3s)`. An exhausted preparation window returns OPERATION_TOO_LARGE with the reserved receipt time in diagnostics and performs zero resource moves. The short-deadline test failed before the fix and passes afterward.

Fresh verification after these fixes:

- `go -C backend/core test ./doc -run TestResource -count=1` passed.
- `go -C backend/core test -race ./doc -run TestResource -count=1` passed (3.969s).
- `go -C backend/core test ./doc -count=1` passed (3.059s).
- No migrations or runtime environments were changed in this follow-up.

## P7 local budget calibration

Added explicit opt-in `TestResourceBudgetCalibration` in `resource_calibration_test.go`. Three same-volume direct disk SQLite runs moved 100 real original files plus 20 empty folders (121 resources including the root, 32 MiB total). Real original acquisition, binding, hashing, copying and local source cleanup were used with stored processing; no parser/index success mock was treated as processing performance. Actual resource version triggers were installed.

Move totals were 868.753 / 601.420 / 566.576 ms; preparation including copy 711.499 / 529.700 / 496.139 ms; root transactions including commit 44.961 / 39.522 / 36.733 ms. All copied originals passed SHA-256 checks. Catalog, merged metadata, three-dimensional aggregate and library stats were also measured and validated. Default test invocation skips; explicit opt-in passes. Temporary data was removed.

Details and proposed conservative 8091 live-check candidate (`128 resources`, `32 MiB`, `15s preparation`, `4 MiB/s estimate`) are in `resource-budget-calibration.md`. This is a direct SQLite, mostly warm-cache, single-client result with in-process source policy responses; it is not sqliteproxy, cold-disk, network-auth, parser, index or concurrent deployment acceptance. No service was started/stopped and no production gate was enabled.

## P7 live creation failure and correction

The first 8091 public create returned INTERNAL. A second call through the shared page handler, using only a new acceptance fixture name, exposed the actual DocServer error: `invalid algo_id: None`. The new service had initialized empty Ext for public tool requests, while the real upstream requires the existing default algorithm. No Core library was created by either failed call.

CreateKnowledge now resolves missing algorithm options to the existing `general_algo` / `General` defaults, fetches the corresponding parser configuration and persists the choice. Page-provided explicit algorithms remain unchanged. Duplicate-name rejection still precedes external parser/provisioning requests.

Red/green regression covers tool default, page default and explicit page algorithm. Focused `TestResource|TestCreateDataset` and race `TestResourceCreateKnowledge|TestCreateDataset` pass. Public acceptance is pending the parent's coordinated Core restart; this agent does not restart services.

## P7 public resource acceptance complete

After READY2, public creation and all assigned resource operations passed against 8091's actual sqliteproxy deployment. Coverage includes 11 tools, multi-library pagination/aggregation, metadata, root movement, complete cross-library trees/new IDs/source invisibility/empty folders, stable-invocation receipt replay and changed-input conflict, delete, independent-root partial outcomes, 129-resource rejection, and 32 MiB + 1 byte rejection with zero migration. Three serial 121-resource / 32 MiB moves took 820.960 / 791.301 / 806.783 ms. Parent requested fixtures retained for browser verification. See resource-live-acceptance.md for methods, exact statistics, boundaries, fixture IDs and private evidence location. Full P7 acceptance remains the parent’s responsibility.
