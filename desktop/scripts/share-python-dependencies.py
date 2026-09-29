#!/usr/bin/env python3
"""Conservatively share byte-identical, independently owned pure Python wheels."""
import argparse
from collections import defaultdict
import hashlib
import importlib.metadata as metadata
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile

EXCLUDED = {'pip', 'setuptools', 'wheel', 'lazyllm', 'umap-learn', 'numba', 'llvmlite'}
NATIVE = {'.so', '.dylib', '.dll', '.pyd', '.exe'}


def is_link(path):
    try:
        info = path.lstat()
    except FileNotFoundError:
        return False  # pruning may remove RECORD-listed bytecode and directories
    return stat.S_ISLNK(info.st_mode) or bool(getattr(info, 'st_file_attributes', 0) & stat.FILE_ATTRIBUTE_REPARSE_POINT)


def regular_files(root):
    if is_link(root):
        raise ValueError('symlink or reparse point')
    files = []
    for directory, dirs, names in os.walk(root, followlinks=False):
        base = Path(directory)
        if any(is_link(base / name) for name in dirs + names):
            raise ValueError('symlink or reparse point')
        files.extend(base / name for name in names if (base / name).is_file())
    return files


def digest_files(files, site):
    h = hashlib.sha256()
    for file in sorted(files):
        h.update(file.relative_to(site).as_posix().encode() + b'\0')
        h.update(hashlib.sha256(file.read_bytes()).digest())
    return h.hexdigest()


def sites_in(runtime):
    result = {}
    for name in ('algorithm', 'auth-service', 'channel-gateway'):
        root = runtime / 'deps/python' / name
        candidates = list(root.glob('lib/python*/site-packages')) + list(root.glob('Lib/site-packages'))
        if len(candidates) != 1:
            raise RuntimeError(f'Expected one site-packages for {name}: {candidates}')
        result[name] = candidates[0]
    return result


def audit_site(name, site):
    distributions = list(metadata.distributions(path=[str(site)]))
    owners = defaultdict(set)
    for dist in distributions:
        for entry in dist.files or []:
            if entry.parts and entry.parts[0] != '..':
                owners[entry.parts[0]].add(dist.metadata['Name'])
    candidates, skipped = [], []
    for dist in distributions:
        package = dist.metadata['Name']
        reason = None
        files, roots = [], set()
        wheel = dist.read_text('WHEEL') or ''
        if package.lower().replace('_', '-') in EXCLUDED:
            reason = 'protected package'
        elif 'Root-Is-Purelib: true' not in wheel or not any(line.endswith('-none-any') for line in wheel.splitlines()):
            reason = 'not a pure Python wheel'
        elif dist.entry_points:
            reason = 'console/plugin entry points retained in original environment'
        elif not dist.files:
            reason = 'missing RECORD'
        else:
            for entry in dist.files:
                p = site / entry
                if '..' in entry.parts or not p.resolve().is_relative_to(site.resolve()):
                    reason = 'external RECORD path'; break
                if (p.exists() and is_link(p)) or any(is_link(parent) for parent in p.parents if parent != site and parent.is_relative_to(site)):
                    reason = 'symlink'; break
                if not p.is_file():
                    # Pruning removes bytecode/tests listed in RECORD; all actual
                    # remaining files are still compared below.
                    continue
                if p.suffix.lower() in NATIVE or p.suffix == '.pth':
                    reason = 'native library or startup hook'; break
                files.append(p)
                roots.add(entry.parts[0])
            if not reason:
                for root in roots:
                    directory = site / root
                    if owners[root] != {package} or not directory.is_dir():
                        reason = 'shared top-level directory or single-file module'; break
                    if not root.endswith('.dist-info') and not (directory / '__init__.py').is_file():
                        reason = 'namespace or non-package directory'; break
                    try:
                        actual = set(regular_files(directory))
                    except ValueError:
                        reason = 'unrecorded symlink or reparse point'; break
                    if actual != {p for p in files if p.is_relative_to(directory)}:
                        reason = 'unrecorded files'; break
        if reason:
            skipped.append({'environment': name, 'name': package, 'version': dist.version, 'reason': reason})
        else:
            candidates.append({'environment': name, 'name': package, 'version': dist.version,
                'digest': digest_files(files, site), 'roots': sorted(roots), 'site': site,
                'files': files, 'bytes': sum(p.stat().st_size for p in files)})
    return candidates, skipped


