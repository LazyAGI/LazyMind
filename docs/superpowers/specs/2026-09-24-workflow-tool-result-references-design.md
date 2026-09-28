# Workflow Tool-Result References Design

## Goal

Make Workflow tool-result compaction recoverable and stable under input overflow: protected recent turns retain their call/result structure while their result bodies may be offloaded; online and durable projections share one stored payload and one resolver-supported reference.

## Scope

This change applies only to Workflow tool results.  It does not change Workflow artifact persistence: `large/` remains the workspace location for large text and JSON artifacts created by `save_artifacts`.

## Storage and Reference Contract

`tool_spills/` is the sole payload store for compacted tool results.  It continues to use a content-derived SHA filename, so identical payloads in the same workspace map to one file.

Model-visible and persisted tool-result notices use the new form:

```
workspace://tool_spills/<safe-tool-name>_<sha256-prefix>.txt
```

The text-resource resolver recognizes only this URI form for compacted Workflow tool results.  It resolves the path against the current trusted Workflow workspace, rejects traversal, non-`tool_spills/` paths, and missing files.  A legacy `large/...` value is not interpreted as a tool-result reference and fails explicitly.  Existing artifact paths under `large/` keep their current artifact-specific handling.

## Compaction Behaviour

On overflow, the Workflow history compactor first attempts to replace every complete tool-result body with its shared spill notice, including results in the last `keep_recent` turns.  Tool-call messages and `tool_call_id` pairings remain unchanged.  If the request still exceeds budget, it removes only complete, non-protected prior turns.  For the current turn it may compact result bodies but never deletes messages.

The persistence projection in `_persist_step()` uses the same planning, write, filename, notice, and URI code as online history compaction.  It no longer calls the `large/` helper for tool results, so a payload cannot become both `tool_spills/` and `large/` merely because it is persisted and later compacted.

## Failure Handling

A failed spill write leaves the original result inline rather than silently writing a different truncated copy.  The compactor can then apply its existing safe turn-removal policy if necessary.  No new partially recoverable reference is emitted.

## Tests

Regression coverage will prove that:

1. an overflowing protected recent turn retains its tool-call/result pairing but receives a spill reference;
2. online and persistence paths render the same URI and create one shared `tool_spills/` payload for identical content;
3. a new URI resolves only inside the active Workflow workspace; traversal, missing targets, and legacy `large/...` tool-result references fail; and
4. large artifact storage in `large/` remains unchanged.
