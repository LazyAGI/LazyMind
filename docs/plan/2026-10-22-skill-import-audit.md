# Skill import audit and completion plan

> **For agentic workers:** Use superpowers:subagent-driven-development. Each scope must retain a failing regression, its fix, and fresh verification evidence.

**Goal:** Make personal, administrator, workflow, and Chat skill imports use consistent source handling, bounded fast acquisition, and responsive interactions.

**Architecture:** Keep website adapters and package acquisition in Go. Reuse that importer from Chat through an authenticated atomic API. Fetch independent documents and files with bounded workers while preserving deterministic packages, shared byte budgets, and cancellation. Reuse metadata normalization across personal import and administrator publication.

**Tech stack:** Go/net/http/GORM, Python requests/Chat tools, React/Ant Design/Axios, Vitest, pytest.

## Constraints

- Work only in `codex/github-skill-import`; the other chat's `codex/skill-link-import` is independent.
- Preserve source URLs, disabled Chat installations, strict pinned built-in sources, package validation, and typed over-limit errors.
- Archive transfer limit: 100 MiB; expanded package: 128 MiB; individual file: 32 MiB; entries: 512.
- Multiple skills require a specific skill link; no unbounded repository-wide selection UI.
- Caller cancellation must terminate outstanding acquisition; invalid, unsafe, ambiguous, and oversized packages must not trigger broader fallback.

## Baseline evidence

- All Go `skillv2/...` and builtin bundle tests pass at `40a4a014`.
- Two 16-file directory imports with 30 ms artificial request latency take 1.006 s, maximum file concurrency 1; tree metadata repeats.
- Two skills.sh resolutions scan 32 documents serially; a rate-limited resolver plus download fetches the same archive twice.
- Raw response truncated from an advertised 100 bytes to 5 bytes is accepted.
- Both URL modals prevent cancellation during a request lasting up to five minutes; personal file import has no visible pending state.
- Chat has a separate 20 MiB GitHub-only importer. Its non-atomic mkdir/write/cleanup path can trash a competing installation.
- Administrator publish skips metadata compatibility applied to personal imports.

## Task 1: Acquisition performance and integrity

**Owner:** backend worker. **Files:** `backend/core/skillv2/sourceurl/*`, `service/*downloader*`, regression tests.

- [x] Keep controlled network probes as regressions for request counts, bounded parallelism, deterministic bytes, short responses, and cancellation.
- [x] Fetch independent candidate documents and selected package files concurrently with a fixed worker bound.
- [x] Give metadata caches stable identity across byte-counting transports; reuse per-import discovery results/archive data without unbounded file caching.
- [x] Reject short/mismatched raw files, preserve all budgets, and measure the same fixtures after changes.

## Task 2: Responsive import UI

**Owner:** frontend worker. **Files:** `skillApi.ts`, `skillUpload.ts`, personal/admin import components, locales, tests.

- [x] Add AbortSignal plumbing and cancellation cleanup; suppress late results after cancel/unmount and duplicate submissions.
- [x] Show local upload progress and pending state; freeze controls that mutate an in-flight selection.
- [x] Accept ZIP consistently and reject files exceeding 100 MiB before upload.
- [x] Refresh current filters/tags after success; do not describe a committed import as failed when a later refresh fails.
- [x] Retain inline actionable errors, retry, and original-link Chat draft handoff.

## Task 3: Chat and workflow boundaries

**Owner:** boundary worker. **Files:** Go RemoteFS routes/handlers/service helper, Python remote store/Chat installer/SDK, workflow request limits, tests.

- [x] Reproduce stale mkdir/write/trash race and assert a competing skill survives failed installation.
- [x] Add authenticated atomic URL installation through the shared Go importer; use it from Chat without a network scan of installed source URLs.
- [x] Preserve disabled installation and duplicate semantics; align SDK and workflow payload limits with archive transfer limits.
- [x] Verify invalid/binary/oversized imports leave no skill rows; document orphan object behavior if storage cleanup cannot be transactional.

## Task 4: Metadata and integration parity

**Owner:** root. **Files:** shared metadata normalization, market service/handler, integration tests and this plan.

- [x] Add failing market publication cases for recoverable plain-colon YAML, long Unicode descriptions, and verified canonical source names.
- [x] Share normalization/fallback behavior with personal imports, retaining originals in metadata and rejecting ambiguous YAML/path-like names.
- [x] Verify source provenance and package resources survive each entry point.
- [x] Run focused Go/Python/frontend checks, controlled before/after measurements, real website imports, and browser interaction checks.
- [x] Review diffs independently, resolve findings, run final checks, and record limitations honestly. Commit the verified change after this checklist is complete.

