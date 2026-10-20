# P3 Original acquisition and binding

Implemented in `doc/original_service.go`, `original_remote.go`, `original_processing.go`; local import and academic acquisition now use shared service. No public arbitrary-path read API; trusted ImportLocalFile bridge remains compatible in shape.

## API integration
- `NewOriginalService(OriginalServiceDeps{DB, SubmitProcessing, ResolveWorkArtifact})`.
- `AcquireOriginal(ctx, caller, AcquireOriginalRequest{Filename, ContentType, Reader, ...})`: trusted stream/host upload channel.
- `AcquireUploadedOriginal(ctx, caller, fileRef)`: existing UploadedFile or completed owner/tenant/expiry-checked temporary UploadSession; Chat `fr_` is rejected and must be resolved by host.
- `AcquireRemoteOriginal(ctx, caller, AcquireRemoteOriginalRequest{URL, DisplayName, ExpectedMIME})`.
- `AcquireWorkOriginal(ctx, caller, artifactID, revisionID)`: requires owning resolver, fixes actual revision.
- `ImportOriginal(ctx, caller, ImportOriginalRequest{FileRef, DatasetID, ParentID, DisplayName, Tags, DataSourceType, ConflictPolicy})` returns committed Document/Task and separate processing submission status. Submission failure retains real IDs and does not return a resource-creation error.
- `PrepareDocumentOriginalBinding`/`PrepareOriginalBinding` copy and hash before transactions. `prepared.SetParentID` sets planned folder; `BindOriginalTx` checks current source/target and uses caller transaction. `CleanupPreparedBinding` only after rollback. `SubmitOriginalProcessing` only after commit.
- `capability/bootstrap/originals.go` provides `newCapabilityOriginalService(db)` with Work artifact owning API adapter. Parent must wire this constructor into runtime resources and upload MCP adapter.

## Guarantees and tests
- Immutable source record; each binding has separate storage copy, Document, Task and bound UploadedFile. Source expiry/removal does not remove bound original.
- Original bytes hashed/preserved; bounded stream, cancellation, PDF signature+EOF, Office container, UTF-8 text, JSON validation.
- Dedicated HTTP transport (no inherited proxy, Bearer or Cookie), address checks at actual dial, redirects bounded/revalidated; private/reserved/DNS targets blocked. Content-Length/truncation/status/content-encoding checked.
- Explicit arXiv `vN` retained in all mirror URLs. Academic identity/version/link logic remains academic.
- Real SQLite tests cover independent bindings, failed processing retains Document, owner/tenant/expiry, malformed bytes, remote oversized/truncated/login/partial response, DNS/redirect boundaries, explicit transaction rollback with one connection, cancellation, fixed Work revision and attachment session conversion.
- `go test ./doc ./academic -count=1` passed (2026-10-20). Focused original tests passed again after attachment conversion additions. Bootstrap owning-artifact adapter tests now pass (fixed published revision, owner/tenant and blob metadata).

## Boundaries / remaining integration
- Existing legacy temporary text upload normalization predates this service. New acquisition never normalizes bytes; a legacy attachment represents the bytes currently stored by that channel.
- Existing parsing pipeline still uses configured process store DB; acquisition/binding use injected DB. SubmitProcessing injection isolates tests and other runtime integrations.
- Unsupported original formats fail explicitly; current allowed file family is PDF, Office, ZIP, TXT/Markdown/HTML/CSV/TSV/JSON/XML. ZIP is preserved, not expanded by new generic import.
- Generic HTTP success/error pages cannot be semantically distinguished when the explicitly requested source is itself HTML; PDF/Office mislabeled login pages are rejected.
- Existing browser non-shared task binding still exists for compatibility; resource/MCP/local/academic paths use the shared service.


# P4 Publication and native visibility

- `publication_fence.go` and task publication paths validate active dataset/document/task, original document ownership and ResourceVersion in the write transaction. Zero-row updates and reparse/transfer transaction errors no longer report success.
- `visibility.go` backfills only validated readonly KB associations with unique Core dataset ownership; existing Core rows/tombstones win; unknown originals and cross-tenant association conflict fail closed. Endpoint remains unavailable until successful backfill.
- Native semantic retrieval (before rerank and after context expansion), keyword retrieval, parent/window nodes, document-position candidates, image and citation output filter through current Core ownership. No cross-call visibility cache.
- Parent integration: initialize `doc.InitializeDocumentVisibility` after migrations, register `doc.FilterDocumentVisibilityHTTP` at `/documents:filterActive`. Historical fixtures prove safe backfill before enabling strict mapping.

