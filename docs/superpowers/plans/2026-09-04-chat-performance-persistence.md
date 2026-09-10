# Chat Performance Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve normalized chat performance metrics across Core's stream boundary, restore them with chat history after refresh, keep diagnostic detail in writable local files, and rebuild the affected containers.

**Architecture:** Algorithm remains the metric producer and local diagnostic sink. Core validates the terminal summary, forwards it live, stores normalized facts in a dedicated run table, and hydrates existing history responses by run ID. Frontend consumes one stable `performance_metrics` shape for live and restored messages.

**Tech Stack:** Python/FastAPI, Go/GORM, PostgreSQL and SQLite migrations, React/TypeScript, Vitest, Docker Compose.

## Global Constraints

- Do not put performance metrics in `run_terminal`, `chat_histories.ext`, prompt text, or assistant content.
- Do not persist `provider_usages`, prompt/completion content, headers, API keys, or provider errors in Core.
- Unknown numeric values remain absent/SQL `NULL`; zero is retained as a known value.
- Accept metrics only from an authoritative `run_finished` event whose `run_id` matches the request.
- Preserve existing user files and unrelated dirty worktree entries.
- Local files live under `data/observability`; POSIX file locking claims remain POSIX-only.
- OpenTelemetry mapping is an exporter concern; database columns use stable LazyMind names.

---

### Task 1: Preserve and validate performance metrics across Core streaming

**Files:**
- Modify: `backend/core/chat/chat.go`
- Modify: `backend/core/chat/redis_cache.go`
- Create: `backend/core/chat/performance_metrics.go`
- Modify: `backend/core/chat/chat_stream_test.go`
- Modify: `backend/core/chat/openapi` definitions in `backend/core/openapi_manual.go`

**Interfaces:**
- Consumes: Algorithm terminal JSON field `performance_metrics` beside `runtime_event`.
- Produces: `RunPerformanceMetrics`, `Validate() error`, and `PerformanceMetrics *RunPerformanceMetrics` on upstream/client stream structs.

- [ ] **Step 1: Write the failing stream-boundary test**

Add a real HTTP stream fixture with a matching `run_finished` event and literal metrics:

```go
"performance_metrics": map[string]any{
    "schema_version": 1, "turn_seq": 3,
    "steps": 3, "model_steps": 2, "tool_steps": 1,
    "wall_ms": 1200, "model_ms": 900, "tool_ms": 200,
    "input_tokens": 100, "output_tokens": 20, "cached_tokens": 40,
}
```

Assert the chunk retains these values. Add malformed/negative and run-ID mismatch cases proving invalid metrics are rejected without changing terminal outcome.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `cd backend/core && go test ./chat -run 'TestStreamChatUpstream.*Performance' -count=1`

Expected: FAIL because `UpstreamStreamChunk` has no performance field.

- [ ] **Step 3: Implement the typed contract and forwarding**

Define pointer-valued optional facts so missing values remain unknown:

