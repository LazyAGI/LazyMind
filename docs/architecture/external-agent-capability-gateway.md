# External Agent capability gateway

LazyMind exposes user-authorized models and tools to supported external Agents
through a local, server-side capability gateway. Provider credentials remain in
LazyMind; the gateway returns capability metadata and execution results only.

## External contract

Supported Agent identities are Codex, Cursor, WorkBuddy, Raccoon, TRAE Work,
and DeepSeek Harness. The LazyMind CLI exposes the gateway to those clients as
one MCP server with four tools:

- `model.list` and `tool.list` return only capabilities currently authorized
  for the calling Agent.
- `model.chat` proxies an OpenAI-compatible chat completion through LazyMind.
- `tool.call` executes an authorized MCP tool or an explicitly exposed
  LazyMind built-in tool. The first built-in adapter is `image_generator`.

Users manage grants per Agent under **Settings → Assistants**. The management
API is `GET/PUT /external-agent-capabilities`; invocation history is available
from `GET /external-agent-capability-invocations`. New grants default to off.

## Enforcement and credentials

Every invocation rechecks all of the following before execution:

1. the caller carries a supported Agent identity;
2. a per-user, per-Agent grant is still enabled;
3. the model or tool still exists, is enabled, and has a verified connection;
4. the requested operation is one of the externally exposed operations.

LazyMind resolves model API keys and MCP headers only after those checks and
performs the upstream request itself. List and call responses never include
stored credentials. Built-in tool results are recursively sanitized before
they leave the Chat service, including removal of local filesystem paths.
External callers cannot update provider, model, MCP server, or credential
configuration through this surface.

Disabling or deleting a connection, losing verification, or revoking a grant
takes effect on the next call. Errors distinguish invalid input, denied access,
unavailable connections, oversized results, and internal failures, and include
a recovery action without echoing upstream secrets.

## Audit

Each attempted `model.chat` or `tool.call` records owner user, Agent identity,
invocation ID, capability ID and name, start/finish time, status, sanitized
error category, available token or result-size usage, and a bounded result
preview. Model previews contain only the returned content and completion
metadata. Tool previews are recursively redacted by key and value patterns;
long strings, collections, and the final serialized preview are capped.
Request bodies, authorization headers, API keys, tool tokens, and raw provider
errors are not stored in the invocation record.

The Assistants settings page exposes this audit data to the owning user with
per-Agent totals, model/tool and failed-call counts, per-capability call counts,
the 50 most recent invocation records, and an on-demand result viewer. Local
generated-image results include a visual preview. Aggregates are computed
across the complete matching history rather than only the displayed page.

The user-facing setup and operation guide is available in
[`docs/external-agent-capabilities.CN.md`](../external-agent-capabilities.CN.md).
