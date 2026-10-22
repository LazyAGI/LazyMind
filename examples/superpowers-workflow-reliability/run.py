#!/usr/bin/env python3
"""Run a real public-API Workflow with one real Codex child. No database writes."""
import argparse
import json
import os
from pathlib import Path
import subprocess

from gates import Guard, GateError, Trace, codex_child, digest, git, now, save, verify_completion
from runtime import Runtime


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default=os.environ.get("LAZYMIND_SERVER_URL"))
    parser.add_argument("--token-file", required=True,
                        help="Private JSON session file with token; never committed")
    parser.add_argument("--state", required=True, type=Path)
    parser.add_argument("--repo", type=Path)
    parser.add_argument("--superpowers", type=Path)
    parser.add_argument("--base")
    parser.add_argument("--model", default="gpt-6-astra")
    parser.add_argument("--codex", default="codex")
    parser.add_argument("--publish-only", action="store_true")
    parser.add_argument("--workflow-id")
    parser.add_argument("--fault", choices=["missing-brief", "empty-brief", "wrong-base",
                                          "missing-coverage", "dispatch-failure", "missing-todo", "stale-todo"])
    parser.add_argument("--resume", action="store_true", help="Claim UI Retry of failed dispatch validation in the saved session")
    args = parser.parse_args()
    args.state = args.state.resolve()
    if args.repo:
        args.repo = args.repo.resolve()
    if args.superpowers:
        args.superpowers = args.superpowers.resolve()
    if not args.url:
        parser.error("--url or LAZYMIND_SERVER_URL is required")
    if not args.publish_only and not args.resume and (args.state / "session.json").exists():
        parser.error("state already has a session; use a fresh directory or explicit --resume")
    args.state.mkdir(parents=True, exist_ok=True)
    trace = Trace(args.state / "trace.jsonl")
    token_data = json.loads(Path(args.token_file).read_text())
    runtime = Runtime(args.url, token_data["token"], trace)
    trace.emit("run_started", kind="workflow", fault=args.fault,
               requested_model=args.model, actual_backend_model="N/A")
    package = Path(__file__).resolve().parents[2] / "workflows/superpowers-reliability"
    if args.publish_only:
        result = runtime.publish(package)
        save(args.state / "publication.json", result)
        print(json.dumps(result, ensure_ascii=False))
        return
    if not all([args.repo, args.superpowers, args.base, args.workflow_id]):
        parser.error("execution requires repo, superpowers, base and workflow-id")
    for name in ("slug.py", "test_slug.py", "docs/plans/slug-plan.md", ".gitignore"):
        expected = (Path(__file__).parent / "fixture" / name).read_bytes()
        actual = subprocess.check_output(["git", "-C", str(args.repo), "show", args.base + ":" + name])
        if actual != expected:
            raise GateError("NOT_THE_APPROVED_FIXTURE")
    if args.state.resolve().is_relative_to(args.repo.resolve()):
        raise GateError("STATE_MUST_BE_OUTSIDE_CHILD_WORKTREE")
    guard = Guard(args.repo, args.state, args.base, trace)
    initial_tests = digest((Path(__file__).parent / "fixture/test_slug.py").read_bytes())
    if args.resume:
        session = json.loads((args.state / "session.json").read_text())["session_id"]
        observed = runtime.control(session)
        completed = {k for k, v in observed["projection"]["nodes"].items()
                     if v["execution"] == "succeeded"}
        if completed != {"prepare"} or not observed["control"].get("active_execution_ids"):
            raise GateError("UI_RETRY_REQUIRED")
    else:
        start = runtime.start(args.workflow_id)
        save(args.state / "start.json", start)
        session = start.get("session_id") or start["session"]["id"]
    save(args.state / "session.json", {"session_id": session})

    def prepare(contract):
        started = now()
        script = args.superpowers / "skills/subagent-driven-development/scripts/task-brief"
        proc = subprocess.run(["bash", str(script), "docs/plans/slug-plan.md", "1", str(guard.brief)],
                              cwd=args.repo, text=True, capture_output=True, timeout=10)
        trace.emit("brief_generated", started_at=started, returncode=proc.returncode,
                   stdout=proc.stdout, stderr=proc.stderr)
        if proc.returncode:
            raise GateError("BRIEF_GENERATION_FAILED")
        guard.set_todo("in_progress")
        result = guard.inspect()
        if args.fault == "missing-brief":
            guard.brief.unlink()
        elif args.fault == "empty-brief":
            guard.brief.write_text("")
        elif args.fault == "missing-todo":
            guard.todo.unlink()
        elif args.fault == "stale-todo":
            guard.set_todo("completed")
        elif args.fault == "wrong-base":
            subprocess.run(["git", "-C", str(args.repo), "commit", "--allow-empty",
                            "-qm", "Injected base mismatch"], check=True)
        if args.fault:
            trace.emit("fault_injected", fault=args.fault)
        return result

    def dispatch(contract):
        if args.fault == "dispatch-failure":
            return guard.dispatch(lambda brief: {"returncode": 23, "turn_completed": False,
                                                  "kind": "fault-injection"})
        # Accepted StepContract is included as context; the child receives no handle/token.
        return guard.dispatch(lambda brief: codex_child(
            args.repo, args.state, "Workflow objective: " + contract.get("prompt", "") +
            "\n\n" + brief, args.model, args.codex))

    def collect(contract):
        if args.fault == "missing-coverage":
            path = args.repo / ".eval/coverage.json"
            rows = json.loads(path.read_text())
            path.write_text(json.dumps(rows[:-1]))
            trace.emit("fault_injected", fault="missing-coverage")
        report = args.repo / ".eval/implementer-report.md"
        if not report.is_file() or not report.read_text().strip():
            raise GateError("REPORT_MISSING")
        return {"head": git(args.repo, "rev-parse", "HEAD"),
                "diff": git(args.repo, "diff", args.base),
                "report": report.read_text(),
                "coverage": json.loads((args.repo / ".eval/coverage.json").read_text()),
                "trace_sha256": digest((args.state / "child-trace.jsonl").read_bytes())}

    actions = {
        "prepare": prepare,
        "validate_dispatch": lambda c: guard.validate(),
        "dispatch": dispatch,
        "collect": collect,
        "validate_completion": lambda c: verify_completion(args.repo, args.base, initial_tests, trace),
        "record": lambda c: {"status": "PASS", "todo": guard.set_todo("completed"),
                             "model_identity": "N/A", "parallel_execution": "NOT_RUN"}}
    failure = None
    for step, action in actions.items():
        if args.resume and step == "prepare":
            continue
        try:
            runtime.execute(session, step, action)
        except GateError as error:
            failure = error
            trace.emit("run_blocked", step=step, code=error.code,
                       details=error.details, launch_count=guard.launch_count)
            break
    final_control = runtime.control(session)
    if failure is None and not final_control.get("projection", {}).get("completed"):
        failure = GateError("RUNTIME_NOT_COMPLETED")
    result = {"result": "FAIL" if failure else "PASS", "failure": failure.code if failure else None,
              "launch_count": guard.launch_count, "session_id": session,
              "completed_at": now(), "control": final_control,
              "artifacts": runtime.call("GET", f"/workflow-sessions/{session}/artifacts")}
    save(args.state / "result.json", result)
    trace.emit("run_completed", result=result["result"], launch_count=guard.launch_count)
    print(json.dumps({k: result[k] for k in ("result", "failure", "launch_count", "session_id")}))
    if failure:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
