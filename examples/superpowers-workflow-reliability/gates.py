"""Deterministic checks at the example executor's actual child-process boundary.

This is a cooperating local executor, not a security sandbox or a Codex hook.
No credentials or Runtime database access belong here.
"""
from __future__ import annotations

import contextlib
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def save(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")
    os.replace(tmp, path)


class GateError(RuntimeError):
    def __init__(self, code, details=None):
        super().__init__(code)
        self.code, self.details = code, details or {}


class Trace:
    def __init__(self, path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)

    def emit(self, event, **fields):
        row = {"at": now(), "event": event, **fields}
        with self.path.open("a") as f:
            f.write(json.dumps(row, ensure_ascii=False) + "\n")
            f.flush()
            os.fsync(f.fileno())
        return row


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args], text=True).strip()


def identity(repo):
    repo = Path(repo).resolve(strict=True)
    return {
        "worktree": str(repo),
        "head": git(repo, "rev-parse", "HEAD"),
        "git_dir": git(repo, "rev-parse", "--absolute-git-dir"),
        "index": git(repo, "rev-parse", "--path-format=absolute", "--git-path", "index"),
        "common_dir": git(repo, "rev-parse", "--path-format=absolute", "--git-common-dir"),
    }


def check_isolation(repos):
    observations = [identity(p) for p in repos]
    for field in ("worktree", "index", "git_dir"):
        values = [p[field] for p in observations]
        if len(set(values)) != len(values):
            raise GateError("ISOLATION_COLLISION", {"field": field})
    return observations


class Guard:
    def __init__(self, repo, state, expected_base, trace):
        self.repo = Path(repo).resolve(strict=True)
        self.state = Path(state).resolve()
        self.state.mkdir(parents=True, exist_ok=True)
        self.expected_base = expected_base
        self.trace = trace
        self.brief = self.state / "brief.md"
        self.todo = self.state / "todo.json"
        self.admission = None
        self.launch_count = 0

    @contextlib.contextmanager
    def lock(self):
        # Independent supervisors for this worktree share the same lock.
        lock_path = Path(identity(self.repo)["git_dir"]) / "reliability-dispatch.lock"
        with lock_path.open("a") as f:
            try:
                fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise GateError("DISPATCH_BUSY") from None
            try:
                yield
            finally:
                fcntl.flock(f, fcntl.LOCK_UN)

    def set_todo(self, status):
        event = self.trace.emit("todo_updated", task_id="task-1", status=status)
        save(self.todo, event)
        return event

    def inspect(self):
        if not self.brief.is_file() or self.brief.is_symlink():
            raise GateError("BRIEF_MISSING")
        data = self.brief.read_bytes()
        if not data.strip():
            raise GateError("BRIEF_EMPTY")
        try:
            todo = json.loads(self.todo.read_text())
        except (OSError, ValueError):
            raise GateError("TODO_MISSING") from None
        if todo.get("task_id") != "task-1" or todo.get("status") != "in_progress":
            raise GateError("TODO_NOT_IN_PROGRESS")
        events = [json.loads(s) for s in self.trace.path.read_text().splitlines()]
        if todo not in events or todo.get("event") != "todo_updated":
            raise GateError("TODO_UNOBSERVED")
        observed = identity(self.repo)
        if observed["head"] != self.expected_base:
            raise GateError("BASE_MISMATCH", {"expected": self.expected_base,
                                              "actual": observed["head"]})
        if git(self.repo, "status", "--porcelain", "--untracked-files=no"):
            raise GateError("BASE_DIRTY")
        return {"brief_sha256": digest(data), "brief_bytes": len(data),
                "todo": todo, "git": observed, "expected_base": self.expected_base}

    def validate(self):
        self.admission = self.inspect()
        self.trace.emit("dispatch_preconditions_passed", **self.admission)
        return self.admission

    def dispatch(self, launch):
        # Recheck inside the lock; a prior valid artifact cannot authorize stale data.
        with self.lock():
            current = self.inspect()
            if self.admission is None or current != self.admission:
                raise GateError("ADMISSION_CHANGED")
            self.trace.emit("dispatch_started", brief_sha256=current["brief_sha256"])
            self.launch_count += 1
            result = launch(self.brief.read_text())
            if result.get("returncode") != 0 or not result.get("turn_completed"):
                self.trace.emit("dispatch_failed", result=result)
                raise GateError("DISPATCH_FAILED", result)
            self.trace.emit("dispatch_completed", result=result)
            return result


