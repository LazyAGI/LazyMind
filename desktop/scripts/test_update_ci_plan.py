"""Exercise CI publication planning after the external policy verifier authenticates history."""

import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('ci_plan', Path(__file__).with_name('prepare-update-ci-plan.py'))
ci_plan = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci_plan)

BASE = 'https://modelscope.cn/datasets/fixture/updates/resolve/master/'
PROFILE = 'format-v2-' + 'a' * 64


def release_record(version, platform='darwin', *, arch=None):
    arch = arch or ('arm64' if platform == 'darwin' else 'amd64')
    rid = f'{platform}-{arch}-{version}'
    identity = {'version': version, 'releaseId': rid, 'platform': platform, 'arch': arch,
                'signingIdentity': 'ABCDEFGHIJ' if platform == 'darwin' else 'LazyMind', 'updaterProtocol': 2}
    config = {**identity, 'trustDomain': 'fixture', 'channel': 'stable', 'audience': 'official', 'mode': 'official',
              'policyURL': BASE + 'updates/policy.json', 'allowedOrigins': ['https://modelscope.cn']}
    release = {**identity, 'minimumSystemVersion': '13.0.0', 'minUpdaterProtocol': 2}
    for kind, name in [('package', 'package.zip' if platform == 'darwin' else 'package.exe'),
                       ('feed', 'feed.json'), ('components', 'components.json')]:
        url = f'{BASE}updates/releases/{rid}/{name}'
        release[kind] = {'url': url, 'size': 100, 'sha256': hashlib.sha256(url.encode()).hexdigest()}
    release['package']['unpackedBytes'] = 200
    rag = {'tag': hashlib.sha256(rid.encode()).hexdigest(), 'pythonAbi': 'cp311',
           'baseFingerprint': 'b' * 64, 'packages': {'milvus-lite': '3.0'}}
    return {'config': config, 'release': release, 'formatProfile': PROFILE, 'componentDescriptor': rag}


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value), encoding='utf-8')


class HistoricalReleasePlanTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.a, self.b, self.c = (release_record(f'0.5.{patch_version}') for patch_version in (222, 223, 224))
        for record, directory in [(self.a, 'history'), (self.b, 'history'), (self.c, 'current')]:
            write_json(self.root / directory / record['config']['releaseId'] / 'release.json', record)
        a, b = self.a['release'], self.b['release']
        self.policy = {'policySequence': 3, 'schemaVersion': 2, 'releases': [a, b], 'revoked': [],
                       'edges': [{'sourceReleaseId': a['releaseId'], 'targetReleaseId': b['releaseId'],
                                  'sourceDataProfiles': [PROFILE], 'targetDataProfile': PROFILE,
                                  'priority': 0, 'migrationProfile': 'format-v2', 'componentTransitions': {
                                      'rag': {'sourceClasses': [self.a['componentDescriptor']],
                                              'targetComponentsSHA256': b['components']['sha256']}}}]}
        self.report(self.b)

    def report(self, source, *, incompatible=None, **overrides):
        rid = source['config']['releaseId']
        value = {'sourceReleaseId': rid, 'targetReleaseId': self.c['config']['releaseId']}
        if incompatible:
            value.update(passed=False, reason=incompatible)
        else:
            value.update(passed=True, readOnly=True, dataProfile=PROFILE, fixtureSHA256='f' * 64,
                         sourceClasses=[source['componentDescriptor']],
                         targetComponentsSHA256=self.c['release']['components']['sha256'])
        write_json(self.root / 'evidence' / f'{rid}.json', {**value, **overrides})

    def prepare(self, *, verify_source_only=False):
        def verified_policy(_validator, _envelope, _config, *, source_only=False):
            return self.b['release'] if source_only else self.policy

        output = self.root / 'plan'
        evidence = None if verify_source_only else self.root / 'evidence'
        with patch.object(ci_plan, 'verified_policy', verified_policy):
            ci_plan.prepare(self.root / 'current', self.root / 'history', evidence,
                            Path('external-policy-verifier'), output, verify_source_only=verify_source_only,
                            fetch=lambda _: io.BytesIO(b'signed history'))
        filename = 'pairs.json' if verify_source_only else 'plan.json'
        return json.loads((output / filename).read_text(encoding='utf-8'))

    def test_source_discovery_supplies_signed_artifacts_with_only_the_baseline_build(self):
        a, b, c = (record['release'] for record in (self.a, self.b, self.c))
        (self.root / 'history' / a['releaseId'] / 'release.json').unlink()
        revoked = release_record('0.5.221')['release']
        self.policy['releases'].extend([
            revoked, release_record('0.5.222', 'windows')['release'],
            release_record('0.5.222', arch='amd64')['release'],
            {**release_record('0.5.220')['release'], 'signingIdentity': 'OTHERTEAM1'},
            {**release_record('0.5.219')['release'], 'updaterProtocol': 1, 'minUpdaterProtocol': 1},
        ])
        self.policy['revoked'].append(revoked['releaseId'])

        pairs = self.prepare(verify_source_only=True)
        self.assertCountEqual([pair['sourceReleaseId'] for pair in pairs], [a['releaseId'], b['releaseId']])
        for pair in pairs:
            baseline = pair['sourceReleaseId'] == b['releaseId']
            self.assertEqual(pair['sourceRelease'], b if baseline else a)
            self.assertIs(pair['baseline'], baseline)
            self.assertEqual(pair['targetReleaseId'], c['releaseId'])

    def test_compatible_history_gets_one_direct_edge_per_verified_source(self):
        revoked = release_record('0.5.221')['release']
        self.policy['releases'].extend([revoked, release_record('0.5.222', 'windows')['release']])
        self.policy['revoked'].append(revoked['releaseId'])
        self.report(self.a)
        plan = self.prepare()
        target_id = self.c['config']['releaseId']
        sources = [self.a['config']['releaseId'], self.b['config']['releaseId']]
        self.assertCountEqual([(e['sourceReleaseId'], e['targetReleaseId']) for e in plan['edges']],
                              [(source_id, target_id) for source_id in sources])
        for edge in plan['edges']:
            report = json.loads(Path(edge['formatEvidence']).read_text(encoding='utf-8'))
            self.assertEqual((report['sourceReleaseId'], report['targetReleaseId']), (edge['sourceReleaseId'], target_id))
            self.assertEqual(edge['componentTransitions']['rag']['sourceClasses'], report['sourceClasses'])
        self.assertCountEqual(plan['releaseIds'], [*sources, target_id])

    def assert_plan_rejected(self):
        with self.assertRaises(ci_plan.PlanError):
            self.prepare()
        self.assertFalse((self.root / 'plan/plan.json').exists())

    def test_missing_older_source_evidence_stops_publication(self):
        self.assert_plan_rejected()

    def test_report_must_match_both_the_source_class_and_target_catalog(self):
        mismatches = {'targetComponentsSHA256': self.b['release']['components']['sha256'],
                      'sourceClasses': [self.b['componentDescriptor']]}
        for field, wrong_value in mismatches.items():
            with self.subTest(field=field):
                self.report(self.a, **{field: wrong_value})
                self.assert_plan_rejected()

    def test_download_failure_cannot_establish_incompatibility(self):
        self.report(self.a, incompatible='download_failed')
        self.assert_plan_rejected()

    def test_explicitly_incompatible_older_source_keeps_the_bridge(self):
        self.report(self.a, incompatible='component_removed')
        edges = self.prepare()['edges']
        self.assertEqual([(edge['sourceReleaseId'], edge['targetReleaseId']) for edge in edges],
                         [(self.b['config']['releaseId'], self.c['config']['releaseId'])])

    def test_publication_stops_when_latest_source_is_incompatible(self):
        self.report(self.a)
        self.report(self.b, incompatible='component_removed')
        self.assert_plan_rejected()


if __name__ == '__main__':
    unittest.main()
