# Cross-mode review fixes — 2026-09-21

Baseline: origin/codex/notifications-official-main at 0721097199e93ad511c8e2481db5c90a2704521e (fresh clone; remote tip rechecked).

- [x] Inspect implementation, review evidence and existing regression harnesses.
- [x] F1: reproduce stale refresh/account switch; session-scoped overlay and refresh dedup, request isolation, early logout invalidation, preload sync and guarded local recovery.
- [x] F2: reproduce 1999/2000/2001 browser/native behavior; bounded continuation and cursor invalidation tests.
- [x] F3: deterministic final UPDATE race test; conditional ack preserving disabled status/reason.
- [x] F4: serialize start/stop/recover; hot-restart and reused-PID regressions using isolated fake services.
- [x] F5: delayed old-child exit; identity-bound cleanup and wait for actual exit after force signal.
- [x] Focused and expanded suites, typecheck/build, diff review, baseline failure reproduction. See verification.md.

No push, commit, native notifications or changes to running services. Live PostgreSQL and native macOS/Windows E2E remain external acceptance work.

## Follow-up: 10 baseline failures, preserve existing UI
- [x] Reproduce account-management 7 and desktop startup 3 failures; inspect current implementation and history.
- [x] Update stale tests to current account UI and parser-readiness contract without production UI changes.
- [x] Run focused and expanded regression suites; document changed coverage and limitations.
