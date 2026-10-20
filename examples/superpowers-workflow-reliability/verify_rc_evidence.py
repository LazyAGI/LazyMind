#!/usr/bin/env python3
"""Offline integrity/semantic checks for recorded evidence. Does not run E2E."""
import hashlib
import json
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE / "evidence/rc"
sha = lambda b: hashlib.sha256(b).hexdigest()


def load(path):
    return json.loads(path.read_text())


def main():
    manifests = [HERE / "evidence/manifest.json", HERE / "evidence/release-audit/manifest.json", ROOT / "manifest.json"]
    counts = []
    for path in manifests:
        entries = load(path)["files"]
        for entry in entries:
            data = (path.parent / entry["path"]).read_bytes()
            assert sha(data) == entry["export_sha256"], entry["path"]
            assert len(data) == entry["bytes"], entry["path"]
        counts.append(len(entries))
    for name, expected in load(ROOT / "provenance.json")["source_sha256"].items():
        assert sha((HERE / name).read_bytes()) == expected, name
    files = {x["path"]: x for x in load(ROOT / "manifest.json")["files"]}
    verification = load(ROOT / "verification-live.json")
    for label, case in verification["cases"].items():
        result = load(ROOT / label / "result.json")
        events = [json.loads(line) for line in (ROOT / label / "trace.jsonl").read_text().splitlines()]
        assert result["control"]["projection"] == case["control_projection"]
        assert result["launch_count"] == case["launch_receipt_count"]
        assert sum(e["event"] == "child_process_started" for e in events) == result["launch_count"]
        if result["launch_count"]:
            raw_hash = files[label + "/child-trace.jsonl"]["raw_sha256"]
            receipt = next(e["result"] for e in events if e["event"] == "dispatch_completed")
            assert receipt["trace_sha256"] == raw_hash == case["child_trace_raw_sha256"]
            child = [json.loads(line) for line in (ROOT / label / "child-trace.jsonl").read_text().splitlines()]
            assert sum(e["type"] == "turn.completed" for e in child) == 1
        else:
            assert not (ROOT / label / "child-trace.jsonl").exists()
            assert result["control"]["projection"]["nodes"]["dispatch"]["execution"] == "none"
        if case["expected_failure"]:
            assert result["failure"] == case["expected_failure"]
            assert not result["control"]["projection"]["completed"]
            assert "result" not in case["selected_slots"]
        else:
            assert result["result"] == "PASS" and result["control"]["projection"]["completed"]
            assert len(case["selected_slots"]) == 6
    before = load(ROOT / "missing-before-recovery.json")
    after = load(ROOT / "missing-brief/result.json")
    assert before["session_id"] == after["session_id"]
    assert before["failure"] == "BRIEF_MISSING" and before["launch_count"] == 0
    attempts = [x for x in after["control"]["session"]["steps"] if x["step_id"] == "validate_dispatch"]
    assert [(x["attempt"], x["status"]) for x in attempts] == [(1, "failed"), (2, "succeeded")]
    print(json.dumps({"result": "PASS", "kind": "offline-evidence-check-not-E2E", "manifest_file_counts": counts, "recorded_sessions": len(verification["cases"])}))


if __name__ == "__main__":
    main()
