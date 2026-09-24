#!/usr/bin/env python3
"""Trim bundled Python files without changing application features or Skills."""

import argparse
from collections import Counter
import importlib.metadata
import json
import os
from pathlib import Path
import stat
import subprocess
import sys


# Keep testing helpers (numpy.testing, scipy._lib._testutils, etc.). Only these
# libraries' actual test suites are excluded, never arbitrary "tests" folders.
TEST_PACKAGES = {'numpy', 'scipy', 'pandas', 'sklearn', 'spacy', 'thinc'}


def is_link(path):
    # Python 3.11 has no Path.is_junction(). uv's Windows Python aliases are
    # directory junctions: is_symlink()/os.walk(followlinks=False) miss them.
    try:
        info = path.lstat()
    except FileNotFoundError:
        return False
    return (stat.S_ISLNK(info.st_mode) or
            bool(getattr(info, 'st_file_attributes', 0) & stat.FILE_ATTRIBUTE_REPARSE_POINT))


def regular_directories(root):
    if is_link(root):
        return
    for directory, dirs, files in os.walk(root, followlinks=False):
        base = Path(directory)
        dirs[:] = sorted(name for name in dirs if not is_link(base / name))
        yield base, files


def files_under(root):
    for base, files in regular_directories(root):
        for name in sorted(files):
            path = base / name
            if not is_link(path):
                yield path


def bytecode_has_source(path):
    if path.suffix not in {'.pyc', '.pyo'}:
        return False
    if path.parent.name == '__pycache__':
        return (path.parent.parent / (path.name.split('.')[0] + '.py')).is_file()
    return path.with_suffix('.py').is_file()


def trim_runtime(runtime, apply):
    runtime = runtime.resolve()
    roots = [runtime / 'runtimes/python', runtime / 'deps/python']
    if not all(p.is_dir() and not is_link(p) and p.resolve().is_relative_to(runtime) for p in roots):
        raise ValueError('Expected a staged runtime with runtimes/python and deps/python directories')
    paths = [p for root in roots for p in files_under(root)]
    sizes = {p: p.stat().st_size for p in paths}
    sites = sorted({p.parent for p in paths if p.parent.name == 'site-packages'} |
                   {parent for p in paths for parent in p.parents
                    if parent.name == 'site-packages' and parent.is_relative_to(runtime)})
    removals = {}
    for path in paths:
        if bytecode_has_source(path):
            removals[path] = 'rebuildable_bytecode'
            continue
        for site in sites:
            if not path.is_relative_to(site):
                continue
            parts = path.relative_to(site).parts
            if parts[0] in TEST_PACKAGES and 'tests' in parts[1:-1]:
                removals[path] = 'dependency_test_suites'
            break

    before = Counter()
    after = Counter()
    groups = Counter()
    for path, size in sizes.items():
        relative = path.relative_to(runtime)
        site = next((s for s in sites if path.is_relative_to(s)), None)
        bucket = (str(site.relative_to(runtime) / path.relative_to(site).parts[0]) if site
                  else str(Path(*relative.parts[:3])))
        before[bucket] += size
        if path in removals:
            groups[removals[path]] += size
        else:
            after[bucket] += size
    if apply:
        for path in sorted(removals):
            path.unlink(missing_ok=True)
        # Remove empty directories only; preserve distribution metadata/licenses.
        # Walk top-down to exclude junctions before reversing for child-first
        # removal. A bottom-up os.walk would already have entered their targets.
        for root in roots:
            directories = [path for path, _ in regular_directories(root)]
            for path in reversed(directories):
                if not is_link(path) and path != root and not any(path.iterdir()):
                    path.rmdir()
    return {
        'schema_version': 1,
        'applied': apply,
        'measurement': 'uncompressed regular-file bytes; not installer download savings',
        'python_bytes_before': sum(sizes.values()),
        'python_bytes_after': sum(sizes.values()) - (sum(groups.values()) if apply else 0),
        'candidate_removed_bytes': sum(groups.values()),
        'candidate_removed_files': len(removals),
        'removal_groups': dict(groups),
        'installed_distributions': {
            str(site.relative_to(runtime)): sorted(
                [{'name': dist.metadata['Name'], 'version': dist.version}
                 for dist in importlib.metadata.distributions(path=[str(site)]) if dist.metadata.get('Name')],
                key=lambda item: item['name'].lower(),
            ) for site in sites
        },
        'packages': [{'path': key, 'before_bytes': value,
                      'after_bytes': after[key] if apply else value}
                     for key, value in before.most_common()],
    }


def write_report(report, path):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
    mib = lambda n: f'{n / 2**20:.2f} MiB'
    lines = ['## Bundled Python size report', '',
             f'- Pruning applied: {report["applied"]}',
             f'- Before: {mib(report["python_bytes_before"])}',
             f'- After: {mib(report["python_bytes_after"])}',
             f'- Removable files: {report["candidate_removed_files"]}',
             '- These are uncompressed file sizes, not installer download savings.', '',
             '| Category | Removable bytes |', '| --- | ---: |']
    lines.extend(f'| {key} | {mib(value)} |' for key, value in report['removal_groups'].items())
    lines.extend(['', '| Largest Python package/tree | Before | After |', '| --- | ---: | ---: |'])
    lines.extend(f'| {row["path"]} | {mib(row["before_bytes"])} | {mib(row["after_bytes"])} |'
                 for row in report['packages'][:30])
    lines.extend(['', '| Largest remaining Python package/tree | After |', '| --- | ---: |'])
    remaining = sorted(report['packages'], key=lambda row: row['after_bytes'], reverse=True)
    lines.extend(f'| {row["path"]} | {mib(row["after_bytes"])} |' for row in remaining[:30] if row['after_bytes'])
    path.with_suffix('.md').write_text('\n'.join(lines) + '\n', encoding='utf-8')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('runtime', type=Path)
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--apply', action='store_true', help='Without this flag, only report candidate savings')
    parser.add_argument('--verify-doubao', action='store_true')
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[2]
    report = trim_runtime(args.runtime, args.apply)
    try:
        if args.verify_doubao:
            report['doubao_http_mock_smoke'] = 'failed'
            subprocess.run([sys.executable, '-I', '-B', str(repo / 'desktop/scripts/desktop-python-profile.py'),
                            '--verify-source', str(repo / 'algorithm/lazyllm')], check=True, timeout=90)
            report['doubao_http_mock_smoke'] = 'passed'
    finally:
        write_report(report, args.report)
    print(f'Bundled Python: {report["python_bytes_before"] / 2**20:.2f} -> '
          f'{report["python_bytes_after"] / 2**20:.2f} MiB (uncompressed)')


if __name__ == '__main__':
    main()
