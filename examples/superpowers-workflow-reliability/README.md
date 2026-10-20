# Superpowers Dispatch Reliability Workflow Integration

[中文](README.CN.md) · [RC acceptance](../../RELEASE_CANDIDATE.md) · [Comparison](comparison.md)

Run **one declared development task** in an isolated Git worktree. A six-step
LazyMind Workflow and its Python executor require a nonempty task brief, observable
in-progress state and the intended full Git SHA before launching Codex. Completion
requires every declared coverage item, unchanged acceptance inputs, permitted file
changes, passing checks and six Runtime Artifacts.

This is a manually authored integration of Superpowers dispatch practices, not an
automatic Skill converter or a complete SDD implementation. The hard gate applies
to the process this executor launches; YAML alone does not intercept other agents.

## Quick start: a CSV task

Prerequisites: macOS/Linux, Python 3.9+, Git supporting `--path-format=absolute`,
Bash, an authenticated Codex CLI, and a running LazyMind installation with the
public `workflow.control.v1` API. [Install/start LazyMind](../../docs/quick_start.md).
The RC was tested with Python 3.9.6, Git 2.50.1 and Codex CLI 0.155.0-alpha.9.
Codex must accept `exec --ignore-user-config --ephemeral --json` and the selected
model. If the CLI is not on PATH, use `--codex /path/to/codex` when configuring.

Start from a checkout containing this example. Select a **new** writable directory,
your local gateway URL and a model available to your Codex account:

```bash
cd examples/superpowers-workflow-reliability
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
export TRIAL=/tmp/lazymind-reliability-trial
export LAZYMIND_URL=http://127.0.0.1:5024
export MODEL=gpt-6-astra  # use a model available in your Codex account
mkdir -m 700 "$TRIAL"
git clone https://github.com/obra/superpowers.git "$TRIAL/superpowers"
git -C "$TRIAL/superpowers" checkout bb92a77741419a4ab5f06e711a283343f1ada0c3
.venv/bin/python cli.py --config "$TRIAL/private/config.json" configure \
  --url "$LAZYMIND_URL" --local-login --superpowers "$TRIAL/superpowers" --model "$MODEL"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" install
.venv/bin/python demo.py "$TRIAL/csv-a"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" run \
  --task "$TRIAL/csv-a/task.json" --repo "$TRIAL/csv-a/worktree" --state "$TRIAL/csv-a/run"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" status --state "$TRIAL/csv-a/run"
```

`5024` is the source runtime's default **local gateway** port; use your selected
installation's actual gateway, not an unrelated instance. `--local-login` calls
the explicit loopback installation's local login endpoint. It writes a mode-0600
session file and never prints the token. For a shared deployment, use HTTPS and
replace `--local-login` with `--token-file /private/session.json` containing
`{"token":"YOUR_AUTHENTICATED_TOKEN"}` in mode 0600. A fresh credential for the same
owner can replace that private file when it expires. Never commit config, credentials,
run directories or user project traces. Do not use a privileged business account
for untrusted plans. This example does not configure LazyMind's chat/embedding models.

Install queries **enabled and disabled** visible workflows. First use publishes and
enables the package in manual mode; subsequent installs reuse the compatible ref.
Before every run, source files are compared semantically against the published
revision. An incompatible same-name package is rejected without mutation. Use a
new config with `configure --workflow-name YOUR_UNIQUE_NAME`, or deliberately update
it via LazyMind authoring. No blind overwrite, automatic upgrade or ignored HTTP 500.
The legacy blank-draft publisher's underlying duplicate-ID defect is not changed.

## Repeat use and inspect results

Create another task worktree and a new run directory; **do not republish**:

```bash
.venv/bin/python demo.py "$TRIAL/csv-b"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" run \
  --task "$TRIAL/csv-b/task.json" --repo "$TRIAL/csv-b/worktree" --state "$TRIAL/csv-b/run"
git -C "$TRIAL/csv-b/worktree" log -1 --oneline
git -C "$TRIAL/csv-b/worktree" show --stat
.venv/bin/python cli.py --config "$TRIAL/private/config.json" status --state "$TRIAL/csv-b/run"
```

Implementation files remain in `worktree/`. `run/result.json` contains authoritative
Runtime state and Artifacts; `trace.jsonl` records stage/API events and
`child-trace.jsonl` records the actual Codex process. `status` independently reads
Core again into `readback-control.json` and `readback-artifacts.json`. The completion
Artifact contains check commands/results, changed-file hashes, HEAD and diff.
The session ID also opens `<your frontend>/workflow-runs/<session_id>`.
Raw traces are private task data, not automatically safe to publish.

## Your own repository and Plan

Supported: a bounded code change with deterministic, already-authored acceptance
checks, **one** numbered Plan task, exact implementation file paths and one Codex
implementer. Multi-task scheduling, automatic reviewer loops and dependency setup
inside arbitrary projects are outside this RC. Execute only trusted task/check
commands: they run with your local user permissions.