def share(runtime, apply=False):
    runtime = runtime.resolve(strict=True)
    sites = sites_in(runtime)
    report_path = runtime / 'config/python-sharing.json'
    report = json.loads(report_path.read_text()) if report_path.exists() else {'schemaVersion': 1, 'shared': []}
    groups, skipped = defaultdict(list), []
    shared_root = runtime / 'deps/python/shared'
    known = {entry['digest'] for entry in report['shared']}
    if shared_root.exists() and any(p.name not in known for p in shared_root.iterdir()):
        raise RuntimeError('Interrupted/untracked shared dependency staging: perform a clean rebuild')
    # Validate previous sharing so resume cannot silently retain missing files.
    for entry in report['shared']:
        shared = runtime / entry['path']
        files = regular_files(shared)
        if not shared.is_relative_to(runtime / 'deps/python/shared') or digest_files(files, shared) != entry['digest']:
            raise RuntimeError(f'Existing shared dependency changed: {shared}')
        for env in entry['environments']:
            pth = sites[env] / ('lazymind-shared-' + entry['digest'] + '.pth')
            if not pth.is_file() or (sites[env] / pth.read_text().strip()).resolve() != shared:
                raise RuntimeError(f'Shared dependency reference missing: {pth}')
    for name, site in sites.items():
        candidates, rejected = audit_site(name, site)
        skipped.extend(rejected)
        for candidate in candidates:
            groups[(candidate['name'], candidate['version'], candidate['digest'])].append(candidate)
    planned = []
    for (_, _, digest), copies in sorted(groups.items()):
        if len(copies) < 2:
            skipped.extend({'environment': c['environment'], 'name': c['name'], 'version': c['version'],
                            'reason': 'no byte-identical copy in another environment'} for c in copies)
            continue
        source = copies[0]
        shared = runtime / 'deps/python/shared' / digest
        entry = {'name': source['name'], 'version': source['version'], 'digest': digest,
                 'path': shared.relative_to(runtime).as_posix(), 'environments': [c['environment'] for c in copies],
                 'topLevels': [r for r in source['roots'] if not r.endswith('.dist-info')],
                 'bytes': source['bytes'], 'savedBytes': source['bytes'] * (len(copies)-1)}
        planned.append(entry)
        if not apply:
            continue
        shared.parent.mkdir(parents=True, exist_ok=True)
        if shared.exists():
            raise RuntimeError(f'Untracked shared directory: {shared}')
        stage = Path(tempfile.mkdtemp(prefix='.share-', dir=shared.parent))
        try:
            for root in source['roots']:
                shutil.copytree(source['site'] / root, stage / root)
            if digest_files(regular_files(stage), stage) != digest:
                raise RuntimeError('Shared copy verification failed')
            stage.rename(shared)
        finally:
            if stage.exists(): shutil.rmtree(stage)
        for copy in copies:
            pth = copy['site'] / ('lazymind-shared-' + digest + '.pth')
            relative = os.path.relpath(shared, copy['site']).replace(os.sep, '/')
            if '\n' in relative or (copy['site'] / relative).resolve() != shared:
                raise RuntimeError('Invalid shared dependency reference')
            pth.write_text(relative + '\n')
            for root in copy['roots']:
                shutil.rmtree(copy['site'] / root)
        # Persist each completed move. Interrupted partial moves fail the next
        # build and require a clean rebuild rather than silently corrupting it.
        report['shared'].append(entry)
        report_path.parent.mkdir(parents=True, exist_ok=True)
        report_path.write_text(json.dumps(report, indent=2) + '\n')
    if apply:
        code = """import importlib, importlib.metadata as m, json, pathlib, sys
runtime = pathlib.Path(sys.argv[1]).resolve()
for entry in json.loads(sys.stdin.read()):
    shared = runtime / entry['path']
    dist = m.distribution(entry['name'])
    assert dist.version == entry['version'], entry
    assert pathlib.Path(dist.locate_file('')).resolve() == shared, entry
    assert dist.read_text('METADATA'), entry
    for name in entry.get('topLevels', []):
        module = importlib.import_module(name)
        assert pathlib.Path(module.__file__).resolve().is_relative_to(shared), name
print('SHARED_IMPORTS_METADATA_OK')
"""
        for name in sites:
            entries = [e for e in report['shared'] if name in e['environments']]
            if not entries: continue
            venv = runtime / 'deps/python' / name
            python = venv / ('Scripts/python.exe' if os.name == 'nt' else 'bin/python')
            subprocess.run([str(python), '-I', '-B', '-c', code, str(runtime)], input=json.dumps(entries),
                           text=True, check=True, timeout=180)
    report.update(applied=apply, candidates=planned, skipped=skipped)
    report['savedBytes'] = sum(e['savedBytes'] for e in report['shared'])
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, indent=2) + '\n')
    print(f"Shared Python dependencies: {report['savedBytes'] / 1048576:.2f} MiB saved; report {report_path}")
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('runtime', type=Path)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    share(args.runtime, args.apply)
