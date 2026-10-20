# Superpowers dispatch reliability

[中文](README.CN.md) · [Design](../../DESIGN.md) · [Comparison](comparison.md) · [Release audit](../../RELEASE_AUDIT.md)

A runnable six-step [Workflow](../../workflows/superpowers-reliability/), a local
external executor, regression tests and real Codex/Runtime traces for
[#2282](https://github.com/obra/superpowers/issues/2282) and part of
[#2245](https://github.com/obra/superpowers/issues/2245).

The executor owns the child process and rechecks prerequisites immediately before
launch. **The YAML alone does not install a Codex-wide hard gate.** No Runtime or
LazyLLM changes are required.

## Quick start

Requirements: Python 3.9+, Git, macOS/Linux (fcntl), authenticated Codex CLI with
the requested model, and LazyMind at the recorded baseline or a compatible public
workflow.control.v1 API. [Start LazyMind](../../docs/quick_start.md) before running
this example. The independent audit used Python 3.9.6, Git 2.50.1 and Codex CLI
0.155.0-alpha.9 on macOS. Check that your CLI supports `--ignore-user-config`,
`--ephemeral` and the requested model; another model/build is a different experiment.
Run only in new disposable repositories.

From this directory:

```bash
python3 -m unittest discover -s . -p 'test_*.py' -v
git clone https://github.com/obra/superpowers.git /tmp/superpowers-eval
git -C /tmp/superpowers-eval checkout bb92a77741419a4ab5f06e711a283343f1ada0c3
python3 fixtures.py /tmp/workflow-fixture
```

Put an existing authenticated LazyMind token in a private mode-0600 JSON file,
`{"token":"..."}`. Never commit it. Set `LAZYMIND_API` to this installation's
explicit API base, e.g. `http://127.0.0.1:5024/api/core`, and `SESSION_FILE`
to that private file.

**First installation for this user only** (no existing `superpowers-reliability`):

```bash
python3 run.py --url "$LAZYMIND_API" --token-file "$SESSION_FILE" \
  --state /tmp/workflow-publication --publish-only
```

For a repeat run, reuse the installed workflow's exact ref instead. The public
`GET /workflow-runtime/v1/workflows` catalog lists refs; its corresponding
`GET /workflow-runtime/v1/workflows/{workflow_ref}` returns the revision and files.
Compare the scenario/state and compiled graph with this package before reuse.
The blank-draft publisher is not an updater: publishing the same ID again on the
tested Runtime fails with a draft uniqueness conflict. No child is launched.
Enable the published workflow in LazyMind's Workflow settings if it is disabled.

Set `WORKFLOW_REF` to the exact returned workflow_ref and `BASE_SHA` to the base
in /tmp/workflow-fixture/fixture.json.

```bash
python3 native.py --root /tmp/native-fixture --superpowers /tmp/superpowers-eval \
  --model gpt-6-astra --codex codex --persist-session

python3 run.py --url "$LAZYMIND_API" --token-file "$SESSION_FILE" \
  --state /tmp/workflow-run --workflow-id "$WORKFLOW_REF" \
  --repo /tmp/workflow-fixture/worktree --base "$BASE_SHA" \
  --superpowers /tmp/superpowers-eval --model gpt-6-astra --codex codex
```

For the audited native trace, use the `thread.started` ID in native/trace.jsonl
to identify **that run's own** Codex rollout file. Export only its tool events:

```bash
python3 export_tool_trace.py --session-file "$NATIVE_ROLLOUT" \
  --expected-cwd /tmp/native-fixture/worktree \
  --output /tmp/native-fixture/native/tool-trace.jsonl
```

`--persist-session` retains local session data; omit it for an ephemeral run. The
exporter checks the experiment cwd and excludes messages/reasoning. Encrypted
tool message bodies are omitted with hashes. CLI output alone may omit spawn calls.

Each run requires a new fixture/state directory. Existing fixtures are rejected;
nothing is reset or force-cleaned. Add one `--fault`: missing-brief, empty-brief,
wrong-base, dispatch-failure, missing-coverage, missing-todo, or stale-todo. Expected failures exit nonzero.
dispatch-failure uses a labeled failing callback; missing-coverage runs a real
child before removing a checklist row.

```bash
python3 probe_runtime.py --url "$LAZYMIND_API" --token-file "$SESSION_FILE" \
  --workflow-id "$WORKFLOW_REF" --state /tmp/runtime-probes
```

Recover missing/empty brief validation by regenerating the same file with upstream
`task-brief PLAN 1 <state>/brief.md`. Open
`<frontend>/workflow-runs/<session_id>`, click **Retry**, then repeat run.py with
`--resume` and without `--fault`. It claims the UI-created attempt in the saved
session. This bounded recovery supports failed dispatch validation after successful
preparation. It does not automatically replay an uncertain child dispatch.

## Parallel reproduction

```bash
python3 parallel.py --root /tmp/parallel-fixture --url "$LAZYMIND_API" \
  --token-file "$SESSION_FILE" --workflow-id "$WORKFLOW_REF" \
  --superpowers /tmp/superpowers-eval --model gpt-6-astra --codex codex
```

This creates a fresh repository with two linked worktrees and launches two actual
Workflow/Codex processes. Success requires separate paths/branches/indexes and
sessions, two completed results, measured overlap and an unchanged parent repo.
It never runs a shared-index race in a business repository.

## Evidence and limits

Both native runs and the final revision-2 normal/parallel Workflow runs pass the
three unchanged tests and nine independent acceptance checks. The earlier
revision-1 normal and recovery runs recorded only the three fixed tests; their
historical evidence has not been backfilled. Missing/empty brief and wrong base produce zero launch attempts.
Missing coverage and failed dispatch produce no final success Artifact. The same
failed session was recovered through the official UI. Missing/stale Todo also
blocks launch. A subsequent real parallel run completed both sessions with
57.548 seconds of overlap and no parent repository/index changes.

See [2282](cases/issue-2282.md), [2245](cases/issue-2245.md) and the
[evidence manifest](evidence/manifest.json). Fault callbacks are not Agent E2E.
The initial CLI stream omits spawn calls. The audited follow-up has three actual
spawn calls/receipts in its tool-only trace. Encrypted message bodies are omitted;
neither a spawn receipt nor an agent report attests the actual backend model.

The current application defaults are deepseek-flash and BAAI/bge-m3; both have
verified, ready configurations. A new Flash UI conversation returned FLASH_READY.
Historical credential failures are retained without describing current readiness. All coding
experiments requested gpt-6-astra through Codex CLI 0.155.0-alpha.9. Actual backend
model identity is N/A. Native SDD includes two reviewers; the minimal Workflow uses
one implementer and deterministic validation. Total token/time costs are therefore
not a controlled efficiency comparison.

The Skill was read and manually converted into a bounded package. Publication used
the Agent Kit's deterministic authoring API with a blank draft; there is no
server-side Skill snapshot binding or AI-generated conversion claim.
Traces include MIT-licensed upstream content: [license](UPSTREAM-LICENSE).
