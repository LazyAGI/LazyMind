"""Idempotent package installation without silently upgrading published workflows."""
import base64
import re
from pathlib import Path
from urllib.parse import quote

import yaml

from gates import GateError


def package_files(name):
    if not re.fullmatch(r"[a-z][a-z0-9-]{2,80}", name):
        raise GateError("WORKFLOW_NAME_INVALID")
    root = Path(__file__).resolve().parents[2] / "workflows/superpowers-reliability"
    files = {str(p.relative_to(root)): p.read_text() for p in root.rglob("*") if p.is_file()}
    spec = yaml.safe_load(files["workflow.yaml"])
    spec["id"] = name
    files["workflow.yaml"] = yaml.safe_dump(spec, sort_keys=False)
    return files


def compatible(expected, observed):
    if set(expected) != set(observed):
        return False
    for path, text in expected.items():
        actual = base64.b64decode(observed[path], validate=True).decode()
        if path.endswith((".yaml", ".yml")):
            a, b = yaml.safe_load(text), yaml.safe_load(actual)
            if path == "workflow.yaml":
                # Authoring finalization adds display tabs. All execution fields must match.
                a.pop("ui", None)
                b.pop("ui", None)
            if a != b:
                return False
        elif text != actual:
            return False
    return True


def ensure(runtime, name, install=False):
    expected = package_files(name)
    # Settings catalog includes disabled owned workflows; enabled-only discovery
    # would accidentally create a second blank draft.
    items = runtime.call("GET", "/chat/settings/workflows")["workflows"]
    matches = [x for x in items if x["workflow_id"] == name]
    if len(matches) > 1:
        raise GateError("WORKFLOW_AMBIGUOUS")
    created = False
    if not matches:
        if not install:
            raise GateError("WORKFLOW_NOT_INSTALLED")
        publication = runtime.publish(expected)
        ref = publication["workflow_ref"]
        created = True
    else:
        ref = matches[0]["workflow_ref"]
    workflow = runtime.call("GET", "/workflow-runtime/v1/workflows/" + quote(ref, safe=""))
    if not compatible(expected, workflow["files"]):
        raise GateError("WORKFLOW_INCOMPATIBLE")
    if install:
        runtime.call("PATCH", "/chat/settings/workflows/" + quote(ref, safe=""), {"call_mode": "manual"})
    elif matches and not matches[0].get("enabled"):
        raise GateError("WORKFLOW_DISABLED")
    return {"workflow_ref": ref, "revision_id": workflow["revision_id"],
            "revision_no": workflow["revision_no"], "created": created}
