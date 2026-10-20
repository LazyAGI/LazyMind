#!/usr/bin/env python3
"""Negative tests against a real Runtime. Does not start any model."""
import argparse
import json
from pathlib import Path

from gates import GateError, Trace, save
from runtime import Runtime


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--url", required=True)
    p.add_argument("--token-file", required=True)
    p.add_argument("--workflow-id", required=True)
    p.add_argument("--state", required=True, type=Path)
    args = p.parse_args()
    args.state.mkdir(parents=True, exist_ok=True)
    r = Runtime(args.url, json.loads(Path(args.token_file).read_text())["token"],
                Trace(args.state / "trace.jsonl"))
    started = r.start(args.workflow_id)
    session = started["session_id"]
    results = []
    try:
        r.execute(session, "dispatch", lambda _: (_ for _ in ()).throw(
            AssertionError("downstream callback must never execute")))
    except GateError as error:
        results.append({"case": "downstream_before_preparation", "rejected": True,
                        "error": error.details})
    else:
        raise AssertionError("Runtime admitted dispatch before preparation")

    import uuid
    state = r.control(session)["control"]
    begun = r.call("POST", f"/workflow-sessions/{session}/executions:begin", {
        "command_id": str(uuid.uuid4()), "expected_state_version": state["state_version"],
        "step_id": "prepare"})
    attempt = begun["receipt"]["execution_id"]
    prefix = f"/workflow-sessions/{session}/hosted-attempts/{attempt}"
    grant = r.call("POST", prefix + ":begin", {})
    try:
        r.call("POST", prefix + ":complete",
               {"execution_handle": grant["execution_handle"], "outcome": "succeeded"})
    except GateError as error:
        results.append({"case": "success_without_required_output", "rejected": True,
                        "error": error.details})
    else:
        raise AssertionError("Runtime accepted success without preparation artifact")
    r.call("POST", prefix + ":complete", {
        "execution_handle": grant["execution_handle"], "outcome": "failed",
        "error_code": "INTENTIONAL_GATE_PROBE", "summary": "Negative API probe finished"})
    save(args.state / "result.json", {"kind": "real-runtime-api-negative-tests",
                                     "model_calls": 0, "checks": results,
                                     "control": r.control(session)})
    print(json.dumps({"result": "PASS", "checks": len(results), "model_calls": 0}))


if __name__ == "__main__":
    main()
