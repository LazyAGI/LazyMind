# MCP client / loader foundation

Implemented in the isolated worktree only. LazyLLM submodule branch `dya/knowledge-management-mcp-client`, commit `cbe4acfe`. Root loader changes remain uncommitted for integration.

## API

- `MCPClient(..., result_mode='text'|'structured')`. Default text output unchanged. Structured generated tools emit JSON `{content, structuredContent, isError}`, even with no text; transport exceptions emit sanitized `MCP_CALL_FAILED` errors. Direct `call_tool` still returns SDK result.
- `request_metadata_provider`: async `(wire_name, arguments) -> dict` supplying trusted request `_meta`. Evaluated once before any setup auth retry.
- `progress_callback`: async SDK callback `(progress, total, message)`; `call_tool(..., metadata=..., progress_callback=...)` supports direct overrides. These never enter model-visible parameters.
- `allowed_tools=None` unrestricted; `[]` skips discovery/registration entirely. Filter before registration.
- Auth recovery only once for explicit 401 during session setup. No automatic replay after dispatch, on timeout/mixed exception groups, or after successful execution with failed cleanup.
- Original wire name and provenance remain. Original MCP JSON Schema retained through generic ModuleTool catalog via trusted `__mcp_input_schema__`; nested/root unions, `$defs`, nulls, defaults remain intact. The server validates full schema; Python adaptation does not coerce MCP parameter values.

## LazyMind loader

`mcp_allowed_tools` centralizes initial/preview/dynamic semantics. OAuth missing whitelist stays fail-closed. Empty allowed list loads zero tools and is not misreported as unavailable. Server runtime accepts the client options above and `auth_provider` / `auth_recovery`. Credential-bound callbacks and builtin `id='lazymind-knowledge'` bypass shared callable cache. Existing static browser/user caching remains unchanged.

## Red-green evidence

- First client test run: 5 failed / 3 passed: empty allowlist contacted discovery, structured mode/provider options absent, nested schema lost.
- Next focused red: 2 failed: top-level union/$defs lost, dispatched 401 invoked recovery.
- Loader red: 4 failed: empty static allowlist unavailable/client constructed, builtin reused cached callable, unrestricted preview empty, dynamic empty whitelist invoked loader.
- Structured transport-error red: 1 failed: non-JSON text exposed exception credentials. Green now returns sanitized typed error.
- Real local HTTP test verifies actual SDK `_meta`, progress SSE notification and structured-result round trip (fixture adjusted to support SDK automatic tools/list output-schema discovery).

## Validation

Command prefix:

```sh
PYTHONPATH=algorithm/lazyllm:algorithm:/Users/dengyuang/workspace/LazyMind_dev/local/build/deps/python/algorithm/lib/python3.11/site-packages /tmp/kb-mcp-tests/bin/python -m pytest -q
```

Files: `algorithm/lazyllm/tests/basic_tests/Tools/{test_mcp_contract,test_mcp_registration,test_tool_runtime,test_tool_retrieval,test_tool_manager_scheduling,test_tool_group_refresh}.py`, `algorithm/lazyllm/tests/advanced_tests/Tools/test_mcp_auth.py`, `tests/algorithm/chat/{test_mcp_tool_loading,test_mcp_tool_names,test_mcp_oauth}.py`.

Result: **116 passed**, 1 pre-existing jieba/pkg_resources deprecation warning, 3.83s. Both root and submodule `git diff --check` passed. Used a temporary pytest environment and existing dependencies read-only; no running service changed.

## Limits

No production Core/8091 E2E or all-project integration tests performed. SDK versions using metadata/progress must support `ClientSession.call_tool(meta=..., progress_callback=...)`; unsupported versions fail closed rather than dropping those options. Host owns stable operation keys and credential lifetime. This layer does not infer completed remote mutation state from transport failure.

## P5 host integration and P6 management group (2026-10-20)

LazyLLM follow-up commit `8c4cca83` preserves ContextVars through the existing synchronous thread bridge. Test first failed because the trusted call context became `None`; green now verifies a running event loop retains its original context without leaking later changes.

New `algorithm/lazymind/chat/service/knowledge_management.py` owns LazyMind-specific policy, while LazyLLM remains generic:

