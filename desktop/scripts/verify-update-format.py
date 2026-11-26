#!/usr/bin/env python3
"""Validate a persisted v2 RAG index with the target release's Python runtime."""

import argparse
from contextlib import closing, contextmanager, ExitStack
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import plistlib
import posixpath
import re
import site
import stat
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit
from urllib.request import build_opener
import zipfile


QUERY = [1.0, 0.0]
EXPECTED_ID = 2
PARTITION = '_default'
SHA256 = re.compile(r'[a-f0-9]{64}\Z')
MAX_METADATA = 1024 * 1024


def require(condition, message):
    if not condition:
        raise ValueError(message)


def read_plain_file(root, relative):
    parts = relative.split('/')
    require(all(part not in ('', '.', '..') and not any(c in part for c in '\\:\x00') for part in parts),
            'Unsafe artifact or fixture path')
    path = root
    for part in parts:
        path = path / part
        require(not path.is_symlink(), 'Unexpected symlink in artifact or fixture')
    require(path.is_file() and path.resolve().is_relative_to(root.resolve()),
            'Expected regular file inside its directory: ' + relative)
    return path


class IncompatibleIndex(ValueError):
    """A persisted index field or query result violates the reader's format contract."""


def require_index(condition, message):
    if not condition:
        raise IncompatibleIndex(message)


def read_index(fixture):
    import numpy as np
    from milvus_lite.index.factory import load_index_from_spec
    from milvus_lite.index.spec import IndexSpec
    from milvus_lite.storage.segment import Segment

    # Opening a collection can load a fallback manifest, recreate an index or
    # write a WAL. Read the primary manifest, Parquet PKs and `.idx` directly.
    manifest_text = read_plain_file(fixture, 'manifest.json').read_text(encoding='utf-8')
    try:
        manifest = json.loads(manifest_text)
        partitions = manifest['partitions']
        require_index(set(partitions) == {PARTITION}, 'Unexpected fixture partitions')
        partition = partitions[PARTITION]
        require_index(not partition['delta_files'] and len(partition['data_files']) == 1, 'Unexpected fixture layout')
        relative = partition['data_files'][0]
        spec_data = manifest['index_specs']['vector']
    except (json.JSONDecodeError, KeyError, TypeError, IndexError) as error:
        raise IncompatibleIndex('Unreadable persisted index manifest') from error
    try:
        spec = IndexSpec.from_dict(spec_data)
    except (KeyError, ValueError) as error:
        raise IncompatibleIndex('Unsupported persisted index specification') from error
    require_index((spec.field_name, spec.index_type, spec.metric_type) == ('vector', 'FLAT', 'COSINE'),
                  'Expected a persisted FLAT/COSINE vector index')
    prefix = 'partitions/' + PARTITION
    require_index(isinstance(relative, str) and relative.startswith('data/') and relative.endswith('.parquet'),
                  'Unexpected fixture data file')
    data = read_plain_file(fixture, prefix + '/' + relative)
    segment = Segment.load(str(data), PARTITION, 'id', spec.field_name)
    index_file = read_plain_file(fixture, f'{prefix}/indexes/{data.stem}.vector.flat.idx')
    try:
        index = load_index_from_spec(spec, str(index_file), segment.vector_dim)
    except ValueError as error:
        # BruteForceIndex.load gives these two diagnostics for a persisted FLAT
        # array whose rank or vector width does not match the Parquet segment.
        layout_error = r'loaded index (?:has shape \([0-9, ]*\), expected \(N, dim\)|dim [0-9]+ != expected dim [0-9]+)'
        if not re.fullmatch(layout_error, str(error)):
            raise
        raise IncompatibleIndex(str(error)) from error
    require_index(index.num_vectors == len(segment.pks), 'Index row count differs from Parquet')
    rows, _ = index.search(np.asarray([QUERY], dtype=np.float32), top_k=1)
    row = int(rows[0, 0])
    require_index(0 <= row < len(segment.pks), 'No matching persisted index row')
    return {'id': segment.pks[row]}


