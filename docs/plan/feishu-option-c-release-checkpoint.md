# Feishu Option C release checkpoint

Date: 2026-08-10

## Release decision

This checkpoint makes Core the owner of ExternalAgent business state and keeps
the channel gateway responsible for durable delivery and Feishu navigation.
The previous architecture inventory's generated page count is not a component
count: its generator paginates symbols and call edges. It is not used as a
cleanup metric.

## Deleted ownership

- Feishu no longer interprets provider approval payloads. Core publishes a
  bounded canonical request view and accepts only opaque action IDs.
- ExternalAgent chat no longer travels through the generic conversation
  executor. Core exposes a typed run SSE endpoint and owns binding, run,
  request, terminal and release state.
- Gateway no longer stores provider thread targets or reduces raw provider
  events into a second run state. Each streamed frame carries the current Core
  snapshot; Feishu only renders it under message/revision/operation fences.
- Assistant remote CardKit actions use the existing durable inbox/outbox path
  instead of daemon threads.
- Durable presentation caches and duplicated task-image delivery state were
  removed. Task images use deterministic child outboxes.

## Remaining owners

- Core: conversation binding, provider thread, run, pending request, approval
  mapping, terminal and control release.
- Gateway: inbox/outbox leases, idempotency, rendered-part recovery, Feishu
  message identity, UI navigation and CardKit replacement.

## Verification

- `go test -race ./externalagent ./chat ./subagent`
- Gateway `unittest discover`: 91 tests
- `git diff --check`
- Independent release audit: P0=0, P1=0 for direct run, replay fencing,
  subscriber cancellation, start persistence and detached create commit.

## Next structural cut

The remaining large duplication is the Gateway common command/capability/
conversation business engine. Its replacement requires one provider-neutral
Core channel-command endpoint; it is intentionally not mixed into this release
checkpoint. The acceptance metric for that follow-up is production net LOC,
not files moved or generated architecture pages.