- Run-bound execution identity and fresh private credentials; nine positive management tools intersect the Core scope. No credential-bound callable caching.
- Host dispatch checks MCP source, builtin origin, stable wire identity, call ID and frozen argument digest. Dynamic and duplicate Core MCP entries bind to the same runtime. Direct calls cannot acquire metadata.
- Every call uses private prepare/claim. Only deletion and cross-library move emit a typed `ask_pending` card. Digest + actual Core snapshot are bound to the confirmation; rejected/expired/changed/mismatched statuses fail closed. Polling is cancellation-aware with a 300-second deadline; workflow full trust cannot bypass business confirmation.
- Structured MCP errors retain the full envelope and mark the host tool result unsuccessful. A transport-uncertain mutation blocks an identical subsequent dispatch in the same runtime pending receipt reconciliation; the client never automatically replays dispatched operations.
- `local_file` upload declares a real host read intent using existing FileResolution. The existing workspace barrier executes first. The bridge opens a pinned, non-symlink file descriptor and streams at most 128 MiB through private `:upload-original`, carrying captured execution in `X-LazyMind-Execution` and the internal service token. It converts the source to `uploaded_file` before hashing, preparing, claiming and dispatching MCP. No Core-host interpretation of model paths.
- Chat and SubAgent plan options carry the same runtime. SubAgent startup and remote workflow spec use only the fresh Core `system_mcp_config` launch field, overwriting inherited parent config; absent/empty fields grant nothing.
- Native registry exposes `knowledge_management` as a separate system MCP group. Native resolution skips this name and wire names because Core binds them separately. Existing `kb` group identity is unchanged (native public API migration is originals agent ownership).
- SubAgent emits typed ask_pending while execution waits. Core TaskEvent forwards these cards to the owning conversation for both generic Work and workflow steps. Core persistence is parent-owned.
- AskCard submits the bound decision directly to `/api/core/knowledge-management/confirmations/{id}/decision`, with `decision`, `arguments_digest` and `snapshot`. It does not send approval as chat text; failure preserves the selected answer for retry.

Private Core contract integrated:

- `POST /internal/knowledge-management:credential` with execution -> token.
- `POST /internal/knowledge-management:prepare` with execution, wire_name, arguments, call_id -> operation_key, arguments_digest, snapshot, requires_confirmation, confirmation_id, status, preview.
- `POST /internal/knowledge-management:status` with execution + confirmation_id -> status and same digest/snapshot.
- `POST /internal/knowledge-management:claim` with prepare payload + bound operation_key/digest/snapshot/confirmation_id -> allowed metadata containing operation_key.
- `POST /internal/knowledge-management:upload-original` multipart file + private execution/token headers -> file_ref.
- `/internal/mcp/knowledge-management` streamable HTTP with per-run bearer credential.

Additional red-green evidence:

- Real ToolManager test caught structured `isError` being wrapped as host `ok: true`; result middleware now preserves envelope and sets `ok: false`.
- Credential-bound loader exception test caught secret-bearing log output; now logs exception class only.
- Seven launch/event/remote tests failed for inherited scope, dropped ask_pending and missing remote scope forwarding; all passed after changes.
- Go typed event test failed `confirmation events lost`; passed after TaskEvent and conversation routing additions. Initial compile was blocked by an unrelated resource_stats shadowing error (fixed by owning agent), and initial fixture lacked SQLite RawMessage fields (corrected before functional red).
- Local upload test failed because no host read was declared. It now verifies denied access sends no bytes, trusted workspace scope is entered before transfer, prepare/claim use only the uploaded reference, and actual SDK dispatch receives the rewritten arguments.
- Management registry test failed missing group; now verifies trusted Core enablement and explicit empty scope.
- Frontend three new confirmation cases initially failed; after direct decision integration all pass. A test-only accessible-name race from Ant's loading icon was corrected by retaining the submit button reference.

Latest validations:

1. Original foundation suite above: **119 passed**, 5.70s (includes new bridge/context and loader cases).
2. Python host suite: `tests/algorithm/chat/{test_knowledge_management_runtime,test_mcp_tool_loading,test_tool_call_guard,test_agent_executor,test_subagent_runner,test_remote_executor,test_tool_registry}.py`: **267 passed**, 4.72s. Afterwards added cancellation/deadline tests; focused runtime rerun **29 passed**, 1.96s.
3. Frontend: bundled Node running `node_modules/vitest/vitest.mjs run src/modules/chat/components/AskCard/index.test.tsx src/modules/chat/components/AssistantMessage/index.test.tsx --no-cache`: **73 passed**, 10.26s. Pre-existing Sass/jsdom warnings only.
4. Core: from `backend/core`, `go test ./subagent -run '^TestKnowledgeConfirmationEventReachesConversation$' -count=1`: **passed**, 0.767s.
5. Scoped root and submodule `git diff --check`: passed. Root modifications remain uncommitted. No runtime services changed.

Remaining acceptance limits: no real 8091/browser login/production MCP end-to-end run; Core credential, snapshot, persistence and decision integration are parent-owned and need combined acceptance. Host multipart test exercises real HTTPX multipart over MockTransport, not a running Core listener. Uncertain outcomes intentionally fail closed; no host receipt-query UI was added. Standalone native/public legacy query shim compatibility is originals agent ownership. Existing workspace approval behavior still applies to local file reads independently of KB business confirmation.

## Review follow-up: structured private errors and complete confirmation preview

- Core private HTTP errors shaped `{error:{code,message,details}}` now become the same JSON envelope used for MCP tool failures, with `isError:true` and outer host `ok:false`. Limits and actionable suggestions inside `details` are preserved. Untyped HTTP failures remain sanitized with status code only.
- This also covers the credential callback: a call-local holder preserves its typed Core diagnostic through the generic MCP adapter's sanitized transport exception path. It cannot leak across calls and does not put LazyMind business logic in LazyLLM. Multipart upload failures use the same typed mapping.
- Python passes the complete Core preview as `knowledge_management_preview`; Go persistence and AskPendingEvent were updated by parent to the same contract. AskCard renders actual resources (including nested refs, names and paths), destination, counts, bytes and impact; title/description are not repeated. Added English/Chinese labels. Both live cards and persisted cards that only carry preview labels render correctly.
- Red evidence: five new tests failed for lost private error structure and omitted preview; then an actual manager+MCP adapter test exposed credential errors still being reduced to `MCP_CALL_FAILED`. Fixed at host call wrapper. Frontend test exposed missing scope/impact. A test insertion initially duplicated the case into unrelated suites (fixture ReferenceError); corrected before testing the functional failure.
- Latest host regression: **279 passed**, 7.40s, across runtime/loading/guard/executor/SubAgent/remote executor/registry. Frontend AskCard + AssistantMessage: **75 passed**, 20.10s; only pre-existing Sass/jsdom and timer warnings. Scoped `git diff --check` passed. No Go code or LazyLLM code changed in this follow-up.
- Reviewed `filters.kb_id`: it is retrieval selection/default, whereas native hard bounds use `KBToolkit._kb_scope`. Parent removed the accidental reinterpretation of ordinary selection as management authorization; only trusted explicit `knowledge_dataset_scope` imposes a hard dataset restriction.
- Final nested-preview contract check after matching actual Core `{resource_ref,name,path}` resources/destination: **2 passed**, 12 unrelated AskCard tests deselected, 5.73s (`-t 'preview scope'`). No `[object Object]` output; names and paths are visible.

## P7 preparation and confirmation-error UI

- Actual E2E harness and plan prepared privately in `/tmp/lazymind-kb-e2e-20261020` (directory 0700, files 0600). Public authenticated Chat/Work, SSE recording, bound decisions, history, cancellation and multipart fixture endpoints; fixed 127.0.0.1:8091, no proxy/redirect. All requests refuse until parent confirms new services ready. The gate was verified without making any network request.
- CLI binary built from current worktree. Separate real SDK stdio harness advertises roots, handles the genuine roots/list request, and tests allowed fixture file versus outside-root/no-roots cases. It only accepts dataset IDs recorded in a verified fixture manifest. No live write performed during preparation.
- Runtime review follow-up: failed knowledge confirmation submission now has a visible localized alert; selected answer remains retryable and no new Chat is emitted. New assertion first failed (missing alert), then full AskCard **14 passed**, 3.68s. No backend change in this UI follow-up.

### Real 8091 acceptance: initial failures and fixes (2026-10-20)

