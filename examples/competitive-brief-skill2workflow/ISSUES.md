# Observed issues; no production fix in this PR

## Intermediate output judged as final deliverable

The published `analyze_positioning` node declares a `positioning_analysis` JSON output. Attempt 1 wrote that artifact, then failed with `objective_incomplete`: the verifier required the complete seven-section Markdown report, including pricing arithmetic, which belongs to the downstream `synthesize_brief` node. The external task became `failed` / `WORKFLOW_EXECUTION_FAILED`.

Evidence: [failed step task](evidence/http/analysis-task-failed.json), [failed projection](evidence/http/projection-failed.json), [terminal external task](evidence/http/tree-status-8.json), and the unchanged [generated graph](evidence/generated/workflow.yaml).

**Hypothesis, not a proven code-level root cause:** the end-to-end user objective is being used as the intermediate step's completion criterion. The successful retry explicitly scoped the objective/user input/runtime instruction to the intermediate JSON contract while retaining every final-report requirement for synthesis. This intervention supports the hypothesis but does not establish which layer needs a fix.

Reproduce with the pinned directory URL, frozen task/corpus and a fresh hosted task as documented in [Reproduction](REPRODUCTION.md). Model generation is nondeterministic; retain the generated package and deployment provenance when comparing attempts. Do not weaken final acceptance or replace auto-generated YAML to claim success.

## URL and dependency fidelity

The user-facing `/blob/.../SKILL.md` URL queued successfully but later failed installation. Using the supported directory URL recovered installation. The imported directory omits the Skill's parent-relative CONNECTORS reference; Preflight warned about missing web-search capability configuration but did not report that missing file. It also adds a default script absent from upstream. See [audit](AUDIT.md) and [snapshot metadata](evidence/skill-snapshot-metadata.json).

The affected source here is `marketing/skills/competitive-brief` at the pinned
commit, not product-management's homonym. The [local packaging supplement](PACKAGING.md)
copies the matching marketing CONNECTORS into the generated archive and verifies
its link locally. This repairs that standalone package's dependency closure; it
does not fix Claude.ai's installer or prove the adapted package runs in LazyMind.

## Review and recovery boundaries

Every generated node has `requires_approval: false`; no actual human approval occurred. A manual advance request took longer than the recorder's 90-second HTTP timeout, but subsequent projection showed it had succeeded. A transport timeout is not evidence that a command failed. Inspect command/session state before retrying a mutation.

The already terminal external task did not resume itself after the separate session retry. Preserve both task and session states. Do not overwrite the original task's failure with a recovered session result.

## Artifact edit failure in this deployment

After session completion, PATCH of the report artifact with base revision 1 and draft version 1 returned HTTP 503 `ARTIFACT_PATCH_FAILED` (retryable). The documented native slot-item PATCH returned HTTP 500 `patch item failed`. Both requests attempted only an explicit agent-authored annotation; neither claimed human review. Subsequent reads retained revision 1 and the original bytes. Evidence: [facade response](evidence/http/edit-response.json), [native response](evidence/http/slot-edit-response.json), [unchanged slots](evidence/http/slots-final.json).

The serving binary lacks a VCS revision and no private server/database logs were inspected, so the cause is unresolved. This is a deployment observation, not a proven defect in the audited current-main commit. No production repair is included in this documentation PR.