def create_index(fixture):
    from milvus_lite.engine.collection import Collection
    from milvus_lite.schema.types import CollectionSchema, DataType, FieldSchema

    require(not fixture.exists(), 'Fixture directory already exists')
    schema = CollectionSchema([
        FieldSchema('id', DataType.INT64, is_primary=True),
        FieldSchema('vector', DataType.FLOAT_VECTOR, dim=2),
    ])
    with closing(Collection('upgrade_fixture', str(fixture), schema)) as collection:
        collection.insert([{'id': 1, 'vector': [0.0, 1.0]}, {'id': EXPECTED_ID, 'vector': QUERY}])
        collection.flush()
        collection.create_index('vector', {'index_type': 'FLAT', 'metric_type': 'COSINE'})
        collection.load()
        require(collection.search([QUERY], top_k=1, anns_field='vector')[0][0]['id'] == EXPECTED_ID,
                'Source runtime did not return the expected fixture ID')
    result = read_index(fixture)
    require(result['id'] == EXPECTED_ID, 'Could not verify the persisted source index')
    return result


def fixture_fingerprint(fixture):
    entries = []
    for path in sorted(fixture.rglob('*')):
        require(not path.is_symlink() and (path.is_dir() or path.is_file()), 'Unexpected entry in fixture')
        digest = None
        if path.is_file():
            with path.open('rb') as stream:
                digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        entries.append((path.relative_to(fixture).as_posix(), digest))
    return hashlib.sha256(json.dumps(entries, separators=(',', ':')).encode()).hexdigest()


def load_json(filename):
    require(filename.stat().st_size <= MAX_METADATA, 'Update metadata exceeds the size limit')
    return json.loads(filename.read_text(encoding='utf-8'))


def verified_file(root, relative, artifact):
    path = read_plain_file(root, relative)
    require(type(artifact['size']) is int and artifact['size'] > 0 and SHA256.fullmatch(artifact['sha256'])
            and path.stat().st_size == artifact['size'], relative + ' size does not match release record')
    with path.open('rb') as stream:
        require(hashlib.file_digest(stream, 'sha256').hexdigest() == artifact['sha256'],
                relative + ' SHA-256 does not match release record')
    return path


def check_identity(config, release):
    for field in ('version', 'platform', 'arch', 'signingIdentity', 'updaterProtocol', 'releaseId'):
        require(config[field] == release[field], 'Release identity differs from config: ' + field)


def release_catalog(catalog_file, release):
    catalog = load_json(catalog_file)
    require(catalog['schemaVersion'] == 1 and catalog['platform'] == release['platform']
            and catalog['arch'] == release['arch'], 'Invalid component catalog identity')
    rag = catalog['components']['rag']
    require(rag['id'] == 'rag', 'Invalid RAG component identity')
    return catalog['components'], {key: value for key, value in rag.items() if key not in ('schemaVersion', 'signingMode')}


def release_inputs(directory, unsigned_test):
    record = load_json(read_plain_file(directory, 'release.json'))
    config, release = record['config'], record['release']
    native_os = {'darwin': 'darwin', 'win32': 'windows'}.get(sys.platform)
    native_arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(
        platform.machine().lower())
    require(native_os and native_arch and config['platform'] == native_os and config['arch'] == native_arch,
            'Run evidence on the package platform and architecture')
    check_identity(config, release)
    require(config['updaterProtocol'] == 2 and re.fullmatch(r'format-v2-[a-f0-9]{64}', record['formatProfile']),
            'Expected a v2 format profile')
    if unsigned_test:
        require(native_os == 'darwin' and config['mode'] == 'unsigned-test'
                and config['channel'] == 'personal-test' and config['audience'] != 'official'
                and config['signingIdentity'] == 'adhoc', 'Expected in-place unsigned-test build identity')
    else:
        require(config['mode'] == 'official' and config['audience'] == 'official',
                'Expected an officially signed build')
    require(type(release['package']['unpackedBytes']) is int and release['package']['unpackedBytes'] > 0,
            'Invalid v2 package unpacked size')
    package = verified_file(directory, 'package.zip' if native_os == 'darwin' else 'package.exe', release['package'])
    catalog_file = verified_file(directory, 'components.json', release['components'])
    entries, descriptor = release_catalog(catalog_file, release)
    require(descriptor == record['componentDescriptor'], 'Release RAG descriptor differs from its component catalog')
    return record, package, catalog_file, entries