- Default DeepSeek real Chat failed before tool execution: upstream HTTP 400 `MissingSessionID`. Parent verified a per-conversation fixed Kimi k3 selection; global model configuration remains unchanged. This failure is not evidence of schema/provider incompatibility.
- GET `/api/core/tools` initially showed management9 inactive. Core sent policy but Python catalog route discarded it and inherited ambient globals. Added explicit request-local registry policy argument, default endpoint empty scope. ASGI regression failed before fix (unexpected active scope), then catalog + registry **49 passed**. Parent verified live GET management active=true after coordinated restart.
- Real Kimi Chat `f5505a42-b1bc-469d-b2d4-9cb931d2ea53` / `h_dm9eb7nksniw` completed but only searched for tools: management provider unavailable, no knowledge.list execution. Logs exposed sanitized TypeError. Public authenticated MCP tools/list reproduced `TypeError: unhashable type: list` from docstring hint generation for JSON Schema nullable type arrays. Minimal fix handles non-string type hints as Any while preserving the full original schema. Regression red→green; MCP contract/registration/auth **35 passed**. Real server catalog now converts all 11 knowledge tools successfully (management9 plus public read2). LazyLLM commit `f85451e8`.
- Raw real run evidence and tokens remain outside Git in private `/tmp/lazymind-kb-e2e-20261020`. These checks do **not** constitute successful management mutation E2E. Awaiting coordinated Chat restart before continuing real internal tool dispatch.

### E2E terminal-state repair

Real approved/rejected management runs were incorrectly classified as awaiting_user_input. The event translator treated a synchronous knowledge-management confirmation as a stop-tool ask_user event. It now forwards the typed management card without setting the stop-turn/final-answer suppression flags; the management tool already waits until Core decision/rejection/timeout within the same run. Ordinary questions and mail cards keep their existing state. Tests model the actual card → tool result → terminal sequence for success/rejection/snapshot-change, plus coexistence with an ordinary unanswered question: baseline **3 failed / 1 passed**, corrected **4 passed**; runtime + event translator + management suite **91 passed**. Files: event_translator.py and test_runtime_events.py. Awaiting coordinated Chat deployment and real terminal verification.

READY5 real verification passed: main Chat creates a 45-byte file in its own runtime workspace with no confirmation, imports through actual local_file host byte bridge, public authenticated original download matches source bytes and SHA-256 exactly. Same-conversation deletion uses actual business confirmation; approved operation deletes fixture, history preserves answered=yes, actual run terminal now completed/normal. See mcp-client-e2e-report.md for IDs and private evidence paths. No services were restarted by this agent.


## Final integrated Python regression (current tree)

One aggregated execution across 38 files completed: **704 passed, 3 warnings, 8.93 seconds** (wall clock 9.70 seconds), exit 0. No tests were skipped or deselected. It covers management policy/transport/loading, native KB scope and ownership, registry/retrieval, host middleware/executor, SubAgent/Writer/PPT artifact contracts, runtime events/translators, remote execution, and LazyLLM schema/registration/auth/scheduling.

Exact command (cwd is this worktree):

