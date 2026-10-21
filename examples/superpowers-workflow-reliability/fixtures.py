"""Create a new disposable repository and linked worktree; never reuse or clean one."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess

from gates import git, now, save


def create(root):
    root = Path(root).resolve()
    root.mkdir(parents=True, exist_ok=False)
    repo, worktree = root / "repository", root / "worktree"
    shutil.copytree(Path(__file__).parent / "fixture", repo)
    env = {**os.environ, "GIT_AUTHOR_DATE": "2026-10-19T00:00:00Z",
           "GIT_COMMITTER_DATE": "2026-10-19T00:00:00Z"}
    for cmd in [["git", "init", "-q", "-b", "main"],
                ["git", "config", "user.name", "Reliability Fixture"],
                ["git", "config", "user.email", "fixture@example.invalid"],
                ["git", "add", "."],
                ["git", "commit", "-qm", "Initial reliability fixture"]]:
        subprocess.run(cmd, cwd=repo, env=env, check=True, capture_output=True)
    base = git(repo, "rev-parse", "HEAD")
    subprocess.run(["git", "worktree", "add", "--quiet", "-b", "evaluation",
                    str(worktree), base], cwd=repo, check=True, capture_output=True)
    result = {"created_at": now(), "repo": str(worktree), "base": base}
    save(root / "fixture.json", result)
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    print(json.dumps(create(parser.parse_args().directory)))
