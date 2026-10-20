#!/usr/bin/env python3
"""Extract tool calls/results from one explicitly identified experiment rollout.

No messages, reasoning, world state, or other sessions are exported.
"""
import argparse
import json
from pathlib import Path

from gates import digest, save


def redact_opaque(value):
    if isinstance(value, str) and value.startswith("gAAAA"):
        return {"encrypted_payload": "[OMITTED]", "sha256": digest(value.encode())}
    if isinstance(value, dict):
        return {k: redact_opaque(v) for k, v in value.items()}
    if isinstance(value, list):
        return [redact_opaque(v) for v in value]
    return value


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--session-file", type=Path, required=True)
    p.add_argument("--expected-cwd", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    args = p.parse_args()
    raw = args.session_file.read_bytes()
    rows = [json.loads(line) for line in raw.decode().splitlines()]
    meta = next(r["payload"] for r in rows if r["type"] == "session_meta")
    if Path(meta["cwd"]).resolve() != args.expected_cwd.resolve():
        raise ValueError("Rollout is not the specified experiment")
    allowed = {
        "function_call": ("name", "arguments", "call_id"),
        "function_call_output": ("call_id", "output"),
        "custom_tool_call": ("name", "input", "call_id", "status"),
        "custom_tool_call_output": ("call_id", "output"),
    }
    exported = []
    for row in rows:
        value = row.get("payload", {})
        fields = allowed.get(value.get("type"))
        if row["type"] == "response_item" and fields:
            record = {k: value[k] for k in fields if k in value}
            if "arguments" in record:
                record["arguments"] = json.loads(record["arguments"])
            exported.append(redact_opaque({"at": row["timestamp"], "type": value["type"],
                                           **record}))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text("".join(json.dumps(r, ensure_ascii=False) + "\n" for r in exported))
    save(args.output.with_suffix(".manifest.json"), {
        "source_session_id": meta["id"], "source_sha256": digest(raw),
        "tool_events": len(exported), "opaque_encrypted_payloads": "omitted with hash", "excluded": ["messages", "reasoning", "world_state",
                                                   "turn_context", "unrelated_sessions"]})
    print(json.dumps({"tool_events": len(exported)}))


if __name__ == "__main__":
    main()
