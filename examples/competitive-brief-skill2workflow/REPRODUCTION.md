# Reproduction

This is a real-service experiment. The scripts do not install a server, supply a model, impersonate an account, mock results or approve a human gate. A successful run may consume model quota. Use your own authorized local/test instance and a fresh output directory for every command.

## Environment and inputs

Recorded host: macOS arm64, Codex CLI `0.155.0-alpha.9`, Core built with Go 1.25.11, connection reporting DeepSeek `deepseek-flash`. Audited checkout: `71a0e5a829f1ef8d9a5d9e9ce720386ba3c69d0e`. The deployed binary has no VCS stamp; [environment.json](evidence/environment.json) records its hash. A rerun on another build/model is a new experiment, not an exact deterministic replay.

The original worktree and its uncommitted submodule change were left untouched. A separate branch/worktree was created from fetched main. This PR modifies only this example and the two examples indexes. A normal already-paired local authentication connection was used. No credentials, private sessions, database contents or unrelated documents are part of the evidence.

Read [Sources](SOURCES.md) before running. The pinned Skill references a parent CONNECTORS file; baseline downloads it separately, while the hosted directory importer omits it. Baseline run 1 had one extra trailing newline in SKILL.md; runs 2–3 and the imported Skill match its exact Git blob. All modes use identical task/corpus bytes. This scoped offline variant deliberately leaves market/SEO/review intelligence unavailable instead of quietly supplying easier substitute metrics.

## Check committed evidence without a model

From the repository root, with Python 3.10+:

```sh
python3 -m venv /tmp/competitive-brief-check-env
/tmp/competitive-brief-check-env/bin/pip install 'markdown-it-py>=3,<5'
/tmp/competitive-brief-check-env/bin/python examples/competitive-brief-skill2workflow/scripts/verify.py
git diff --check
```

The verifier checks consistency of recorded evidence, not whether a newly generated report is factually correct. Read the substantive failures in [Evaluation](EVALUATION.md).

## Run the original Skill

Use an installed, authenticated Codex CLI supporting `exec --json --ignore-user-config --ephemeral --sandbox read-only`. The runner downloads only the pinned public SKILL.md and CONNECTORS.md, verifies the Skill Git blob, and records the prompt, raw events, report, elapsed time and exit status.

```sh
python3 examples/competitive-brief-skill2workflow/scripts/reproduce.py \
  --out /tmp/competitive-brief-baseline-new baseline --runs 3 --codex codex
```

The recorded prompt is preserved as [a path-normalized template](evidence/baseline/prompt.txt). The reproduction runner expresses the same input/access restrictions; directory paths and wording of its wrapper are not byte-identical to the original prompt. SKILL.md and frozen business inputs are pinned. The default model was not exposed in the original CLI JSON; record a resolved model if your CLI supports it. No web/paid connector is required for this offline run. Do not call any proposed follow-ups.

Raw event logs can contain upstream source text and model reasoning. Keep them local. The committed baseline logs retain command metadata, output hashes and usage; they omit source bodies and internal reasoning. Published reports normalize trailing whitespace only.

## Run hosted automatic conversion

Follow the project's [local deployment instructions](../../README.md) and [external Agent contract](../../docs/external-agent-skill-workflow-trial.md). Make sure your own instance has a ready model. The current HTTP implementation, audited in [AUDIT.md](AUDIT.md), takes precedence over obsolete route examples.

Set `LAZYMIND_WORKFLOW_BASE_URL` to your explicit Core base URL (for example a local gateway URL ending in `/api/core`) and `LAZYMIND_WORKFLOW_TOKEN` to an authorized token using your normal credential mechanism. Never put real tokens into committed commands. `LAZYMIND_WORKFLOW_USER_ID` is supported only for an explicitly trusted local development identity, never as an authentication bypass on a shared deployment.

```sh
python3 examples/competitive-brief-skill2workflow/scripts/reproduce.py \
  --out /tmp/competitive-brief-connection-new connection
python3 examples/competitive-brief-skill2workflow/scripts/reproduce.py \
  --out /tmp/competitive-brief-submit-new submit \
  --idempotency-key my-competitive-brief-trial-001 --source-format tree
```

This calls `POST /external-agent/workflow-tasks` with the full frozen input, pinned directory URL and `config.reuse_workflow: false`. The service performs install/reuse, Preflight, model-backed generation, publication and session execution. It does not use the built-in Product Solution Delivery Workflow or the host-authored draft route. To independently reproduce the source-link failure, use `--source-format blob` with a different idempotency key.

