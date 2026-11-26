#!/usr/bin/env python3
"""Prepare a v2 CI release plan against the last signed public policy."""

import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import tempfile
from urllib.request import build_opener


spec = importlib.util.spec_from_file_location('publish_desktop_update', Path(__file__).with_name('publish-desktop-update.py'))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)

TRUST_FIELDS = ('trustDomain', 'audience', 'channel', 'mode', 'updaterProtocol', 'policyURL')
SOURCE_FIELDS = ('platform', 'arch', 'signingIdentity', 'updaterProtocol')
RAG_CLASS_FIELDS = ('tag', 'pythonAbi', 'baseFingerprint', 'packages')
REPOSITORY = r'[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*'


class PlanError(ValueError):
    """An error message owned by this script rather than any external response."""


def write_bytes(path, data):
    with path.open('xb') as stream:
        stream.write(data)


def write_json(path, value):
    write_bytes(path, (json.dumps(value, indent=2) + '\n').encode('utf-8'))


def read_json(path):
    if path.is_symlink() or not path.is_file():
        raise PlanError('Expected a regular file for publication input')
    with path.open('rb') as stream:
        raw = stream.read(publisher.MAX_POLICY_BYTES + 1)
    if len(raw) > publisher.MAX_POLICY_BYTES:
        raise PlanError('Publication input is too large')
    return json.loads(raw), raw


def read_records(directory):
    records = {}
    if directory is None:
        return records
    if not directory.is_dir():
        raise PlanError('Expected a release record directory')
    for path in sorted(directory.rglob('release.json')):
        record, raw = read_json(path)
        rid = record['release']['releaseId']
        if (not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,127}', rid) or rid in records
                or record['config']['releaseId'] != rid):
            raise PlanError('Invalid or duplicate release identity')
        records[rid] = (record, raw)
    return records


def validate_record(record, expected):
    actual = record['config']
    if any(expected[field] != actual[field] for field in TRUST_FIELDS):
        raise PlanError('Release records do not share the same v2 policy identity')
    if any(actual[field] != record['release'][field] for field in ('releaseId', 'version', *SOURCE_FIELDS)):
        raise PlanError('Release identity differs from its packaged configuration')
    if (not re.fullmatch(r'format-v2-[0-9a-f]{64}', record.get('formatProfile', ''))
            or not all(record.get('componentDescriptor', {}).get(field) for field in RAG_CLASS_FIELDS)):
        raise PlanError('Each v2 release requires a data format profile and RAG component metadata')


def read_reports(directory):
    reports = {}
    if directory is None:
        return reports
    if not directory.is_dir():
        raise PlanError('Expected a format evidence directory')
    for path in sorted(directory.rglob('*.json')):
        value, _ = read_json(path)
        pair = (value['sourceReleaseId'], value['targetReleaseId'])
        if pair in reports:
            raise PlanError('Duplicate compatibility evidence for a release pair')
        reports[pair] = (path.resolve(), value)
    return reports


def verified_policy(validator, envelope, config, *, source_only=False):
    args = [str(validator), 'verify', '--historical', '--envelope', str(envelope), '--config', str(config)]
    if source_only:
        args.append('--select-source')
    result = subprocess.run(args, capture_output=True, text=True, timeout=30)
    if result.returncode:
        raise PlanError('Published policy or upgrade source verification failed')
    return json.loads(result.stdout)


def verified_edge(pair, source, target, report, baseline):
    if report is None:
        raise PlanError('Native format evidence is required for each release pair')
    path, value = report
    format_changed = source['formatProfile'] != target['formatProfile']
    if value.get('passed') is False:
        reason = value.get('reason')
        if baseline:
            raise PlanError('The latest published source must pass native compatibility verification')
        if (set(value) != {'sourceReleaseId', 'targetReleaseId', 'passed', 'reason'}
                or reason not in ('component_removed', 'format_mismatch', 'index_incompatible')
                or (reason == 'format_mismatch') != format_changed):
            raise PlanError('Invalid native incompatibility result for the selected release artifacts')
        return None
    classes = [{field: source['componentDescriptor'][field] for field in RAG_CLASS_FIELDS}]
    target_sha = target['release']['components']['sha256']
    if (format_changed or value.get('passed') is not True or value.get('readOnly') is not True
            or value.get('dataProfile') != target['formatProfile'] or value.get('sourceClasses') != classes
            or value.get('targetComponentsSHA256') != target_sha
            or not re.fullmatch(r'[0-9a-f]{64}', value.get('fixtureSHA256', ''))):
        raise PlanError('Native evidence does not match the selected release artifacts')
    return {**pair, 'priority': 0, 'formatEvidence': str(path),
            'componentTransitions': {'rag': {'sourceClasses': classes, 'targetComponentsSHA256': target_sha}}}


