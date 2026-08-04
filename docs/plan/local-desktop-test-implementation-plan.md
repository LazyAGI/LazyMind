# Local and Desktop Test Implementation Plan

Status: in progress

## Batch 1: CI safety net

- [x] Add Local Runtime Manager tests to the standard test entry point.
- [x] Add Desktop shell tests to the standard test entry point.
- [x] Run the batch on macOS and run `make lint`.
- [x] Commit batch 1.

## Batch 2: shared runtime-mode contract

- [x] Parameterize shared Local/Desktop frontend behavior tests.
- [x] Cover auto-login, feature visibility, API routing, and readiness differences.
- [x] Run the batch on macOS and run `make lint`.
- [x] Commit batch 2.

## Batch 3: runtime smoke harness

- [x] Add reusable Local/Desktop runtime smoke scenarios.
- [x] Cover start, readiness, session/API access, stop, and cleanup.
- [x] Cover shared-data and ownership conflict behavior where practical.
- [x] Run the batch on macOS and run `make lint`.
- [x] Commit batch 3.

## Batch 4: Electron behavior tests

- [x] Add behavior-level preload/IPC contract tests.
- [x] Cover bridge success, failure, arguments, and listener cleanup.
- [x] Cover Desktop startup/readiness and shell-only behavior.
- [x] Run the batch on macOS and run `make lint`.
- [x] Commit batch 4.

## Batch 5: packaged application smoke tests

- [x] Add reusable post-install/post-package application smoke tooling.
- [x] Wire Windows installer verification to launch and verify the runtime.
- [x] Wire macOS packaged application verification into the installer workflow.
- [x] Run all locally runnable macOS tests and `make lint`.
- [x] Commit batch 5.

## Completion

- [x] All five commits exist and the worktree contains no uncommitted task changes.
- [x] Final macOS test and lint results are recorded.