```go
type RunPerformanceMetrics struct {
    SchemaVersion int     `json:"schema_version"`
    TurnSeq       *int    `json:"turn_seq,omitempty"`
    Steps         int     `json:"steps"`
    ModelSteps    int     `json:"model_steps"`
    ToolSteps     int     `json:"tool_steps"`
    WallMS        *int64  `json:"wall_ms,omitempty"`
    ModelMS       *int64  `json:"model_ms,omitempty"`
    ToolMS        *int64  `json:"tool_ms,omitempty"`
    TTFTMS        *int64  `json:"ttft_ms,omitempty"`
    Model         string  `json:"model,omitempty"`
    InputTokens   *int64  `json:"input_tokens,omitempty"`
    OutputTokens  *int64  `json:"output_tokens,omitempty"`
    TotalTokens   *int64  `json:"total_tokens,omitempty"`
    CachedTokens  *int64  `json:"cached_tokens,omitempty"`
    CacheInputTokens *int64 `json:"cache_input_tokens,omitempty"`
    ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"`
    MaxInputTokens *int64 `json:"max_input_tokens,omitempty"`
    ContextInputTokens *int64 `json:"context_input_tokens,omitempty"`
}
```

Validation accepts schema version 1 and finite non-negative integers only. Add the field to `LazyChatData`, `UpstreamStreamChunk`, `ChatChunkResponse`, and `upstreamStreamChunkFromData`.

- [ ] **Step 4: Run tests and verify GREEN**

Run: `cd backend/core && go test ./chat -run 'TestStreamChatUpstream.*Performance' -count=1`

- [ ] **Step 5: Add OpenAPI schemas**

Add `RunPerformanceMetrics` and reference it from `ChatChunkResponse` and `ConversationHistoryItem` without changing `RunTerminal`.

### Task 2: Store normalized run facts in a dedicated Core table

**Files:**
- Modify: `backend/core/common/orm/models.go`
- Modify: `backend/core/common/orm/all_models.go`
- Create: `backend/core/migrations/dev_mode/v0_3/20260904163000_add_chat_run_performance.up.sql`
- Create: `backend/core/migrations/dev_mode/v0_3/20260904163000_add_chat_run_performance.down.sql`
- Modify: `backend/core/migrations/version_mode/v0_3/20260805000000_workflow_runtime_release.up.sql`
- Modify: `backend/core/migrations/version_mode/v0_3/20260805000000_workflow_runtime_release.down.sql`
- Create: `backend/core/chat/performance_store.go`
- Create: `backend/core/chat/performance_store_test.go`

**Interfaces:**
- Consumes: validated `RunPerformanceMetrics` and authoritative run ownership fields.
- Produces: `persistRunPerformance(ctx, db, record) error`, `loadRunPerformance(ctx, db, runIDs)`, and `ChatRunPerformance` ORM model.

- [ ] **Step 1: Write failing persistence tests**

Use real SQLite/GORM and assert:

```go
assert row.RunID == "run-1"
assert row.ModelMS != nil && *row.ModelMS == 900
assert row.TTFTMS == nil
```

Cover idempotent upsert, known zero, unknown-as-null, and no provider payload column.

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend/core && go test ./chat -run 'TestPersistRunPerformance|TestLoadRunPerformance' -count=1`

- [ ] **Step 3: Add ORM model and migrations**

Create `chat_run_performance` with `run_id` primary key; indexed `conversation_id`, `history_id`, and `user_id`; nullable numeric fact columns; status/model/version/timestamps. Add the table to ORM reset ordering and both PostgreSQL/SQLite migration modes.

- [ ] **Step 4: Implement idempotent store/load helpers**

Use `clause.OnConflict{Columns: []clause.Column{{Name: "run_id"}}, UpdateAll: true}` after validating non-empty ownership identifiers. Derive response-only rates from stored facts; never persist provider arrays.

- [ ] **Step 5: Run tests and verify GREEN**

Run: `cd backend/core && go test ./chat ./common/orm ./migrate -count=1`

### Task 3: Persist authoritative metrics and hydrate chat history

**Files:**
- Modify: `backend/core/chat/conversation_logic.go`
- Modify: `backend/core/chat/conversation.go`
- Modify: `backend/core/chat/recovery.go`
- Modify: `backend/core/chat/chat_stream_test.go`
- Modify: `backend/core/chat/conversation_logic_test.go`
- Modify: `backend/core/chat/recovery_test.go`

**Interfaces:**
- Consumes: Task 1 stream metrics and Task 2 store helpers.
- Produces: live SSE chunks and history items with identical `performance_metrics` summaries.

- [ ] **Step 1: Write failing integration tests**

Assert that a terminal Algorithm frame results in:

```json
{"runtime_event":{"type":"run_finished"},"performance_metrics":{"schema_version":1,"model_ms":900}}
```