def validate_app_zip(package, app_name, unpacked_bytes):
    paths = set()
    links = set()
    seen_bytes = 0
    with zipfile.ZipFile(package) as archive:
        for entry in archive.infolist():
            name = entry.orig_filename.rstrip('/')
            parts = name.split('/')
            require(parts[0] == app_name and all(part not in ('', '.', '..') and not any(c in part for c in '\\:\x00')
                                                 for part in parts), 'ZIP entry outside the expected App bundle')
            require(name not in paths, 'Duplicate ZIP entry')
            paths.add(name)
            seen_bytes += entry.file_size
            # ditto includes AppleDouble metadata alongside the bundle files.
            require(seen_bytes <= unpacked_bytes + 64 * 1024 * 1024, 'ZIP exceeds release expansion size')
            file_type = stat.S_IFMT(entry.external_attr >> 16)
            require(file_type in (0, stat.S_IFREG, stat.S_IFDIR, stat.S_IFLNK), 'Unexpected ZIP file type')
            if file_type == stat.S_IFLNK:
                require(0 < entry.file_size <= 4096, 'Invalid ZIP symlink target length')
                target = archive.read(entry).decode('utf-8')
                relative = posixpath.normpath(posixpath.join(posixpath.dirname(name), target))
                require(not target.startswith('/') and not any(c in target for c in '\\:\x00')
                        and relative.startswith(app_name + '/'), 'ZIP symlink leaves the App bundle')
                links.add(name)
    for name in paths:
        parent = posixpath.dirname(name)
        while parent:
            require(parent not in links, 'ZIP entry uses a symlink as its directory')
            parent = posixpath.dirname(parent)


def output(command, *, timeout=90, env=None, include_stderr=False):
    result = subprocess.run(command, capture_output=True, check=True, timeout=timeout, env=env,
                            encoding='utf-8', errors='replace')
    return result.stdout + result.stderr if include_stderr else result.stdout


def verify_macos_app(app, signing_identity, unsigned_test):
    with read_plain_file(app, 'Contents/Info.plist').open('rb') as stream:
        bundle_id = plistlib.load(stream)['CFBundleIdentifier']
    require(bundle_id == 'ai.lazymind.desktop', 'Unexpected macOS App identity')
    output(['/usr/bin/codesign', '--verify', '--deep', '--strict', str(app)])
    signature = output(['/usr/bin/codesign', '-dv', '--verbose=4', str(app)], include_stderr=True).splitlines()
    if unsigned_test:
        require('Signature=adhoc' in signature and 'TeamIdentifier=not set' in signature,
                'Expected ad-hoc signature on in-place test App')
    else:
        require(f'TeamIdentifier={signing_identity}' in signature
                and any(line.startswith('Authority=Developer ID Application:') for line in signature),
                'macOS App signature differs from release record')


def run_powershell(script, env, *, timeout=90):
    # Paths and identities are data, not interpolated PowerShell commands.
    powershell = Path(os.environ['SystemRoot']) / 'System32/WindowsPowerShell/v1.0/powershell.exe'
    output([str(powershell), '-NoProfile', '-NonInteractive', '-Command', '$ErrorActionPreference = "Stop"; ' + script],
           timeout=timeout, env={**os.environ, **env})


def verify_windows_signature(executable, signing_identity):
    run_powershell(
        '$signature = Get-AuthenticodeSignature -LiteralPath $env:LM_EVIDENCE_EXECUTABLE; '
        '$type = [System.Security.Cryptography.X509Certificates.X509NameType]::SimpleName; '
        'if ($signature.Status -ne "Valid" -or '
        '$signature.SignerCertificate.GetNameInfo($type, $false) -cne $env:LM_EVIDENCE_SIGNER) { exit 1 }',
        {'LM_EVIDENCE_EXECUTABLE': str(executable), 'LM_EVIDENCE_SIGNER': signing_identity})


