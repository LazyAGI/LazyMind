"""Small native macOS archives exercising the producer and compatibility verifier."""

import json
from pathlib import Path
import plistlib
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch
import zipfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
VERIFY = runpy.run_path(str(HERE / 'verify-update-format.py'))
BASE = 'https://modelscope.cn/datasets/fixture/repo/resolve/master/updates/unsigned-test/demo/in-place/round-41/'
CONFIG = dict(mode='unsigned-test', audience='personal', trustDomain='inplace-41-' + 'a' * 64, channel='personal-test',
              policyURL=BASE + 'policy.json', allowedOrigins=['https://modelscope.cn'],
              updaterProtocol=2, signingIdentity='adhoc', publicKeys={'fixture': 'AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA='})


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value))


def signed_app(root, version, name='LazyMind.app', bundle_id='ai.lazymind.desktop'):
    app = root / name
    contents = app / 'Contents'
    resources = contents / 'Resources'
    runtime = resources / 'runtime'
    executable = contents / 'MacOS' / 'LazyMind'
    executable.parent.mkdir(parents=True)
    shutil.copyfile('/usr/bin/true', executable)
    executable.chmod(0o755)
    (contents / 'Info.plist').write_bytes(plistlib.dumps(dict(CFBundleExecutable='LazyMind', CFBundleIdentifier=bundle_id,
        CFBundlePackageType='APPL', CFBundleName=name[:-4], CFBundleVersion=version, CFBundleShortVersionString=version)))
    config = dict(CONFIG, releaseId='darwin-arm64-' + version, platform='darwin', arch='arm64', version=version)
    write_json(runtime / 'manifest.json', {'version': version})
    descriptor = dict(id='rag', revision='a' * 64, platform='darwin', arch='arm64')
    write_json(runtime / 'config/python-components.json', dict(schemaVersion=1, platform='darwin', arch='arm64',
                                                               components={'rag': descriptor}))
    shutil.copyfile(ROOT / 'local/local-runtime-manager/update-format-v2.json', runtime / 'config/update-format-v2.json')
    native = {key: config[key] for key in ('releaseId', 'version', 'platform', 'arch', 'mode', 'signingIdentity')}
    write_json(resources / 'app-update.yml', {'lazymind': dict(native, protocol=2)})
    helper = runtime / 'bin' / 'lazymind-update-helper'
    helper.parent.mkdir()
    helper.write_text("#!/bin/sh\nprintf '%s\\n' '" + json.dumps({'status': 'enabled', 'config': config}) + "'\n")
    helper.chmod(0o755)
    subprocess.run(['/usr/bin/codesign', '--force', '--sign', '-', '--timestamp=none', str(app)], check=True,
                   capture_output=True)
    archive = root / 'package.zip'
    with zipfile.ZipFile(archive, 'w') as zipped:
        for file in sorted(app.rglob('*')):
            if file.is_file():
                zipped.write(file, file.relative_to(root))
    release = dict(platform='darwin', signingIdentity='adhoc', package={'unpackedBytes': archive.stat().st_size})
    return resources, archive, release


@unittest.skipUnless(sys.platform == 'darwin', 'requires native codesign and ditto')
class InPlaceUpdatePackages(unittest.TestCase):
    def test_both_builds_supply_their_own_verified_native_resources(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            packages = {}
            for role, version in [('source', '0.5.301'), ('target', '0.5.302')]:
                _, archive, release = signed_app(root / role, version)
                config = dict(CONFIG, platform='darwin', arch='arm64', releaseId='darwin-arm64-' + version, version=version)
                record = dict(config=config, release=release, formatProfile='v2-fixture')
                packages[root / role] = (record, archive, None, {'rag': {'pythonAbi': 'cp311'}})

            # The tiny signed Apps have no Python distribution. Exercise the
            # verifier through its target runtime lookup without generating a report.
            class TargetOpened(Exception):
                pass

            opened = []
            def runtime_boundary(resources, record, _catalog):
                self.assertEqual(resources.parent.parent.name, 'LazyMind.app')
                version = json.loads((resources / 'runtime/manifest.json').read_text())['version']
                self.assertEqual(version, record['config']['version'])
                opened.append(version)
                if len(opened) == 2:
                    raise TargetOpened
                return resources / 'runtime', None

            args = SimpleNamespace(source=root / 'source', target=root / 'target', output=root / 'report.json',
                                   unsigned_test=True, source_record_output=None, source_components_dir=None)
            generate = VERIFY['generate_evidence']
            with patch.dict(generate.__globals__, release_inputs=lambda folder, _mode: packages[folder],
                            verify_runtime=runtime_boundary, extract_overlay=lambda *_: None,
                            index_worker=lambda *_: True, fixture_fingerprint=lambda _: None):
                with self.assertRaises(TargetOpened):
                    generate(args)
            self.assertEqual(opened, ['0.5.301', '0.5.302'])
            self.assertFalse(args.output.exists())

    def test_old_name_wrong_identifier_and_official_mode_are_rejected(self):
        for name, bundle_id, unsigned in [('LazyMind Test.app', 'ai.lazymind.desktop.test', True),
                                          ('LazyMind.app', 'ai.lazymind.desktop.test', True),
                                          ('LazyMind.app', 'ai.lazymind.desktop', False)]:
            with self.subTest(name=name, bundle_id=bundle_id, unsigned=unsigned), tempfile.TemporaryDirectory() as work:
                root = Path(work)
                _, archive, release = signed_app(root, '0.5.301', name, bundle_id)
                with self.assertRaises(ValueError), VERIFY['materialized_app'](archive, release, root / 'out', unsigned):
                    self.fail('unexpected package accepted')

    def test_release_staging_uses_the_adhoc_app_identity_and_exact_archive_members(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work).resolve()
            stage = root / '.lazymind-unsigned-v2.fixture'
            scripts = stage / 'desktop/scripts'
            scripts.mkdir(parents=True)
            for name in ['prepare-update-release.mjs', 'unsigned-update-build.cjs']:
                shutil.copyfile(HERE / name, scripts / name)
            write_json(scripts / '.unsigned-update-test.local.json', CONFIG)
            resources, archive, _ = signed_app(root, '0.5.301')
            subprocess.run(['node', str(scripts / 'prepare-update-release.mjs'), '--resources', str(resources),
                            '--package', str(archive), '--minimum-system-version', '13.0.0',
                            '--base-url', BASE, '--output', str(root / 'release')], check=True)
            record = json.loads((root / 'release/darwin-arm64-0.5.301/release.json').read_text())
            self.assertEqual(record['config']['policyURL'], CONFIG['policyURL'])
            self.assertEqual(record['release']['package']['url'], BASE + 'releases/darwin-arm64-0.5.301/package.zip')


if __name__ == '__main__':
    unittest.main()
