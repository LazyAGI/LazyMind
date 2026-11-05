# Research notes → a reviewable Workflow report

[中文](README.CN.md)

Turn six synthetic usability interviews into source-linked findings and a research decision memo. This example imports Anthropic's `synthesize-research` Skill, asks LazyMind to generate and publish a Workflow, and supplies explicit input materials. No hand-authored Workflow YAML is used.

## Verified result and version

Read the [final report](result.md) and [compact run record](run-summary.json). Tested on 2026-11-05 with DeepSeek `deepseek-flash`: Core `405f31ae` from [PR #921](https://github.com/LazyAGI/LazyMind/pull/921), Algorithm `59d2a836`, and LazyLLM `89f4e031`. The Algorithm fixes are available on the [review branch](https://github.com/NicoYang1984/LazyMind/tree/codex/skill2workflow-execution-contracts); they are not included in this Example PR. This is not a claim that unmodified main passes. Follow the repository Quick Start when building the test version, using isolated storage and ports.

The generated five-step graph was: normalize inputs → identify themes → prioritize findings → develop opportunities → compose synthesis. Final-step Attempts were success / failure / failure / success. After explicit user retry, public Artifact edits shortened and corrected the final deliverable. The final Session was completed and both selected Artifacts were readable. This supports a reviewed demo, not an autonomous success claim.

## Source

The Skill comes from [knowledge-work-plugins](https://github.com/anthropics/knowledge-work-plugins/tree/2574e29377c7173d0cecffef29a6ecbd7f8ccb5d/product-management/skills/synthesize-research), pinned at `2574e29377c7173d0cecffef29a6ecbd7f8ccb5d`, under Apache-2.0. The packaging script changes only `../../CONNECTORS.md` to `references/CONNECTORS.md`, includes the connector reference and LICENSE, and checks original and adapted hashes in [ORIGIN.json](ORIGIN.json). No endorsement or collaboration is implied. No external connectors are required for this synthetic fixture.

## Reproduce

Use an isolated LazyMind instance with Hosted Conversion enabled and a configured conversation model that supports tool calls. Model calls consume provider quota. Obtain a test-user Bearer token through the instance's normal login flow; never commit it.

From this directory:

```sh
python3 prepare_request.py --output /tmp/research-example-run
export LAZYMIND_BASE_URL=http://127.0.0.1:30090
# Set LAZYMIND_TOKEN to your test-user token in your local environment.
curl --fail-with-body "$LAZYMIND_BASE_URL/api/core/external-agent/workflow-tasks" \
  -H "Authorization: Bearer $LAZYMIND_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @/tmp/research-example-run/request.json
```

The script downloads pinned source files, verifies hashes, creates a reproducible ZIP, and creates a fresh idempotency key. Reuse the same request file when retrying submission after a network interruption. Use a new output directory for an independent run. `reuse_workflow=false` requests a new model conversion.

Poll `GET /api/core/external-agent/workflow-tasks/{task_id}` using the same authentication. Record the actual `workflow_revision_id` and `session_id`. Inspect:

- `GET /api/core/workflow-sessions/{session_id}/projection` for per-step Attempts and state.
- `GET /api/core/workflow-sessions/{session_id}/artifacts` for intermediate and final Artifacts.
- `GET /api/core/workflow-artifacts/{artifact_id}` for the selected Artifact value. File-backed values must also be opened or downloaded through the instance's authorized resource access.

Respect binding, review and approval states; do not bypass them. A failed run is not a successful example. Keep the generated package unchanged when inspecting it.

## Check the business result

Compare the final report against [research-data.md](research-data.md), not just Task status:

- All six participants remain identifiable; verbatim quotes match their sources.
- First-use participants are P01/P02/P04/P06 (4); weekly users are P03/P05 (2).
- Import discovery problems affect P01/P02/P04 (3/6). P06 found import using the keyboard and is a counterexample.
- Progress feedback problems affect P03/P04 (2/6); the accessibility blocker affects P06 (1/6).
- Recommendations are proposals for a two-week validation plan, not implemented improvements or statistically established population findings.

## Review is part of this example

The initial five-step run completed, but independent source review rejected invented completion outcomes and session details. The final step was rerun through the public Workflow rewind operation, with explicit source corrections; the earlier Attempt and Artifacts were retained. This is a reviewed workflow, not a claim of error-free autonomous research. Read the selected Session Artifact after a correction, and check the current Session state as well as the outer Task state.

## Limits

Model-generated graphs and prose can vary. A successful pipeline is not proof of factual accuracy; review participant counts and counterevidence. This fixture does not establish command execution, connector access, Approval or Retry reliability. It does not benchmark native Skill execution against Workflow execution. Preserve failed Attempts and old Artifacts when correcting a report through the normal Workflow controls.

Run `python3 check_report.py result.md` to check source IDs and exact quotes. This does not replace the factual review above.
