#!/usr/bin/env python3
"""Use a pinned, published RAG bundle; never produce a new archive.

The published manifest is a release contract. The matching full algorithm lock
is installed by the native build, then checked here before optional files are
removed. Archive hash/manifest checks and real base/overlay imports are required.
"""
import argparse
import importlib.util
import importlib.metadata as metadata
import json
import os
from pathlib import Path
import platform
import shutil
import sys
import sysconfig
import tempfile

from packaging.requirements import Requirement
from packaging.utils import canonicalize_name
from packaging.version import Version

ROOT = Path(__file__).resolve().parents[2]


def load_script(name):
    spec = importlib.util.spec_from_file_location(name.replace('-', '_'), Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


components = load_script('build-python-components')
verification = load_script('verify-python-components')


def lock_versions(path):
    result = {}
    for line in path.read_text(encoding='utf-8').splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        req = Requirement(line)
        pins = list(req.specifier)
        if req.marker or req.url or req.extras or len(pins) != 1 or pins[0].operator != '==' or '*' in pins[0].version:
            raise RuntimeError(f'Published component requires exact unconditional pins: {line}')
        name = canonicalize_name(req.name)
        if name in result:
            raise RuntimeError(f'Duplicate pinned distribution: {name}')
        result[name] = pins[0].version
    if not result:
        raise RuntimeError('Empty published dependency lock')
    return result


def validate_versions(expected, actual):
    missing = expected.keys() - actual.keys()
    extra = actual.keys() - expected.keys()
    changed = [f'{n}: expected {expected[n]}, installed {actual[n]}' for n in expected.keys() & actual.keys()
               if Version(expected[n]) != Version(actual[n])]
    if missing or extra or changed:
        raise RuntimeError(f'Published RAG dependency lock mismatch; clean rebuild using its lock. '
                           f'Missing={sorted(missing)}, extra={sorted(extra)}, changed={sorted(changed)}')


def remove_empty_dirs(site):
    for directory, _, _ in os.walk(site, topdown=False):
        path = Path(directory)
        if path != site and not path.is_symlink() and not any(path.iterdir()):
            path.rmdir()


def stage(runtime, output, catalog_path, lock_path, cache):
    runtime = runtime.resolve(strict=True)
    site = Path(sysconfig.get_path('purelib')).resolve()
    if not site.is_relative_to((runtime / 'deps/python/algorithm').resolve()):
        raise RuntimeError('Run with the staged algorithm Python')
    host_platform = 'windows' if sys.platform == 'win32' else sys.platform
    host_arch = {'amd64': 'amd64', 'x86_64': 'amd64', 'arm64': 'arm64'}.get(platform.machine().lower())
    if (host_platform, host_arch) not in {('windows', 'amd64'), ('darwin', 'arm64')} or sys.version_info[:3] != (3, 11, 15):
        raise RuntimeError('Published bundle requires native Windows x64 or macOS arm64 CPython 3.11.15')
    catalog = json.loads(catalog_path.read_text(encoding='utf-8'))
    entry = catalog['components']['rag']
    for key, value in [('schemaVersion', 1), ('platform', host_platform), ('arch', host_arch), ('pythonAbi', 'cp311')]:
        if catalog.get(key) != value or entry.get(key) != value:
            raise RuntimeError(f'Wrong published catalog {key}')
    if Path(entry['filename']).name != entry['filename']:
        raise RuntimeError('Invalid published filename')
    expected = lock_versions(lock_path)
    for name, version in entry['packages'].items():
        if name not in expected or Version(expected[name]) != Version(version):
            raise RuntimeError(f'Published package differs from lock: {name}')
    distributions = {canonicalize_name(d.metadata['Name']): d for d in metadata.distributions(path=[str(site)])}
    validate_versions(expected, {n: d.version for n, d in distributions.items()})
    slim = catalog.get('desktopProfile') == 'slim-providers-v1'
    if slim:
        profile = load_script('desktop-python-profile')
        profile.verify_source(ROOT / 'algorithm/lazyllm')
        profile.remove_providers(runtime, site)
        distributions = {canonicalize_name(d.metadata['Name']): d for d in metadata.distributions(path=[str(site)])}
    protected = {canonicalize_name(Requirement(line.strip()).name)
                 for line in (ROOT / 'algorithm/requirements.txt').read_text().splitlines()
                 if line.strip() and not line.lstrip().startswith('#')}
    protected -= set.union(*components.SEEDS.values())
    selected = components.dependency_groups(distributions, protected)['rag']
    if selected != set(entry['packages']):
        raise RuntimeError('RAG dependency boundary changed; publish a deliberately validated component release')
    owners = {}
    for name, dist in distributions.items():
        for path in components.distribution_files(dist, site):
            owners.setdefault(path, set()).add(name)
    paths = sorted({p for name in selected for p in components.distribution_files(distributions[name], site)})
    if any(not owners[p] <= selected for p in paths):
        raise RuntimeError('Published RAG shares files with base distributions')
    cache.mkdir(parents=True, exist_ok=True)
    cached = cache / entry['filename']
    if cached.exists():
        verification.acquire_bundle(entry, cache, cache)  # A corrupt cache fails, never silently used.
    else:
        with tempfile.TemporaryDirectory(prefix='.download-', dir=cache) as temp:
            downloaded = verification.acquire_bundle(entry, Path(temp), None)
            downloaded.replace(cached)
    logs = runtime.parent / 'published-python-check'
    logs.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='published-rag-', dir=runtime.parent) as temp:
        temporary = Path(temp)
        overlay = verification.extract_bundle(cached, temporary / 'payload', entry)
        moved = []
        try:
            for path in paths:
                backup = temporary / 'backup' / path.relative_to(site)
                backup.parent.mkdir(parents=True, exist_ok=True)
                path.replace(backup)
                moved.append((path, backup))
            remove_empty_dirs(site)
            verification.run_child('base', ['--without-grpc'] if slim else [], logs)
            verification.run_child('overlay', ['--overlay', str(overlay)], logs)
        except BaseException:
            for path, backup in reversed(moved):
                path.parent.mkdir(parents=True, exist_ok=True)
                backup.replace(path)
            raise
    # Keep the exact published manifest, fingerprint, hash and URL. Only after
    # frozen versions + real imports passed is this runtime paired with it.
    target = runtime / 'config/python-components.json'
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(components.encoded(catalog))
    output.mkdir(parents=True, exist_ok=True)
    (output / 'python-components.json').write_bytes(components.encoded(catalog))
    (output / 'SHA256SUMS').write_text(f"{entry['sha256']}  {entry['filename']}\n", encoding='utf-8')
    (output / 'PUBLISHED-COMPONENT.txt').write_text(
        f"Uses existing published component; no new ZIP was generated.\n{entry['url']}\n", encoding='utf-8')
    report = {'source': 'published', 'filename': entry['filename'], 'url': entry['url'],
              'sha256': entry['sha256'], 'lockedDistributions': len(expected),
              'baseImports': True, 'overlayImports': True, 'logs': str(logs)}
    (runtime / 'config/published-python-verification.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('runtime', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--catalog', type=Path, default=ROOT / 'desktop/python-components/windows-amd64.json')
    parser.add_argument('--lock', type=Path, default=ROOT / 'desktop/python-components/windows-amd64-requirements.lock')
    parser.add_argument('--cache', type=Path, default=ROOT / 'desktop/cache/published-python/windows-amd64')
    args = parser.parse_args()
    stage(args.runtime, args.output, args.catalog, args.lock, args.cache)
