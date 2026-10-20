#!/usr/bin/env python3
"""Apply the reviewed Milvus Lite 3.0 manifest replacement and atomic index publication fixes to a Windows build.

The original source hash and version are pinned. Update RECORD before component
fingerprints are calculated; never replace os.rename globally or edit user data.
"""
import argparse
import base64
import csv
import hashlib
import importlib.metadata as metadata
import io
import json
import os
from pathlib import Path
import sys
import sysconfig
import tempfile

from packaging.version import Version

PATCH_ID = 'milvus-lite-3.0-windows-storage-v2'
SOURCE = 'milvus_lite/storage/manifest.py'
ORIGINAL_SHA256 = '59b45341edf6531e68736d37d7f93aba5355d8daa06b11ad120289b3e234fcd6'
PATCHED_SHA256 = '33403589370c7690031dc7deb3a092461ad8f6ff48e14428003472d50f01b4a9'

INDEX_SOURCE = 'milvus_lite/storage/segment.py'
INDEX_ORIGINAL_SHA256 = 'a158780b53d15eb99cad39763ee6cd176d9a8a6545e2d46b51b5334303e0224d'
INDEX_PATCHED_SHA256 = 'ab50646c4b9a5264be725f19496ea8137c489ebbbf73caf02ae008210400617e'
INDEX_OLD = b'            idx.save(path)'
INDEX_NEW = b'''            # Publish only a complete index: background indexing may be interrupted
            # by shutdown while another load observes the final filename.
            import tempfile
            fd, temporary = tempfile.mkstemp(prefix=".index-", suffix=".tmp", dir=index_dir)
            os.close(fd)
            try:
                idx.save(temporary)
                with open(temporary, "rb+") as saved:
                    os.fsync(saved.fileno())
                os.replace(temporary, path)
            finally:
                if os.path.exists(temporary):
                    os.unlink(temporary)'''


def patches():
    return [
        (SOURCE, ORIGINAL_SHA256, PATCHED_SHA256,
         b'os.rename(tmp_path, target_path)', b'os.replace(tmp_path, target_path)'),
        (INDEX_SOURCE, INDEX_ORIGINAL_SHA256, INDEX_PATCHED_SHA256, INDEX_OLD, INDEX_NEW),
    ]


def digest(data):
    return hashlib.sha256(data).hexdigest()


def record_hash(data):
    return 'sha256=' + base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b'=').decode()


def checked_file(site, path):
    if path.is_symlink() or not path.resolve().is_relative_to(site) or not path.is_file():
        raise RuntimeError(f'Missing or linked Milvus patch input: {path}')
    return path


def inspect(site, name, original_hash, patched_hash):
    site = site.resolve(strict=True)
    distributions = [d for d in metadata.distributions(path=[str(site)])
                     if d.metadata.get('Name', '').lower().replace('_', '-') == 'milvus-lite']
    if len(distributions) != 1 or Version(distributions[0].version) != Version('3.0'):
        raise RuntimeError('Milvus patch requires exactly one milvus-lite==3.0 distribution; review dependency upgrades')
    dist = distributions[0]
    records = [p for p in dist.files or [] if str(p).endswith('.dist-info/RECORD')]
    if len(records) != 1:
        raise RuntimeError('Milvus RECORD missing or ambiguous')
    record = checked_file(site, Path(dist.locate_file(records[0])))
    source = checked_file(site, site / name)
    original, record_bytes = source.read_bytes(), record.read_bytes()
    if digest(original) not in {original_hash, patched_hash}:
        raise RuntimeError('Milvus source hash changed; refusing to apply an unreviewed patch')
    rows = list(csv.reader(io.StringIO(record_bytes.decode('utf-8'), newline='')))
    matches = [r for r in rows if r and r[0] == name]
    if len(matches) != 1 or matches[0] != [name, record_hash(original), str(len(original))]:
        raise RuntimeError('Milvus source does not match RECORD')
    return source, record, original, record_bytes, rows


def atomic_write(path, data):
    with tempfile.NamedTemporaryFile(prefix='.milvus-patch-', dir=path.parent, delete=False) as output:
        temporary = Path(output.name)
        try:
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        except BaseException:
            output.close()
            temporary.unlink(missing_ok=True)
            raise
    try:
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def verify_site(site):
    for name, before, after, _, _ in patches():
        _, _, source, _, _ = inspect(site, name, before, after)
        if digest(source) != after:
            raise RuntimeError('Windows Milvus storage fix is missing; rebuild this component')


def patch_site(site):
    # Validate every input before modifying any file; refuse unreviewed wheels.
    inputs = [(spec, inspect(site, *spec[:3])) for spec in patches()]
    record = inputs[0][1][1]
    rows = inputs[0][1][4]
    changed_sources = []
    for (name, before, after, old, new), (source, _, original, _, _) in inputs:
        if digest(original) == before:
            patched = original.replace(old, new)
            if digest(patched) != after:
                raise RuntimeError('Unexpected Milvus patch result')
            changed_sources.append((source, original, patched))
            for row in rows:
                if row and row[0] == name:
                    row[1:] = [record_hash(patched), str(len(patched))]
    stream = io.StringIO(newline='')
    csv.writer(stream, lineterminator='\n').writerows(rows)
    written = []
    try:
        for source, original, patched in changed_sources:
            atomic_write(source, patched)
            written.append((source, original))
        if changed_sources:
            atomic_write(record, stream.getvalue().encode('utf-8'))
    except BaseException:
        for source, original in reversed(written):
            atomic_write(source, original)
        raise
    for _, (source, _, _, _, _) in inputs:
        for cached in (source.parent / '__pycache__').glob(source.stem + '.*.pyc'):
            checked_file(site.resolve(), cached).unlink()
    verify_site(site)
    return {'patch': PATCH_ID, 'distribution': 'milvus-lite', 'version': '3.0',
            'sources': [{'source': name, 'originalSha256': before, 'patchedSha256': after}
                        for name, before, after, _, _ in patches()],
            'recordUpdated': True, 'changed': bool(changed_sources)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('runtime', type=Path)
    parser.add_argument('--verify-only', action='store_true')
    args = parser.parse_args()
    if sys.platform != 'win32':
        parser.error('This patch is only applied by native Windows builds')
    runtime = args.runtime.resolve(strict=True)
    site = Path(sysconfig.get_path('purelib')).resolve()
    if not site.is_relative_to((runtime / 'deps/python/algorithm').resolve()):
        parser.error('Run with this staged runtime algorithm Python')
    if args.verify_only:
        verify_site(site)
        print('WINDOWS_MILVUS_PATCH_VERIFIED')
        return
    report = patch_site(site)
    path = runtime / 'config/milvus-windows-patch.json'
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
