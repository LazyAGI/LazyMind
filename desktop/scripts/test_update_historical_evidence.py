"""Exercise compatibility outcomes and integrity checks for historical sources."""

from contextlib import redirect_stderr, redirect_stdout
from http.client import BadStatusLine
import hashlib
import io
import json
from pathlib import Path
import plistlib
import runpy
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch
from urllib.request import OpenerDirector
import zipfile


PROBE = runpy.run_path(str(Path(__file__).with_name('verify-update-format.py')))
BASE = 'https://modelscope.cn/datasets/fixture/repository/resolve/master/updates'


def stage(root, version, cluster=False):
    root.mkdir()
    rid = f'darwin-arm64-{version}'
    release = dict(releaseId=rid, version=version, platform='darwin', arch='arm64', signingIdentity='ABCDEFGHIJ',
                   updaterProtocol=2, minUpdaterProtocol=2, minimumSystemVersion='13.0.0')
    config = {key: release[key] for key in ('releaseId', 'version', 'platform', 'arch', 'signingIdentity', 'updaterProtocol')}
    config.update(mode='official', audience='official', channel='stable', trustDomain='fixture',
                  policyURL=BASE + '/stable.json', allowedOrigins=['https://modelscope.cn'],
                  publicKeys={'fixture': 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='})
    if version == '0.5.222':
        config['publicKeys']['older'] = 'AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE='
    revision = hashlib.sha256(version.encode()).hexdigest()
    rag = dict(id='rag', tag=revision, revision=revision, pythonAbi='cp311', baseFingerprint='b' * 64,
               platform='darwin', arch='arm64', packages={'milvus-lite': '3.0'})
    bundle = root / f'rag-{version}.zip'
    with zipfile.ZipFile(bundle, 'w') as archive:
        archive.writestr('bundle-manifest.json', json.dumps(rag))
        archive.writestr('site-packages/libfixture.dylib', bytes.fromhex('feedfacf') + version.encode())
    rag.update(filename=bundle.name, sizeBytes=bundle.stat().st_size, sha256=hashlib.sha256(bundle.read_bytes()).hexdigest(),
               url=f'{BASE}/components/{bundle.name}', unpackedBytes=4096)
    entries = {'rag': rag}
    if cluster:
        entries['cluster'] = {**rag, 'id': 'cluster'}
    catalog = json.dumps(dict(schemaVersion=1, platform='darwin', arch='arm64', components=entries)).encode()
    identity = {key: config[key] for key in ('releaseId', 'version', 'platform', 'arch', 'signingIdentity', 'mode')}
    manifest = json.dumps({'version': version}).encode()
    recipe = b'{"version":2,"families":{"rag":"v1"},"segments":[],"lazyllmVariants":null}'
    with zipfile.ZipFile(root / 'package.zip', 'w') as archive:
        archive.writestr('LazyMind.app/Contents/Info.plist', plistlib.dumps({'CFBundleIdentifier': 'ai.lazymind.desktop'}))
        resources = 'LazyMind.app/Contents/Resources/'
        for name, contents in {
            'app-update.yml': json.dumps({'lazymind': {**identity, 'protocol': 2}}),
            'runtime/config/update-format-v2.json': recipe, 'runtime/config/python-components.json': catalog,
            'runtime/manifest.json': manifest, 'runtime/deps/python/algorithm/bin/python': version,
            'runtime/bin/lazymind-update-helper': json.dumps({'status': 'enabled', 'config': config}),
        }.items():
            archive.writestr(resources + name, contents)
    for field, filename, content in [('package', 'package.zip', (root / 'package.zip').read_bytes()), ('components', 'components.json', catalog),
                                     ('feed', 'feed.json', b'{}')]:
        (root / filename).write_bytes(content)
        release[field] = dict(url=f'{BASE}/releases/{rid}/{filename}', size=len(content), sha256=hashlib.sha256(content).hexdigest())
    release['package']['unpackedBytes'] = 16384
    record = dict(config=config, release=release, manifestSHA256=hashlib.sha256(manifest).hexdigest(),
                  formatProfile='format-v2-' + hashlib.sha256(recipe).hexdigest(), componentDescriptor=rag)
    (root / 'release.json').write_text(json.dumps(record))
    return release


class HistoricalEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.enterContext(patch.object(sys, 'platform', 'darwin'))
        self.enterContext(patch.object(PROBE['platform'], 'machine', return_value='arm64'))

    def pair(self, *, cluster=False):
        args = SimpleNamespace(source=self.root / 'source', target=self.root / 'target', output=self.root / 'report.json',
                               published_source=self.root / 'published.json', source_record_output=self.root / 'source-record.json',
                               unsigned_test=False, source_components_dir=None, target_components_dir=None)
        args.published_source.write_text(json.dumps(stage(args.source, '0.5.222', cluster=cluster)))
        stage(args.target, '0.5.224')
        return args

    def historical_cli(self, args):
        return ['verify-update-format.py', '--published-source', str(args.published_source), '--target', str(args.target),
                '--source-record-output', str(args.source_record_output), '--output', str(args.output)]

    def test_verified_published_source_writes_its_record_and_an_old_to_new_report(self):
        args = self.pair()
        source = json.loads((args.source / 'release.json').read_text())
        target = json.loads((args.target / 'release.json').read_text())
        (args.source / 'release.json').unlink()
        assets = {source['release'][field]['url']: (args.source / name).read_bytes()
                  for field, name in [('package', 'package.zip'), ('components', 'components.json'), ('feed', 'feed.json')]}
        for record, root in [(source, args.source), (target, args.target)]:
            rag = record['componentDescriptor']
            assets[rag['url']] = (root / rag['filename']).read_bytes()
        requested, workers = [], []
        measured_fixture = None

        def fetch(request, *unused, **kwargs):
            url = request if isinstance(request, str) else request.full_url
            requested.append(url)
            return io.BytesIO(assets[url])

        def native(command, **kwargs):
            nonlocal measured_fixture
            executable = Path(command[0]).name
            stdout, stderr = '', ''
            if executable == 'ditto':
                with zipfile.ZipFile(command[-2]) as archive:
                    archive.extractall(command[-1])
            elif executable == 'codesign':
                stderr = 'TeamIdentifier=ABCDEFGHIJ\nAuthority=Developer ID Application: Fixture'
            elif executable == 'lazymind-update-helper' and command[1:] == ['config']:
                stdout = Path(command[0]).read_text()
            elif executable == 'python' and '--worker' in command:
                action = command[command.index('--worker') + 1]
                fixture = Path(command[command.index('--fixture') + 1])
                overlay = Path(command[command.index('--overlay') + 1])
                version = Path(command[0]).read_text()
                self.assertEqual((overlay / 'libfixture.dylib').read_bytes()[4:].decode(), version)
                workers.append((action, version, fixture))
                if action == 'create-index':
                    fixture.mkdir()
                    (fixture / 'index').write_text(version)
                    measured_fixture = PROBE['fixture_fingerprint'](fixture)
                elif action == 'read-index':
                    self.assertEqual((fixture / 'index').read_text(), '0.5.222')
                else:
                    self.fail(f'Unexpected worker command {action}')
                stdout = '{"id":2}'
            else:
                self.fail(f'Unexpected native command {command}')
            return subprocess.CompletedProcess(command, 0, stdout, stderr)

        with patch.object(sys, 'argv', self.historical_cli(args)), patch.object(OpenerDirector, 'open', side_effect=fetch), \
                patch.object(subprocess, 'run', side_effect=native):
            PROBE['main']()
        self.assertTrue({source['release'][kind]['url'] for kind in ('package', 'components')} <= set(requested))
        self.assertEqual([(cmd, version) for cmd, version, _ in workers], [('create-index', '0.5.222'), ('read-index', '0.5.224')])
        self.assertEqual(workers[0][2], workers[1][2])
        self.assertEqual(json.loads(args.source_record_output.read_text()), source)
        report = json.loads(args.output.read_text())
        self.assertIs(report['passed'], True)
        self.assertIs(report['readOnly'], True)
        self.assertEqual((report['sourceReleaseId'], report['targetReleaseId']), ('darwin-arm64-0.5.222', 'darwin-arm64-0.5.224'))
        self.assertEqual(report['dataProfile'], source['formatProfile'])
        self.assertEqual(report['fixtureSHA256'], measured_fixture)
        self.assertEqual(report['targetComponentsSHA256'], target['release']['components']['sha256'])
        self.assertEqual(report['sourceClasses'], [{key: source['componentDescriptor'][key]
                                                   for key in ('tag', 'baseFingerprint', 'pythonAbi', 'packages')}])

    def test_removed_component_is_reported_for_the_historical_pair_only(self):
        args = self.pair(cluster=True)
        with patch.object(args, 'published_source', None):
            with self.assertRaisesRegex(ValueError, 'removed'):
                PROBE['generate_evidence'](args)
        self.assertFalse(args.output.exists())
        PROBE['generate_evidence'](args)
        self.assertEqual(json.loads(args.output.read_text()), {
            'sourceReleaseId': 'darwin-arm64-0.5.222', 'targetReleaseId': 'darwin-arm64-0.5.224',
            'passed': False, 'reason': 'component_removed',
        })

    def test_native_timeout_stops_verification_without_a_report(self):
        args = self.pair()
        materialize = Mock(side_effect=subprocess.TimeoutExpired('codesign', 90))
        with patch.dict(PROBE['generate_evidence'].__globals__, materialized_app=materialize):
            with self.assertRaises(subprocess.TimeoutExpired):
                PROBE['generate_evidence'](args)
        materialize.assert_called_once()
        self.assertFalse(args.output.exists())

    def test_malformed_http_response_fails_at_download_without_leaking_response_text(self):
        args = self.pair()
        log = io.StringIO()
        with patch.object(sys, 'argv', self.historical_cli(args)), \
                patch.object(OpenerDirector, 'open', side_effect=BadStatusLine('sensitive-response')), \
                redirect_stdout(log), redirect_stderr(log), self.assertRaises(SystemExit) as stopped:
            runpy.run_path(str(Path(__file__).with_name('verify-update-format.py')), run_name='__main__')
        self.assertEqual(stopped.exception.code, 1)
        self.assertIn('failed source.download: BadStatusLine', log.getvalue())
        self.assertNotIn('sensitive-response', log.getvalue())
        self.assertFalse(args.output.exists())

    def test_damaged_historical_download_never_reaches_the_native_probe(self):
        args = self.pair()
        source = json.loads(args.published_source.read_text())
        argv = self.historical_cli(args)
        for field, wrong_size in [('package', False), ('components', False), ('package', True)]:
            with self.subTest(field=field, wrong_size=wrong_size):
                damaged_url = source[field]['url']
                requested = []

                def fetch(request, *unused, **kwargs):
                    url = request if isinstance(request, str) else request.full_url
                    requested.append(url)
                    content = (args.source / url.rsplit('/', 1)[1]).read_bytes()
                    if url == damaged_url:
                        content = content + b'!' if wrong_size else b'!' + content[1:]
                    return io.BytesIO(content)

                with patch.object(sys, 'argv', argv), patch.object(OpenerDirector, 'open', side_effect=fetch), \
                        patch.object(subprocess, 'run', side_effect=AssertionError('Executed an unverified package')):
                    with self.assertRaisesRegex(ValueError, '(?i)size|sha-?256|digest|hash|integrity'):
                        PROBE['main']()
                self.assertIn(damaged_url, requested)
                self.assertFalse(args.output.exists())
                self.assertFalse(args.source_record_output.exists())


if __name__ == '__main__':
    unittest.main()
