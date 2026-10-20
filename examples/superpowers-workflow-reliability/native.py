#!/usr/bin/env python3
"""Run the unmodified upstream SDD skill on a fresh fixture using real Codex."""
import argparse
import json
from pathlib import Path
import subprocess
import time

from fixtures import create
from gates import digest, now, save


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--root", type=Path, required=True)
    p.add_argument("--superpowers", type=Path, required=True)
    p.add_argument("--codex", default="codex")
    p.add_argument("--model", default="gpt-6-astra")
    p.add_argument("--persist-session", action="store_true", help="Retain this evaluation session for tool-only trace export")
    args = p.parse_args()
    args.root = args.root.resolve()
    fixture = create(args.root)
    state = args.root / "native"
    state.mkdir()
    skill = args.superpowers.resolve() / "skills/subagent-driven-development/SKILL.md"
    prompt = (
        "This is an authorized isolated reliability experiment. Execute the approved plan "
        f"docs/plans/slug-plan.md using the unmodified skill at {skill}. Read the skill and "
        "its implementer/reviewer templates and task-brief script. Use real subagents for "
        f"implementation and review, all inheriting {args.model}, without changing models. "
        "You are already in an isolated worktree. Follow the skill normally, with no injected "
        "faults or added dispatch gates. Complete the task and write .eval/native-report.md. "
        "Do not push, create a PR, create nested worktrees or modify other repositories. "
        "Local commits are authorized. Do not pause for optional clarification. If real "
        "SubAgent tools are unavailable, report BLOCKED; do not implement directly or simulate."
    )
    (state / "prompt.txt").write_text(prompt)
    command = [args.codex, "exec", "--ignore-user-config",
               "--model", args.model, "--enable", "multi_agent", "--sandbox", "workspace-write",
               "--json", "--color", "never", "-c", 'model_reasoning_effort="medium"',
               "--cd", fixture["repo"], "--output-last-message", str(state / "final.txt"), "-"]
    if not args.persist_session:
        command.insert(2, "--ephemeral")
    started, tick = now(), time.monotonic()
    with (state / "trace.jsonl").open("w") as out, (state / "stderr.log").open("w") as err:
        result = subprocess.run(command, input=prompt, text=True, stdout=out, stderr=err, timeout=900)
    save(state / "launch.json", {"kind": "native-skill", "started_at": started,
         "completed_at": now(), "duration_seconds": round(time.monotonic() - tick, 3),
         "returncode": result.returncode, "requested_model": args.model,
         "actual_backend_model": "N/A", "fixture": fixture,
         "trace_sha256": digest((state / "trace.jsonl").read_bytes()),
         "limitation": "This CLI JSON stream may omit collaboration spawn payloads; inspect evidence before claiming dispatch/model attestation."})
    patch = subprocess.check_output(["git", "-C", fixture["repo"], "diff", fixture["base"], "--", "slug.py"])
    (state / "final.patch").write_bytes(patch)
    print(json.dumps({"returncode": result.returncode, "evidence": str(state)}))
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
