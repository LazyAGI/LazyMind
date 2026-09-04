# Chat Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make chat performance observation request-scoped, locally persisted, and safe when online chat modules are shared concurrently.

**Architecture:** Carry model-call usage with the model completion event, aggregate it inside the request translator, and write summary/full JSONL records through a serialized local writer. Synchronize the frontend preference through a local utility and event, while keeping chat-history terminals free of performance metrics.

**Tech Stack:** Python, LazyLLM, FastAPI/SSE, Go preference API, React/TypeScript, Vitest, pytest.

## Global Constraints

- Do not modify unrelated untracked files.
- Do not persist prompts, assistant outputs, uploaded file contents, or API credentials in local observation records.
- Missing provider fields remain unknown rather than being converted to zero.
- Preserve existing run status, failure, retry, and stream recovery behavior.

### Task 1: Define request-scoped model-call observations

**Files:**
- Modify: `algorithm/lazyllm/lazyllm/module/llms/onlinemodule/base/model_call_runner.py`
- Modify: `algorithm/lazyllm/lazyllm/module/llms/onlinemodule/base/model_outcome.py`
- Modify: `algorithm/lazyllm/lazyllm/module/llms/onlinemodule/base/onlineChatModuleBase.py`
- Modify: `algorithm/lazymind/chat/service/component/event_translator.py`
- Modify: `algorithm/lazymind/chat/service/run_metrics.py`
- Test: `algorithm/tests/chat/test_run_metrics.py`
- Test: `algorithm/tests/chat/test_online_chat_observation.py`

**Interfaces:** Add a model-call usage payload to completion events; expose translator aggregation without reading process-global usage as a fallback.

- [ ] Write failing tests for usage attached to the matching model-call event, retries counted once, and concurrent module copies having separate IDs.
- [ ] Run the focused Python tests and confirm the expected failures.
- [ ] Implement the smallest event/state changes to carry normalized usage and aggregate by model-call ID.
- [ ] Run focused tests, then the LazyLLM/chat test subsets.
- [ ] Commit the task.

### Task 2: Isolate shared OnlineChatModule execution

**Files:**
- Modify: `algorithm/lazyllm/lazyllm/module/servermodule.py`
- Modify: `algorithm/lazymind/chat/engine/agent_runtime/executor.py`
- Modify: `algorithm/lazyllm/lazyllm/module/stream_helper.py`
- Test: `algorithm/tests/chat/test_online_chat_observation.py`

**Interfaces:** Agent execution receives a per-run module copy and request-local stream sink; synchronous final-value calls do not mutate shared stream state.

- [ ] Add a failing concurrency test that uses barriers and distinct sinks/inputs.
- [ ] Run it to verify the current shared-state failure.
- [ ] Implement per-run `share()`/sink isolation with no global lock around model I/O.
- [ ] Run the concurrency and stream helper tests.
- [ ] Commit the task.

### Task 3: Add local summary/full observation writer

**Files:**
- Create: `algorithm/lazymind/chat/service/local_observation.py`
- Modify: `algorithm/lazymind/chat/service/chat_service.py`
- Test: `algorithm/tests/chat/test_local_observation.py`

**Interfaces:** `LocalObservationWriter.write_summary(record)` and `write_full(record)` append redacted JSONL records with serialized writes and rotation.

- [ ] Add failing tests for concurrent appends, redaction, malformed input, and rotation.
- [ ] Run focused tests and confirm failure.
- [ ] Implement the writer and call it at run completion without adding metrics to the persisted terminal.
- [ ] Run local-writer and chat-service tests.
- [ ] Commit the task.

### Task 4: Make metrics semantics explicit and frontend-compatible

**Files:**
- Modify: `algorithm/lazymind/chat/service/run_metrics.py`
- Modify: `frontend/src/modules/chat/utils/performanceStats.ts`
- Modify: `frontend/src/modules/chat/utils/StreamManager.ts`
- Test: `algorithm/tests/chat/test_run_metrics.py`
- Test: `frontend/src/modules/chat/utils/performanceStats.test.ts`

- [ ] Add failing tests for unknown cache rates, run elapsed/first-output naming, and model-call aggregation.
- [ ] Implement semantic field changes while accepting legacy fields.
- [ ] Run Python and Vitest metrics tests.
- [ ] Commit the task.

### Task 5: Synchronize the frontend performance preference

**Files:**
- Create: `frontend/src/utils/performanceStatsPreference.ts`
- Modify: `frontend/src/modules/settings/index.tsx`
- Modify: `frontend/src/modules/chat/components/newChatContainer/index.tsx`
- Test: `frontend/src/utils/performanceStatsPreference.test.ts`

- [ ] Add failing tests for default-off state, local storage, custom events, server sync, and persistence.
- [ ] Implement the utility and replace duplicated preference state flow.
- [ ] Run the focused Vitest suite and the frontend type check.
- [ ] Commit the task.

### Task 6: Full verification and review

- [ ] Run all changed Python tests.
- [ ] Run all changed frontend tests and type checking.
- [ ] Inspect the final diff for database metrics writes, raw-content leakage, and unrelated changes.
- [ ] Report exact verification results and remaining limitations.
