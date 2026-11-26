"""Exercise single-platform A/B publication against a local ModelScope transport.

The production planner, Ed25519 publisher, client decision and public readback
are real. Native index reports are fixture inputs, not an installer smoke test.
"""

import base64
import hashlib
import io
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import tempfile
import unittest
from urllib.error import HTTPError

HERE = Path(__file__).resolve().parent
CONTRACT = runpy.run_path(str(HERE / 'test_update_public_baseline.py'))
MAKE = CONTRACT['FIXTURE']['release_record']
BASE = CONTRACT['FIXTURE']['BASE']
PROFILE = CONTRACT['FIXTURE']['PROFILE']
PLAN = CONTRACT['PLAN']
WRITE = CONTRACT['write_json']
RUNTIME = HERE.parents[1] / 'local/local-runtime-manager'


class PerPlatformPublication(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        temp = tempfile.TemporaryDirectory()
        cls.addClassCleanup(temp.cleanup)
        cls.validator = CONTRACT['build_validator'](Path(temp.name))
        cls.client = Path(temp.name) / ('client.exe' if os.name == 'nt' else 'client')
        subprocess.run(['go', 'test', '-c', '-o', str(cls.client), './cmd/update-helper'], cwd=RUNTIME, check=True)

    def setUp(self):
        work = tempfile.TemporaryDirectory()
        self.addCleanup(work.cleanup)
        self.root = Path(work.name)
        self.remote, self.events = {}, []
        (self.root / 'fixture.pem').write_text(CONTRACT['PRIVATE'])

    def fetch(self, url):
        self.events.append(('read', url))
        if url not in self.remote:
            raise HTTPError(url, 404, 'missing fixture', {}, None)
        return io.BytesIO(self.remote[url])

    def upload_file(self, *, repo_id, repo_type, revision, path_in_repo, path_or_fileobj, **_):
        self.assertEqual((repo_id, repo_type, revision), ('fixture/updates', 'dataset', 'master'))
        url = BASE + path_in_repo
        self.events.append(('upload', url))
        self.remote[url] = Path(path_or_fileobj).read_bytes()

    def build(self, version, platform, policy_url):
        record = MAKE(version, platform)
        record['config'].update(policyURL=policy_url, publicKeys={'fixture': CONTRACT['PUBLIC']})
        release = record['release']
        folder = self.root / 'builds' / release['releaseId']
        folder.mkdir(parents=True)
        for kind in ('package', 'components', 'feed'):
            artifact = release[kind]
            filename = artifact['url'].split('/')[-1]
            raw = (release['releaseId'] + '/' + kind).encode()
            (folder / filename).write_bytes(raw)
            artifact.update(size=len(raw), sha256=hashlib.sha256(raw).hexdigest())
        WRITE(folder / 'release.json', record)
        return record

    def sign(self, plan, out):
        args = [str(self.validator), 'sign', '--records', str(plan / 'records'), '--plan', str(plan / 'plan.json'),
                '--key-id', 'fixture', '--key-file', str(self.root / 'fixture.pem'), '--output', str(out / 'policy.json'),
                '--config-output', str(out / 'config.json')]
        if (plan / 'previous-envelope.json').is_file():
            args.extend(['--previous-envelope', str(plan / 'previous-envelope.json')])
        subprocess.run(args, check=True, capture_output=True)

    def prepare(self, name, target, source=None):
        work = self.root / name
        current = work / 'current'
        target_id = target['release']['releaseId']
        shutil.copytree(self.root / 'builds' / target_id, current / target_id)
        history = evidence = None
        if target['config']['policyURL'] in self.remote:
            # An existing signed policy can contain only the other platform.
            # The native discovery boundary must distinguish that from this platform's upgrade.
            PLAN.prepare(current, None, None, self.validator, work / 'discover', verify_source_only=True, fetch=self.fetch)
            pairs = json.loads((work / 'discover/pairs.json').read_bytes())
            self.assertEqual([(p['sourceReleaseId'], p['targetReleaseId'], p['baseline']) for p in pairs],
                             [(source['release']['releaseId'], target_id, True)] if source else [])
        if source:
            src = source['release']
            history, evidence = work / 'native-records', work / 'native-reports'
            WRITE(history / src['releaseId'] / 'release.json', source)
            WRITE(evidence / (src['releaseId'] + '.json'), {
                'sourceReleaseId': src['releaseId'], 'targetReleaseId': target_id, 'passed': True, 'readOnly': True,
                'dataProfile': PROFILE, 'fixtureSHA256': 'f' * 64, 'sourceClasses': [source['componentDescriptor']],
                'targetComponentsSHA256': target['release']['components']['sha256']})
        PLAN.prepare(current, history, evidence, self.validator, work / 'plan', fetch=self.fetch)
        self.sign(work / 'plan', work)
        return work

    def publish(self, work):
        PLAN.publisher.publish(work / 'current', work / 'policy.json', work / 'config.json', 'fixture/updates',
                               validator=self.validator, api=self, fetch=self.fetch,
                               expected_policy_sha256=(work / 'plan/expected-policy-sha256').read_text().strip())
        url = json.loads((work / 'config.json').read_bytes())['policyURL']
        self.assertEqual(self.remote[url], (work / 'policy.json').read_bytes())
        last_upload = max(i for i, event in enumerate(self.events) if event[0] == 'upload')
        self.assertEqual(self.events[last_upload], ('upload', url))
        self.assertIn(('read', url), self.events[last_upload + 1:])
        for i, event in enumerate(self.events[:last_upload]):
            if event[0] == 'upload' and not event[1].endswith('/policy.json'):
                self.assertIn(('read', event[1]), self.events[i+1:last_upload])

    def client_check(self, installed, target):
        config = installed['config']
        env = dict(os.environ, LAZYMIND_UPDATE_UNIT_CHILD='1', LAZYMIND_UPDATE_UNIT_ARGS='["check"]',
                   LAZYMIND_UPDATE_UNIT_CONFIG=base64.b64encode(json.dumps(config).encode()).decode(),
                   LAZYMIND_UPDATE_UNIT_ROOT=str(self.root / ('account-' + config['platform'])))
        request = {'envelope': json.loads(self.remote[config['policyURL']]),
                   'state': {'systemVersion': '99.0.0', 'dataProfile': PROFILE, 'components': {}}}
        result = subprocess.run([str(self.client), '-test.run=^TestUpdateHelperSubprocess$'], input=json.dumps(request),
                                env=env, text=True, capture_output=True, check=True)
        decision = json.loads(result.stdout)
        self.assertEqual((decision['status'], decision['next']['releaseId']), ('direct', target['release']['releaseId']))

    def test_first_and_next_release_need_only_their_platform(self):
        for family in ('windows', 'darwin'):
            with self.subTest(platform=family):
                url = BASE + f'updates/{family}/policy.json'
                a, b = [self.build(v, family, url) for v in ('1.0.0', '1.1.0')]
                self.publish(self.prepare(family + '-A', a))
                self.publish(self.prepare(family + '-B', b, a))
                self.client_check(a, b)

    def test_shared_pointer_introduces_each_platform_independently_in_either_order(self):
        base = self.root
        for first, second in [('windows', 'darwin'), ('darwin', 'windows')]:
            with self.subTest(first=first):
                self.root = base / first
                self.root.mkdir()
                shutil.copyfile(base / 'fixture.pem', self.root / 'fixture.pem')
                self.remote, self.events = {}, []
                url = BASE + 'updates/shared/policy.json'
                installed = {}
                for platform in (first, second):
                    installed[platform] = self.build('1.0.0', platform, url)
                    self.publish(self.prepare(platform + '-A', installed[platform]))
                for platform in (first, second):
                    b = self.build('1.1.0', platform, url)
                    self.publish(self.prepare(platform + '-B', b, installed[platform]))
                    self.client_check(installed[platform], b)

    def test_shared_pointer_preserves_foreign_history_and_rejects_a_stale_concurrent_plan(self):
        url = BASE + 'updates/policy.json'
        wa, wb, ma, mb, revoked = [self.build(ver, platform, url) for ver, platform in
            [('1.0.0', 'windows'), ('1.1.0', 'windows'), ('1.0.0', 'darwin'), ('1.1.0', 'darwin'), ('0.9.0', 'windows')]]
        seed = self.root / 'seed'
        for row in (wa, revoked):
            WRITE(seed / 'records' / row['release']['releaseId'] / 'release.json', row)
        revoked_id = revoked['release']['releaseId']
        WRITE(seed / 'plan.json', {**CONTRACT['validity'](1), 'releaseIds': [r['release']['releaseId'] for r in (wa, revoked)],
                                   'edges': [], 'revoked': [revoked_id]})
        self.sign(seed, seed)
        PLAN.publisher.publish(self.root / 'builds', seed / 'policy.json', seed / 'config.json', 'fixture/updates',
                               validator=self.validator, api=self, fetch=self.fetch, expected_policy_sha256='absent')
        self.publish(self.prepare('mac-first', ma))
        w = self.prepare('windows-plan', wb, wa)
        m = self.prepare('mac-stale', mb, ma)
        self.publish(w)
        before = dict(self.remote)
        with self.assertRaises(ValueError):
            self.publish(m)
        self.assertEqual(self.remote, before)
        self.publish(self.prepare('mac-retry', mb, ma))
        self.client_check(wa, wb)
        self.client_check(ma, mb)
        verified = PLAN.verified_policy(self.validator, self.root / 'mac-retry/policy.json', self.root / 'mac-retry/config.json')
        self.assertIn(revoked_id, verified['revoked'])
        self.assertEqual({(e['sourceReleaseId'], e['targetReleaseId']) for e in verified['edges']},
                         {(wa['release']['releaseId'], wb['release']['releaseId']), (ma['release']['releaseId'], mb['release']['releaseId'])})


if __name__ == '__main__':
    unittest.main()
