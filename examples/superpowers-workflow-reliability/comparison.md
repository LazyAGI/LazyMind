# What changes when execution enters a Workflow?

The original native Skill normal sample **passed**. All negative cases below are
explicit injections, not naturally reproduced upstream failures. No fair repeated
trial supports reliability, latency, token or cost improvement percentages.

| Issue / check | Native Skill | Native plugin showcase | Advanced six-step executor | Evidence / boundary |
| --- | --- | --- | --- | --- |
| #2282 Brief | Normal published run passed | Native check must succeed before Core authorizes implementation | Rechecks nonempty on-disk Brief immediately before owned Codex launch | [Case](cases/issue-2282.md), [native record](evidence/public/native.json); artifact vs filesystem checks differ |
| #2282 state | Skill instructions | Core graph, attempts, Panel and human review | Same Core primitives plus persisted in_progress/completed events | No Codex Todo UI synchronization |
| #2282 recovery | No claim | Unchanged-input Retry failed again; edited-input recovery BLOCKED in tested Panel | Restore on-disk brief, UI Retry, CLI --resume | [Current status](REVIEW_RESPONSE.md), [historical CLI recovery](evidence/public/recovery.json) |
| #2245 Base SHA | Successful normal observation, no adversarial trial | Not implemented in this small demo | Actual HEAD must equal full expected SHA before launch | [Wrong-base injection](evidence/public/wrong-base.json) |
| #2245 Coverage | Native task tests passed | Human reviews code and agent-reported test Artifacts | Exact declared coverage set, protected tests and permitted changes gate success | [Missing-coverage injection](evidence/public/missing-coverage.json) |
| Model / isolation / full SDD | Not evaluated comparatively | Not implemented | Cooperating worktree/index checks only | No model attestation, OS containment or full planning/reviewer loop |

Core owns authorization, dependency admission, attempt fencing, required output
slots and user recovery controls. The package's native Python function validates
Brief **business semantics**; Core alone does not understand a Superpowers Brief.
The function runs through the existing pinned tool loader and post-step-check
extension, not an external loop that later syncs statuses. The Advanced executor
still owns the actual local child-process boundary. Neither path intercepts
ordinary Codex tools outside the explicitly selected Workflow.

See [PR validation](PR_VALIDATION.md) for historical vs current runs and
[public evidence](evidence/public/README.md) for provenance. Unit tests, package
compilation, native-worker API probes and actual plugin/desktop E2Es are distinct.