def run_windows_installer(executable, arguments, *, timeout):
    # Start-Process -Wait also waits for the NSIS process tree, including the
    # uninstaller's re-execution from a temporary location.
    run_powershell('$process = Start-Process -FilePath $env:LM_EVIDENCE_EXECUTABLE '
                   '-ArgumentList $env:LM_EVIDENCE_ARGUMENTS -Wait -PassThru; '
                   'if ($process.ExitCode -ne 0) { exit 1 }',
                   {'LM_EVIDENCE_EXECUTABLE': str(executable), 'LM_EVIDENCE_ARGUMENTS': arguments}, timeout=timeout)


@contextmanager
def materialized_app(package, release, directory, unsigned_test):
    if release['platform'] == 'darwin':
        name = 'LazyMind.app'
        validate_app_zip(package, name, release['package']['unpackedBytes'])
        output(['/usr/bin/ditto', '-x', '-k', str(package), str(directory)], timeout=600)
        app = directory / name
        verify_macos_app(app, release['signingIdentity'], unsigned_test)
        yield app / 'Contents/Resources'
        return

    # Full NSIS installation warms its bundled Python. Only run this on fresh,
    # disposable Windows runners because NSIS owns per-user install registration.
    require(os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted' and os.environ.get('GITHUB_ACTIONS') == 'true',
            'Windows evidence requires a disposable GitHub-hosted runner')
    require(not (Path(os.environ['LOCALAPPDATA']) / 'Programs/LazyMind').exists() and not directory.exists(),
            'Refusing to reuse an existing Windows installation')
    require(not any(character.isspace() for character in str(directory)),
            'Windows evidence scratch path must not contain whitespace')
    verify_windows_signature(package, release['signingIdentity'])
    # In NSIS the /D argument is last and takes the complete destination path.
    run_windows_installer(package, f'/S --full-install /D={directory}', timeout=1950)
    app = read_plain_file(directory, 'LazyMind.exe')
    uninstall = read_plain_file(directory, 'Uninstall LazyMind.exe')
    try:
        verify_windows_signature(app, release['signingIdentity'])
        yield directory / 'resources'
    finally:
        run_windows_installer(uninstall, '/S', timeout=300)