```sh
LAZYMIND_AGENTIC_WORKSPACE=/tmp/lazymind-kb-e2e-20261020/final-tests-agent-workspace PYTHONPATH=algorithm/lazyllm:algorithm:/Users/dengyuang/workspace/LazyMind_dev/local/build/deps/python/algorithm/lib/python3.11/site-packages /tmp/kb-mcp-tests/bin/python -m pytest -q --import-mode=importlib tests/algorithm/chat/test_knowledge_management_runtime.py tests/algorithm/chat/test_mcp_catalog_route.py tests/algorithm/chat/test_mcp_tool_loading.py tests/algorithm/chat/test_mcp_tool_names.py tests/algorithm/chat/test_mcp_oauth.py tests/algorithm/chat/test_kb_toolkit.py tests/algorithm/chat/test_kb_ownership.py tests/algorithm/chat/test_kb_reranker.py tests/algorithm/chat/test_knowledge_search_service.py tests/algorithm/chat/test_tool_registry.py tests/algorithm/chat/test_tool_retrieval.py tests/algorithm/chat/test_tool_call_guard.py tests/algorithm/chat/test_agent_executor.py tests/algorithm/chat/test_subagent_task_context.py tests/algorithm/chat/test_subagent_attachment_access.py tests/algorithm/chat/test_subagent_chat_tools.py tests/algorithm/chat/test_subagent_runner.py tests/algorithm/chat/test_handle_chat_subagent_tools.py tests/algorithm/chat/test_remote_executor.py tests/algorithm/chat/test_remote_executor_client_errors.py tests/algorithm/chat/test_runtime_events.py tests/algorithm/chat/test_agentic_event_translator.py tests/algorithm/chat/test_schedule_tool_schema.py tests/algorithm/chat/subagent/test_artifact_image_values.py tests/algorithm/chat/subagent/test_context_drafts.py tests/algorithm/chat/subagent/test_ppt_publisher_contract.py tests/algorithm/chat/subagent/test_save_artifacts_batch.py tests/algorithm/chat/subagent/test_save_artifacts_schema.py tests/algorithm/chat/subagent/test_subagent_memory_store.py tests/algorithm/chat/subagent/test_workflow_package_materialization.py algorithm/lazyllm/tests/basic_tests/Tools/test_mcp_contract.py algorithm/lazyllm/tests/basic_tests/Tools/test_mcp_registration.py algorithm/lazyllm/tests/basic_tests/Tools/test_tool_runtime.py algorithm/lazyllm/tests/basic_tests/Tools/test_tool_retrieval.py algorithm/lazyllm/tests/basic_tests/Tools/test_tool_manager_scheduling.py algorithm/lazyllm/tests/basic_tests/Tools/test_tool_group_refresh.py algorithm/lazyllm/tests/advanced_tests/Tools/test_mcp_auth.py algorithm/lazyllm/tests/advanced_tests/Tools/test_tools_manager.py
```

The initial combined collection encountered the two repositories' duplicate basename `test_tool_retrieval.py`; collection stopped before executing tests. Using pytest's `--import-mode=importlib` resolved that harness-only collision without changing source files or deleting caches. The successful run above is the full selected suite. Logs and exact file manifest: private `final-python-suite.log`, `final-python-suite-command.txt`, `final-python-suite-files.json`. Tests use a private `LAZYMIND_AGENTIC_WORKSPACE`, preventing checkout state creation.

Limits: this is the focused Python regression, not the entire repository test suite. The separate legacy advanced `test_mcp.py` that invokes `npx -y` to fetch an external filesystem server was intentionally excluded; real MCP transport and CLI roots are covered by the recorded SDK fixture tests and 8091 E2E. The three warnings are existing jieba/pkg_resources and Pydantic deprecations. No frontend rebuild, service restart, or repeated clean test runs were performed.

## Soft-deleted source tasks in the running HTTP list

Parent-authorized Core follow-up: `doc/task.go` now excludes tasks with an explicit same-dataset source document tombstone only from `task_state=running`. The outer query applies the restriction to both LazyLLM worker statuses and local pre-dispatch upload states, including count/pagination. It does not modify stored task/worker states or historical queries. Existing unrelated edits elsewhere in task.go belong to the shared implementation and were preserved.

TDD HTTP regression `TestListTasksHTTPRunningExcludesSoftDeletedSources`: real httptest server + real database, external scan policy only stubbed. Before production change the running page returned total=5 instead of 3 (both deleted local and worker tasks leaked). After change all three subtests pass: running pages retain active worker, unbound upload and not-yet-published source; history retains deleted tasks and existing WAITING/WORKING presentation; success filter retains deleted-source completed history.

Validation from `backend/core`: `go test ./doc -run '^TestListTasksHTTPRunningExcludesSoftDeletedSources$' -count=1` passed (0.559s); `go test ./doc -count=1` passed (17.227s); targeted `git diff --check` passed. Private evidence: `/tmp/lazymind-kb-e2e-20261020/task-list-{red,green,doc-suite}.log`. Existing macOS Keychain deprecation build warnings remain. No service restart was performed; deployed UI verification remains with the parent after coordinated Core reload.

Boundary: absent/unbound source rows have no authoritative tombstone and remain eligible as uploads. The fix intentionally does not classify physically missing sources as deleted, fabricate worker completion, or alter the task state machine.
