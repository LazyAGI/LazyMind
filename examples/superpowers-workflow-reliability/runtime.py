"""Public LazyMind API adapter for the guarded example executor."""
import json
from pathlib import Path
import urllib.request
import urllib.error
import uuid
import socket

from gates import GateError, now


def public(value):
    if isinstance(value, dict):
        return {k: ("[REDACTED]" if k in {"token", "execution_handle", "lease_token", "api_key"}
                    else public(v)) for k, v in value.items()}
    if isinstance(value, list):
        return [public(v) for v in value]
    return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise urllib.error.HTTPError(req.full_url, code, "redirect refused", headers, fp)


class Runtime:
    def __init__(self, url, token, trace):
        self.url, self.token, self.trace = url.rstrip("/"), token, trace

    def call(self, method, path, body=None):
        started = now()
        req = urllib.request.Request(
            self.url + path, method=method,
            data=json.dumps(body).encode() if body is not None else None,
            headers={"Authorization": "Bearer " + self.token,
                     "Content-Type": "application/json",
                     "Workflow-Contract-Version": "workflow.v1"})
        try:
            with urllib.request.build_opener(NoRedirect).open(req, timeout=45) as response:
                status, data = response.status, json.load(response)
        except urllib.error.HTTPError as error:
            status = error.code
            try:
                data = json.load(error)
            except ValueError:
                data = {"error": "non-JSON API error"}
        except (urllib.error.URLError, TimeoutError, socket.timeout):
            self.trace.emit("runtime_transport_failed", method=method, path=path,
                            started_at=started, completed_at=now())
            raise GateError("RUNTIME_UNREACHABLE") from None
        self.trace.emit("runtime_api", method=method, path=path, started_at=started,
                        completed_at=now(), status=status,
                        request=public(body), response=public(data))
        if status >= 400 or data.get("ok") is False or data.get("code", 0) not in (0, 200):
            raise GateError("RUNTIME_REJECTED", {"status": status, "response": public(data)})
        return data.get("result", data.get("data", data))

    def publish(self, package):
        if isinstance(package, dict):
            files = package
        else:
            package = Path(package)
            files = {str(p.relative_to(package)): p.read_text()
                     for p in package.rglob("*") if p.is_file()}
        draft = self.call("POST", "/workflow-authoring/v1/drafts",
                          {"name": "Superpowers reliability evaluation",
                           "source_type": "blank", "files": files})
        draft_id = draft.get("draft_id") or draft.get("id") or draft["draft"]["id"]
        self.call("POST", f"/workflow-drafts/{draft_id}:validate", {})
        diagnostics = self.call("GET", f"/workflow-authoring/v1/drafts/{draft_id}/diagnostics")
        if not diagnostics.get("valid"):
            raise GateError("DIAGNOSTICS_FAILED", diagnostics)
        return self.call("POST", f"/workflow-authoring/v1/drafts/{draft_id}:publish", {})

    def start(self, workflow_id, objective="Run the approved isolated slug fixture reliability evaluation."):
        command = str(uuid.uuid4())
        preparation = self.call("POST", "/workflow-preparations", {
            "preparation_id": command, "idempotency_key": command,
            "workflow_id": workflow_id, "origin_host": "external-agent",
            "controller_host": "external-agent", "control_protocol": "workflow.control.v1",
            "host_binding_required": False, "workflow_mode": "auto",
            "origin_ref": command,
            "request_context": objective,
            "input_bindings": {}})
        return self.call("POST", "/workflow-preparations/" +
                         preparation.get("preparation_id", command) + ":consume", {})

    def control(self, session):
        return self.call("GET", f"/workflow-sessions/{session}/control")

    def execute(self, session, step, function):
        state = self.control(session)
        control = state["control"]
        # Core admission, not this program, decides whether the step may start.
        active = control.get("active_execution_ids") or []
        if active:
            if len(active) != 1:
                raise GateError("AMBIGUOUS_EXECUTION")
            # A UI Retry already created this attempt. Claim it; never begin again.
            attempt = active[0]
        else:
            result = self.call("POST", f"/workflow-sessions/{session}/executions:begin", {
                "command_id": str(uuid.uuid4()), "expected_state_version": control["state_version"],
                "step_id": step})
            attempt = result["receipt"]["execution_id"]
        prefix = f"/workflow-sessions/{session}/hosted-attempts/{attempt}"
        grant = self.call("POST", prefix + ":begin", {})
        handle, contract = grant["execution_handle"], grant["step_contract"]
        if contract["step_id"] != step or grant.get("executor_host") != "external-agent":
            raise GateError("CONTRACT_MISMATCH")
        self.trace.emit("step_started", step=step, attempt_id=attempt)
        try:
            output = function(contract)
            outputs = contract.get("required_outputs") or contract["declared_outputs"]
            if len(outputs) != 1:
                raise GateError("OUTPUT_CONTRACT_MISMATCH")
            self.call("POST", prefix + "/artifacts", {
                "execution_handle": handle,
                "artifact": {"slot": outputs[0], "content_type": "json", "seq": 1, "value": output}})
        except Exception as error:
            code = error.code if isinstance(error, GateError) else type(error).__name__
            self.call("POST", prefix + ":complete", {
                "execution_handle": handle, "outcome": "failed",
                "error_code": code, "summary": code})
            self.trace.emit("step_failed", step=step, error_code=code)
            if isinstance(error, GateError):
                raise
            raise GateError(code) from error
        receipt = self.call("POST", prefix + ":complete", {
            "execution_handle": handle, "outcome": "succeeded", "summary": step + " verified"})
        self.trace.emit("step_completed", step=step, attempt_id=attempt)
        return output, receipt
