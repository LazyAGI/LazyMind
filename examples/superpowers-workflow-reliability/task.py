"""A bounded user-declared task: one tracked Plan task, one isolated worktree."""
import json
import re
import subprocess
from pathlib import Path, PurePosixPath

from gates import GateError, coverage_check, digest, git, identity, now


def relative_file(value):
    if not isinstance(value, str) or not value or "\\" in value:
        raise GateError("TASK_PATH_INVALID")
    p = PurePosixPath(value)
    if not p.parts or any(ord(c) < 32 for c in value) or p.is_absolute() or str(p) != value or any(x in ("..", ".git", ".eval") for x in p.parts):
        raise GateError("TASK_PATH_INVALID")
    return value


class Task:
    def __init__(self, manifest, repo):
        self.path, self.repo = Path(manifest).resolve(strict=True), Path(repo).resolve(strict=True)
        self.raw = self.path.read_bytes()
        self.data = json.loads(self.raw)
        d = self.data
        required = {"schema_version", "task_id", "plan", "plan_task", "expected_base",
                    "allowed_files", "protected_files", "coverage", "checks"}
        if set(d) != required or d["schema_version"] != 1:
            raise GateError("TASK_SCHEMA_INVALID")
        if not isinstance(d["task_id"], str) or not re.fullmatch(r"[a-z][a-z0-9-]{1,63}", d["task_id"]):
            raise GateError("TASK_ID_INVALID")
        if type(d["plan_task"]) is not int or d["plan_task"] < 1:
            raise GateError("PLAN_TASK_INVALID")
        if not isinstance(d["expected_base"], str) or not re.fullmatch(r"[a-f0-9]{40}", d["expected_base"]):
            raise GateError("BASE_SHA_REQUIRED")
        for key in ("allowed_files", "protected_files"):
            if not isinstance(d[key], list) or not d[key] or len(set(d[key])) != len(d[key]):
                raise GateError("TASK_SCOPE_INVALID")
            for name in d[key]:
                relative_file(name)
        relative_file(d["plan"])
        self.protected = sorted(set([d["plan"], *d["protected_files"]]))
        if set(d["allowed_files"]) & set(self.protected):
            raise GateError("TASK_SCOPE_OVERLAP")
        if not isinstance(d["checks"], list) or not d["checks"]:
            raise GateError("CHECKS_REQUIRED")
        check_ids = []
        for check in d["checks"]:
            if set(check) != {"id", "argv"} or not isinstance(check["id"], str) or not check["id"].strip():
                raise GateError("CHECK_INVALID")
            if not isinstance(check["argv"], list) or not check["argv"] or not all(isinstance(x, str) and x for x in check["argv"]):
                raise GateError("CHECK_INVALID")
            check_ids.append(check["id"])
        if len(set(check_ids)) != len(check_ids):
            raise GateError("CHECK_INVALID")
        self.coverage = []
        used = set()
        if not isinstance(d["coverage"], list) or not d["coverage"]:
            raise GateError("COVERAGE_REQUIRED")
        for item in d["coverage"]:
            if set(item) != {"id", "description", "checks"} or not all(isinstance(item[x], str) and item[x].strip() for x in ("id", "description")):
                raise GateError("COVERAGE_INVALID")
            if not isinstance(item["checks"], list) or not item["checks"] or not all(isinstance(x, str) for x in item["checks"]) or not set(item["checks"]) <= set(check_ids):
                raise GateError("COVERAGE_CHECK_UNBOUND")
            self.coverage.append(item["id"])
            used.update(item["checks"])
        if len(set(self.coverage)) != len(self.coverage) or used != set(check_ids):
            raise GateError("COVERAGE_CHECK_UNBOUND")
        if self.path.is_relative_to(self.repo):
            raise GateError("MANIFEST_MUST_BE_OUTSIDE_WORKTREE")
        self.original = {}
        for name in self.protected:
            content = subprocess.check_output(["git", "-C", str(self.repo), "show", d["expected_base"] + ":" + name], stderr=subprocess.PIPE)
            self.original[name] = digest(content)
        self.initial_identity = identity(self.repo)
        self.validate_binding()

    def path_in_repo(self, name):
        p = self.repo / name
        # Reject symlink leaves and parents even when their targets are inside.
        for item in [p, *p.parents]:
            if item == self.repo:
                break
            if item.is_symlink():
                raise GateError("TASK_SYMLINK_UNSUPPORTED")
        if not p.resolve().is_relative_to(self.repo):
            raise GateError("TASK_PATH_INVALID")
        return p

    def validate_binding(self):
        if self.path.read_bytes() != self.raw:
            raise GateError("TASK_MANIFEST_CHANGED")
        observed = identity(self.repo)
        if git(self.repo, "rev-parse", "--show-toplevel") != str(self.repo) or observed["git_dir"] == observed["common_dir"]:
            raise GateError("LINKED_WORKTREE_REQUIRED")
        if any(observed[k] != self.initial_identity[k] for k in ("worktree", "git_dir", "index", "common_dir")):
            raise GateError("WORKTREE_IDENTITY_CHANGED")
        for name in [*self.protected, *self.data["allowed_files"]]:
            self.path_in_repo(name)
        for name, expected in self.original.items():
            p = self.path_in_repo(name)
            if not p.is_file() or digest(p.read_bytes()) != expected:
                raise GateError("PROTECTED_INPUT_CHANGED", {"file": name})
        report_dir = self.repo / ".eval"
        if report_dir.is_symlink():
            raise GateError("TASK_SYMLINK_UNSUPPORTED")
        if subprocess.run(["git", "-C", str(self.repo), "check-ignore", "-q", ".eval/coverage.json"]).returncode:
            raise GateError("REPORT_IGNORE_REQUIRED")
        return {"task_id": self.data["task_id"], "manifest_sha256": digest(self.raw),
                "plan": self.data["plan"], "plan_task": self.data["plan_task"],
                "protected_sha256": self.original, "coverage_ids": self.coverage}

    def prepare_brief(self, upstream, output, trace):
        self.validate_binding()
        headings = []
        fenced = False
        for line in (self.repo / self.data["plan"]).read_text().splitlines():
            if line.startswith("```"):
                fenced = not fenced
            if not fenced and re.match(r"^#+[ \t]+Task[ \t]+" + str(self.data["plan_task"]) + r"([^0-9]|$)", line):
                headings.append(line)
        if len(headings) != 1:
            raise GateError("PLAN_TASK_AMBIGUOUS")
        script = Path(upstream) / "skills/subagent-driven-development/scripts/task-brief"
        started = now()
        result = subprocess.run(["bash", str(script), self.data["plan"], str(self.data["plan_task"]), str(output)],
                                cwd=self.repo, capture_output=True, text=True, timeout=15)
        trace.emit("brief_generated", started_at=started, returncode=result.returncode,
                   task_id=self.data["task_id"], plan_sha256=self.original[self.data["plan"]])
        if result.returncode or not Path(output).is_file():
            raise GateError("PLAN_TASK_NOT_FOUND")
        text = Path(output).read_text()
        if not text.strip() or not re.search(r"(?<![\w-])" + re.escape(self.data["task_id"]) + r"(?![\w-])", text.splitlines()[0]) or not all(re.search(r"(?<![\w-])" + re.escape(i) + r"(?![\w-])", text) for i in self.coverage):
            raise GateError("PLAN_TASK_UNBOUND")
        # The task declaration is outside the model's writable workspace.
        with Path(output).open("a") as f:
            f.write("\n\nAccepted task declaration (must satisfy every coverage id):\n" + json.dumps(self.data, indent=2))
        return digest(Path(output).read_bytes())

    def validate_scope(self):
        self.validate_binding()
        if subprocess.run(["git", "-C", str(self.repo), "merge-base", "--is-ancestor", self.data["expected_base"], "HEAD"], capture_output=True).returncode:
            raise GateError("BASE_ANCESTRY_CHANGED")
        changed = git(self.repo, "diff", "--name-only", self.data["expected_base"]).splitlines()
        changed += git(self.repo, "ls-files", "--others", "--exclude-standard").splitlines()
        if not changed:
            raise GateError("NO_IMPLEMENTATION_CHANGE")
        if set(changed) - set(self.data["allowed_files"]):
            raise GateError("SCOPE_CHANGED")
        return sorted(set(changed))

    def complete(self, trace):
        self.validate_scope()
        path = self.repo / ".eval/coverage.json"
        if not path.is_file() or path.is_symlink():
            raise GateError("COVERAGE_MISSING")
        rows = coverage_check(json.loads(path.read_text()), self.coverage)
        results = []
        for check in self.data["checks"]:
            started = now()
            process = subprocess.run(check["argv"], cwd=self.repo, capture_output=True, text=True, timeout=120)
            result = {"id": check["id"], "argv": check["argv"], "started_at": started,
                      "completed_at": now(), "returncode": process.returncode,
                      "stdout": process.stdout, "stderr": process.stderr}
            trace.emit("acceptance_check", **result)
            results.append(result)
            if process.returncode:
                raise GateError("ACCEPTANCE_FAILED", {"check_id": check["id"]})
        changed = self.validate_scope()
        return {"binding": self.validate_binding(), "coverage": rows, "checks": results,
                "changed_files": changed, "head": git(self.repo, "rev-parse", "HEAD"),
                "diff": git(self.repo, "diff", self.data["expected_base"]),
                "file_sha256": {n: digest((self.repo / n).read_bytes()) if (self.repo / n).is_file() else None for n in changed}}
