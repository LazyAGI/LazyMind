# Workflow startup clarification knowledge-base binding

## Problem

When a user starts a workflow and explicitly mentions a knowledge base, the first
request stores both mentions in the chat-history `ext`. If the workflow asks a
startup clarification question, the clarification-card submission is sent as a
new request with `ask_answers_structured` but without the original mentions.
Core therefore builds the workflow request without `knowledge_base_ids`, and the
workflow incorrectly falls back to public search.

The 2026-10-09 `AI PPT Planner` conversation reproduces this exactly: history
sequence 1 contains the workflow mention, knowledge base `aaa`, and the pending
startup ask; sequence 2 contains the matching structured answer and no mentions.

## Scope

Restore explicit knowledge-base mentions only while submitting the startup
clarification card that was created by the same workflow-selection turn. Do not
introduce general cross-turn mention inheritance, and do not inherit skills,
tools, conversations, or unrelated workflows.

## Design

Before current-turn mentions are resolved, inspect a formal
`ask_answers_structured` submission. Walk chat history newest-first and locate the
unanswered pending ask whose `ask_id` matches the submission. It qualifies as a
workflow-startup continuation only when that same history record contains an
explicit workflow mention.

For a qualifying history record, copy only its `knowledge_base` mentions into
the current raw request. Preserve all current mentions and deduplicate by
knowledge-base resource ID. The ordinary mention resolver then performs the
existing dataset-existence and read-ACL checks, merges dataset IDs into the
request, and returns the existing user-visible error if the resource is no
longer available.

This makes the binding narrow and self-expiring: it applies only to the answer
for the exact pending ask, and later workflow turns cannot inherit it because
they do not carry the original explicit workflow mention and matching startup
ask in the same history record.

## Failure and security behavior

- Missing, malformed, mismatched, or already-answered asks do not inherit.
- A current explicit knowledge-base mention wins naturally; duplicates are
  removed and other current mention types are preserved.
- An inherited knowledge base is revalidated by the existing resolver, so
  revocation or deletion between clarification and submission fails closed.
- No historical resource is silently enabled during an ordinary chat turn.

## Verification

Add focused tests for successful restoration, ask-ID mismatch, absence of a
workflow mention, preservation of current mentions, deduplication, and ACL or
deletion revalidation through the existing resolver. Run the focused tests,
then the complete Core chat package tests before pushing.
