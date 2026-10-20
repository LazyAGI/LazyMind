#!/usr/bin/env python3
"""Export only this experiment's allowlisted traces with path redaction and hashes."""
import argparse
import hashlib
import json
from pathlib import Path
import re

from gates import now, save


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--source", type=Path, required=True)
    p.add_argument("--destination", type=Path, required=True)
    p.add_argument("--upstream", type=Path, required=True)
    args = p.parse_args()
    root, dest = args.source.resolve(), args.destination.resolve()
    dest.mkdir(parents=True, exist_ok=True)
    replacements = [(str(root), "$EVAL_ROOT"), (str(args.upstream.resolve()), "$SUPERPOWERS_ROOT"),
                    (str(Path(__file__).resolve().parents[2]), "$LAZYMIND_ROOT"),
                    (str(Path.home()), "$HOME")]
    paths = ["manifest.json", "native-prompt.txt", "native-trace.jsonl",
             "native-final.txt", "native-final.patch", "native-stderr.log", "native-independent-checks.json",
             "native-independent-checks.jsonl", "fault-summary.json", "unit-tests.log",
             "unit-tests-continued.log", "model-readiness.json", "flash-chat-verification.json"]
    paths += [str(p.relative_to(root)) for p in (root / "native-worktree/.eval").glob("*") if p.is_file()]
    for case in ["native-audited/native", "workflow-normal", "workflow-final", "missing-brief/run", "empty-brief/run",
                 "wrong-base/run", "wrong-base-actual/run", "dispatch-failure/run", "missing-coverage/run",
                 "retry-missing-brief/run", "runtime-probes", "final-publication",
                 "missing-todo/run", "stale-todo/run", "parallel-workflows",
                 "parallel-workflows/run-1", "parallel-workflows/run-2"]:
        for name in ["trace.jsonl", "result.json", "child-trace.jsonl", "child-final.txt",
                     "child-stderr.log", "child-prompt.txt", "diagnostics.json", "publication.json", "launch.json", "prompt.txt", "final.txt",
                     "stderr.log", "runner.log", "before.json", "final.patch", "tool-trace.jsonl", "tool-trace.manifest.json", "independent-checks.json"]:
            if (root / case / name).is_file():
                paths.append(case + "/" + name)
    audited_reports = root / "native-audited/worktree/.eval"
    paths += [str(p.relative_to(root)) for p in audited_reports.glob("*") if p.is_file()]
    files = []
    for relative in paths:
        src = root / relative
        if not src.is_file():
            continue
        raw = src.read_bytes()
        content = raw.decode()
        for original, replacement in replacements:
            content = content.replace(original, replacement)
        if re.search(r"sk-[A-Za-z0-9]{16,}|Bearer [A-Za-z0-9_.-]{16,}", content):
            raise ValueError("Possible credential in " + relative)
        if src.suffix == ".jsonl":
            for line in content.splitlines():
                row = json.loads(line)
                if row.get("type") in {"reasoning", "analysis"}:
                    raise ValueError("Private reasoning item in " + relative)
        exported = "baseline.json" if relative == "manifest.json" else relative
        dst = dest / exported
        dst.parent.mkdir(parents=True, exist_ok=True)
        dst.write_text(content)
        files.append({"path": exported, "raw_sha256": hashlib.sha256(raw).hexdigest(),
                      "export_sha256": hashlib.sha256(dst.read_bytes()).hexdigest(),
                      "bytes": dst.stat().st_size})
    save(dest / "manifest.json", {
        "exported_at": now(), "kind": "redacted-real-execution-evidence",
        "redaction": "Known local roots replaced; handles redacted at API logging. No unrelated sessions collected.",
        "files": files})


if __name__ == "__main__":
    main()