def coverage_check(rows, required):
    if not isinstance(rows, list):
        raise GateError("COVERAGE_INVALID")
    ids = [r.get("id") if isinstance(r, dict) else None for r in rows]
    if any(not isinstance(i, str) for i in ids) or len(ids) != len(set(ids)) or set(ids) != set(required):
        raise GateError("COVERAGE_MISMATCH", {"required": list(required), "observed": ids})
    for row in rows:
        evidence = row.get("evidence")
        if row.get("status") != "DONE" or not isinstance(evidence, str) or not evidence.strip():
            raise GateError("COVERAGE_INCOMPLETE", {"id": row.get("id")})
    return rows


def verify_completion(repo, base, expected_test_hash, trace):
    repo = Path(repo)
    try:
        rows = json.loads((repo / ".eval/coverage.json").read_text())
    except (OSError, ValueError):
        raise GateError("COVERAGE_MISSING") from None
    coverage_check(rows, ("spaces", "punctuation", "empty"))
    if digest((repo / "test_slug.py").read_bytes()) != expected_test_hash:
        raise GateError("TESTS_CHANGED")
    changed = git(repo, "diff", "--name-only", base).splitlines()
    changed += git(repo, "ls-files", "--others", "--exclude-standard").splitlines()
    if set(changed) - {"slug.py"}:
        raise GateError("SCOPE_CHANGED", {"files": changed})
    started = now()
    test = subprocess.run(["python3", "-m", "unittest", "-v"], cwd=repo,
                          text=True, capture_output=True, timeout=30)
    acceptance = subprocess.run(["python3", "-c", """
from slug import slugify
cases = [(" A  B ", "a-b"), ("A\\tB\\nC", "a-b-c"), ("A---B", "a-b"),
         ("--a--", "a"), ("Room 42!", "room-42"), ("Café", "caf")]
for value, expected in cases:
    assert slugify(value) == expected, (value, slugify(value), expected)
for value in ["", " ! ", "---"]:
    try:
        slugify(value)
    except ValueError:
        pass
    else:
        raise AssertionError("expected ValueError")
print("9 independent acceptance checks passed")
"""], cwd=repo, text=True, capture_output=True, timeout=30)
    result = {"started_at": started, "completed_at": now(), "returncode": test.returncode,
              "stdout": test.stdout, "stderr": test.stderr, "coverage": rows,
              "test_sha256": expected_test_hash, "changed_files": changed,
              "independent_acceptance": {"returncode": acceptance.returncode,
                 "stdout": acceptance.stdout, "stderr": acceptance.stderr}}
    trace.emit("completion_tests", **result)
    if test.returncode or acceptance.returncode:
        raise GateError("TEST_FAILURE", result)
    return result


def codex_child(repo, state, brief, model, binary):
    state, repo = Path(state), Path(repo)
    (repo / ".eval").mkdir(exist_ok=True)
    prompt = (
        "You are the implementer SubAgent for an accepted LazyMind Workflow step. "
        "Implement the following approved task yourself; do not dispatch subagents. "
        "Only change slug.py and ignored .eval reports. Do not edit tests or push. "
        "Run the tests before and after, self-review, and write .eval/implementer-report.md. "
        "Local commits are authorized. The coverage checklist in the task is mandatory.\n\n"
        + brief
    )
    (state / "child-prompt.txt").write_text(prompt)
    command = [binary, "exec", "--ignore-user-config", "--ephemeral", "--model", model,
               "--sandbox", "workspace-write", "--json", "--color", "never",
               "-c", 'model_reasoning_effort="medium"', "--cd", str(repo),
               "--output-last-message", str(state / "child-final.txt"), "-"]
    started, tick = now(), time.monotonic()
    with (state / "child-trace.jsonl").open("w") as out, (state / "child-stderr.log").open("w") as err:
        result = subprocess.run(command, input=prompt, text=True, stdout=out, stderr=err,
                                timeout=600)
    events = []
    for line in (state / "child-trace.jsonl").read_text().splitlines():
        try:
            events.append(json.loads(line))
        except ValueError:
            pass
    terminal = next((e for e in reversed(events) if e.get("type") == "turn.completed"), None)
    return {"started_at": started, "completed_at": now(),
            "duration_seconds": round(time.monotonic() - tick, 3),
            "returncode": result.returncode, "turn_completed": terminal is not None,
            "requested_model": model, "actual_backend_model": "N/A",
            "usage": terminal.get("usage") if terminal else None,
            "trace_sha256": digest((state / "child-trace.jsonl").read_bytes())}
