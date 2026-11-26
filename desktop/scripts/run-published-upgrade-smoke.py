"""Select each platform's verified predecessor and run one installed upgrade."""

import argparse
import json
import os
from pathlib import Path
import re
import runpy
import subprocess
import tempfile
from types import SimpleNamespace
from urllib.request import build_opener

HERE = Path(__file__).resolve().parent
SMOKE = runpy.run_path(str(HERE / 'smoke-published-upgrade.py'))
PROBE = runpy.run_path(str(HERE / 'verify-update-format.py'))
PLAN = runpy.run_path(str(HERE / 'prepare-update-ci-plan.py'))
require, load = SMOKE['require'], SMOKE['read_json']


def baseline(verification, platform):
    target = load(verification / (platform + '-target.json'))
    pairs = load(verification / (platform + '-baseline.json'))
    require(isinstance(pairs, list) and len(pairs) <= 1, 'Bad native baseline selection')
    require(target['config']['platform'] == {'windows': 'windows', 'macos': 'darwin'}[platform], 'Foreign native target')
    if not pairs:
        return target, None
    pair = pairs[0]
    require(pair['baseline'] is True and pair['targetReleaseId'] == target['release']['releaseId'], 'Wrong native target')
    source_id = pair['sourceReleaseId']
    require(source_id == Path(source_id).name and source_id not in ('', '.', '..'), 'Invalid source ID')
    source = load(verification / 'records' / platform / source_id / 'release.json')
    report = load(verification / 'evidence' / platform / (source_id + '.json'))
    require(source['release'] == pair['sourceRelease'] and source['release']['releaseId'] == source_id and
            report['passed'] is True and report['sourceReleaseId'] == source_id and report['targetReleaseId'] == pair['targetReleaseId'],
            'Smoke source differs from native compatibility evidence')
    return target, source


def matrix(verification, platforms):
    result = []
    for platform in platforms:
        target, source = baseline(verification, platform)
        if source is None:
            print(f"First {platform} release {target['release']['releaseId']}: no installed predecessor to upgrade.")
        else:
            result.append({'platform': platform})
    return result


def verify_test_round(config_file, template, base):
    dataset = re.fullmatch(rf"https://modelscope\.cn/datasets/({PLAN['REPOSITORY']})/resolve/master/.+", base)
    require(dataset is not None, 'Expected the configured public ModelScope test base')
    env = dict(os.environ, TEST_BASE_URL=base, LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON=json.dumps(template), UPDATE_REPO=dataset[1])
    subprocess.run(['node', str(HERE / 'resolve-unsigned-test-round.mjs'), '--component-prefix', str(config_file)],
                   env=env, check=True, capture_output=True)


def run_native(args):
    require(os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted' and os.environ.get('GITHUB_ACTIONS') == 'true',
            'Native replacement requires a disposable GitHub-hosted runner')
    target, source = baseline(args.verification, args.platform)
    require(source is not None, 'No verified A on this platform')
    actual = target['config']
    template = json.loads(os.environ['SMOKE_PUBLIC_CONFIG'])
    require(template['publicKeys'] == actual['publicKeys'] and template['allowedOrigins'] == actual['allowedOrigins'] and
            template['mode'] == actual['mode'] and template['signingIdentity'] == actual['signingIdentity'],
            'Build does not match the configured signer and public keys')
    with tempfile.TemporaryDirectory() as temp:
        work = Path(temp).resolve()
        config = work / 'config.json'
        config.write_text(json.dumps(actual), encoding='utf-8')
        if actual['mode'] == 'unsigned-test':
            # Derive and check the trust domain and physical policy URL with the trusted template.
            verify_test_round(config, template, os.environ['TEST_BASE_URL'])
        else:
            require(all(actual[k] == template[k] for k in ('trustDomain', 'audience', 'channel', 'policyURL')), 'Unexpected official channel')
        publisher = PLAN['publisher']
        origins = {origin.rstrip('/') for origin in actual['allowedOrigins']}
        opener = build_opener(publisher.PinnedRedirects(origins))
        signed = publisher.read_remote_policy(lambda url: opener.open(url, timeout=60), actual['policyURL'])
        require(signed is not None, 'Published policy disappeared')
        envelope = work / 'public-policy.json'
        envelope.write_bytes(signed)
        verified = subprocess.run([str(args.validator.resolve()), 'verify', '--config', str(config), '--envelope', str(envelope)],
                                  capture_output=True, text=True, check=True, timeout=30)
        policy = json.loads(verified.stdout)
        a, b = (r['release']['releaseId'] for r in (source, target))
        releases = {r['releaseId']: r for r in policy['releases']}
        require(releases.get(a) == source['release'] and releases.get(b) == target['release'] and
                not {a, b}.intersection(policy['revoked'] or []) and
                any(e['sourceReleaseId'] == a and e['targetReleaseId'] == b for e in policy['edges']),
                'The live signed policy does not authorize this native A -> B')

        source_dir, target_dir = work / 'source', work / 'target'
        source_dir.mkdir()
        target_dir.mkdir()
        PROBE['download_source'](source['release'], target, source_dir)
        (source_dir / 'release.json').write_text(json.dumps(source), encoding='utf-8')
        (target_dir / 'release.json').write_text(json.dumps(target), encoding='utf-8')
        # Older releases expose only RAG. Exercise the optional capabilities A
        # actually provides, once on the native runner for this publication.
        selected = ','.join(sorted({'rag', 'cluster'}.intersection(load(source_dir / 'components.json')['components']))) or 'none'
        app = (Path.home() / 'Applications/LazyMind.app') if args.platform == 'macos' else (Path(os.environ['LOCALAPPDATA']) / 'Programs/LazyMind/LazyMind.exe')
        smoke = SimpleNamespace(source=source_dir, target=target_dir, app=app, report=args.report.resolve(), runtime_root=None,
                                policy_url=actual['policyURL'], signer=actual['signingIdentity'], selected=selected, provision_only=True)
        SMOKE['run'](smoke)
        smoke.provision_only = False
        SMOKE['run'](smoke)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest='command', required=True)
    plan = actions.add_parser('matrix')
    plan.add_argument('verification', type=Path)
    plan.add_argument('platforms', nargs='+', choices=['macos', 'windows'])
    native = actions.add_parser('run')
    for key in ('verification', 'validator', 'report'):
        native.add_argument('--' + key, type=Path, required=True)
    native.add_argument('--platform', choices=['macos', 'windows'], required=True)
    args = parser.parse_args()
    if args.command == 'matrix':
        lanes = matrix(args.verification, args.platforms)
        with open(os.environ['GITHUB_OUTPUT'], 'a', encoding='utf-8') as out:
            print('lanes=' + json.dumps(lanes, separators=(',', ':')), file=out)
    else:
        run_native(args)
