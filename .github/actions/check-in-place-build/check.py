"""Dispatch-owned guard for an in-place build of a selectable source ref."""

import json
import os
from pathlib import Path
import plistlib
import runpy
import subprocess
import sys
import zipfile


def resolve(scripts, *options):
    result = subprocess.run(['node', str(scripts / 'resolve-unsigned-test-round.mjs'), *options],
                            check=True, capture_output=True, text=True, timeout=30)
    return json.loads(result.stdout)


def check(phase):
    trusted = Path(__file__).resolve().parents[3] / 'desktop/scripts'
    expected = resolve(trusted)
    if phase == 'resolver':
        selected = Path.cwd() / 'desktop/scripts'
        if resolve(selected) != expected or resolve(selected, '--target-config') != resolve(trusted, '--target-config'):
            raise ValueError('Selected git_ref does not build the requested in-place round. Choose an in-place compatible ref.')
        return

    if phase != 'artifact':
        raise ValueError('Unknown build check')
    config = resolve(trusted, '--target-config')
    output = Path(os.environ['RUNNER_TEMP']) / 'unsigned-update-test'
    directory = output / config['releaseId']
    if set(output.rglob('release.json')) != {directory / 'release.json'} or directory.is_symlink():
        raise ValueError('Expected one release for this run and target version')
    probe = runpy.run_path(str(trusted / 'verify-update-format.py'))
    record, package, _, _ = probe['release_inputs'](directory, True)
    if record['config'] != config:
        raise ValueError('Packaged configuration differs from this run')
    release = record['release']
    for kind, filename in [('package', 'package.zip'), ('feed', 'feed.json'), ('components', 'components.json')]:
        if release[kind]['url'] != expected['baseURL'] + 'releases/' + config['releaseId'] + '/' + filename:
            raise ValueError('Packaged release URLs differ from this round')
    probe['verified_file'](directory, 'feed.json', release['feed'])
    probe['validate_app_zip'](package, 'LazyMind.app', release['package']['unpackedBytes'])
    with zipfile.ZipFile(package) as archive:
        def metadata(name):
            entry = archive.getinfo('LazyMind.app/Contents/' + name)
            if not 0 < entry.file_size <= 1024 * 1024:
                raise ValueError('Invalid app metadata size')
            return archive.read(entry)

        info = plistlib.loads(metadata('Info.plist'))
        if (info['CFBundleIdentifier'], info['CFBundleExecutable'], info['CFBundleShortVersionString']) != (
                'ai.lazymind.desktop', 'LazyMind', config['version']):
            raise ValueError('Archive bundle identity differs from this in-place build')
        embedded = json.loads(metadata('Resources/app-update.yml'))
        identity = {k: config[k] for k in ('releaseId', 'version', 'platform', 'arch', 'mode', 'signingIdentity')}
        if embedded['lazymind'] != dict(identity, protocol=2) or embedded.get('updaterCacheDirName') != (
                'LazyMind-' + config['trustDomain'] + '-' + config['audience'] + '-' + config['channel']):
            raise ValueError('Archive update identity differs from this round')


if __name__ == '__main__':
    try:
        check(sys.argv[1])
    except (ValueError, OSError, KeyError, subprocess.SubprocessError, zipfile.BadZipFile) as exc:
        raise SystemExit(f'In-place build check failed: {exc}') from None