def format_profile(runtime):
    recipe = load_json(read_plain_file(runtime, 'config/update-format-v2.json'))
    normalized = {
        'version': recipe['version'],
        'families': dict(sorted(recipe['families'].items())),
        'segments': [{key: item[key] for key in ('type', 'name', 'table', 'sql')} for item in recipe['segments']],
        'lazyllmVariants': recipe.get('lazyllmVariants'),
    }
    encoded = json.dumps(normalized, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
    return 'format-v2-' + hashlib.sha256(encoded).hexdigest()


def inspect_runtime(resources, release, catalog_file, descriptor, expected=None):
    runtime = resources / 'runtime'
    manifest = read_plain_file(runtime, 'manifest.json')
    with manifest.open('rb') as stream:
        manifest_sha = hashlib.file_digest(stream, 'sha256').hexdigest()
    profile = format_profile(runtime)
    if expected is not None:
        require(manifest_sha == expected['manifestSHA256'], 'Packaged runtime manifest differs from release record')
        require(profile == expected['formatProfile'], 'Packaged format recipe differs from release record')
    packaged_catalog = verified_file(runtime, 'config/python-components.json', release['components'])
    require(packaged_catalog.read_bytes() == catalog_file.read_bytes(), 'Packaged component catalog differs from staging')
    identity = load_json(read_plain_file(resources, 'app-update.yml'))['lazymind']
    expected_identity = {key: release[key] for key in ('releaseId', 'version', 'platform', 'arch', 'signingIdentity')}
    expected_identity.update(protocol=release['updaterProtocol'],
                             mode=expected['config']['mode'] if expected else identity['mode'])
    require(identity == expected_identity, 'Packaged update metadata differs from release record')
    windows = release['platform'] == 'windows'
    helper = read_plain_file(runtime, 'bin/lazymind-update-helper.exe' if windows else 'bin/lazymind-update-helper')
    if windows:
        verify_windows_signature(helper, release['signingIdentity'])
    response = json.loads(output([str(helper), 'config']))
    require(set(response) == {'status', 'config'} and response['status'] == 'enabled',
            'Packaged helper update configuration is disabled or invalid')
    config = response['config']
    check_identity(config, release)
    require(config['mode'] == identity['mode'] and (expected is None or expected['config'] == config),
            'Packaged helper identity differs from release record')
    python = runtime / ('deps/python/algorithm/Scripts/python.exe' if windows else 'deps/python/algorithm/bin/python')
    require(python.is_file() and python.resolve().is_relative_to(runtime.resolve()), 'Bundled algorithm Python is missing')
    record = dict(config=config, release=release, manifestSHA256=manifest_sha,
                  formatProfile=profile, componentDescriptor=descriptor)
    return record, runtime, python


def verify_runtime(resources, record, catalog_file):
    _, runtime, python = inspect_runtime(resources, record['release'], catalog_file, record['componentDescriptor'], expected=record)
    return runtime, python


def load_script(filename):
    spec = importlib.util.spec_from_file_location(filename[:-3], Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def extract_overlay(entry, directory, bundles, python, config, tools):
    filename = entry['filename']
    require(filename not in ('', '.', '..') and Path(filename).name == filename
            and not any(c in filename for c in '/\\:\x00'), 'Unsafe component archive name')
    require(entry['platform'] == config['platform'] and entry['arch'] == config['arch']
            and type(entry['sizeBytes']) is int and entry['sizeBytes'] > 0 and SHA256.fullmatch(entry['sha256']),
            'Invalid component artifact')
    directory.mkdir()
    if bundles:
        read_plain_file(bundles, filename)
    # Acquire the exact bundle from the immutable catalog, never a replacement
    # descriptor obtained by resolving its mutable online tag.
    archive = tools.acquire_bundle(entry, directory, bundles)
    overlay = tools.extract_bundle(archive, directory / 'payload', entry)
    if config['platform'] == 'darwin':
        tools.verify_macos_team_id(overlay, python=python, adhoc=config['mode'] == 'unsigned-test',
                                   required_binary=tools.MACOS_CLUSTER_LIBRARY if entry['id'] == 'cluster' else None)
    return overlay


def index_worker(python, command, fixture, overlay, runtime, abi):
    args = [str(python), '-I', '-B', '-X', 'utf8', str(Path(__file__).resolve()),
            '--worker', command, '--fixture', str(fixture), '--overlay', str(overlay),
            '--runtime', str(runtime), '--expected-abi', abi]
    if command == 'read-index':
        args.append('--classify-index')
    row = json.loads(output(args, timeout=120))
    if command == 'read-index' and row == {'status': 'index_incompatible'}:
        return False
    require(isinstance(row, dict) and set(row) == {'id'} and type(row['id']) is int, 'Invalid native index worker result')
    return row['id'] == EXPECTED_ID


def download_source(release, target, directory):
    config = target['config']
    require(all(release[key] == config[key] for key in ('platform', 'arch', 'signingIdentity', 'updaterProtocol')),
            'Historical release is not for the target platform, signer and updater')
    require(type(release['package']['unpackedBytes']) is int and release['package']['unpackedBytes'] > 0,
            'Invalid v2 package unpacked size')
    publisher = load_script('publish-desktop-update.py')
    origins = {origin.rstrip('/') for origin in config['allowedOrigins']}
    opener = build_opener(publisher.PinnedRedirects(origins))
    package = directory / ('package.zip' if release['platform'] == 'darwin' else 'package.exe')
    catalog = directory / 'components.json'
    for path, artifact in [(catalog, release['components']), (package, release['package'])]:
        url = artifact['url']
        parsed = urlsplit(url)
        origin = 'https://' + parsed.netloc
        require(parsed.scheme == 'https' and parsed.username is None and parsed.password is None and origin in origins,
                'Historical artifact URL is outside the configured origins')
        publisher.destination(url, origin + '/')
        limit = artifact['size']
        require(type(limit) is int and limit > 0 and SHA256.fullmatch(artifact['sha256']), 'Invalid artifact size or SHA-256')
        require(path != catalog or limit <= MAX_METADATA, 'Component catalog exceeds the size limit')
        digest = hashlib.sha256()
        size = 0
        with opener.open(url, timeout=60) as stream, path.open('xb') as file:
            while chunk := stream.read(min(1024 * 1024, limit - size + 1)):
                size += len(chunk)
                require(size <= limit, path.name + ' exceeds signed size')
                digest.update(chunk)
                file.write(chunk)
        require(size == limit and digest.hexdigest() == artifact['sha256'], path.name + ' size or SHA-256 mismatch')
    return package, catalog


def check_pair(source, target, old_components, new_components):
    old, new = source['config'], target['config']
    require(old['releaseId'] != new['releaseId'] and old['version'] != new['version'],
            'Source and target must be distinct releases')
    for key in ('platform', 'arch', 'mode', 'audience', 'channel', 'trustDomain', 'policyURL', 'signingIdentity'):
        require(old[key] == new[key], 'Source and target trust differ: ' + key)
    if source['formatProfile'] != target['formatProfile']:
        return 'format_mismatch'
    if not set(old_components) <= set(new_components):
        return 'component_removed'
    return None


def write_json(path, value):
    with path.open('x', encoding='utf-8') as stream:
        json.dump(value, stream, separators=(',', ':'))
        stream.write('\n')


def generate_evidence(args):
    require(not args.output.exists(), 'Format evidence output already exists')
    if args.source_record_output:
        require(not args.source_record_output.exists() and args.source_record_output.resolve() != args.output.resolve(),
                'Source record output must be a new, separate file')
    target, new_package, new_catalog, new_components = release_inputs(args.target, args.unsigned_test)
    tools = load_script('verify-python-components.py')
    scratch = os.environ.get('RUNNER_TEMP') if sys.platform == 'win32' else None
    with tempfile.TemporaryDirectory(prefix='lm-update-evidence-', dir=scratch) as temp:
        staging = Path(temp).resolve()
        fixture = staging / 'old-index'
        before = None
        with ExitStack() as old_app:
            if args.source:
                source, old_package, old_catalog, old_components = release_inputs(args.source, args.unsigned_test)
            else:
                release = load_json(args.published_source)
                old_package, old_catalog = download_source(release, target, staging)
                old_components, descriptor = release_catalog(old_catalog, release)
                resources = old_app.enter_context(materialized_app(old_package, release, staging / 'source-app', args.unsigned_test))
                source, runtime, python = inspect_runtime(resources, release, old_catalog, descriptor)
            reason = check_pair(source, target, old_components, new_components)
            if not reason:
                if args.source:
                    resources = old_app.enter_context(materialized_app(old_package, source['release'], staging / 'source-app', args.unsigned_test))
                    runtime, python = verify_runtime(resources, source, old_catalog)
                entry = old_components['rag']
                overlay = extract_overlay(entry, staging / 'source-rag', args.source_components_dir, python, source['config'], tools)
                require(index_worker(python, 'create-index', fixture, overlay, runtime, entry['pythonAbi']),
                        'Source runtime did not retrieve its own fixture ID')
                before = fixture_fingerprint(fixture)
        # Source and target installs must never overlap on Windows. Even for a
        # negative historical result, verify the new package's recorded format.
        if not args.source or not reason:
            with materialized_app(new_package, target['release'], staging / 'target-app', args.unsigned_test) as resources:
                runtime, python = verify_runtime(resources, target, new_catalog)
                if not reason:
                    entry = new_components['rag']
                    overlay = extract_overlay(entry, staging / 'target-rag', args.target_components_dir, python, target['config'], tools)
                    if not index_worker(python, 'read-index', fixture, overlay, runtime, entry['pythonAbi']):
                        reason = 'index_incompatible'
                    if 'cluster' in new_components:
                        cluster = extract_overlay(new_components['cluster'], staging / 'target-cluster', args.target_components_dir,
                                                  python, target['config'], tools)
                        output([str(python), '-I', '-B', '-X', 'utf8', str(Path(__file__).with_name('verify-python-components.py')),
                                '--worker', 'cluster', '--overlay', str(cluster), '--runtime', str(runtime)], timeout=180)
        if before is not None and fixture_fingerprint(fixture) != before:
            reason = 'index_incompatible'

    evidence = {'sourceReleaseId': source['config']['releaseId'], 'targetReleaseId': target['config']['releaseId']}
    if reason:
        require(args.published_source, {'format_mismatch': 'Source and target v2 formats differ',
                                        'component_removed': 'Target removed a source component',
                                        'index_incompatible': 'Target runtime cannot read the original index unchanged'}[reason])
        evidence.update(passed=False, reason=reason)
    else:
        source_class = {key: old_components['rag'][key] for key in ('tag', 'pythonAbi', 'baseFingerprint', 'packages')}
        evidence.update(dataProfile=target['formatProfile'], targetComponentsSHA256=target['release']['components']['sha256'],
                        fixtureSHA256=before, sourceClasses=[source_class], passed=True, readOnly=True)
    if args.source_record_output:
        write_json(args.source_record_output, source)
    write_json(args.output, evidence)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--worker', choices=['read-index', 'create-index'], help=argparse.SUPPRESS)
    parser.add_argument('--fixture', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--overlay', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--runtime', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--expected-abi', help=argparse.SUPPRESS)
    parser.add_argument('--classify-index', action='store_true', help=argparse.SUPPRESS)
    sources = parser.add_mutually_exclusive_group()
    sources.add_argument('--source', type=Path, help='Staged source release directory')
    sources.add_argument('--published-source', type=Path, help='Source release from the verified historical policy')
    parser.add_argument('--source-record-output', type=Path, help='Write the reconstructed historical build record')
    parser.add_argument('--target', type=Path, help='Staged target release directory')
    parser.add_argument('--output', type=Path, help='New, immutable format evidence JSON')
    parser.add_argument('--source-components-dir', type=Path, help='Local source component bundle directory')
    parser.add_argument('--target-components-dir', type=Path, help='Local target component bundle directory')
    parser.add_argument('--unsigned-test', action='store_true', help='Verify ad-hoc macOS in-place test packages')
    args = parser.parse_args()
    if args.worker:
        require(args.fixture, 'Expected a fixture directory')
        if args.overlay:
            require(args.runtime and args.expected_abi and f'cp{sys.version_info.major}{sys.version_info.minor}' == args.expected_abi
                    and Path(sys.prefix).resolve() == (args.runtime / 'deps/python/algorithm').resolve(),
                    'Run the index worker with the release algorithm Python')
            site.addsitedir(str(args.overlay))
            import milvus_lite
            require(Path(milvus_lite.__file__).resolve().is_relative_to(args.overlay.resolve()),
                    'Milvus Lite must come from the pinned RAG overlay')
        if args.worker == 'read-index':
            try:
                result = read_index(args.fixture)
            except IncompatibleIndex:
                if not args.classify_index:
                    raise
                result = {'status': 'index_incompatible'}
        else:
            result = create_index(args.fixture)
        print(json.dumps(result))
        return
    if not ((args.source or args.published_source) and args.target and args.output):
        parser.error('A source, --target and --output are required')
    if bool(args.published_source) != bool(args.source_record_output):
        parser.error('--published-source and --source-record-output must be used together')
    generate_evidence(args)


if __name__ == '__main__':
    try:
        main()
    except (subprocess.SubprocessError, zipfile.BadZipFile):
        raise SystemExit('Native update evidence process or archive failed') from None
    except (OSError, RuntimeError, ValueError, KeyError, TypeError, IndexError) as error:
        print(f'Native update evidence failed: {error}', file=sys.stderr)
        raise SystemExit(1) from None
