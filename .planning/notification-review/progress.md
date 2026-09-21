# Verification and implementation — 2026-09-21

## Implemented
- R1: read-only preference/reference reads; callback-free account detail for resolution; prepare/freeze explicit rules before production handler transactions; resolve inherited defaults before create/batch transactions; propagate resolution failure instead of silently disabling configured targets.
- R2: no implicit first-recipient fallback.
- R3: disconnect requires matching credential revision and runtime fence; reset per-lease account snapshot.
- R4: claim checks channel preference; channel close writes durable tombstones; dispatch preserves close reason across receipts, missing targets, and HTTP errors. Regression covers close/reopen.
- R5/R6: QR refresh returns public session view; lifecycle-managed recovery scanner, per-session/version lease, interruptible polling, fenced final account save.
- R7: pre-send authentication transport/HTTP failures distinguished from ambiguous send responses; retryable auth failures never count as sent/unknown.
- R8: bounded recovery loop separated from ready-event dispatch; summary cancellation honors parent context.
- R9: refreshed targets replace stale snapshots; late pagination responses rejected after refresh/scope changes.
- R10: verified Core source_notification_id stored with Gateway attempts; UI groups via explicit source/known gateway ID/retry ancestry; dead picker branches removed.

## Evidence
- Core targeted: `go test ./taskcenter ./scheduler .` passed.
- Gateway full: 195 passed, 4 failed. All four failures reproduced against HEAD (old target-kind expectations and resume-support expectations).
- Gateway new regression file: 13 passed; baseline red checks were also performed before fixes.
- Frontend notification suite: 72 passed, 7 failed. All seven AccountManagement tests also fail against HEAD in isolated archive.
- TypeScript full check blocked by existing syntax errors at frontend/src/modules/chat/utils/message.test.ts:49.
- `git diff --check` passed.
- `make lint` stops at Python lint violations. Changed-file residual lint findings reproduced in HEAD; newly introduced formatting issues corrected.
- Go formatting, workflow naming, migration immutability, and test location checks passed.
- Full Core `go test ./...`: fails only in common error-catalog validation and migrate aggregate notification migration (SQLite near EXISTS). Both failures reproduced with the same tests against HEAD.
- Final focused UI rerun after type narrowing: 27 passed (Notifications + TargetPicker).
- Additional source-only TypeScript check excludes test syntax blocker; reports preexisting errors elsewhere, no diagnostics in modules/notifications.

## Boundaries
- No production channel send, real PostgreSQL multi-instance failover, or end-to-end QR scan was exercised.
- Cross-service target validation is a snapshot, not a distributed transaction; gateway enqueue/claim still performs final checks. No new cross-service account revision protocol introduced.
- Recovery is bounded to one loop: a slow summary no longer blocks ready notifications, but can delay later summaries.
- Broad account-management UI/test cleanup and unrelated existing lint failures are not included.
- Original desktop/scripts/build-darwin-arm64.sh modification was not touched. No commit/push made.

## Desktop-dev follow-up (2026-09-21)
- Recovered stale development sessions: shell readiness now checks the actual Electron child, not just a surviving watcher/Vite pair. Four shell regression tests pass.
- Found repeated assistantSessionSet ENOENT: external-runtime development incorrectly selected the installer staging CLI path. Added resolveAgentConnectorPath and wired main.js to local/build/bin/lazymind in this mode; explicit override and bundled behavior preserved.
- Six new path regression tests failed before implementation and passed afterward. Combined desktop-dev/native-notification suite: 101 passed. main.js syntax and git diff --check pass.
- Live watcher restarted Electron; startup log at 2026-09-21T09:21:45.956Z confirms DESKTOP_NOTIFICATION_SESSION_VERIFIED. Authenticated notification feed returns HTTP 200.
- Actual macOS banner visibility remains unverified; do not treat session verification or delivery acknowledgement as proof of a visible banner. No journal reset or forced resend performed.