in live SSE, one database row only when the matching history run is persisted, and the same summary on `GetConversationHistory`. Add single-answer and dual-answer cases.

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend/core && go test ./chat -run 'Test.*Performance.*(Stream|History|Dual)' -count=1`

- [ ] **Step 3: Implement authoritative capture/persistence**

Carry metrics only with accepted terminal decisions, pass them through `publishRuntimeChunk`, and upsert after the matching history row succeeds. Ignore late/mismatched runs.

- [ ] **Step 4: Implement batched history hydration and lifecycle cleanup**

Load metrics for all run IDs on a history page in one query and attach summaries by run ID, including nested dual answers. Retain rows in trash. Add `ChatRunPerformance` deletion to `purgeConversation` and delete the rejected dual-answer row when `SetChatHistory` removes it.

- [ ] **Step 5: Run tests and verify GREEN**

Run: `cd backend/core && go test ./chat -count=1`

### Task 4: Emit derivable context facts and make local observation storage writable

**Files:**
- Modify: `algorithm/lazymind/chat/service/run_metrics.py`
- Modify: `algorithm/tests/chat/test_run_metrics.py`
- Modify: `docker-compose.yml`

**Interfaces:**
- Consumes: last adapted model usage frame and configured model context window.
- Produces: optional `context_input_tokens` and writable `data/observability` summary/full JSONL files.

- [ ] **Step 1: Write the failing context-fact test**

Assert the last model input is emitted independently of aggregate input:

```python
assert metrics['input_tokens'] == 150
assert metrics['context_input_tokens'] == 90
assert metrics['context_ratio'] == 0.09
```

- [ ] **Step 2: Run the test and verify RED**

Run: `cd algorithm && PYTHONPATH="$PWD/lazyllm:$PWD" python -m pytest -q tests/chat/test_run_metrics.py -k context_input_tokens`

- [ ] **Step 3: Implement the fact and Compose mount**

Emit `context_input_tokens` when observable. Configure Chat with:

```yaml
- ./data/observability:/var/lib/lazymind/observability
LAZYMIND_OBSERVABILITY_DIR: /var/lib/lazymind/observability
```

- [ ] **Step 4: Run tests and validate Compose**

Run:

```bash
cd algorithm && PYTHONPATH="$PWD/lazyllm:$PWD" python -m pytest -q tests/chat/test_run_metrics.py tests/chat/test_local_observation.py
docker compose config --quiet
```

### Task 5: Restore frontend statistics from hydrated history

**Files:**
- Modify: `frontend/src/modules/chat/components/newChatContainer/types.ts`
- Modify: `frontend/src/modules/chat/utils/performanceStats.ts`
- Modify: `frontend/src/modules/chat/utils/performanceStats.test.ts`
- Regenerate: `frontend/src/api/generated/core-client/api.ts`
- Regenerate: `frontend/scripts/openapi/specs/core.yaml`
- Regenerate: `frontend/scripts/openapi/.openapi-cache.json`

**Interfaces:**
- Consumes: `performance_metrics` on live chunks and history items.
- Produces: identical folded session statistics before and after refresh.

- [ ] **Step 1: Write failing restored-history test**

Build a literal history-shaped assistant message with persisted base facts and assert derived cache rate, token throughput, and context ratio match the live shape.

- [ ] **Step 2: Run the test and verify RED**

Run: `cd frontend && npm test -- --run src/modules/chat/utils/performanceStats.test.ts`

- [ ] **Step 3: Add typed history fields and derivation fallbacks**

Add `performance_metrics` to `ChatMessage` and nested answers. Prefer provided derived fields for live compatibility and calculate them from facts when a restored record omits them.

- [ ] **Step 4: Regenerate Core OpenAPI client and verify tests/typecheck**

Run:

```bash
cd frontend
npm run gen:openapi -- core --skip-cache
npm test -- --run src/modules/chat/utils/performanceStats.test.ts src/modules/settings/SettingsPage.developer.test.tsx
npm run typecheck
```

### Task 6: Full verification and container rebuild

**Files:**
- Verify all modified files.

**Interfaces:**
- Consumes: completed tasks 1-5.
- Produces: tested source and running affected development containers.

- [ ] **Step 1: Run repository quality gates**

Run:

```bash
make lint
cd backend/core && go test ./...
cd algorithm && PYTHONPATH="$PWD/lazyllm:$PWD" python -m pytest -q tests/chat/test_run_metrics.py tests/chat/test_local_observation.py
cd frontend && npm test -- --run src/modules/chat/utils/performanceStats.test.ts src/modules/settings/SettingsPage.developer.test.tsx && npm run typecheck
docker compose config --quiet
```

- [ ] **Step 2: Inspect diff and commit implementation**

Review `git diff --check`, `git status --short`, and the diff against `origin/main`. Stage only task files and commit with a scoped message.

- [ ] **Step 3: Build affected images**

Run:

```bash
docker compose build core frontend
docker compose up -d --force-recreate core frontend chat
```

Chat is recreated, not image-built, because this Compose service uses the algorithm image plus bind-mounted LazyMind/LazyLLM source.

- [ ] **Step 4: Runtime smoke verification**

Check container health, verify Chat sees `LAZYMIND_OBSERVABILITY_DIR`, confirm `/var/lib/lazymind/observability` is writable, and inspect logs for migration/stream/local-observation errors.
