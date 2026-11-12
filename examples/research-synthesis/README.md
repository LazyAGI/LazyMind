# Research synthesis: Skill → Workflow → reviewed report

[中文](README.CN.md) · [Final report](result.md)

Import Anthropic's `synthesize-research` Skill and turn six synthetic interviews into a source-linked decision memo. LazyMind automatically generated and published five steps: normalize evidence → identify themes → prioritize findings → develop opportunities → compose the report. The reviewed workflow completed, including a user-authorized retry and Artifact edits. No Workflow YAML was manually authored.

## Reproduce

Use an isolated LazyMind instance with this PR's fixes, Hosted Conversion, a tool-capable conversation model and a normally authenticated test-user token. Model calls consume provider quota. From this directory:

```sh
python3 prepare_request.py --output /tmp/research-example-run
export LAZYMIND_BASE_URL=http://127.0.0.1:30090
# Set LAZYMIND_TOKEN locally through the instance's normal login flow.
curl --fail-with-body "$LAZYMIND_BASE_URL/api/core/external-agent/workflow-tasks" \
  -H "Authorization: Bearer $LAZYMIND_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @/tmp/research-example-run/request.json
```

The script verifies pinned source hashes, packages the parent CONNECTORS dependency, and binds `research_data` and `research_scope`. It requests a new model conversion (`reuse_workflow=false`). Reuse the request file after a submission network failure; use a new output directory for a new run.

Poll `GET /api/core/external-agent/workflow-tasks/{task_id}`. Inspect `GET /api/core/workflow-sessions/{session_id}/projection` and `/artifacts`, then read the selected result at `GET /api/core/workflow-artifacts/{artifact_id}`. Open file-backed results through authorized resource access. Respect approval and retry limits; inspect Session state after a rewind rather than relying on the outer Task's earlier success.

## Acceptance and result

Run `python3 check_report.py result.md` for source IDs and verbatim quotes. Also review the 4 first-use / 2 weekly-user split, source-linked discovery 3/6, progress 2/6, accessibility 1/6, P06's keyboard counterexample and unknown completion outcomes. Proposed improvements must not be described as implemented.

Verified on 2026-11-05 with `deepseek-flash`, Core `405f31ae`, Algorithm `59d2a836` and LazyLLM `89f4e031`. The two Algorithm changes are included in this PR. Session `55ebc40e-393e-5070-931f-00ad4619829b` completed; the selected report was readable and matched [result.md](result.md). Six quotes and key counts passed review. Two failed final-step attempts preceded the authorized successful retry; a final Artifact edit shortened and corrected the report. This is a successful reviewed workflow, not an error-free autonomous run. Approval was not exercised. Raw logs and model drafts are kept outside this repository.

## Source and demo

[Upstream Skill](https://github.com/anthropics/knowledge-work-plugins/tree/2574e29377c7173d0cecffef29a6ecbd7f8ccb5d/product-management/skills/synthesize-research), Apache-2.0; fixed source and adapted hashes are in [ORIGIN.json](ORIGIN.json). The ZIP includes LICENSE and changes only `../../CONNECTORS.md` to `references/CONNECTORS.md`. No upstream endorsement is implied. No external connectors are needed.

75-second demo: show the research question and inputs (15s), source and generated five-step workflow (20s), final report with quote/count checks (25s), then the review/retry/edit interaction and remaining limits (15s). Model-generated graphs and text vary; this example does not benchmark speed, establish universal reliability or demonstrate command execution.
