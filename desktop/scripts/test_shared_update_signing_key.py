"""A single-signature shared policy must still verify on the other installed OS."""

import base64
import json
import subprocess
import unittest

import importlib.util
from pathlib import Path

spec = importlib.util.spec_from_file_location('platform_tests', Path(__file__).with_name('test_platform_update_publication.py'))
platform_tests = importlib.util.module_from_spec(spec)
spec.loader.exec_module(platform_tests)


class SharedSigningKeyTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        platform_tests.PerPlatformPublication.setUpClass()
        cls.addClassCleanup(platform_tests.PerPlatformPublication.doClassCleanups)

    @staticmethod
    def make_second_key(fx):
        seed = bytes.fromhex('9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60')
        key2 = base64.b64encode(bytes.fromhex('302e020100300506032b657004220420') + seed).decode()
        (fx.root / 'second.pem').write_text('-----BEGIN PRIVATE KEY-----\n' + key2 + '\n-----END PRIVATE KEY-----\n')
        pub2 = base64.b64encode(bytes.fromhex('d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a')).decode()
        return pub2

    def test_other_installed_platform_still_trusts_the_shared_pointer(self):
        fx = platform_tests.PerPlatformPublication()
        fx.setUp()
        self.addCleanup(fx.doCleanups)
        url = platform_tests.BASE + 'updates/shared/policy.json'
        wa, wb = [fx.build(v, 'windows', url) for v in ('1.0.0', '1.1.0')]
        ma, mb = [fx.build(v, 'darwin', url) for v in ('1.0.0', '1.1.0')]
        # A Windows client already installed with just fixture (K1) cannot
        # look up either of these key IDs, even when the public bytes match.
        pub2 = self.make_second_key(fx)
        for record in (ma, mb):
            record['config']['publicKeys'].update(second=pub2, alias=platform_tests.CONTRACT['PUBLIC'])
            platform_tests.WRITE(fx.root / 'builds' / record['release']['releaseId'] / 'release.json', record)
        fx.publish(fx.prepare('wa', wa))
        fx.publish(fx.prepare('ma', ma))
        fx.publish(fx.prepare('wb', wb, wa))
        fx.client_check(wa, wb)
        work = fx.prepare('mb', mb, ma)
        first_plan = fx.root / 'standalone.json'
        target = mb['release']['releaseId']
        platform_tests.WRITE(first_plan, {**platform_tests.CONTRACT['validity'](1),
                                          'releaseIds': [target], 'edges': [], 'revoked': []})
        prior = dict(fx.remote)
        for key, private in [('second', 'second.pem'), ('alias', 'fixture.pem')]:
            with self.subTest(key=key):
                # Each key is valid for a fresh macOS policy. An invalid key
                # or fixture must not turn the shared-channel rejection green.
                subprocess.run([str(fx.validator), 'sign', '--records', str(fx.root / 'builds' / target),
                    '--plan', str(first_plan), '--key-id', key, '--key-file', str(fx.root / private),
                    '--output', str(fx.root / (key + '-first.json')), '--config-output', str(fx.root / (key + '-first-config.json'))],
                    check=True, capture_output=True)
                candidate = fx.root / (key + '.json')
                rejected = subprocess.run([str(fx.validator), 'sign', '--records', str(work / 'plan/records'),
                    '--plan', str(work / 'plan/plan.json'), '--previous-envelope', str(work / 'plan/previous-envelope.json'),
                    '--key-id', key, '--key-file', str(fx.root / private), '--output', str(candidate),
                    '--config-output', str(fx.root / (key + '-config.json'))], capture_output=True, text=True)
                self.assertNotEqual(rejected.returncode, 0, "shared extension changed its signing key ID")
                self.assertFalse(candidate.exists())
        self.assertEqual(fx.remote, prior)
        # The uploader also accepts an externally assembled, valid envelope.
        # Re-label the existing signature with the alias for the same bytes.
        alias = json.loads((work / 'policy.json').read_bytes())
        alias['keyId'] = 'alias'
        relabeled = fx.root / 'alias-envelope.json'
        platform_tests.WRITE(relabeled, alias)
        subprocess.run([str(fx.validator), 'verify', '--config', str(work / 'config.json'),
                        '--envelope', str(relabeled)], check=True, capture_output=True)
        with self.assertRaises(ValueError):
            platform_tests.PLAN.publisher.publish(work / 'current', relabeled, work / 'config.json', 'fixture/updates',
                validator=fx.validator, api=fx, fetch=fx.fetch,
                expected_policy_sha256=(work / 'plan/expected-policy-sha256').read_text().strip())
        self.assertEqual(fx.remote, prior)
        fx.publish(work)
        fx.client_check(wa, wb)
        fx.client_check(ma, mb)


    def test_joint_rotation_requires_every_active_native_source_to_trust_the_new_key(self):
        fx = platform_tests.PerPlatformPublication()
        fx.setUp()
        self.addCleanup(fx.doCleanups)
        url = platform_tests.BASE + 'updates/shared/policy.json'
        key = self.make_second_key(fx)
        current, histories, reports = [fx.root / name for name in ('both-current', 'verified-sources', 'both-evidence')]
        records = {}
        for platform in ('windows', 'darwin'):
            a, b = [fx.build(v, platform, url) for v in ('1.0.0', '1.1.0')]
            for row in (a, b):
                row['config']['publicKeys']['second'] = key
                platform_tests.WRITE(fx.root / 'builds' / row['release']['releaseId'] / 'release.json', row)
            records[platform] = (a, b)
            fx.publish(fx.prepare(platform + '-A', a))

        for platform, (a, b) in records.items():
            verified = fx.prepare(platform + '-verified', b, a)
            platform_tests.shutil.copytree(verified / 'native-records', histories / platform)
            platform_tests.shutil.copytree(verified / 'native-reports', reports / platform)
            platform_tests.shutil.copytree(verified / 'current', current / platform)

        plan = fx.root / 'combined-plan'
        platform_tests.PLAN.prepare(current, histories, reports, fx.validator, plan, fetch=fx.fetch)
        envelope, config = fx.root / 'rotation.json', fx.root / 'rotation-config.json'
        args = [str(fx.validator), 'sign', '--records', str(plan / 'records'), '--plan', str(plan / 'plan.json'),
                '--previous-envelope', str(plan / 'previous-envelope.json'), '--key-id', 'second',
                '--key-file', str(fx.root / 'second.pem'), '--output', str(envelope), '--config-output', str(config),
                '--verified-source-records']
        incomplete = subprocess.run(args + [str(histories / 'darwin')], capture_output=True)
        self.assertNotEqual(incomplete.returncode, 0)
        self.assertFalse(envelope.exists())
        subprocess.run(args + [str(histories)], check=True, capture_output=True)
        publish_args = dict(validator=fx.validator, api=fx, fetch=fx.fetch,
                            expected_policy_sha256=(plan / 'expected-policy-sha256').read_text().strip())
        unchanged = dict(fx.remote)
        with self.assertRaises(ValueError):
            platform_tests.PLAN.publisher.publish(current, envelope, config, 'fixture/updates',
                verified_source_records=histories / 'darwin', **publish_args)
        self.assertEqual(fx.remote, unchanged)
        platform_tests.PLAN.publisher.publish(current, envelope, config, 'fixture/updates',
            verified_source_records=histories, **publish_args)
        for a, b in records.values():
            fx.client_check(a, b)


if __name__ == '__main__':
    unittest.main()
