# Performance Report: reviewed monthly reporting

Turn four synthetic channel rows into a six-step Workflow and a [readable report](result.md). The example demonstrates same-session recovery, Artifact delivery and independent arithmetic checks; a reviewer corrects narrative claims before delivery.

Source: [Anthropic Performance Report](https://github.com/anthropics/knowledge-work-plugins/blob/2574e29377c7173d0cecffef29a6ecbd7f8ccb5d/marketing/skills/performance-report/SKILL.md), Apache-2.0. [ORIGIN.json](ORIGIN.json) pins hashes and adaptations: include the parent CONNECTORS file, rewrite its reference, and namespace the Skill name. No upstream endorsement is implied.

## Reproduce

Requires Python 3, a running LazyMind instance with Hosted Conversion, a valid user token and a configured model. Model calls consume provider quota. Run in an isolated test instance.

```sh
python3 examples/performance-report/prepare_request.py --output /tmp/performance-example
curl --fail-with-body "$LAZYMIND_BASE_URL/api/core/external-agent/workflow-tasks" \
  -H "Authorization: Bearer $LAZYMIND_TOKEN" -H 'Content-Type: application/json' \
  --data-binary @/tmp/performance-example/request.json
python3 examples/performance-report/check_metrics.py
```

The builder verifies source hashes, includes licensing, creates a deterministic ZIP and requests fresh model conversion. It does not handwrite Workflow YAML. Inspect the returned task/session and generated input IDs, review step outputs, and read the final Artifact. Model-generated plans can differ. Run the checker against your downloaded report; its appendix must contain all 34 metric rows. Check narrative claims separately.

## Verified run

Core `405f31ae`, Algorithm `59d2a836`, DeepSeek `deepseek-flash`; these fixes are included in PR #921. Task `134232b8-5bd5-44c8-9110-421e60251666`, Session `f0f83fa7-3227-5f43-902b-e897c531cf2c`, Revision `446508aa-427c-4f3c-b123-f937a93641ce`.

All six steps completed. The fifth step recovered from a provider 402 in the same Session; the sixth was advanced through the public API. Task reached `succeeded`, Session reached completed. Final reviewed Artifact `9825c8d8-0514-4c66-9378-97bed66a8753` was read back successfully. All 136 appendix values passed exact independent arithmetic, including CPM change **0.95**, not 0.96.

The model draft needed factual edits: Social had different revenue totals across months, CPM increased, and proposed tests were not observed experiments. `result.md` is explicitly reviewed output. Approval and unrestricted unattended correctness are not demonstrated. No campaigns, messages or purchases were executed. Raw evidence remains outside Git.

Demo (75 seconds): show the four inputs (10s), source and generated six-step plan (15s), same-session retry and completion (15s), report and arithmetic checker (25s), explain reviewer edits (10s).
