# Chat Observability Design

## Goal

Make chat performance observation request-scoped and concurrency-safe, keep performance records out of chat history persistence, and synchronize the developer-facing performance switch like the sensitive-word switch.

## Design

- A chat run owns its observation state. Model-call completion events carry the usage for that model call, so aggregation does not select records from the process-global LazyLLM usage map.
- Shared online modules are copied for execution with isolated module identity and request-local streaming sinks. Synchronous consumers that only need a final value use non-streaming calls.
- The run terminal keeps status and recovery metadata. Performance metrics are emitted in the current SSE stream and written to two local JSONL files: a compact summary and a detailed record. The writer is serialized, append-only, bounded by file rotation, and redacts prompts, outputs, files, and credentials.
- The frontend stores the performance preference locally, synchronizes it through a custom event and the user-preferences API, and uses the SSE summary for the active run. Existing history remains compatible when metrics are absent.
- Missing provider metrics remain unknown (`null`/omitted), not zero. Names distinguish run elapsed time and first-output latency from provider-native timings.

## Acceptance criteria

1. Two concurrent calls through one logical chat module cannot cross their output sink, usage, or observation records.
2. Multiple model roles and retries are represented as distinct model-call records with stable logical call IDs.
3. Disabling the frontend preference immediately hides the performance bar across settings and chat surfaces and persists through refresh.
4. No performance metrics are written into the chat-history database payload by the new path.
5. Summary and full local records survive concurrent writes without malformed JSONL and do not contain raw prompt/output/file/credential values.
6. Existing chat, preference, stream, and metrics tests continue to pass.
