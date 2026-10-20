# Workflow knowledge-base clarification binding implementation plan

> Execute in the isolated `fix/kb` worktree. Follow test-driven development and
> verify each task before moving on.

## Task 1: Capture the regression in tests

1. Add table-driven unit tests in `backend/core/chat/mentions_test.go` for a
   helper that restores knowledge-base mentions from the matching pending ask.
2. Cover the successful workflow-startup case, mismatched ask ID, missing
   workflow mention, current-mention preservation, and deduplication.
3. Run the focused tests and confirm they fail because the helper is absent.

## Task 2: Implement narrow restoration

1. Add a helper in `backend/core/chat/mentions.go` that reads the structured ask
   submission and matching history metadata.
2. Require the same history record to contain the pending ask, an explicit
   workflow mention, and knowledge-base mentions; copy only knowledge bases.
3. Invoke the helper in `backend/core/chat/conversation.go` immediately before
   `applyChatMentions`, so inherited IDs use the normal existence and ACL path.
4. Format the changed Go files and rerun the focused tests until green.

## Task 3: Verify behavior and regressions

1. Add or retain an integration-level assertion that the restored mention
   becomes an explicit `knowledge_base_ids` binding.
2. Run `go test ./chat` from `backend/core`.
3. Run any repository lint or validation command required by CI for changed Go
   files, and inspect the final diff for scope creep.

## Task 4: Publish and validate remotely

1. Commit the design, tests, and implementation on branch `fix/kb`.
2. Push `fix/kb` to the configured remote and create a pull request if CI only
   runs for pull requests.
3. Monitor every required remote check to completion. Diagnose and fix any
   in-scope failure, push the correction, and repeat until CI is green.
