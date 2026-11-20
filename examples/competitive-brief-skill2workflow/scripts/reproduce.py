#!/usr/bin/env python3
"""Small recorder for the real hosted API and original-Skill Codex baseline.

No model/Workflow mocks, implicit credentials, automatic approvals or YAML edits.
Use a disposable output directory; recorded responses are not automatically public.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.error
import urllib.request

CASE = Path(__file__).resolve().parents[1]
COMMIT = 'c75fda00ff0e41c8ee331ae312c8a07f1967653b'
BLOB = '1a7df218996ed350c67e6899a60321f205bb69d6'
REPO = 'https://github.com/anthropics/knowledge-work-plugins'
SENSITIVE_KEYS = {'authorization', 'access_token', 'refresh_token', 'token', 'api_key',
                  'user_id', 'owner_user_id', 'created_by', 'updated_by',
                  'create_user_id', 'owner_user_name', 'create_user_name'}


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def sha(data):
    return hashlib.sha256(data).hexdigest()


def scrub(value):
    if isinstance(value, dict):
        return {k: '[REDACTED]' if k.lower() in SENSITIVE_KEYS else scrub(v)
                for k, v in value.items()}
    if isinstance(value, list):
        return [scrub(v) for v in value]
    if isinstance(value, str):
        value = re.sub(r'user:[^:\s"/]+:', 'user:[REDACTED]:', value)
        value = re.sub(r'/Users/[^\s"\']+', '[LOCAL_PATH_REDACTED]', value)
    return value


def save(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')


def frozen_input():
    return ((CASE / 'evidence/task.md').read_text() + '\n\n' +
            (CASE / 'evidence/source-cards.md').read_text())


def api(method, path, body, out):
    base = os.environ.get('LAZYMIND_WORKFLOW_BASE_URL', '').rstrip('/')
    if not base:
        raise SystemExit('Set LAZYMIND_WORKFLOW_BASE_URL to the explicit Core URL (include /api/core for a gateway).')
    headers = {'Content-Type': 'application/json', 'Workflow-Contract-Version': 'workflow.v1'}
    token = os.environ.get('LAZYMIND_WORKFLOW_TOKEN')
    user = os.environ.get('LAZYMIND_WORKFLOW_USER_ID')
    if token:
        headers['Authorization'] = 'Bearer ' + token
    elif user:
        headers['X-User-Id'] = user
    else:
        raise SystemExit('Provide an authorized token or a trusted LOCAL DEVELOPMENT user ID; do not guess identities.')
    raw_body = json.dumps(body).encode() if body is not None else None
    started, tick = now(), time.monotonic()
    req = urllib.request.Request(base + path, method=method, data=raw_body, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=90) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    except (OSError, TimeoutError) as error:
        save(out, {'started_at': started, 'method': method, 'path': path,
                   'elapsed_seconds': time.monotonic() - tick, 'transport_error': scrub(str(error))})
        raise SystemExit('Transport failed; see the recorded error.')
    try:
        data = json.loads(raw)
    except ValueError:
        data = {'text': raw.decode('utf-8', 'replace')}
    save(out, {'started_at': started, 'elapsed_seconds': round(time.monotonic() - tick, 3),
               'method': method, 'path': path, 'http_status': status, 'response': scrub(data)})
    payload = data.get('result', data.get('data', {}))
    print(json.dumps({k: payload.get(k) for k in ['task_id', 'status', 'stage', 'next_action', 'error_code']}, ensure_ascii=False))
    if payload.get('status') == 'waiting_user_action':
        print('STOP: open the returned LazyMind URL for human action. This script never approves or resumes.')
    if status >= 400 or data.get('code', 0) != 0 or data.get('ok') is False:
        raise SystemExit(1)
    return payload


def baseline(args):
    out = args.out
    skilldir = out / 'upstream/marketing/skills/competitive-brief'
    skilldir.mkdir(parents=True)
    for relative in ['marketing/skills/competitive-brief/SKILL.md', 'marketing/CONNECTORS.md']:
        url = f'https://raw.githubusercontent.com/anthropics/knowledge-work-plugins/{COMMIT}/{relative}'
        with urllib.request.urlopen(url, timeout=30) as response:
            content = response.read()
        if relative.endswith('SKILL.md'):
            blob = hashlib.sha1(b'blob ' + str(len(content)).encode() + b'\0' + content).hexdigest()
            if blob != BLOB:
                raise SystemExit('Skill blob differs from the pinned source; refusing to run.')
        (out / 'upstream' / relative).write_bytes(content)
    frozen = out / 'frozen-input.txt'
    frozen.write_text(frozen_input())
    inputs = [skilldir / 'SKILL.md', out / 'upstream/marketing/CONNECTORS.md', frozen]
    prompt = ('Execute the original competitive-brief Skill. First read these three input files with a tool, '
              'in order: ' + ', '.join(str(p) for p in inputs) + '. These are the only permitted input files. '
              'Do not read personal files, credentials, other workspaces or session history. '
              'Do not call connectors, live web search or subagents. The task explicitly requests offline replay. '
              'Return the complete report as your final answer. Do not claim human approval or performance metrics.')
    (out / 'prompt.txt').write_text(prompt)
    for n in range(1, args.runs + 1):
        run = out / f'baseline-{n}'
        run.mkdir()
        command = [args.codex, 'exec', '--ignore-user-config', '--ephemeral', '--skip-git-repo-check',
                   '--sandbox', 'read-only', '--json', '--color', 'never', '-C', str(out),
                   '-o', str(run / 'report.md'), '-']
        started, tick = now(), time.monotonic()
        with (run / 'events.private.jsonl').open('w') as stdout, (run / 'stderr.private.txt').open('w') as stderr:
            result = subprocess.run(command, input=prompt, text=True, stdout=stdout, stderr=stderr)
        save(run / 'run.json', {'started_at': started, 'elapsed_seconds': round(time.monotonic() - tick, 3),
                               'exit_code': result.returncode, 'prompt_sha256': sha(prompt.encode()),
                               'frozen_input_sha256': sha(frozen.read_bytes()), 'model': 'CLI default; not exposed by JSON events'})
        print(f'baseline {n}: exit {result.returncode}')
        if result.returncode:
            break


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--out', required=True, type=Path, help='New directory outside the checkout; never overwritten')
    sub = p.add_subparsers(dest='action', required=True)
    sub.add_parser('connection')
    submit = sub.add_parser('submit')
    submit.add_argument('--idempotency-key', required=True)
    submit.add_argument('--skill-name', default='competitive-brief-public-trial')
    submit.add_argument('--source-format', choices=['tree', 'blob'], default='tree')
    status = sub.add_parser('status')
    status.add_argument('task_id')
    capture = sub.add_parser('capture')
    capture.add_argument('--skill-id', required=True)
    capture.add_argument('--session-id', required=True)
    capture.add_argument('--draft-id', required=True)
    advance = sub.add_parser('advance', help='One explicit assisted advance; never loops or approves')
    advance.add_argument('--request', required=True, type=Path)
    b = sub.add_parser('baseline')
    b.add_argument('--codex', default='codex')
    b.add_argument('--runs', type=int, choices=[1, 2, 3], default=3)
    args = p.parse_args()
    args.out = args.out.resolve()
    args.out.mkdir(parents=True, exist_ok=False)
    if args.action == 'baseline':
        baseline(args)
    elif args.action == 'connection':
        api('GET', '/external-agent/workflow-capabilities', None, args.out / 'connection.json')
    elif args.action == 'submit':
        path = 'marketing/skills/competitive-brief' + ('/SKILL.md' if args.source_format == 'blob' else '')
        body = {'agent_type': 'codex', 'skill': {'name': args.skill_name, 'url': f'{REPO}/{args.source_format}/{COMMIT}/{path}'},
                'task_description': frozen_input(), 'idempotency_key': args.idempotency_key, 'config': {'reuse_workflow': False}}
        save(args.out / 'request.json', body)
        api('POST', '/external-agent/workflow-tasks', body, args.out / 'submit.json')
    elif args.action == 'status':
        api('GET', f'/external-agent/workflow-tasks/{args.task_id}/result', None, args.out / 'result.json')
    elif args.action == 'advance':
        body = json.loads(args.request.read_text())
        session = body['session_id']
        if not re.fullmatch(r'[a-f0-9-]{36}', session):
            raise SystemExit('Expected an experiment session UUID.')
        if body.get('tool') != 'advance_step' or len(body.get('steps', [])) != 1:
            raise SystemExit('Only a single explicit advance_step request is supported.')
        save(args.out / 'request.json', body)
        api('POST', f'/workflow-sessions/{session}:advance-step', body, args.out / 'advance.json')
    else:
        api('POST', '/workflow-conversions:preflight', {'skill_id': args.skill_id}, args.out / 'preflight.json')
        api('GET', f'/workflow-drafts/{args.draft_id}', None, args.out / 'draft.json')
        for suffix in ['projection', 'steps', 'slots', 'artifacts']:
            api('GET', f'/workflow-sessions/{args.session_id}/{suffix}', None, args.out / f'{suffix}.json')
    print('Review recordings before publishing: automatic redaction is not a privacy guarantee.')


if __name__ == '__main__':
    main()