Read the returned task ID and poll manually at the server's `poll_after_seconds` interval. Use a new output directory for each sample:

```sh
python3 examples/competitive-brief-skill2workflow/scripts/reproduce.py \
  --out /tmp/competitive-brief-status-001 status TASK_ID_FROM_SUBMIT
```

Do not infer completion from a published Workflow. Inspect `status`, `done`, `result_ready`, errors and progress. If the task asks for human action, open its returned LazyMind URL and stop automation at that boundary. This experiment's generated steps had no approval gate; do not generalize that to other conversions.

Once Skill, draft and session IDs exist, capture their state:

```sh
python3 examples/competitive-brief-skill2workflow/scripts/reproduce.py \
  --out /tmp/competitive-brief-capture-001 capture \
  --skill-id YOUR_SKILL_ID --draft-id YOUR_DRAFT_ID --session-id YOUR_SESSION_ID
```

The explicit Preflight call records dependencies; the hosted task also ran Preflight itself. Capture generated YAML/state/scenario and publish revision from the real draft/result. The published revision is reusable, but this trial did not measure a second reuse execution. For artifacts stored as file references, read only the explicit path returned for **your experiment's** artifact, or use the supported artifact download interface. Do not scan upload directories or session history. Keep raw API outputs private until reviewed: IDs and paths can identify an account, and generated text is not automatically safe to publish.

## Reproduce the assisted recovery separately

Do not apply these steps to a different error without diagnosis. In this run the first analysis attempt was judged against the downstream report contract. The unchanged generated node expected JSON. The exact agent intervention is preserved in [retry request](evidence/requests/retry-analysis-request.json), [synthesis request](evidence/requests/synthesis-request.json) and [no-battlecard request](evidence/requests/battlecard-request.json).

Copy a request to a local file. Replace the recorded experiment session ID, command ID and expected state version with your own values. Read projection first; only act on a ready/retryable node. Preserve `retry_origin: automatic` for an agent-triggered retry and all final-report requirements. Do not falsely attribute it to a human.

```sh
python3 examples/competitive-brief-skill2workflow/scripts/reproduce.py \
  --out /tmp/competitive-brief-retry-001 advance --request /tmp/my-retry-request.json
```

This issues one official `POST /workflow-sessions/{id}:advance-step`. The recorder has a 90-second HTTP timeout. A timeout can occur after server acceptance: inspect projection and `GET /workflow-commands/{command_id}` before resending, using the same command ID if retrying an uncertain mutation. Never equate a lost response with a failed step.

After analysis succeeded, the original external task stayed terminally failed. Two explicit advances completed synthesis using the original full input and the generated no-request battlecard branch. Record this as assisted **session** completion; do not rewrite the external-task outcome. Human review remains pending.

The edit probe used `PATCH /workflow-artifacts/{artifact_id}` with base revision/draft version and an agent-only annotation; the native slot route was then tried. Both failed and left report revision 1 unchanged. The [request](evidence/requests/edit-request.json) and [responses](evidence/http/) are preserved. Successful edit/rollback and real human approval require a future run; no approval bypass or database patch was attempted.

## Relevant current-main regression checks

The experiment installed Go 1.25.11 and an isolated Python 3.12 environment. It initialized the worktree's pinned LazyLLM submodule. Initial dependency/import and sandbox-listener failures were environment setup problems, not recorded as product failures. After dependencies and localhost permission were available, these checks passed:

```sh
cd backend/core
go test ./skillv2/sourceurl -count=1
go test ./workflow -count=1 -run 'TestPreflightSkillWorkflowConversion|TestCreateExternalAgentWorkflowTask|Test.*External.*(Skill|Source|Task|Idempot)'
```

From the repository root, using an isolated environment with the relevant declared algorithm/LazyLLM dependencies, and scratch `LAZYLLM_HOME` / `LAZYMIND_HOME` paths:

```sh
PYTHONPATH=algorithm:algorithm/lazyllm python -m pytest \
  tests/algorithm/test_workflow_sdk.py tests/algorithm/test_workflow_agent_kit.py -q
```

The Python result was 92 passed. Logs and installed test-package versions are in [checks](evidence/checks/). These are narrow regressions, not a full-suite certification or a replacement for the separately recorded live E2E trial.
