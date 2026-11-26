"""Verify the prebuild public locator with the real unsigned-test policy verifier."""

import io
import json
from pathlib import Path
import runpy
import subprocess
import tempfile
import unittest
from urllib.error import HTTPError

HERE = Path(__file__).resolve().parent
VERIFY = runpy.run_path(str(HERE / 'verify-published-test-baseline.py'))
FIXTURE = runpy.run_path(str(HERE / 'test_update_public_baseline.py'))


class PrebuildBaselineTest(unittest.TestCase):
    def test_signed_224_can_build_225_and_other_inputs_stop_before_packaging(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            validator = FIXTURE['build_validator'](root, unsigned=True)
            a = FIXTURE['FIXTURE']['release_record']('0.5.224')
            prefix = 'updates/unsigned-test/demo/in-place/round-41/'
            url = FIXTURE['FIXTURE']['BASE'] + prefix + 'policy.json'
            a['config'].update(publicKeys={'fixture': FIXTURE['PUBLIC']}, policyURL=url, trustDomain='inplace-41-' + 'a' * 64,
                               audience='personal', channel='personal-test', mode='unsigned-test', signingIdentity='adhoc')
            a['release']['signingIdentity'] = 'adhoc'
            for kind in ('package', 'feed', 'components'):
                descriptor = a['release'][kind]
                descriptor['url'] = descriptor['url'].replace('/updates/releases/', '/' + prefix + 'releases/')
            rid = a['config']['releaseId']
            write = FIXTURE['write_json']
            write(root / 'records' / rid / 'release.json', a)
            write(root / 'plan.json', {**FIXTURE['validity'](1), 'releaseIds': [rid], 'edges': [], 'revoked': []})
            (root / 'key.pem').write_text(FIXTURE['PRIVATE'])
            subprocess.run([str(validator), 'sign', '--records', 'records', '--plan', 'plan.json',
                            '--key-id', 'fixture', '--key-file', 'key.pem', '--config-output', 'a.json', '--output', 'policy.json'],
                           cwd=root, check=True)
            envelope = (root / 'policy.json').read_bytes()
            for case in ('valid', 'isolated round only', 'another trust domain', 'wrong key', 'same version', 'downgrade'):
                with self.subTest(case=case):
                    version = {'same version': '0.5.224', 'downgrade': '0.5.223'}.get(case, '0.5.225')
                    target = {**a['config'], 'version': version, 'releaseId': 'darwin-arm64-' + version}
                    if case == 'wrong key':
                        target['publicKeys'] = {'fixture': 'gTl3Dqh9F19Wo1Rmw0x+zMuNipG07jeiXfYPW4/Js5Q='}
                    if case == 'another trust domain':
                        target['trustDomain'] = 'inplace-41-' + 'b' * 64
                    filename = root / 'target.json'
                    filename.write_text(json.dumps(target))
                    def fetch(requested_url):
                        self.assertEqual(requested_url, url)
                        if case == 'isolated round only':
                            raise HTTPError(url, 404, 'missing', {}, None)
                        return io.BytesIO(envelope)
                    if case == 'valid':
                        self.assertEqual(VERIFY['verify'](filename, validator, fetch)['releaseId'], rid)
                    elif case == 'isolated round only':
                        with self.assertRaisesRegex(VERIFY['plan'].PlanError, r'(?i)(new|fresh) (test )?round|test_round\s*[=:]\s*new'):
                            VERIFY['verify'](filename, validator, fetch)
                    else:
                        with self.assertRaises(VERIFY['plan'].PlanError):
                            VERIFY['verify'](filename, validator, fetch)
