# Chat Performance Persistence Design

## Goal

Make chat performance statistics available during a live stream and after a page refresh without turning chat history, local diagnostic files, or provider-specific usage payloads into one coupled storage model.

## Storage ownership

Four stores have distinct responsibilities:

1. `user_ui_preferences.performance_stats_enabled` remains the durable per-user feature preference.
2. The chat SSE carries one normalized `performance_metrics` summary on the authoritative `run_finished` frame for immediate display.
3. Core owns a dedicated `chat_run_performance` table containing the normalized facts required to restore the UI after refresh.
4. Chat writes redacted operational observations under `data/observability`: a compact JSONL stream and a full JSONL stream. Provider-specific usage frames and model event details stay local and never enter the product database or browser response.

The existing `chat_histories.run_terminal` remains limited to run outcome semantics. Performance fields must not be embedded in `run_terminal`, `chat_histories.ext`, prompts, or assistant content.

## Current chain defect

Algorithm emits `performance_metrics` beside `runtime_event`, but Core's `LazyChatData` and `UpstreamStreamChunk` do not declare the field. JSON decoding therefore drops it before Core forwards the terminal chunk. Core must explicitly parse, validate, persist, and forward the summary.

## Database model

`chat_run_performance` is owned by Core and has one row per authoritative run:

- Identity and ownership: `run_id` primary key, `conversation_id`, `history_id`, `user_id`, `turn_seq`.
- Version and lifecycle: `schema_version`, `status`, `observed_at`, `created_at`, `updated_at`.
- Model and work counts: `model`, `model_steps`, `tool_steps`.
- Measured durations: nullable `wall_ms`, `model_ms`, `tool_ms`, `ttft_ms`.
- Token facts: nullable `input_tokens`, `output_tokens`, `total_tokens`, `cached_tokens`, `reasoning_tokens`.
- Context facts: nullable `max_input_tokens`, `context_input_tokens`.

Only finite, non-negative values are accepted. Unknown values remain SQL `NULL`; they are not converted to zero. The row is upserted by `run_id`, and a stale run must not overwrite the current history owner's metrics.

The following are derived at read time and are not authoritative database columns:

- `cache_hit_rate = cached_tokens / input_tokens` when both values are observable and the denominator is positive.
- `tok_s = output_tokens / model duration` when both values are observable and model duration is positive.
- `context_ratio = context_input_tokens / max_input_tokens` when both values are observable and the denominator is positive.

The API may return those derived values for frontend compatibility. Provider usage arrays and raw provider payloads are never stored in this table.

## Data flow

1. Algorithm collects model/tool events and normalized usage.
2. Algorithm produces an authoritative `run_finished` frame with a redacted `performance_metrics` summary and writes the summary/full local observations independently.
3. Core validates that the runtime event belongs to the expected `run_id`, validates the metric schema and numeric bounds, and retains the summary only for an accepted authoritative terminal.
4. Core forwards the same normalized summary in the terminal SSE chunk.
5. Core finalizes chat history and upserts the matching performance row. The existing run ownership guard prevents late or retried runs from replacing a newer run.
6. Conversation history loading queries performance rows for the page's run IDs in one batch and attaches `performance_metrics` to the corresponding history items. This includes single-answer and multi-answer histories.
7. The frontend continues folding metrics from message objects; live and restored messages therefore use the same rendering path.

No extra frontend request is required for refresh restoration.

## Local observation files

The Chat service receives an explicit writable directory:

```text
LAZYMIND_OBSERVABILITY_DIR=/var/lib/lazymind/observability
./data/observability:/var/lib/lazymind/observability
```

It writes:

- `performance-summary.jsonl`: normalized redacted summaries for lightweight inspection.
- `performance-full.jsonl`: normalized metrics plus redacted provider usage/model events for diagnosis and future export.

Both files remain bounded by rotation. In-process writes use a thread lock; POSIX deployments additionally use `flock` for cross-process serialization. The design does not claim a Windows cross-process lock.

Local-write failure is logged but does not suppress the terminal SSE or database summary. Database persistence and local telemetry are independent sinks.

## Privacy, retention, and deletion

- The database table stores no prompt, completion text, headers, API keys, provider errors, or provider-specific raw usage.
- API reads are scoped through the owning conversation and authenticated user, rather than by unrestricted `run_id` lookup.
- Deleting a conversation removes its performance rows in the same deletion workflow.
- Local observations contain operational identifiers and redacted metrics only. Rotation bounds retention; they are not used as a user-facing source of truth.

## OpenTelemetry compatibility

The database uses stable LazyMind domain names instead of experimental OpenTelemetry `gen_ai.*` names. A future exporter maps the normalized facts to the then-current OpenTelemetry GenAI semantic conventions.

`run_id` remains an application correlation identifier and must not be emitted as an OpenTelemetry `trace_id` unless it is an actual valid trace identifier. Real trace/span context, when available, belongs in an export envelope alongside resource and instrumentation-scope metadata.

The JSONL files are not advertised as OTLP JSON. A future adapter or Collector pipeline can transform their stable event envelope into OTLP logs, spans, and metrics.

## Failure behavior

- Invalid or mismatched metrics are omitted and logged; the chat response still completes.
- A missing provider usage frame produces nullable token fields, not fabricated zeroes.
- A Core database persistence failure does not corrupt chat history. The live SSE may still display the summary, while logs make the loss of refresh persistence visible.
- Duplicate terminal delivery is safe because persistence is idempotent by `run_id`.

## Tests and verification

- Algorithm tests cover normalized facts, unknown usage, redaction, and local file writes.
- Core stream tests prove metrics survive Algorithm-to-Core decoding, validation, SSE forwarding, and authoritative-run selection.
- Core persistence tests cover upsert idempotency, unknown-as-null, ownership guards, batched history hydration, multi-answer history, and conversation deletion.
- Frontend tests prove a restored history message produces the same session statistics as a live terminal frame.
- Compose validation proves Chat receives a writable `data/observability` mount.
- Build and runtime verification cover `core`, `chat`, and `frontend`; only `core` and `frontend` require image builds because Chat loads the repository's mounted Python sources in this development Compose setup.
