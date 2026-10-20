"""Six accepted Runtime steps for a declared project task; no fixture IDs."""
import json
import uuid
from pathlib import Path

from gates import Guard, GateError, codex_child, digest, git, now, save


STEPS = ("prepare", "validate_dispatch", "dispatch", "collect", "validate_completion", "record")


def execute(runtime, task, config, state, workflow, resume=False, fault=None):
    guard = Guard(task.repo, state, task.data["expected_base"], runtime.trace,
                  task_id=task.data["task_id"], binding_check=task.validate_binding)
    binding = {"task": digest(task.raw), "repo": str(task.repo), "config_model": config["model"],
               "reasoning": config["reasoning"], "workflow": workflow}
    binding["workflow"] = {k: v for k, v in workflow.items() if k != "created"}
    if resume:
        saved = json.loads((state / "session.json").read_text())
        if saved["binding"] != binding:
            raise GateError("RESUME_INPUT_CHANGED")
        session = saved["session_id"]
        control = runtime.control(session)
        nodes = control["projection"]["nodes"]
        if nodes["prepare"]["execution"] != "succeeded" or any(nodes[s]["execution"] != "none" for s in STEPS[2:]):
            raise GateError("UNSAFE_REPLAY_REFUSED")
        if any(x.get("event") == "dispatch_started" for x in map(json.loads, runtime.trace.path.read_text().splitlines())):
            raise GateError("UNSAFE_REPLAY_REFUSED")
        prep = next(x["artifact_value"] for x in control["session"].get("slots", [])
                    if x.get("selected") and x["slot_id"] == "preparation")
        guard.expected_brief_hash = prep["brief_sha256"]
        # Only the authenticated Workflow UI may issue user recovery controls.
        # This executor claims a UI-created attempt; it never impersonates that UI.
        if not control["control"].get("active_execution_ids"):
            raise GateError("UI_RETRY_REQUIRED")
        if (state / "result.json").exists():
            with (state / "previous-results.jsonl").open("a") as f:
                f.write(json.dumps(json.loads((state / "result.json").read_text())) + "\n")
    else:
        started = runtime.start(workflow["workflow_ref"], "Execute declared task " + task.data["task_id"])
        session = started.get("session_id") or started["session"]["id"]
        save(state / "session.json", {"session_id": session, "binding": binding})
        control = runtime.control(session)
        if control["session"]["pinned_revision_id"] != workflow["revision_id"]:
            raise GateError("RUNTIME_REVISION_CHANGED")
    runtime.trace.emit("project_run_started", task_id=task.data["task_id"], resume=resume,
                       fault=fault, requested_model=config["model"], actual_backend_model="N/A")

    def prepare(contract):
        guard.expected_brief_hash = task.prepare_brief(config["superpowers"], guard.brief, runtime.trace)
        guard.set_todo("in_progress")
        receipt = {**guard.inspect(), "task": task.data}
        # Only explicit experiment flags inject faults. Never enabled by configuration.
        if fault == "missing-brief":
            guard.brief.unlink()
        elif fault == "empty-brief":
            guard.brief.write_text("")
        if fault in ("missing-brief", "empty-brief"):
            runtime.trace.emit("fault_injected", fault=fault)
        return receipt

    def dispatch(contract):
        instructions = (
            "Only edit these declared implementation files: " + json.dumps(task.data["allowed_files"]) +
            ". Do not edit the Plan or protected tests. Do not push. "
            "Write .eval/coverage.json as an array of {id,status,evidence}, exactly one DONE row with "
            "nonempty evidence text for every declared coverage id. Run the declared checks. "
        )
        return guard.dispatch(lambda brief: codex_child(task.repo, state,
            "Accepted step: " + contract["prompt"] + "\n\n" + brief,
            config["model"], config["codex"], instructions=instructions, reasoning=config["reasoning"]))

    def collect(contract):
        task.validate_scope()
        report = task.repo / ".eval/implementer-report.md"
        coverage = task.repo / ".eval/coverage.json"
        if not report.is_file() or report.is_symlink() or not report.read_text().strip():
            raise GateError("REPORT_MISSING")
        if fault == "missing-coverage":
            rows = json.loads(coverage.read_text())
            coverage.write_text(json.dumps(rows[:-1]))
            runtime.trace.emit("fault_injected", fault=fault)
        return {"head": git(task.repo, "rev-parse", "HEAD"), "report": report.read_text(),
                "coverage": json.loads(coverage.read_text()),
                "child_trace_sha256": digest((state / "child-trace.jsonl").read_bytes())}

    actions = [prepare, lambda c: guard.validate(), dispatch, collect,
               lambda c: task.complete(runtime.trace),
               lambda c: {"status": "PASS", "task_id": task.data["task_id"],
                          "todo": guard.set_todo("completed"), "backend_model": "N/A"}]
    failure = None
    for step, action in zip(STEPS, actions):
        if resume and step == "prepare":
            continue
        try:
            runtime.execute(session, step, action)
        except GateError as error:
            failure = error.code
            runtime.trace.emit("run_blocked", step=step, code=failure, launch_count=guard.launch_count)
            break
    control = runtime.control(session)
    artifacts = runtime.call("GET", f"/workflow-sessions/{session}/artifacts")
    slots = {x["slot_id"] for x in control["session"].get("slots", []) if x.get("selected")}
    if not failure and (not control["projection"]["completed"] or slots !=
                       {"preparation", "admission", "execution", "evidence", "completion", "result"}):
        failure = "RUNTIME_NOT_COMPLETED"
    result = {"result": "FAIL" if failure else "PASS", "failure": failure,
              "session_id": session, "launch_count": guard.launch_count, "completed_at": now(),
              "control": control, "artifacts": artifacts}
    save(state / "result.json", result)
    runtime.trace.emit("project_run_completed", result=result["result"], failure=failure)
    return {k: result[k] for k in ("result", "failure", "session_id", "launch_count")}
