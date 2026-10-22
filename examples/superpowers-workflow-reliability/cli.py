#!/usr/bin/env python3
"""Configure, install, preflight, run and inspect the bounded reliability integration."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import urllib.request
from urllib.parse import urlsplit
import uuid

from gates import GateError, Trace, git, save
try:
    from lifecycle import ensure
except ModuleNotFoundError:
    print('Missing dependency: install requirements.txt in the Python environment used for this CLI.')
    raise SystemExit(2)
from project import execute
from runtime import Runtime, NoRedirect
from task import Task

UPSTREAM_SHA = "bb92a77741419a4ab5f06e711a283343f1ada0c3"
HINTS = {
    "UI_RETRY_REQUIRED": "Open this session in the authenticated LazyMind Workflow page and click Retry, then rerun with --resume. External API credentials cannot issue user control actions.",
    "WORKFLOW_NOT_INSTALLED": "Run install with this configuration first.",
    "WORKFLOW_DISABLED": "Run install to enable this exact compatible Workflow in manual mode.",
    "WORKFLOW_INCOMPATIBLE": "Use a new workflow name in a new config, or review and publish compatible files through LazyMind authoring. Existing drafts are never overwritten.",
    "WORKFLOW_AMBIGUOUS": "Choose a unique workflow name; the visible catalog has multiple matches.",
    "RUNTIME_REJECTED": "Inspect the private trace for the HTTP status; 401/403 requires a fresh login for the owning user. Do not blindly repeat failed publication.",
    "RUNTIME_UNREACHABLE": "Check the configured URL and LazyMind health. Inspect saved session/trace before retrying an uncertain write.",
    "BRIEF_MISSING": "Run restore-brief with the same task/repo/state, click Retry in the authenticated Workflow page, then run --resume.",
    "BRIEF_EMPTY": "Run restore-brief with the same task/repo/state, click Retry in the authenticated Workflow page, then run --resume.",
    "BASE_MISMATCH": "Use the declared full SHA in a clean new linked worktree; do not reset a worktree containing work.",
    "LINKED_WORKTREE_REQUIRED": "Use git worktree add to create a dedicated linked worktree at the intended base.",
    "REPORT_IGNORE_REQUIRED": "Commit a .gitignore rule for .eval/ before capturing the task base SHA.",
    "UNSAFE_REPLAY_REFUSED": "A dispatch may already have happened. Inspect status, trace and Git changes; do not automatically replay it. Resolve the old session in LazyMind before starting a reviewed new task.",
    "CODEX_LOGIN_REQUIRED": "Run codex login using the selected CLI, then repeat preflight.",
    "CODEX_FLAGS_UNSUPPORTED": "Use a Codex CLI supporting --ignore-user-config, --ephemeral and --json. See tested versions in README.",
    "TOKEN_PERMISSIONS": "Use a private regular token file with mode 0600.",
    "CHILD_TIMEOUT": "The owned process group was terminated after 600 seconds. Inspect the failed session and worktree; dispatch is not automatically retried.",
    "CHILD_INTERRUPTED": "The owned process group was terminated. Inspect the failed session and worktree; dispatch is not automatically retried.",
}


def private_save(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(value, f, indent=2)
        f.write("\n")


def url_base(value):
    u = urlsplit(value)
    if u.scheme not in ("http", "https") or not u.hostname or u.username or u.password or u.query or u.fragment:
        raise GateError("URL_INVALID")
    if u.scheme == "http" and u.hostname not in ("127.0.0.1", "localhost", "::1"):
        raise GateError("REMOTE_HTTPS_REQUIRED")
    return value.rstrip("/") if u.path.rstrip("/").endswith("/api/core") else value.rstrip("/") + "/api/core"


def credentials(config):
    path = Path(config["token_file"])
    if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077:
        raise GateError("TOKEN_PERMISSIONS")
    token = json.loads(path.read_text()).get("token")
    if not isinstance(token, str) or not token:
        raise GateError("TOKEN_REQUIRED")
    return token


def configure(args):
    if args.config.exists():
        raise GateError("CONFIG_EXISTS")
    base = url_base(args.url)
    token_file = args.token_file
    if args.local_login:
        u = urlsplit(base)
        if u.hostname not in ("127.0.0.1", "localhost", "::1"):
            raise GateError("LOCAL_LOGIN_LOOPBACK_ONLY")
        request = urllib.request.Request(base[:-len('/api/core')] + '/_local/admin-session',
                                         data=b'{}', method='POST', headers={'Content-Type': 'application/json'})
        with urllib.request.build_opener(NoRedirect).open(request, timeout=20) as response:
            data = json.load(response)
        token = data.get('data', data).get('token')
        if not token:
            raise GateError("LOCAL_LOGIN_UNAVAILABLE")
        token_file = args.config.with_suffix('.session.json')
        private_save(token_file, {'token': token})
    binary = shutil.which(args.codex)
    if not binary:
        raise GateError("CODEX_NOT_FOUND")
    config = {"url": base, "token_file": str(token_file.resolve()), "superpowers": str(args.superpowers.resolve()),
              "model": args.model, "reasoning": args.reasoning, "codex": binary, "workflow_name": args.workflow_name}
    credentials(config)
    private_save(args.config, config)
    return {"result": "CONFIGURED", "next": "install"}


def preflight(config, runtime):
    if sys.version_info < (3, 9):
        raise GateError("PYTHON_39_REQUIRED")
    if not shutil.which('git') or not shutil.which('bash'):
        raise GateError("GIT_OR_BASH_MISSING")
    if not config.get('model') or config.get('reasoning') not in ('low', 'medium', 'high'):
        raise GateError("MODEL_CONFIG_INVALID")
    binary = config['codex']
    try:
        version = subprocess.run([binary, '--version'], capture_output=True, text=True, timeout=15)
        flags = subprocess.run([binary, 'exec', '--ignore-user-config', '--ephemeral', '--json', '--help'],
                               capture_output=True, text=True, timeout=15)
        login = subprocess.run([binary, 'login', 'status'], capture_output=True, timeout=15)
    except (OSError, subprocess.TimeoutExpired):
        raise GateError("CODEX_UNAVAILABLE") from None
    if version.returncode or flags.returncode or 'Usage:' not in flags.stdout:
        raise GateError("CODEX_FLAGS_UNSUPPORTED")
    if login.returncode:
        raise GateError("CODEX_LOGIN_REQUIRED")
    upstream = Path(config['superpowers'])
    if git(upstream, 'rev-parse', 'HEAD') != UPSTREAM_SHA or git(upstream, 'status', '--porcelain'):
        raise GateError("UPSTREAM_VERSION_MISMATCH")
    runtime.call('GET', '/chat/settings/workflows')
    return {"python": '.'.join(map(str, sys.version_info[:3])), "codex": version.stdout.strip(),
            "requested_model": config['model'], "backend_model": "N/A",
            "model_availability": "verified by the accepted dispatch, not by a preflight model call",
            "upstream_sha": UPSTREAM_SHA, "authenticated_runtime": True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True, type=Path, help='Private configuration outside the target worktree')
    sub = parser.add_subparsers(dest='command', required=True)
    c = sub.add_parser('configure')
    c.add_argument('--url', required=True)
    login = c.add_mutually_exclusive_group(required=True)
    login.add_argument('--local-login', action='store_true')
    login.add_argument('--token-file', type=Path)
    c.add_argument('--superpowers', required=True, type=Path)
    c.add_argument('--codex', default='codex')
    c.add_argument('--model', required=True)
    c.add_argument('--reasoning', choices=['low', 'medium', 'high'], default='medium')
    c.add_argument('--workflow-name', default='superpowers-reliability-rc1')
    for name in ['install', 'preflight']:
        sub.add_parser(name)
    for name in ['run', 'restore-brief']:
        p = sub.add_parser(name)
        p.add_argument('--task', required=True, type=Path)
        p.add_argument('--repo', required=True, type=Path)
        p.add_argument('--state', required=True, type=Path)
        if name == 'run':
            p.add_argument('--resume', action='store_true')
            p.add_argument('--fault', choices=['missing-brief', 'empty-brief', 'missing-coverage'],
                           help='Explicit fault injection for a disposable test repository only')
    s = sub.add_parser('status')
    s.add_argument('--state', required=True, type=Path)
    args = parser.parse_args()
    args.config = args.config.resolve()
    if args.command == 'configure':
        return configure(args)
    config = json.loads(args.config.read_text())
    config['url'] = url_base(config['url'])
    token = credentials(config)
    state = getattr(args, 'state', None)
    if state:
        state = state.resolve()
        if args.command == 'run' and args.resume and args.fault:
            raise GateError('RESUME_FAULT_CONFLICT')
        if args.command == 'run' and not args.resume:
            state.mkdir(parents=True, mode=0o700, exist_ok=False)
        elif not state.is_dir():
            raise GateError('STATE_NOT_FOUND')
        if state.stat().st_mode & 0o077:
            raise GateError('STATE_PERMISSIONS')
    else:
        state = args.config.parent / ('lifecycle-' + str(uuid.uuid4()))
        state.mkdir(mode=0o700)
    trace = Trace(state / 'trace.jsonl')
    runtime = Runtime(config['url'], token, trace)
    if args.command == 'status':
        session = json.loads((state / 'session.json').read_text())['session_id']
        control = runtime.control(session)
        save(state / 'readback-control.json', control)
        save(state / 'readback-artifacts.json', runtime.call('GET', f'/workflow-sessions/{session}/artifacts'))
        return {'session_id': session, 'status': control['session']['status'],
                'completed': control['projection']['completed'],
                'steps': {k: v['execution'] for k, v in control['projection']['nodes'].items()}}
    checks = preflight(config, runtime)
    trace.emit('preflight', executor_sources={n: __import__('hashlib').sha256(Path(__file__).with_name(n).read_bytes()).hexdigest() for n in ['cli.py', 'project.py', 'task.py', 'gates.py', 'runtime.py', 'lifecycle.py']}, **checks)
    workflow = ensure(runtime, config['workflow_name'], install=args.command == 'install')
    save(state / 'workflow.json', workflow)
    if args.command in ('install', 'preflight'):
        return {'result': 'PASS', **workflow, 'preflight': checks}
    task = Task(args.task, args.repo)
    if state.is_relative_to(task.repo) or args.config.is_relative_to(task.repo) or Path(config['token_file']).is_relative_to(task.repo):
        raise GateError('PRIVATE_STATE_MUST_BE_OUTSIDE_WORKTREE')
    with (state / 'runner.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise GateError('RUNNER_BUSY') from None
        if args.command == 'restore-brief':
            saved = json.loads((state / 'session.json').read_text())
            if saved['binding']['task'] != __import__('hashlib').sha256(task.raw).hexdigest() or saved['binding']['repo'] != str(task.repo):
                raise GateError('RESUME_INPUT_CHANGED')
            c = runtime.control(saved['session_id'])
            if c['projection']['nodes']['dispatch']['execution'] != 'none':
                raise GateError('UNSAFE_REPLAY_REFUSED')
            task.prepare_brief(config['superpowers'], state / 'brief.md', trace)
            return {'result': 'BRIEF_RESTORED', 'next': 'Click Retry in the authenticated Workflow page, then run --resume with the same arguments'}
        if not args.resume:
            if git(task.repo, 'status', '--porcelain') or (task.repo / '.eval').exists():
                raise GateError('WORKTREE_NOT_FRESH')
            # Keep base mismatch inside the Runtime admission gate so its failed attempt is observable.
        return execute(runtime, task, config, state, workflow, args.resume, args.fault)


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    try:
        result = main()
        print(json.dumps(result))
        raise SystemExit(1 if result.get('result') == 'FAIL' else 0)
    except GateError as error:
        print(json.dumps({'result': 'FAIL', 'code': error.code, 'hint': HINTS.get(error.code,
            'Check task schema, Plan heading/coverage, protected files and private local trace. No automatic replay is performed.')}))
        raise SystemExit(1)
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print(json.dumps({'result': 'FAIL', 'code': type(error).__name__,
                          'hint': 'Check dependencies, private config/task files and Git inputs; no credentials or local paths are printed.'}))
        raise SystemExit(1)
