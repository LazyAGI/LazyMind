#!/usr/bin/env python3
"""Check public historical excerpts offline; optionally compare with pinned Git blobs.

This checks integrity and recorded semantics, not fresh E2E or third-party attestation.
"""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess

HERE = Path(__file__).resolve().parent
PUBLIC = HERE / 'evidence/public'
CASES = {'normal', 'brief-missing', 'wrong-base', 'missing-coverage', 'recovery', 'reuse-custom', 'native'}
STEPS = ('prepare', 'validate_dispatch', 'dispatch', 'collect', 'validate_completion', 'record')
SLOTS = {'preparation', 'admission', 'execution', 'evidence', 'completion', 'result'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def pointer(data, path):
    for key in path.split('/')[1:]:
        key = key.replace('~1', '/').replace('~0', '~')
        data = data[int(key)] if isinstance(data, list) else data[key]
    return data


def events(record):
    rows = {}
    for path, value in record['values'].items():
        index, key = path.lstrip('/').split('/', 1)
        rows.setdefault(int(index), {})[key] = value
    return [rows[i] for i in sorted(rows)]


def slots(values):
    return {v for p, v in values.items() if p.endswith('/slot_id') and
            values.get(p.removesuffix('/slot_id') + '/selected') is True}


def successful(values):
    require(values['/result'] == 'PASS' and values['/failure'] is None, 'successful result')
    require(values['/control/projection/completed'] is True, 'Runtime completed')
    require(values['/control/session/status'] == 'completed', 'Runtime status')
    require(values['/launch_count'] == 1 and slots(values) == SLOTS, 'one child and six Artifacts')
    require(all(values[f'/control/projection/nodes/{s}/execution'] == 'succeeded' for s in STEPS), 'six steps')


def validate_case(name, case):
    records = case['records']
    v = records['result']['values']
    rows = events(records['events'])
    names = [r['event'] for r in rows]
    children = [r for r in rows if r['event'] == 'child_process_started']
    require(len(children) == v['/launch_count'], 'launch count disagrees with receipts')
    if children:
        require(all(r['pid'] > 0 for r in children), 'missing PID')
        receipts = [r for r in rows if r['event'] == 'dispatch_completed']
        require(len(receipts) == 1 and receipts[0]['result/returncode'] == 0 and
                receipts[0]['result/turn_completed'] is True, 'child completion')
        require(list(records['child']['values'].values()) == ['turn.started', 'turn.completed'], 'child terminal event')
        hashes = records['child_hashes']['values']
        raw_hash = next(v for p, v in hashes.items() if p.endswith('/raw_sha256'))
        exported = next(v for p, v in hashes.items() if p.endswith('/export_sha256'))
        require(raw_hash == receipts[0]['result/trace_sha256'], 'raw child hash binding')
        require(exported == records['child']['sha256'], 'exported child hash binding')
    else:
        require('child' not in records and 'dispatch_started' not in names, 'unexpected dispatch')
        require(v['/control/projection/nodes/dispatch/execution'] == 'none', 'dispatch must be unexecuted')
    if name in ('normal', 'recovery'):
        successful(v)
    else:
        failure = {'brief-missing': 'BRIEF_MISSING', 'wrong-base': 'BASE_MISMATCH',
                   'missing-coverage': 'COVERAGE_MISMATCH'}[name]
        require(v['/result'] == 'FAIL' and v['/failure'] == failure, 'expected negative result')
        require(v['/control/projection/completed'] is False and v['/control/session/status'] == 'failed', 'negative Runtime state')
        require('result' not in slots(v) and v['/control/projection/nodes/record/execution'] == 'none', 'no success artifact')
        require(any(r.get('error_code') == failure for r in rows), 'missing failure event')
    if name == 'normal':
        order = ['brief_generated', 'todo_updated', 'dispatch_preconditions_passed', 'dispatch_started',
                 'child_process_started', 'dispatch_completed']
        require([names.index(n) for n in order] == sorted(names.index(n) for n in order), 'dispatch ordering')
        require(next(r['status'] for r in rows if r['event'] == 'todo_updated') == 'in_progress', 'observable Todo')
        require([r['step'] for r in rows if r['event'] == 'step_completed'] == list(STEPS), 'stage evidence')
        require(any(p.endswith('/checks') and all(c['returncode'] == 0 for c in x) for p, x in v.items()), 'acceptance checks')
        readback = records['independent_readback']['values']
        require(readback['/cases/normal/changed_files'] == ['csv_records.py'] and
                readback['/cases/normal/parent_unchanged'] and readback['/cases/normal/protected_inputs_unchanged'], 'Git readback')
    if name == 'brief-missing':
        require(v['/launch_count'] == 0 and v['/control/projection/nodes/validate_dispatch/execution'] == 'failed', 'brief gate')
        require(names.index('brief_generated') < names.index('fault_injected') < names.index('step_failed'), 'brief injection order')
    if name == 'wrong-base':
        readback = records['independent_readback']['values']
        base, head = (readback['/cases/wrong-base/' + k] for k in ('base', 'final_head'))
        require(base != head and all(re.fullmatch('[0-9a-f]{40}', s) for s in (base, head)), 'actual base mismatch')
        require(v['/launch_count'] == 0, 'base gate')
    if name == 'missing-coverage':
        require(v['/launch_count'] == 1 and v['/control/projection/nodes/validate_completion/execution'] == 'failed', 'coverage gate')
        require(names.index('dispatch_completed') < names.index('fault_injected') < names.index('step_failed'), 'coverage injection order')
        coverage = next(x for p, x in v.items() if p.endswith('/value/coverage'))
        expected = next(r['binding']['coverage_ids'] for r in rows if r['event'] == 'dispatch_preconditions_passed')
        require({r['id'] for r in coverage} < set(expected), 'missing coverage row')
    if name == 'recovery':
        before = records['before']['values']
        require(before['/session_id'] == v['/session_id'] and before['/failure'] == 'BRIEF_MISSING' and
                before['/launch_count'] == 0 and before['/control/projection/completed'] is False, 'same-session recovery')
        attempts = [(v[p.replace('/step_id', '/attempt')], v[p.replace('/step_id', '/status')])
                    for p, x in v.items() if p.endswith('/step_id') and '/steps/' in p and x == 'validate_dispatch']
        require(attempts == [(1, 'failed'), (2, 'succeeded')], 'retained failed attempt')
        ui = records['ui_retry']['values']['']
        require(any('click(103)' in x.get('arguments', {}).get('code', '') for x in ui) and
                any('Step status: queued' in x.get('excerpt', '') and v['/session_id'] in x['excerpt'] for x in ui), 'authorized UI retry evidence')


def verify(public=PUBLIC, source_repository=None):
    manifest = json.loads((public / 'manifest.json').read_text())
    require(set(manifest['files']) == {n + '.json' for n in CASES}, 'required evidence cases')
    require(manifest['schema_version'] == 1 and re.fullmatch('[0-9a-f]{40}', manifest['archive_commit']), 'manifest version')
    for name, expected in manifest['source_sha256'].items():
        require(sha((HERE / name).read_bytes()) == expected, 'execution source changed: ' + name)
    for filename, expected in manifest['files'].items():
        raw = (public / filename).read_bytes()
        require(sha(raw) == expected, 'public hash mismatch: ' + filename)
        case = json.loads(raw)
        require(bool(case['classification']), 'classification required')
        for rec in case['records'].values():
            path = PurePosixPath(rec['source'])
            require(not path.is_absolute() and '..' not in path.parts and path.parts[0] == 'evidence', 'source path')
            require(re.fullmatch('[0-9a-f]{64}', rec['sha256']) and rec['values'], 'source hash and selections')
            if source_repository:
                blob = subprocess.check_output(['git', '-C', str(source_repository), 'show',
                    manifest['archive_commit'] + ':examples/superpowers-workflow-reliability/' + rec['source']])
                require(sha(blob) == rec['sha256'], 'historical source hash')
                original = [json.loads(line) for line in blob.splitlines()] if rec['format'] == 'jsonl' else json.loads(blob)
                require(all(pointer(original, p) == v for p, v in rec['values'].items()), 'excerpt differs from historical source')
        name = filename[:-5]
        if name in ('normal', 'brief-missing', 'wrong-base', 'missing-coverage', 'recovery'):
            validate_case(name, case)
        elif name == 'reuse-custom':
            install = case['records']['installation']['values']['/installation']
            require(install['first_created'] is True and install['repeat_created'] is False and install['same_revision'] is True, 'installation reuse')
            revisions = set()
            for label in ('csv-a', 'csv-b', 'custom-labels', 'custom-unicode'):
                values = case['records'][label]['values']; successful(values)
                revisions.add(values['/control/session/pinned_revision_id'])
            require(len(revisions) == 1, 'same Workflow revision reused')
        elif name == 'native':
            accept = case['records']['acceptance']['values']['']
            require(accept['returncode'] == accept['independent_acceptance']['returncode'] == 0, 'native acceptance')
    return {'result': 'PASS', 'kind': 'offline historical evidence check; not E2E', 'cases': len(CASES),
            'historical_git_sources_checked': bool(source_repository)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify-sources', type=Path, help='Optional repository containing the pinned historical commit')
    args = parser.parse_args()
    print(json.dumps(verify(source_repository=args.verify_sources)))


if __name__ == '__main__':
    main()
