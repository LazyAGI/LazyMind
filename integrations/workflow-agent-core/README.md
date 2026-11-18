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
2. Forward recognized Workflow tools through the original MCP implementation.
   Normalize operation names with `workflowOperationName`; execution handles and
   Core admission remain authoritative for mutations.
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

An `awaiting_user` state may carry `control.input_request`. Show the returned
questions and wait for an actual user answer; `workflow.input.answer` submits that
answer against its artifact revision. It does not approve a separate review or
resume a stopped run. An explicit user turn can answer while automatic turns remain
paused. Only a subsequent Core control response may admit the next execution.

DSH provides the SDK-hook adapter. Codex has a separate native-session adapter in
`../codex-workflow` that reuses the shared dispatcher; availability of its native
control channel must be checked on the installed host. Sharing this library alone
does not prove that panel-to-host wake or cancellation works on a particular machine.
The SDK-free adapter in `../dsh-workflow/tests/shared-runtime.test.ts` exercises the
same coordination and delivery rules without depending on either host SDK.
Run `pnpm typecheck`, `pnpm test`, and `pnpm bundle` in `../dsh-workflow` to validate
both the shared library and DSH. See that package's README for deterministic CLI
archive generation. Additional hosts implement their native adapter and installation
integration while preserving the same MCP contracts and receipt rules.