# P6 Host transfer and native compatibility

- `doc.UploadOriginalHTTP`: POST `/knowledge/originals` behind authenticated `document.write`, exactly one multipart field `file`, raw OriginalRef JSON response. It receives bytes and never opens a caller-supplied Core path. Parent owns route registration.
- CLI MCP bridge intercepts `knowledge.document.upload` source `local_file`, requires host-advertised MCP roots, authorizes every path before any byte transfer, resolves symlinks and opens through `os.Root`, checks regular-file size, streams authenticated multipart bytes, and replaces source with `uploaded_file`/`file_ref`. No roots means `LOCAL_FILE_BRIDGE_REQUIRED`; no inferred home/cwd grant.
- Core API transport supports bounded multipart streaming and reopens the body from the same authorized descriptor after credential refresh; upload redirects are rejected. Existing requiredTools get/search unchanged.
- KBToolkit normal/public registration now contains exactly five reads. A validated pinned Workflow explicitly declaring `kb` without the new management group receives three legacy query shims on that instance, preserving method names/schema and shared Core services. Normal Chat/new group registrations never mutate globally.
- New PPT source declares `knowledge_management` with `kb`, uses `knowledge.list`, `items`, `id` / `resource_ref.knowledge_base_id`, and explicit kb_ids for search. Existing fixed revisions remain intact.
- Native Host local-file transfer and management-group registration are owned by mcp_client agent, not this report's implementation.

# Latest verification (2026-10-20)

- Core focused original/security/HTTP/publication/visibility/academic-version/Work-adapter tests passed in doc, academic and capability/bootstrap.
- Common direct-API audit passed. Common constructor audit now reports only the other worker's `provision knowledge base` error; resources notified to catalogue it. Owned original/visibility/publication errors are in `common/error_catalog_knowledge_originals.go`.
- Python KB ownership/toolkit/search/subagent focused suite: **144 passed, 8 deselected**, three existing dependency deprecation warnings. Includes exact public5, old fixed-workflow shims, normal-instance isolation, and new PPT selection regressions.
- CLI `go test ./... -count=1`: coreapi, mcpbridge and all other packages passed except DSH bundled package-manager fixture in adapters/mcpclient, whose host Homebrew Node cannot load `libllhttp.9.3.dylib`. This is an environment failure; no runtime/dependency installation was attempted.
- CLI auth-refresh stream replay checks exact bytes on both requests. Original upload ownership/no-Document, root escape/symlink escape/relative-path rejection, exact byte transfer and redirect rejection pass.
- `GOOS=windows GOARCH=amd64 go test -c ./internal/mcpbridge` passed (binary written outside repo).
- `git diff --check` passed across shared worktree. No service deployment or live account/remote download E2E performed.

# Remaining precise limits

- Published Work artifact resolver rejects a requested revision newer than published head, but the current artifact model has no per-revision historical-published flag; explicit older revisions are accepted under the owning API. A history-of-publication check would require owning model support.
- Work personal owner namespaces are now supported through the actual owning API and Main Chat/SubAgent producer semantics; explicit tenant namespaces remain strict. See review fixes below.
- Existing completed temporary text uploads may already have normalized bytes; new source acquisition preserves the stored byte stream exactly. Legacy UploadedFile records lacking content_hash are returned as such; bind computes and validates a new target hash.
- Formats currently supported: PDF, doc/docx/xls/xlsx/ppt/pptx, ZIP, txt/md/html/htm/csv/tsv/json/xml. Other legacy suffixes such as markdown/jsonl/pptm fail explicitly. New generic ZIP imports preserve ZIP, do not expand it.
- Backfill executes on initialization and may refuse ambiguous/corrupt historical associations. This is fail-closed, not a blanket historical hide. Legacy originals outside controlled upload root cannot be moved through the new byte binder.
- Existing parsing worker still uses configured process store DB; new binding uses injected DB and tests inject submission. Native filtering blocks stale index reads but does not cancel all already-running external index work.
- Host MCP roots are the CLI authorization source; hosts without roots support need an explicit future host-grant mechanism before local-file transfer can operate. Windows amd64 mcpbridge tests cross-compiled successfully; no Windows runtime E2E.