## Acceptance evidence

Record actual commands/results and measured counts below as tasks finish. Full TypeScript and unrelated workflow baseline failures must be distinguished from regressions; no claim of universal website support or bypassing source/network limits.


### Implemented behavior

- Source acquisition uses six workers, a stable bounded metadata cache and request-owned selected-package reuse. Resolution and download share one 100 MiB transfer allowance. Raw files are checked against tree size and Git blob SHA before ZIP assembly; mismatch recovers through the pinned archive.
- All ZIP readers enforce the 100 MiB archive size on disk, including direct uploads and self-extracting ZIP preambles. The 32 MiB/file, 128 MiB expanded and 512-file limits remain enforced.
- Personal/admin URL import and local upload support cancel, progress, retry, duplicate-submit suppression and unmount cleanup. A late request cannot overwrite a newer attempt. Success refreshes current filters and metadata tags without converting refresh failure into import failure.
- Chat and local Python package installation use the authenticated atomic Core importer. New Chat installations remain disabled; exact retries reuse the existing source without downloading it again. Removed the old independent Python GitHub downloader and path-based failure cleanup.
- Administrator publishing retains verified website names and uses shared URL metadata compatibility. Fixed binary object metadata insertion to persist SQL NULL. URL imports retain request fallback metadata, source URL provenance and original resources; no synthetic executable is added.

### Verification results

- `go test ./skillv2/... ./cmd/builtin-skill-bundle ./remotefs`: passed after the final SHA and archive-cap changes.
- Full `go test -race ./skillv2/sourceurl ./skillv2/service ./skillv2/remotefs`: passed. Additional focused SHA/concurrency/cancellation/budget race tests passed after SHA verification was added.
- `go vet ./skillv2/... ./remotefs ./workflow`: passed.
- Core OpenAPI/route tests and the 25 MiB workflow request regression passed. Exported public OpenAPI is byte-identical to the checked-in frontend spec because the new RemoteFS endpoint is private.
- Python: 139 tests passed with the real read-only main-checkout LazyLLM runtime (atomic Chat, RemoteFS transport, package store, execution fixture, SDK, tool errors).
- Frontend: 9 test files / 73 tests passed; changed-file ESLint and production build passed.
- Browser: actual administrator modal verified with isolated API responses for pending/cancel and oversize/Chat recovery. The original query/fragment survives in an editable unsent Chat draft. Screenshot: `skill-import-recovery.jpg` in this chat's visualization directory.
- Final live import through resolution, acquisition, metadata, blobs and revision persistence passed for all four sites: skills.sh 7.92 s, SkillsMP 4.03 s, ClawHub 2.63 s, SkillHub 0.46 s (15.04 s total). An earlier baseline run took about 101 s. These are network-sensitive observations; use the controlled measurements below for isolated performance comparisons.

| Controlled fixture | Before | After |
|---|---:|---:|
| Two imports × 16 files, 30 ms/request | 1.001 s, one worker, two tree requests | 0.200 s, six workers, one tree request |
| skills.sh × 16 documents, 20 ms/request | 342 ms | 64 ms |
| Archive discovery plus selected import | Two identical repository downloads | One repository download |

### Known limits and retained contracts

- Public sources still depend on upstream availability. Multiple-skill repositories require a specific skill link; unsupported/private catalog pages require a public GitHub directory or ZIP. Chat does not bypass package limits.
- PostgreSQL advisory-lock behavior was reviewed; executable integration tests use SQLite. No production PostgreSQL instance was available for this task.
- Existing content-addressed storage may retain an unreferenced binary object after a later database rollback. Skill/revision/draft/blob rows roll back; removing shared hash objects without a safe collector could damage concurrent successful imports, so this broader storage lifecycle is unchanged.
- Full TypeScript checking still reports existing unrelated diagnostics. Previously reproduced unrelated workflow model-selection test failures are not import regressions. Scope-specific tests, frontend build, and checks above pass.
- Builtin/cloud imports retain their stricter locked-source and validation contracts. Imported skill uploads remain subject to the existing upload-session lifecycle.

Independent reviews covered the download, metadata, UI and atomic installation boundaries. Reported error-code and equal-length corruption findings were fixed and rechecked. No remaining actionable issue was found against this plan.
