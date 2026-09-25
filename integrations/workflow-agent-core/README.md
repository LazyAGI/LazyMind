# Workflow agent integration

This is a source library bundled into host plugins, not a server or an MCP proxy.
Agents continue to call LazyMind through MCP; the shared runtime coordinates host
turns and the reverse path from Panel commands to HostAction delivery.

## Boundaries

- `protocol`: LazyMind result/control parsing and URL validation; no host event formats.
- `runtime`: binding restoration, turn/tool admission, granted execution draining,
  nested result return, HostAction polling/claim/settle and uncertain-send recovery.
- `transport`: authenticated loopback Bridge HTTP client. Credentials are injected;
  file access and profile locking belong to the host bootstrap.
- `panel-store`: optional session-scoped UI presentation state; no credentials or runtime imports.

There are no host SDK or Node imports in this library. The DSH build currently
imports the source by relative path and bundles it into both published entry
points. It does not require another installed package at runtime. Protocol and
UI entry points remain separate from the credential-bearing transport.

## Implementing a host

Implement the minimal `RuntimeAdapter<A>` delivery surface with an opaque host
agent handle: `id`, `resolve`, `prompt`, `cancel` and `warn`. The optional
`reconcile`/`eventSeq` methods provide durable receipt evidence; without them,
an uncertain delivery stays unknown. Hosts with turn/tool hooks can additionally
provide `parent`, `isLive`, `isRunning`, `history` and `canReturnResult` for
ownership recovery, nested workers and result coordination. Normalize tool
operations, input sources and history into `ToolCall`, `HostInput` and `HostEvent`
only when those hooks exist. Never treat a host tool name or a Panel read alone
as proof of driver ownership.

Create a coordinator and dispatcher with one shared AbortSignal. Wire host hooks:

1. Before each turn/step, call `beforeTurn`; false means reject automatic entry.
2. Before tools, call `beforeTool`; a returned reason means deny the call. Use
   `denial` for synchronous host guards, with the actual caller's handle.
3. After successful Workflow tools, call `afterResult`, then `shouldConclude` to
   end the turn when required. Keep the original MCP result successful if only
   post-commit control synchronization failed.
4. Forward idle/disposal to `idle`/`forget`. For nested tool logs, consume the
   optional `takeNestedLink` metadata in the host's own event format.
5. Run `dispatcher.poll()`. Abort, await polling and all in-flight hook work, then
   dispose registrations and the coordinator at shutdown.

`prompt` resolves after host admission and returns its durable event sequence
(zero is allowed on initial admission if the host has acknowledged receipt).
It must use the action ID as the input correlation ID. A thrown exception after
dispatch becomes unknown, never a blind retry. When available, `reconcile` returns the exact
persisted input sequence; zero means no evidence, not permission to resend.
Hosts without compatible durable input evidence cannot claim full automatic
recovery under the existing Core receipt protocol.

Cancellation is targeted by the coordinator's Workflow ownership; host `cancel`
must act on the supplied handle without silently switching to another session.
Optional goal hooks adapt a host's autonomous-loop facility; its private goal
formats and revision handling remain in the host adapter.

DSH is the production adapter. The SDK-free fake adapter in
`../dsh-workflow/tests/shared-runtime.test.ts` exercises the same shared runtime.
Run `pnpm typecheck`, `pnpm test`, and `pnpm bundle` in `../dsh-workflow` to validate
both the shared library and DSH. See that package's README for deterministic CLI
archive generation. A second production host still needs its native adapter and
installation integration.