# Owned paths (worktree-relative; prefix /Users/dengyuang/.codex/worktrees/knowledge-management-mcp/LazyMind_dev/)

- backend/core/doc/original_service.go, original_remote.go, original_processing.go, original_http.go and their original_*_test.go tests
- backend/core/doc/import_bridge.go; narrow shared edits task.go and task_external.go
- backend/core/doc/publication_fence.go, publication_fence_test.go, visibility.go, visibility_test.go
- backend/core/academic/acquirer.go, imports.go, academic_test.go, acquirer_version_test.go
- backend/core/capability/bootstrap/originals.go, originals_test.go
- backend/core/common/error_catalog_knowledge_originals.go
- algorithm/lazymind/chat/engine/tools/infra/kb_ownership.py; algo/search_kb.py; kb.py; lazy_kb.py
- algorithm/lazymind/chat/service/document_selection.py
- algorithm/lazymind/chat/engine/subagent/runner.py: only legacy KB instance selection; other edits owned by mcp_client
- tests/algorithm/chat/test_kb_ownership.py, test_kb_toolkit.py; test_subagent_runner.py: only two appended compatibility/PPT tests
- local/lazymind-cli/internal/coreapi/original_upload.go, original_upload_test.go, client.go streaming support
- local/lazymind-cli/internal/mcpbridge/original_upload.go, original_upload_test.go, bridge.go transfer hook
- workflows/ppt-workflow/scenario/state.yml


# Data review fixes (2026-10-20, completed)

Three reproduced defects have been fixed rather than left as compatibility limits.

1. **Late external publication cannot become a new historical document on restart.** Failed batch/transfer publication records exact dataset/external IDs in `rejected_document_publications` after its transaction rolls back. Backfill ignores those IDs and admits historical associations only within a persisted per-dataset ID boundary in `document_ownership_backfills`; completion is durable, and retry after interruption retains the same boundary. Before the first migration, associations matching a Core-managed original/parse path remain subject to the publisher, including tombstones. An unbound tombstone lacking enough original metadata makes unknown historical associations ambiguous, so they are skipped rather than resurrected. This does not globally hide validated standalone historical records.
2. **Real owner namespaces work.** Owning `artifact.GetRevision` validates the user; Main Chat/SubAgent revisions whose storage namespace equals that owner can be acquired into the caller's current tenant. This namespace is not treated as a shared tenant grant. Explicit artifact tenant namespaces must still equal the caller tenant. The resulting acquired reference is itself bound to the caller tenant and cannot be reused across tenants.
3. **Real artifact content classifications work.** Blob-backed revisions derive MIME from ArtifactBlob; `revision.ContentType=file` is no longer passed as a MIME. Inline text/json revisions preserve their actual immutable JSON payload as JSON bytes and retain matching SHA256.

Regression evidence:
- Before fix, both late-add/delete/rejected-publication/restart cases (first migration and previously initialized) reproduced resurrection, and a completed migration accepted a newly arriving orphan.
- New tests assert durable rejection survives transaction rollback; completed checkpoint excludes future associations; failed checkpoint retry cannot widen its historical range.
- Original reviewer overlay `TestReviewLatePublicationDoesNotResurrectDeletedOriginal` now passes unchanged.
- Tests call actual `artifact.DualWriteMainChat` and `artifact.DualWriteSubAgent` for file, text and JSON outputs. Generated PDF bytes/hash import under default tenant, owner spoofing fails, acquired references reject another tenant, explicit tenant rejection remains covered, and inline bytes/hash are preserved.
- Full `go test ./doc ./academic ./capability/bootstrap -count=1` passed. Final focused visibility/publication/Work tests passed. Common audit found only a concurrent runtime guard alias; parent added it and reports common tests green.
- Resources agent integrated `20261020130000_document_ownership_admission.up/down.sql` into release aggregate and verified SQLite/PostgreSQL schema equality and downgrade. No deployment or live database changes.

Additional owned files: `backend/core/common/orm/document_ownership_admission.go`, the above dev migration pair; `doc/document_test.go` adds only these models to the fixture. Resources owns release aggregation/schema equality tests.

## Native HTTP account tenant correction (2026-10-20)