def prepare(current, history, evidence, validator, output, *, repo=None, verify_source_only=False,
            unsigned_test=False, validity_days=90, fetch=None):
    records = read_records(current)
    if not records:
        raise PlanError('Current build must provide update release records')
    config = next(iter(records.values()))[0]['config']
    mode = 'unsigned-test' if unsigned_test else 'official'
    if config['mode'] != mode or config['updaterProtocol'] != 2:
        raise PlanError('Automatic publication requires v2 records in the selected release mode')
    policy_url = config['policyURL']
    source = re.match(rf'^https://modelscope\.cn/datasets/({REPOSITORY})/resolve/master/', policy_url)
    if not source or (repo is not None and source[1] != repo):
        raise PlanError('Policy URL does not match the ModelScope publication repository')
    base = source[0]
    publisher.destination(policy_url, base)
    if 'https://modelscope.cn' not in {origin.rstrip('/') for origin in config['allowedOrigins']}:
        raise PlanError('Policy URL origin is not pinned by the build')

    builds = set()
    for record, _ in records.values():
        build = record['config']
        validate_record(record, config)
        platform = (build['platform'], build['arch'])
        if platform in builds or build['version'] != config['version']:
            raise PlanError('Expected one artifact per platform/architecture for the new version')
        builds.add(platform)
        for artifact in ('package', 'feed', 'components'):
            publisher.destination(record['release'][artifact]['url'], base)

    if fetch is None:
        opener = build_opener(publisher.PinnedRedirects(config['allowedOrigins']))
        fetch = lambda url: opener.open(url, timeout=60)
    previous = publisher.read_remote_policy(fetch, policy_url)
    historical_records = read_records(history)
    reports = read_reports(evidence)
    selected = dict(records)
    pairs, edges = [], []
    sequence = 1
    if previous is None:
        if historical_records or reports:
            raise PlanError('No published policy exists for the requested upgrade source')
    else:
        with tempfile.TemporaryDirectory(prefix='lm-published-policy-') as tmp:
            old_path = Path(tmp) / 'policy.json'
            write_bytes(old_path, previous)
            for target_id, (target, _) in records.items():
                config_file = Path(tmp) / 'config.json'
                config_file.write_text(json.dumps(target['config']), encoding='utf-8')
                publication = verified_policy(validator, old_path, config_file)
                sequence = publication['policySequence'] + 1
                if any(release['releaseId'] == target_id for release in publication['releases']):
                    raise PlanError('The release is already in the signed public policy')
                # A shared official pointer can already contain the other OS.
                # Revoked releases and prior signing identities still count as
                # platform history: neither allows a new first-release bypass.
                if mode == 'official' and not any(r['platform'] == target['config']['platform'] for r in publication['releases']):
                    continue
                if not historical_records and not verify_source_only:
                    raise PlanError('An existing platform requires release records from native source verification')
                # The verifier also checks that the target is newer than the latest matching release.
                latest = verified_policy(validator, old_path, config_file, source_only=True)
                revoked = set(publication['revoked'] or [])
                for source in publication['releases']:
                    source_id = source['releaseId']
                    if source_id in revoked or any(source[k] != latest[k] for k in SOURCE_FIELDS):
                        continue
                    baseline = source_id == latest['releaseId']
                    pair = {'sourceReleaseId': source_id, 'targetReleaseId': target_id, 'dataProfile': target['formatProfile']}
                    pairs.append({**pair, 'sourceRelease': source, 'baseline': baseline})
                    # With no build artifact, the native job reconstructs even the latest
                    # source from the package and catalog pinned by this verified policy.
                    if verify_source_only and (not baseline or not historical_records):
                        continue
                    loaded = historical_records.get(source_id)
                    if loaded is None or loaded[0]['release'] != source:
                        raise PlanError('Source release record does not match published policy')
                    prior = loaded[0]
                    validate_record(prior, config)
                    if baseline and prior['formatProfile'] != target['formatProfile']:
                        raise PlanError('The baseline upgrade requires matching v2 data formats')
                    # Clients that need a bridge must also trust the new signed policy.
                    selected[source_id] = loaded
                    if verify_source_only:
                        continue
                    edge = verified_edge(pair, prior, target, reports.pop((source_id, target_id), None), baseline)
                    if edge is not None:
                        edges.append(edge)
    if reports:
        raise PlanError('Unexpected compatibility evidence')

    output.mkdir(parents=True, exist_ok=True)
    if output.is_symlink() or any(output.iterdir()):
        raise PlanError('Publication plan directory must be empty')
    write_json(output / 'pairs.json', pairs)
    if previous is not None:
        write_bytes(output / 'previous-envelope.json', previous)
    expected = 'absent' if previous is None else hashlib.sha256(previous).hexdigest()
    write_bytes(output / 'expected-policy-sha256', (expected + '\n').encode('ascii'))
    for rid, (_, raw) in selected.items():
        folder = output / 'records' / rid
        folder.mkdir(parents=True)
        write_bytes(folder / 'release.json', raw)
    if not verify_source_only:
        now = datetime.now(timezone.utc).replace(microsecond=0)
        write_json(output / 'plan.json', {'policySequence': sequence, 'issuedAt': now.isoformat(),
            'expiresAt': (now + timedelta(days=validity_days)).isoformat(), 'releaseIds': list(selected),
            'edges': edges, 'revoked': []})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--current', type=Path, required=True)
    parser.add_argument('--history', type=Path)
    parser.add_argument('--evidence', type=Path)
    parser.add_argument('--validator', type=Path, required=True)
    parser.add_argument('--output-dir', type=Path, required=True)
    parser.add_argument('--repo', help='Expected ModelScope publication repository (owner/name)')
    parser.add_argument('--verify-source-only', action='store_true')
    parser.add_argument('--unsigned-test', action='store_true')
    parser.add_argument('--validity-days', type=int, default=90)
    args = parser.parse_args()
    if not 1 <= args.validity_days <= 365:
        parser.error('--validity-days must be between 1 and 365')
    try:
        prepare(args.current, args.history, args.evidence, args.validator.resolve(), args.output_dir, repo=args.repo,
                verify_source_only=args.verify_source_only, unsigned_test=args.unsigned_test, validity_days=args.validity_days)
    except PlanError as exc:
        raise SystemExit(f'Desktop update plan rejected: {exc}') from None
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        # Network errors can contain temporary signed URLs.
        raise SystemExit('Desktop update plan rejected: could not load the publication inputs') from None


if __name__ == '__main__':
    main()
