"""Exercise recovery from signed public history when the build artifact is unavailable."""

import hashlib
import io
import json
import os
from pathlib import Path
import re
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from unittest.mock import patch
from urllib.error import HTTPError
from urllib.request import OpenerDirector


SCRIPTS = Path(__file__).resolve().parent
ROOT = SCRIPTS.parents[1]
FIXTURE = runpy.run_path(str(SCRIPTS / 'test_update_ci_plan.py'))
PLAN = FIXTURE['ci_plan']
PAIRS = runpy.run_path(str(SCRIPTS / 'verify-update-ci-pairs.py'))
PROBE = runpy.run_path(str(SCRIPTS / 'verify-update-format.py'))
write_json = FIXTURE['write_json']
# Synthetic Ed25519 test vector with a seed of 32 identical bytes.
PUBLIC = 'iojj3XQJ8ZX9UtstPLpdcspnCb8dlBIb83SIAbQPb1w='
PRIVATE = ('-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB\n'
           '-----END PRIVATE KEY-----\n')


def validity(sequence):
    now = datetime.now(timezone.utc).replace(microsecond=0)
    return {'policySequence': sequence, 'issuedAt': now.isoformat(), 'expiresAt': (now + timedelta(days=1)).isoformat()}


def build_validator(directory, *, unsigned=False):
    filename = 'unsigned-publisher' if unsigned else 'update-publisher'
    dest = directory / (filename + '.exe' if os.name == 'nt' else filename)
    supplied = 'LAZYMIND_TEST_UNSIGNED_UPDATE_PUBLISHER' if unsigned else 'LAZYMIND_TEST_UPDATE_PUBLISHER'
    if binary := os.environ.get(supplied):
        shutil.copy2(binary, dest)
    else:
        tags = ['-tags', 'lazymind_unsigned_test'] if unsigned else []
        subprocess.run(['go', 'build', *tags, '-o', str(dest), './cmd/update-publisher'],
                       cwd=ROOT / 'local/local-runtime-manager', check=True)
    return dest


class PublicBaselineTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        temporary = tempfile.TemporaryDirectory()
        cls.addClassCleanup(temporary.cleanup)
        cls.validator = build_validator(Path(temporary.name))

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.a, self.b = (FIXTURE['release_record'](v) for v in ('0.5.224', '0.5.225'))
        self.remote = {}
        for record in (self.a, self.b):
            record['config']['publicKeys'] = {'fixture': PUBLIC}
            for kind in ('package', 'feed', 'components'):
                descriptor = record['release'][kind]
                raw = (record['release']['releaseId'] + '/' + kind).encode()
                descriptor.update(size=len(raw), sha256=hashlib.sha256(raw).hexdigest())
                self.remote[descriptor['url']] = raw
        self.a_id, self.b_id = (record['release']['releaseId'] for record in (self.a, self.b))
        write_json(self.root / 'prior-build' / self.a_id / 'release.json', self.a)
        write_json(self.root / 'current' / self.b_id / 'release.json', self.b)
        write_json(self.root / 'initial-plan.json', {**validity(1), 'releaseIds': [self.a_id], 'edges': [], 'revoked': []})
        (self.root / 'key.pem').write_text(PRIVATE)
        subprocess.run([str(self.validator), 'sign', '--records', 'prior-build', '--plan', 'initial-plan.json',
                        '--key-id', 'fixture', '--key-file', 'key.pem', '--output', 'policy.json', '--config-output', 'public.json'],
                       cwd=self.root, check=True)
        self.policy_url = self.b['config']['policyURL']
        self.remote[self.policy_url] = (self.root / 'policy.json').read_bytes()
        shutil.rmtree(self.root / 'prior-build')
        self.report = {'sourceReleaseId': self.a_id, 'targetReleaseId': self.b_id, 'dataProfile': self.b['formatProfile'],
                       'passed': True, 'readOnly': True, 'sourceClasses': [self.a['componentDescriptor']],
                       'targetComponentsSHA256': self.b['release']['components']['sha256'], 'fixtureSHA256': 'b' * 64}

    def fetch(self, url, **_):
        return io.BytesIO(self.remote[url])

    def discover(self):
        PLAN.prepare(self.root / 'current', None, None, self.validator, self.root / 'discovery',
                     verify_source_only=True, fetch=self.fetch)
        return json.loads((self.root / 'discovery/pairs.json').read_text())

    def test_missing_run_artifact_selects_the_signed_latest_and_pins_its_envelope(self):
        pairs = self.discover()
        self.assertEqual(len(pairs), 1)
        self.assertIs(pairs[0]['baseline'], True)
        self.assertEqual(pairs[0]['sourceRelease'], self.a['release'])
        self.assertEqual((self.root / 'discovery/expected-policy-sha256').read_text().strip(),
                         hashlib.sha256(self.remote[self.policy_url]).hexdigest())

    def test_confirmed_404_allows_a_new_channel_but_never_a_network_error(self):
        def missing(url):
            self.assertEqual(url, self.policy_url)
            raise HTTPError(url, 404, 'not found', {}, None)
        PLAN.prepare(self.root / 'current', None, None, self.validator, self.root / 'first', fetch=missing)
        plan = json.loads((self.root / 'first/plan.json').read_text())
        self.assertEqual((plan['policySequence'], plan['releaseIds'], plan['edges']), (1, [self.b_id], []))
        self.assertEqual((self.root / 'first/expected-policy-sha256').read_text().strip(), 'absent')
        def unavailable(url):
            raise HTTPError(url, 503, 'unavailable', {}, None)
        with self.assertRaises((HTTPError, PLAN.PlanError)):
            PLAN.prepare(self.root / 'current', None, None, self.validator, self.root / 'unavailable', fetch=unavailable)
        self.assertFalse((self.root / 'unavailable/plan.json').exists())

    def test_native_verification_cli_distinguishes_absent_policy_from_unavailable_policy(self):
        for status in (404, 503):
            with self.subTest(status=status):
                output = self.root / f'verify-source-{status}'
                argv = ['prepare-update-ci-plan.py', '--verify-source-only', '--current', str(self.root / 'current'),
                        '--validator', str(self.validator), '--output-dir', str(output)]
                error = HTTPError(self.policy_url, status, 'fixture HTTP response', {}, None)
                with patch.object(sys, 'argv', argv), patch.object(OpenerDirector, 'open', side_effect=error) as fetch:
                    if status == 404:
                        PLAN.main()
                        self.assertEqual(json.loads((output / 'pairs.json').read_text()), [])
                        self.assertEqual((output / 'expected-policy-sha256').read_text().strip(), 'absent')
                    else:
                        with self.assertRaises(SystemExit) as stopped:
                            PLAN.main()
                        self.assertNotEqual(stopped.exception.code, 0)
                        self.assertFalse((output / 'pairs.json').exists())
                        self.assertFalse((output / 'expected-policy-sha256').exists())
                    fetch.assert_called_once_with(self.policy_url, timeout=60)

    def test_reconstruction_downloads_signed_files_and_requires_native_baseline_success(self):
        # Give the native job the plan it would receive after successful signature verification.
        discovery = self.root / 'discovery'
        pair = {'sourceReleaseId': self.a_id, 'targetReleaseId': self.b_id, 'sourceRelease': self.a['release'],
                'dataProfile': self.b['formatProfile'], 'baseline': True}
        write_json(discovery / 'pairs.json', [pair])
        (discovery / 'expected-policy-sha256').write_text(hashlib.sha256(self.remote[self.policy_url]).hexdigest() + '\n')
        evidence = self.root / 'verification'
        def native(command, *, check):
            self.assertTrue(check)
            args = lambda flag: Path(command[command.index(flag) + 1])
            self.assertIn('--published-source', command, 'the baseline GitHub artifact expired')
            published = json.loads(args('--published-source').read_text())
            self.assertEqual(published, self.a['release'])
            with tempfile.TemporaryDirectory() as directory, patch.object(OpenerDirector, 'open', side_effect=self.fetch):
                package, catalog = PROBE['download_source'](published, self.b, Path(directory))
                self.assertEqual(package.read_bytes(), self.remote[published['package']['url']])
                self.assertEqual(catalog.read_bytes(), self.remote[published['components']['url']])
            # Native execution itself is exercised by the separate host-specific index probe.
            write_json(args('--source-record-output'), self.a)
            write_json(args('--output'), self.report)
        with patch.object(subprocess, 'run', side_effect=native):
            PAIRS['verify_pairs'](discovery, self.root / 'current', self.root / 'expired', evidence, 'macos', False)
        def prepare(output):
            PLAN.prepare(self.root / 'current', evidence / 'records', evidence / 'evidence', self.validator,
                         self.root / output, fetch=self.fetch)
        prepare('accepted')
        edges = json.loads((self.root / 'accepted/plan.json').read_text())['edges']
        self.assertEqual([(edge['sourceReleaseId'], edge['targetReleaseId']) for edge in edges], [(self.a_id, self.b_id)])
        report = next((evidence / 'evidence').rglob('*.json'))
        write_json(report, {'sourceReleaseId': self.a_id, 'targetReleaseId': self.b_id, 'passed': False, 'reason': 'index_incompatible'})
        with self.assertRaises(PLAN.PlanError):
            prepare('failed')
        self.assertFalse((self.root / 'failed/plan.json').exists())

    def test_public_recovery_requires_a_verified_signature(self):
        envelope = json.loads(self.remote[self.policy_url])
        envelope['signature'] = 'AA=='  # Other well-formed record fields must not bypass the signature check.
        self.remote[self.policy_url] = json.dumps(envelope).encode()
        with self.assertRaisesRegex(PLAN.PlanError, 'verification failed'):
            self.discover()
        self.assertFalse((self.root / 'discovery/pairs.json').exists())

    def test_failed_unsigned_source_record_cannot_replace_the_configured_trust_key(self):
        validator = build_validator(self.root, unsigned=True)
        source, current = 'failed-source', 'trusted-target'
        prefix = 'updates/unsigned-test/demo/in-place/round-41/'
        url = FIXTURE['BASE'] + prefix + 'policy.json'
        for record, directory in [(self.a, source), (self.b, current)]:
            record['config'].update(mode='unsigned-test', audience='personal', channel='personal-test', policyURL=url,
                                    signingIdentity='adhoc', trustDomain='inplace-41-' + 'a' * 64)
            record['release']['signingIdentity'] = 'adhoc'
            for kind in ('package', 'feed', 'components'):
                artifact = record['release'][kind]
                artifact['url'] = artifact['url'].replace('/updates/releases/', '/' + prefix + 'releases/')
            if directory == current:
                record['config']['publicKeys'] = {'fixture': 'gTl3Dqh9F19Wo1Rmw0x+zMuNipG07jeiXfYPW4/Js5Q='}
            write_json(self.root / directory / record['release']['releaseId'] / 'release.json', record)
        subprocess.run([str(validator), 'sign', '--records', source, '--plan', 'initial-plan.json',
                        '--key-id', 'fixture', '--key-file', 'key.pem', '--output', 'foreign-envelope.json',
                        '--config-output', 'foreign-config.json'], cwd=self.root, check=True)
        # This policy is genuinely signed and verifies with the failed build's key.
        subprocess.run([str(validator), 'verify', '--historical', '--config', 'foreign-config.json',
                        '--envelope', 'foreign-envelope.json'], cwd=self.root, check=True, stdout=subprocess.DEVNULL)
        self.remote[url] = (self.root / 'foreign-envelope.json').read_bytes()
        with self.assertRaisesRegex(PLAN.PlanError, 'verification failed'):
            PLAN.prepare(self.root / current, self.root / source, None, validator, self.root / 'untrusted',
                         verify_source_only=True, unsigned_test=True, fetch=self.fetch)
        self.assertFalse((self.root / 'untrusted/pairs.json').exists())

    def test_recovered_public_package_must_match_the_signed_hash(self):
        package = self.a['release']['package']
        self.remote[package['url']] = b'x' * package['size']
        with tempfile.TemporaryDirectory() as directory, patch.object(OpenerDirector, 'open', side_effect=self.fetch):
            with self.assertRaisesRegex(ValueError, 'SHA-256'):
                PROBE['download_source'](self.a['release'], self.b, Path(directory))

    @unittest.skipIf(os.name == 'nt', 'manual publication runs on Ubuntu')
    def test_manual_renewal_binds_downloaded_report_before_signing(self):
        workflow = (ROOT / '.github/workflows/desktop-update-publish.yml').read_text()
        steps = re.split(r'^      - ', workflow.split('\n  publish:\n', 1)[1], flags=re.M)[1:]
        start = next(i for i, step in enumerate(steps) if step.startswith('name: Download successful build artifacts'))
        end = next(i for i, step in enumerate(steps) if step.startswith('name: Assemble and sign explicit update policy'))
        # The manual dispatch has no current-run evidence. Preserve the order and
        # evaluate its step gates before invoking each shell with a fresh process.
        scripts = []
        for step in steps[start:end + 1]:
            condition = re.search(r'^        if: (.+)$', step, re.M)
            if condition:
                gate = condition[1].strip("'")
                self.assertIn(gate, ('inputs.use_current_run', '!inputs.use_current_run'), 'unknown manual step gate')
                if gate == 'inputs.use_current_run':
                    continue
            if step.startswith('name: Build the offline update publisher'):
                continue
            self.assertIn('        run: |\n', step, 'unexpected manual publication step')
            header, shell = step.split('        run: |\n', 1)
            bindings = re.findall(r'^          (\w+): \$\{\{\s*([\w.]+)\s*\}\}$', header, re.M)
            scripts.append((bindings, '\n'.join(line.removeprefix('          ') for line in shell.splitlines())))

        source_run, target_run = '45142962628', '45142962629'
        archives = self.root / 'artifacts'
        for run, record in [(source_run, self.a), (target_run, self.b)]:
            rid = record['release']['releaseId']
            write_json(archives / run / f'desktop-update-macos-{run}' / rid / 'release.json', record)
        proof = archives / target_run / f'desktop-format-evidence-macos-{target_run}'
        # The archived native verification also contains a baseline record. It must
        # not become a duplicate of the input build record when signing.
        write_json(proof / 'records/macos' / self.a_id / 'release.json', self.a)
        report = proof / 'evidence/macos' / (self.a_id + '.json')
        edge = {'sourceReleaseId': self.a_id, 'targetReleaseId': self.b_id, 'dataProfile': self.report['dataProfile'],
                'priority': 0, 'componentTransitions': {'rag': {'sourceClasses': self.report['sourceClasses'],
                'targetComponentsSHA256': self.report['targetComponentsSHA256']}}}
        gh = self.root / 'gh'
        gh.write_text('#!' + sys.executable + '\n' + '''import argparse, os, shutil, sys
from pathlib import Path
action = sys.argv[1:3]
assert action in (['run', 'view'], ['run', 'download'])
run = sys.argv[3]
source = Path(os.environ['ARCHIVES']) / run
assert source.is_dir()
if action[-1] == 'view':
    print('success')
else:
    parser = argparse.ArgumentParser()
    parser.add_argument('--dir', '-D', required=True, type=Path)
    matching = parser.add_mutually_exclusive_group(required=True)
    matching.add_argument('--name', '-n')
    matching.add_argument('--pattern', '-p', action='append')
    args = parser.parse_args(sys.argv[4:])
    files = [source / args.name] if args.name else [f for f in source.iterdir() if any(f.match(p) for p in args.pattern)]
    if not files or any(not f.is_dir() for f in files):
        sys.exit(1)
    for folder in files:
        shutil.copytree(folder, args.dir if args.name else args.dir / folder.name, dirs_exist_ok=True)
''')
        gh.chmod(0o700)
        # Create the previously published A→B at sequence 2 through the real signer.
        records = self.root / 'previous-releases'
        for record in (self.a, self.b):
            write_json(records / record['release']['releaseId'] / 'release.json', record)
        prior_report = self.root / 'prior-evidence.json'
        write_json(prior_report, self.report)
        prior_plan = {**validity(2), 'releaseIds': [self.a_id, self.b_id], 'revoked': [],
                      'edges': [{**edge, 'formatEvidence': str(prior_report)}]}
        write_json(self.root / 'previous.json', prior_plan)
        subprocess.run([str(self.validator), 'sign', '--records', str(records), '--plan', 'previous.json',
                        '--key-id', 'fixture', '--key-file', 'key.pem', '--output', 'prior-envelope.json',
                        '--config-output', 'prior-config.json'], cwd=self.root, check=True)
        for case in ('matching', 'missing', 'wrong platform archive', 'source run archive', 'wrong target', 'wrong catalog'):
            with self.subTest(case=case):
                work = self.root / case
                work.mkdir()
                (work / 'desktop').symlink_to(ROOT / 'desktop', target_is_directory=True)
                shutil.copy2(self.validator, work / 'update-publisher')
                plan = {**prior_plan, **validity(3), 'edges': [dict(edge)]}
                report.unlink(missing_ok=True)
                extra_archive = None
                if case == 'missing':
                    # A dispatcher's arbitrary local path cannot replace CI evidence.
                    plan['edges'][0]['formatEvidence'] = str(prior_report)
                elif case in ('wrong platform archive', 'source run archive'):
                    run, platform = (target_run, 'windows') if case == 'wrong platform archive' else (source_run, 'macos')
                    extra_archive = archives / run / f'desktop-format-evidence-{platform}-{run}'
                    write_json(extra_archive / 'evidence/macos' / report.name, self.report)
                else:
                    value = dict(self.report)
                    if case == 'wrong target':
                        value['targetReleaseId'] = self.a_id
                    if case == 'wrong catalog':
                        value['targetComponentsSHA256'] = 'f' * 64
                    write_json(report, value)
                context = {'inputs.build_run_ids': source_run + ',' + target_run, 'inputs.plan': json.dumps(plan),
                           'secrets.LAZYMIND_UPDATE_POLICY_PRIVATE_KEY': PRIVATE, 'vars.LAZYMIND_UPDATE_POLICY_KEY_ID': 'fixture',
                           'inputs.use_current_run': 'false', 'github.repository': 'fixture/repository', 'github.token': 'fixture'}
                for bindings, shell in scripts:
                    env = dict(PATH=str(self.root) + os.pathsep + os.environ['PATH'], ARCHIVES=str(archives), RUNNER_TEMP=str(work))
                    env.update({name: context[reference] for name, reference in bindings})
                    result = subprocess.run(['bash', '-euc', shell], cwd=work, env=env, capture_output=True, text=True)
                    if result.returncode:
                        break
                if extra_archive:
                    shutil.rmtree(extra_archive)
                envelope = work / 'update-policy.json'
                if case == 'matching':
                    self.assertEqual(result.returncode, 0, result.stderr)
                    def verify(filename, config):
                        return json.loads(subprocess.check_output([str(self.validator), 'verify', '--envelope',
                                          str(filename), '--config', str(config)]))
                    renewed = verify(envelope, work / 'update-config.json')
                    prior = verify(self.root / 'prior-envelope.json', self.root / 'prior-config.json')
                    self.assertEqual(renewed['edges'], prior['edges'])
                    self.assertEqual(renewed['policySequence'], 3)
                else:
                    self.assertNotEqual(result.returncode, 0, 'unexpected manual signature: ' + case)
                    self.assertFalse(envelope.exists())


if __name__ == '__main__':
    unittest.main()