The production native Chat caller sends `X-User-Id` and its configured internal token, without a tenant header. Its runtime `LAZYMIND_CORE_API_URL` targets Core directly. The Core `handleAPI` permission argument is gateway metadata; this local native call does not traverse the public gateway's `document.read` rule. Consequently native `qa.read` users are not blocked by that gateway rule, but the previous request-to-caller conversion incorrectly normalized missing tenant to `default`.

The existing runtime Auth identity lookup is now shared as `common.GetCurrentIdentity`. Internal requests validate the service token and resolve the enabled account's current tenant, role and permissions through Auth `/user/{id}/identity/internal`, ignoring supplied tenant/role claims. Gateway-authenticated requests retain their already-injected identity headers. No tenant is inferred from requested datasets, no identity is cached across requests, and capability calls with a trusted Principal are unchanged.

The shared document caller handles this for visibility and native readers. Catalog list/get, document read/ensure-parsed and metadata aggregation propagate identity resolution failure explicitly, so their legacy service-level UserID fallback cannot turn a failed lookup into a default-tenant request. The legacy document authorization boundary also checks the resolved tenant before ACL/source policy.

Tests use real HTTP requests with the native header shape, an Auth test server, a source-policy test server and SQLite. They cover nondefault tenant visibility and original text reads, catalog list/get, same-owner foreign-tenant exclusion, forged tenant and invalid internal-token rejection, disabled/unavailable/mismatched account rejection, and source-policy propagation of the resolved tenant. Common tests cover plain/enveloped Auth responses and missing credentials.

Validation:
- Red reproduction: `/tmp/knowledge-tenant-red.log`.
- `go test ./doc ./common -count=1` passed (17.126s / 1.862s), `/tmp/knowledge-tenant-doc-common.log`.
- `GOTOOLCHAIN=go1.25.11 go test . -run 'TestKnowledge|Test.*Execution|Test.*Identity' -count=1` passed (1.393s), `/tmp/knowledge-tenant-root125.log`.
- Final expanded HTTP regression, including invalid service credentials and cross-tenant catalog/read requests, passed (15.568s), `/tmp/knowledge-tenant-final-http.log`; scoped `git diff --check` passed.
- Go 1.26 full-Core linking reproduced the pre-existing linker SIGSEGV; Go 1.25.11 was used as already documented in progress.md. This is not a deployed-service test.

Files for this follow-up: new `common/current_identity.go`, `common/current_identity_test.go`, `doc/visibility_http_test.go`; identity extraction only in `knowledge_runtime.go`; narrow caller/error/tenant changes in `doc/scan_source_access.go`, `visibility.go`, `dataset.go`, `document.go`, `processing.go`, and `system_query.go`. Other changes in those shared files remain their prior owners' work. No service restart, backup execution or live database mutation.

## P7 real 8091 acceptance and observed-state correction (2026-10-20)

Parent deployed the complete worktree to isolated 8091 after native consistent backups, enabled management and Artifact V2 only for this acceptance, and coordinated Core/Chat restarts. This agent did not restart services or touch 8090. Credentials and raw API evidence remain private in `/Users/dengyuang/.local/share/lazymind-acceptance/knowledge-originals-20261020` (0700 directory; script/session/evidence files private). No tokens or passwords are in this report.

Actual public MCP acceptance so far:

- `remote_file`: downloaded the W3C public dummy PDF independently, imported through public `knowledge.document.upload`, and verified durable original bytes exactly (13,264 bytes, SHA256 `3df79d34abbca99308e79cb94461c1893582604d68329a41fd4bec1885e6adb4`). Real Core document `doc_d439ce1706d5a5b3e57e3f3f47a1525e` belongs to dedicated parsed library `ds_4ea0a40c2431c9304ed67bf6155b4bff`. DocServer exact `DOC_ADD` task succeeded; public content read returns “Dummy PDF file”.
- `work_artifact`: a new Main Chat conversation using explicitly fixed Kimi produced a real completed export; the conversation artifact endpoint invoked the real producer and published artifact `a0a0ecbd-948e-41cc-8770-a9ba40aedbf1`, revision `bc2e537b-0554-434a-a5a9-c704793af5e6`. Public MCP imported that exact fixed revision into `doc_3108abb9f70bdf3ae7b7f95e0448e755`. Original 843 bytes match revision SHA256 `fad1fac0abcf8e1695dabe559315718a84c2306e641b48f06c6ad01a7f30dee3`. The real producer's text-class revision contains immutable inline JSON; those actual JSON bytes are preserved as `application/json`, rather than reconstructing Markdown. No DB rows, histories or artifacts were fabricated.
- A new dedicated Auth account with a nondefault tenant used the real create/login APIs. It created library `ds_0aaab659dfddfb5460362c05128a9f78`, imported the same exact PDF as `doc_a8c8415a274544b2ecd17ddf637f8f9d`, and read its own catalog/document. Reading the default fixture was rejected. An actual native Chat-shaped request (running Chat's internal credential + X-User-Id, no tenant header) returned exactly its active document. Existing users' roles/enabled status were not changed.
- Readonly historical admission check: existing `rerank验收20261009` library `ds_bb2434443cfe2b852eed775e45b45ed9`, document `doc_5dfe1d3475f7c8b57b2b821f38d04fef`, remained readable through public document.get with content, visible through actual native internal headers, and returned by public knowledge.search. No existing resource was modified.

Real acceptance exposed two status defects, now reproduced and fixed in the worktree:

1. Stored cross-library moves skipped parser submission correctly, but the original binder created tasks with empty state. The legacy task list interpreted them as uploading; the page displayed the uploading warning and 99+. The binder now initializes stored tasks as SUCCEEDED. It does not rewrite unrelated historical task records.
2. Metadata prioritized PDF conversion NONE/SUCCEEDED above the actual parsing task. Ordinary submissions also left the stage table pending indefinitely. A single readonly `observeDocumentProcessing` resolver now overlays the current exact task result for metadata and processing coverage. Core ownership, latest-created task, and current processing revision are read in one snapshot; the external result must match task ID, document binding, and KB. It never persists late parser results. New submissions capture their actual processing level/revision in existing task metadata; historical tasks prove parsing only, and unknown chunk/index stages remain unchanged.

Verification:

- New tests failed before changes with `NONE` masking parser SUCCESS and stored task state empty (`/tmp/kb-processing-red.log`).
- Regression tests cover newer task RUNNING despite late old SUCCESS, exact external task/doc/KB association, stale processing revision not proving indexing, parsed-only and historical task stage limits, unchanged persisted state, correct available coverage, and stored bind-only terminal state.
- Full `go test ./doc -count=1` passed, 17.225s (`/tmp/kb-processing-doc-full.log`). `git diff --check` passed.
- Additional owned paths: `backend/core/doc/processing_observation.go`, `processing_observation_test.go`; narrow changes in `document_service.go`, `processing.go`, `task_types.go`, `task.go`, `original_service.go`.

Pending at this checkpoint: parent-coordinated Core deployment of the status corrections, actual public metadata/coverage recheck, and dedicated fixture deletion followed by native invisibility/old-reference rejection. The private fixture manifest retains all IDs for that final validation. Default DeepSeek failed upstream with MissingSessionID; the explicitly fixed Kimi conversation succeeded without changing global model settings.

### Processing observation review corrections

The first readonly resolver still treated an external task ID without a matching observation as RUNNING and issued per-document queries. Both were reproduced before correction: 32-document coverage performed 161 queries, and missing/foreign task observations incorrectly appeared running (`/tmp/kb-processing-review-red.log`).

- Missing exact external observations (including unavailable readonly DB or missing KB binding) now produce UNKNOWN, never inferred RUNNING. Changed document resource version/external binding returns unknown stages rather than fabricated pending defaults. Existing persisted stage values are retained unless current task evidence overrides them.
- Metadata and coverage share one batched snapshot implementation: dataset, active documents, persisted stages and latest-created owned tasks are read in a readonly repeatable-read Core transaction. External tasks are queried afterward in batches of at most 500 IDs and matched by task ID + document ID + exact KB. Late old tasks still cannot win through updated_at. Stored-library coverage needs only dataset/document reads.
- A real SQLite regression with 501 current documents plus 501 older tasks asserts exactly 6 queries (4 Core reads + 2 external batches), full available coverage and no fabricated processing count. Separate tests verify missing observation and changed-binding UNKNOWN behavior.
- Full `go test ./doc -count=1` passed again, 17.615s (`/tmp/kb-processing-review-full.log`); `git diff --check` passed. No services restarted by this agent.

### P7 READY4 final live verification — passed

After the parent reloaded Core and rebuilt frontend, the private acceptance script's `verify-processing` phase passed against actual public APIs:

| Source | Public parse status | Parsed content | Library coverage |
|---|---|---|---|
| Public remote PDF | SUCCESS | Present | 2 available / 2 total; 0 failed / 0 processing |
| Real published fixed Work revision | SUCCESS | Present | 2 available / 2 total; 0 failed / 0 processing |
| Nondefault tenant remote PDF | SUCCESS | Present | 1 available / 1 total; 0 failed / 0 processing |

Then each of these three dedicated documents was deleted through public `knowledge.resource.delete` using its own account. Actual native Chat-shaped visibility requests changed from one admitted document before deletion to zero afterward for all three. Public `knowledge.document.get` using each old resource reference was rejected. Only the ORIGINALS fixture documents were deleted; root/client fixtures were untouched. The two empty dedicated libraries, new test account and producer artifacts/conversations remain identified in the private manifest; no existing account or historical resource was altered.

Finally, READY4 readonly revalidation of existing `rerank验收20261009` again passed public get with content, native admission and public search returning its same historical document. This establishes historical admission survived the new deployment as well as the fixture deletions.

Private evidence: `processing-verified.private.json`, `fixture.json` (before/after native results, public delete responses, old-reference rejections), `legacy-ready4-read.private.json`, and individual RPC records in the private acceptance directory above. No unresolved original-source/status/deletion acceptance item remains in this agent's P7 scope. Host local-file/CLI acceptance and Chat/Work orchestration integration are the other agents' separate scope. No service restart or 8090 operation was performed by this agent.

## Real HTTP SQLite proxy checkpoint concurrency correction

A parent-supplied live error pointed to `conversationCheckpoint` issuing raw `BEGIN IMMEDIATE` through the HTTP SQLite driver. The existing common transaction helper already understood proxy ownership, but this checkpoint bypassed it. A raw execute request released the proxy server gate while leaving SQLite inside the transaction; concurrent resource transactions failed with `cannot start a transaction within a transaction`, while unrelated ordinary writes could enter that transaction and be rolled back with it.

Reused the prior checkout's test approach and narrow fix as readonly reference. Only this worktree changed: exported existing detection as `common.IsSQLiteProxy`, and routed the checkpoint's proxy branch through existing `ImmediateTransactionWithSQLiteBusyRetry`. Direct SQLite and PostgreSQL checkpoint paths remain intact.

New `backend/core/chat/conversation_checkpoint_proxy_test.go` starts the real local runtime manager HTTP SQLite server on a random loopback port using only `t.TempDir` databases, build/runtime roots and host-home. It concurrently writes actual `orm.Document` and `orm.ResourceOperationReceipt` rows, checks ordinary writes, commit versus rollback, cancellation of queued transactions, and preservation of caller-owned outer transactions.

Evidence:
- Before fix: repeated exact nested-transaction error, ordinary write escaping checkpoint, and missing resource document/receipt rows; `/tmp/kb-checkpoint-proxy-red.log`.
- `GOTOOLCHAIN=go1.25.11 go test ./chat ./common -run 'TestConversationCheckpointSQLiteProxy|Test.*SQLite|Test.*ConversationFork|Test.*Forkable' -count=1` passed (chat 3.384s, common 0.744s), `/tmp/kb-checkpoint-proxy-green.log`.
- `GOTOOLCHAIN=go1.25.11 go test -race ./chat -run '^TestConversationCheckpointSQLiteProxy$' -count=1` passed (4.336s), `/tmp/kb-checkpoint-proxy-race.log`.
- No running database, private configuration, 8090 process or 8091 service was modified/restarted by this test.

### Dedicated test account disablement — real negative acceptance

At the parent's explicit authorization, verified the newly created `p7-originals-*` account and its tenant against the private fixture manifest, confirmed its existing JWT could still call public knowledge.list, then disabled only that account through the actual admin user API. The same already-issued JWT now receives HTTP 401 from public MCP knowledge.list. An actual native request using the running Chat service's internal credential and that user ID (no tenant claim) receives HTTP 403 from Core visibility. Admin GET confirms the fixture is inactive. Evidence is private `disabled-account-verification.private.json`. No existing user was changed; no fake active run was created for an internal capability claim. The actual role catalog contained only system-admin and user, both with document.write, so no existing shared role was altered to manufacture a readonly role scenario.

### Workspace path ownership investigation

The actual running 8091 Chat and Algo processes both contain `LAZYMIND_AGENTIC_WORKSPACE=/Users/dengyuang/workspace/lazymind-runtime-8091/data/homes/lazymind/agent_workspace`; the local manager already injects this in algorithmServiceEnv. The unprefixed AGENTIC_WORKSPACE is not what the LAZYMIND Config namespace reads. Therefore absence from the process-compose top-level environment alone does not establish a missing runtime injection.

However, checkout workspace/tool-retrieval-state contains three JSON files and three locks, 1191 bytes. One filename (`205b8bd35e09aaf6b8ea878683a7161fe0f78a33154e00a7d3e10759f621602f.json`) exactly matches the hash of the real 8091 SubAgent scope for task `d1af2986-e2df-4020-a198-a19152d98bd9`. These cannot all be dismissed as pytest debris. No files were removed or configuration changed. Parent assigned further runner/config provenance investigation to mcp_client; scope hash and safe selected process environment evidence were handed over.

## Indexed public processing-status propagation correction

Later READY6 readback of document `doc_6f9bc58d7269759181e5bfbb505ef21e` exposed a remaining public-list adapter gap that the parsed-only READY4 checks did not cover. Readonly production evidence: dataset `ds_09b10f333dd36b049f016609bfc22977` is indexed/revision 1; Core task `task_a2dd9b4b1bdc26313b977bc7be636d1f` records actual submitted indexed/revision 1; exact external task `d57ef308-5e0d-4093-8c93-0656ace57291` matches document and KB and is SUCCESS/error_code 200. The durable stage table still contains its initial pending values.

Root cause: DocumentService discarded the observed stage state and retained only the raw parser status. The capability list adapter separately read the stale persisted table, so parse_status was SUCCESS while processing_status remained pending. DocumentMetadata now carries the existing observer's complete ProcessingState and ProcessingLevel; both public list and get map those same fields. The adapter's second stage-table query was removed. Unproven chunk/index pending/running defaults now become unknown while persisted terminal results remain available; a matching indexed/revision task SUCCESS proves all three stages.

Regression tests exercise the actual startParseTasksAtLevel submission and publication path, assert the outbound indexed level and durable submitted revision 7, then verify complete observed metadata from the exact external SUCCESS. Adapter tests seed stale pending state and assert list/get both return ready using the observed metadata, with zero adapter rereads of document_processing_states. Existing stale-revision/foreign-binding/history/501-document batch regressions remain passing.

`GOTOOLCHAIN=go1.25.11 go test ./doc ./capability/internal/coreadapter ./capability/bootstrap -count=1` passed (17.808s / 0.598s / 1.515s), `/tmp/kb-observed-metadata-full.log`; diff-check passed. Fix is ready for parent-coordinated reload and actual indexed readback. No runtime database or service was modified by this agent.

## Work fixed-revision source error boundary

Work source resolution now produces typed resource errors through the capability adapter: missing revision/artifact, wrong owner, cross-tenant source and mismatched artifact/revision return NOT_FOUND; an owned future draft or a revision of an artifact with no published head returns INVALID_ARGUMENT. The explicit revision is ownership-checked before its publication status is revealed. The internal optional-head pinning path remains supported.

A pre-existing Artifact GetRevision branch also collapsed a failed artifact-row query into ErrNotFound. It now distinguishes record-not-found from a genuine database error, so infrastructure failures remain INTERNAL instead of being misclassified by the new source mapping. No Artifact V2 rollout default changed; no legacy artifacts are copied or implicitly upgraded. The implementation plan now explicitly states that Work fixed sources depend on existing published versioned artifacts and that only isolated 8091 acceptance enabled the existing Artifact V2 flag.

Tests exercise `coreadapter.KnowledgeManagement.Upload`, not merely the direct resolver: seven source-error cases were reproduced as INTERNAL before the fix; post-fix assertions check the exact capability codes. Dropping the head table and the artifact table separately proves genuine DB failures remain INTERNAL. Existing actual Main Chat/SubAgent dual-writer positive tests still pass.

`GOTOOLCHAIN=go1.25.11 go test ./capability/bootstrap ./artifact ./common -count=1` passed (1.165s / 0.788s / 2.438s), `/tmp/kb-work-errors-full.log`; red evidence `/tmp/kb-work-errors-red.log`. Code is ready for the parent's coordinated Core reload. No runtime configuration/database/service change by this agent.