Commit the Plan, acceptance tests and a `.gitignore` rule for `.eval/` before recording
the base. Create a fresh linked worktree at that full SHA. Keep the task JSON and
private run/config directories outside it. The primary checkout is rejected.

```bash
BASE=$(git -C /path/to/repository rev-parse HEAD)
git -C /path/to/repository worktree add -b my-reviewed-task /path/to/new-worktree "$BASE"
```

Use the generated `task.json` as the schema example. All fields are required:

| Field | Contract |
| --- | --- |
| schema_version | `1` |
| task_id | A short lowercase identifier; present in the selected Plan heading |
| plan / plan_task | Tracked relative path; integer N selecting `### Task N: task-id` |
| expected_base | Full 40-character SHA containing the Plan and protected tests |
| allowed_files | Exact relative implementation paths; no globs, traversal or symlinks |
| protected_files | Tracked acceptance files that the child and checks must leave unchanged |
| coverage | Unique `{id, description, checks: [check-id]}` objects; every ID appears in that Plan task |
| checks | Unique `{id, argv: [executable, arg, ...]}` objects; no shell expansion; all are mapped to coverage |

Each requirement must map to a real assertion in the declared tests. Mapping an ID
to a command does not prove that the command is meaningful; the manifest and tests
are trusted caller inputs. The executor runs all commands and protects their declared
files. It rejects missing/extra/duplicate/skipped coverage and non-text evidence.
Use the same `run` command with your manifest/worktree. A manifest change requires
a reviewed new task, not reuse of a failed session with changed inputs.

## Missing brief and recovery

On a **disposable** demo repository, append `--fault missing-brief` to `run`. The
real upstream script first creates the brief; the explicitly labeled injection
then deletes it. Validation fails before a child is launched. Expected negative
runs exit 1. Do not use fault injection against business work.

```bash
.venv/bin/python cli.py --config "$TRIAL/private/config.json" restore-brief \
  --task "$TRIAL/csv-a/task.json" --repo "$TRIAL/csv-a/worktree" --state "$TRIAL/csv-a/run"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" run --resume \
  --task "$TRIAL/csv-a/task.json" --repo "$TRIAL/csv-a/worktree" --state "$TRIAL/csv-a/run"
```

Apply these commands to the *failed missing-brief run*, not the completed Quick Start
run. Restore re-extracts the same committed task. Explicit `--resume` invokes the
public control Retry operation, retaining the failed Attempt and creating a new
one in the same session. An already queued UI Retry can also be claimed. Preparation
must have succeeded and dispatch must never have started. Previous result snapshots
remain in `previous-results.jsonl` and Core retains Attempt history.

For wrong base, edited Plan/tests, or invalid task declarations, inspect the error,
correct the reviewed inputs and create a fresh worktree/run. Do not reset away work.
For a failed/timeout/interrupted or uncertain dispatch, use `status`, inspect Git and
the trace, and resolve the existing session in LazyMind before deliberately starting
a new task. The executor refuses automatic replay. A handled timeout/SIGINT/SIGTERM
kills its owned process group; SIGKILL, machine failure, detached grandchildren and
durable distributed recovery are not guaranteed. Never delete run state to conceal
an unresolved dispatch. Check commands have a 120-second limit; Codex has 600 seconds.

## Evidence, tests and limits

```bash
.venv/bin/python -m unittest discover -s . -p 'test_*.py' -v
.venv/bin/python cli.py --config "$TRIAL/private/config.json" preflight
```

Preflight checks dependencies/flags, Codex login, pinned clean upstream source,
Runtime authorization and package compatibility. It cannot attest backend model
identity or prove model availability without a model call; the accepted dispatch
records that outcome. The requested model/reasoning are configurable. No model call
is hidden in installation or preflight.

See [RC acceptance and real traces](../../RELEASE_CANDIDATE.md),
[#2282](cases/issue-2282.md), [#2245](cases/issue-2245.md) and
[comparison](comparison.md). Before: native SDD asks the model to follow dispatch
prerequisites. After: this executor makes the selected prerequisites executable and
blocks its launch when they fail. **Both historical normal native samples passed**;
no natural missing-brief failure or comparative reliability/efficiency gain is claimed.

Core enforces graph admission, Attempt leases and required output slots. This
executor verifies brief/state/base and task semantics. Its cooperating worktree
lock is not OS containment. Codex Todo UI synchronization, actual backend model
attestation, arbitrary-process isolation and full SDD parity are not implemented.

Historical `run.py`, `native.py`, `parallel.py` and their 89-file evidence manifest
retain the fixed-fixture comparison; `cli.py` is the user-task RC entry point.
The prior release audit remains a dated snapshot, not certification of later code.
Upstream text/scripts are MIT licensed: [notice](UPSTREAM-LICENSE). New example code
uses the repository's [Apache-2.0 license](../../LICENSE).
