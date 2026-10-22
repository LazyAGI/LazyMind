#!/usr/bin/env python3
"""Two real Workflow/Codex runs in fresh linked worktrees; no shared business repo."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime as dt
import json
from pathlib import Path
import subprocess
import sys

from fixtures import create
from gates import GateError, check_isolation, digest, git, identity, now, save


def snapshot(repo):
    value = identity(repo)
    value.update({"branch": git(repo, "branch", "--show-current"),
                  "tree": git(repo, "rev-parse", "HEAD^{tree}"),
                  "tracked_status": git(repo, "status", "--porcelain", "--untracked-files=no"),
                  "slug_sha256": digest((Path(repo) / "slug.py").read_bytes()),
                  "index_sha256": digest(Path(value["index"]).read_bytes())})
    return value


def validate_observations(before, after, parent_before, parent_after, runs):
    if len(before) != 2 or len(after) != 2 or len(runs) != 2:
        raise GateError("INCOMPLETE_PARALLEL_OBSERVATION")
    if parent_before != parent_after:
        raise GateError("PARENT_REPOSITORY_CHANGED")
    for field in ("worktree", "git_dir", "index", "branch"):
        if len({x[field] for x in before}) != len(before):
            raise GateError("ISOLATION_COLLISION", {"field": field})
    starts, ends = [], []
    for old, new, run in zip(before, after, runs):
        if any(old[k] != new[k] for k in ("worktree", "git_dir", "index", "branch")):
            raise GateError("WORKTREE_IDENTITY_CHANGED")
        if old["head"] == new["head"] or old["slug_sha256"] == new["slug_sha256"] or new["tracked_status"]:
            raise GateError("IMPLEMENTATION_NOT_ISOLATED_AND_COMMITTED")
        result, window = run["result"], run["child"]
        if run["returncode"] != 0 or result["result"] != "PASS" or not result["control"]["projection"]["completed"]:
            raise GateError("CHILD_WORKFLOW_FAILED")
        starts.append(dt.datetime.fromisoformat(window["started_at"]))
        ends.append(dt.datetime.fromisoformat(window["completed_at"]))
    if len({r["result"]["session_id"] for r in runs}) != len(runs):
        raise GateError("SESSION_COLLISION")
    overlap = (min(ends) - max(starts)).total_seconds()
    if overlap <= 0:
        raise GateError("NO_OBSERVED_CONCURRENCY")
    return round(overlap, 3)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--url", required=True)
    parser.add_argument("--token-file", required=True)
    parser.add_argument("--workflow-id", required=True)
    parser.add_argument("--superpowers", required=True, type=Path)
    parser.add_argument("--model", default="gpt-6-astra")
    parser.add_argument("--codex", default="codex")
    args = parser.parse_args()
    root = args.root.resolve()
    fixture = create(root)
    parent, first, second = root / "repository", root / "worktree", root / "worktree-b"
    subprocess.run(["git", "-C", str(parent), "worktree", "add", "--quiet", "-b", "evaluation-b",
                    str(second), fixture["base"]], check=True)
    repos = [first, second]
    check_isolation([parent, *repos])  # Actual prelaunch hard gate in this supervisor.
    parent_before = snapshot(parent)
    before = [snapshot(repo) for repo in repos]
    started = now()
    save(root / "before.json", {"parent": parent_before, "workers": before})

    def worker(i):
        state = root / ("run-" + str(i + 1))
        state.mkdir()
        command = [sys.executable, str(Path(__file__).with_name("run.py")),
                   "--url", args.url, "--token-file", str(Path(args.token_file).resolve()),
                   "--workflow-id", args.workflow_id, "--repo", str(repos[i]),
                   "--base", fixture["base"], "--state", str(state),
                   "--superpowers", str(args.superpowers.resolve()), "--model", args.model,
                   "--codex", args.codex]
        with (state / "runner.log").open("w") as output:
            process = subprocess.run(command, stdout=output, stderr=subprocess.STDOUT, timeout=900)
        result = json.loads((state / "result.json").read_text())
        trace = [json.loads(line) for line in (state / "trace.jsonl").read_text().splitlines()]
        child = next(row["result"] for row in trace if row["event"] == "dispatch_completed")
        return {"returncode": process.returncode, "result": result, "child": child}

    failure, runs, after, overlap = None, [], [], None
    try:
        with ThreadPoolExecutor(max_workers=2) as pool:
            runs = list(pool.map(worker, range(2)))
        after = [snapshot(repo) for repo in repos]
        parent_after = snapshot(parent)
        overlap = validate_observations(before, after, parent_before, parent_after, runs)
    except Exception as error:
        failure = error.code if isinstance(error, GateError) else type(error).__name__
        parent_after = snapshot(parent)
    save(root / "result.json", {"kind": "real-parallel-workflow-e2e", "started_at": started,
         "completed_at": now(), "result": "FAIL" if failure else "PASS", "failure": failure,
         "base": fixture["base"], "observed_overlap_seconds": overlap,
         "before": before, "after": after, "parent_before": parent_before,
         "parent_after": parent_after, "runs": runs,
         "limit": "Two cooperating processes in disposable linked worktrees; not hostile-process filesystem containment or a general parallel isolation proof."})
    print(json.dumps({"result": "FAIL" if failure else "PASS", "failure": failure,
                      "observed_overlap_seconds": overlap}))
    if failure:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
