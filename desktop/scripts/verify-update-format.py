#!/usr/bin/env python3
"""Validate a persisted v2 RAG index with the target release's Python runtime."""

import argparse
from contextlib import closing, contextmanager
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


def read_index(fixture):
    import numpy as np
    from milvus_lite.index.factory import load_index_from_spec
    from milvus_lite.index.spec import IndexSpec
    from milvus_lite.storage.segment import Segment

    # Opening a collection can load a fallback manifest, recreate an index or
    # write a WAL. Read the primary manifest, Parquet PKs and `.idx` directly.
    manifest = json.loads(read_plain_file(fixture, 'manifest.json').read_text(encoding='utf-8'))
    require(set(manifest['partitions']) == {PARTITION}, 'Unexpected fixture partitions')
    partition = manifest['partitions'][PARTITION]
    require(not partition['delta_files'] and len(partition['data_files']) == 1, 'Unexpected fixture layout')
    spec = IndexSpec.from_dict(manifest['index_specs']['vector'])
    require((spec.field_name, spec.index_type, spec.metric_type) == ('vector', 'FLAT', 'COSINE'),
            'Expected a persisted FLAT/COSINE vector index')
    prefix = 'partitions/' + PARTITION
    relative = partition['data_files'][0]
    require(relative.startswith('data/') and relative.endswith('.parquet'), 'Unexpected fixture data file')
    data = read_plain_file(fixture, prefix + '/' + relative)
    segment = Segment.load(str(data), PARTITION, 'id', spec.field_name)
    index_file = read_plain_file(fixture, f'{prefix}/indexes/{data.stem}.vector.flat.idx')
    index = load_index_from_spec(spec, str(index_file), segment.vector_dim)
    require(index.num_vectors == len(segment.pks), 'Index row count differs from Parquet')
    rows, _ = index.search(np.asarray([QUERY], dtype=np.float32), top_k=1)
    row = int(rows[0, 0])
    require(0 <= row < len(segment.pks), 'No matching persisted index row')
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


def release_inputs(directory, unsigned_test):
    record = load_json(read_plain_file(directory, 'release.json'))
    config, release = record['config'], record['release']
    native_os = {'darwin': 'darwin', 'win32': 'windows'}.get(sys.platform)
    native_arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(
        platform.machine().lower())
    require(native_os and native_arch and config['platform'] == native_os and config['arch'] == native_arch,
            'Run evidence on the package platform and architecture')
    for field in ('version', 'platform', 'arch', 'signingIdentity', 'updaterProtocol', 'releaseId'):
        require(config[field] == release[field], 'Release identity differs from config: ' + field)
    require(config['updaterProtocol'] == 2 and re.fullmatch(r'format-v2-[a-f0-9]{64}', record['formatProfile']),
            'Expected a v2 format profile')
    if unsigned_test:
        require(native_os == 'darwin' and config['mode'] == 'unsigned-test'
                and config['channel'] == 'personal-test' and config['audience'] != 'official'
                and config['signingIdentity'] == 'adhoc', 'Expected isolated unsigned-test build identity')
    else:
        require(config['mode'] == 'official' and config['audience'] == 'official',
                'Expected an officially signed build')
    require(type(release['package']['unpackedBytes']) is int and release['package']['unpackedBytes'] > 0,
            'Invalid v2 package unpacked size')
    package = verified_file(directory, 'package.zip' if native_os == 'darwin' else 'package.exe', release['package'])
    catalog_file = verified_file(directory, 'components.json', release['components'])
    catalog = load_json(catalog_file)
    require(catalog['schemaVersion'] == 1 and catalog['platform'] == native_os and catalog['arch'] == native_arch,
            'Invalid component catalog identity')
    rag = catalog['components']['rag']
    descriptor = {key: value for key, value in rag.items() if key not in ('schemaVersion', 'signingMode')}
    require(descriptor == record['componentDescriptor'] and rag['id'] == 'rag',
            'Release RAG descriptor differs from its component catalog')
    return record, package, catalog_file, catalog['components']


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
    require(bundle_id == ('ai.lazymind.desktop.test' if unsigned_test else 'ai.lazymind.desktop'),
            'Unexpected macOS App identity')
    output(['/usr/bin/codesign', '--verify', '--deep', '--strict', str(app)])
    signature = output(['/usr/bin/codesign', '-dv', '--verbose=4', str(app)], include_stderr=True).splitlines()
    if unsigned_test:
        require('Signature=adhoc' in signature and 'TeamIdentifier=not set' in signature,
                'Expected ad-hoc signature on isolated test App')
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
def materialized_app(package, record, directory, unsigned_test):
    config = record['config']
    if config['platform'] == 'darwin':
        name = 'LazyMind Test.app' if unsigned_test else 'LazyMind.app'
        validate_app_zip(package, name, record['release']['package']['unpackedBytes'])
        output(['/usr/bin/ditto', '-x', '-k', str(package), str(directory)], timeout=600)
        app = directory / name
        verify_macos_app(app, config['signingIdentity'], unsigned_test)
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
    verify_windows_signature(package, config['signingIdentity'])
    # In NSIS the /D argument is last and takes the complete destination path.
    run_windows_installer(package, f'/S --full-install /D={directory}', timeout=1950)
    app = read_plain_file(directory, 'LazyMind.exe')
    uninstall = read_plain_file(directory, 'Uninstall LazyMind.exe')
    try:
        verify_windows_signature(app, config['signingIdentity'])
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


def verify_runtime(resources, record, catalog_file):
    config = record['config']
    runtime = resources / 'runtime'
    manifest = read_plain_file(runtime, 'manifest.json')
    with manifest.open('rb') as stream:
        require(hashlib.file_digest(stream, 'sha256').hexdigest() == record['manifestSHA256'],
                'Packaged runtime manifest differs from release record')
    packaged_catalog = verified_file(runtime, 'config/python-components.json', record['release']['components'])
    require(packaged_catalog.read_bytes() == catalog_file.read_bytes(), 'Packaged component catalog differs from staging')
    identity = {key: config[key] for key in ('releaseId', 'version', 'mode', 'platform', 'arch', 'signingIdentity')}
    identity['protocol'] = config['updaterProtocol']
    require(load_json(read_plain_file(resources, 'app-update.yml'))['lazymind'] == identity,
            'Packaged update metadata differs from release record')
    require(format_profile(runtime) == record['formatProfile'], 'Packaged format recipe differs from release record')
    windows = config['platform'] == 'windows'
    helper = read_plain_file(runtime, 'bin/lazymind-update-helper.exe' if windows else 'bin/lazymind-update-helper')
    if windows:
        verify_windows_signature(helper, config['signingIdentity'])
    require(json.loads(output([str(helper), 'config'])) == {'status': 'enabled', 'config': config},
            'Packaged helper identity differs from release record')
    python = runtime / ('deps/python/algorithm/Scripts/python.exe' if windows else 'deps/python/algorithm/bin/python')
    require(python.is_file() and python.resolve().is_relative_to(runtime.resolve()), 'Bundled algorithm Python is missing')
    return runtime, python


def component_tools():
    spec = importlib.util.spec_from_file_location('component_tools', Path(__file__).with_name('verify-python-components.py'))
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
    result = output([str(python), '-I', '-B', '-X', 'utf8', str(Path(__file__).resolve()),
                     '--worker', command, '--fixture', str(fixture), '--overlay', str(overlay),
                     '--runtime', str(runtime), '--expected-abi', abi], timeout=120)
    require(json.loads(result) == {'id': EXPECTED_ID}, 'Native runtime did not retrieve the old fixture ID')


def generate_evidence(args):
    require(not args.output.exists(), 'Format evidence output already exists')
    source, old_package, old_catalog, old_components = release_inputs(args.source, args.unsigned_test)
    target, new_package, new_catalog, new_components = release_inputs(args.target, args.unsigned_test)
    old, new = source['config'], target['config']
    require(old['releaseId'] != new['releaseId'] and old['version'] != new['version'],
            'Source and target must be distinct releases')
    for key in ('platform', 'arch', 'mode', 'audience', 'channel', 'trustDomain', 'policyURL', 'signingIdentity'):
        require(old[key] == new[key], 'Source and target trust differ: ' + key)
    require(source['formatProfile'] == target['formatProfile'], 'Source and target v2 formats differ')
    require(set(old_components) <= set(new_components), 'Target removed a source component')

    tools = component_tools()
    scratch = os.environ.get('RUNNER_TEMP') if sys.platform == 'win32' else None
    with tempfile.TemporaryDirectory(prefix='lm-update-evidence-', dir=scratch) as temp:
        staging = Path(temp).resolve()
        fixture = staging / 'old-index'
        with materialized_app(old_package, source, staging / 'source-app', args.unsigned_test) as resources:
            runtime, python = verify_runtime(resources, source, old_catalog)
            entry = old_components['rag']
            overlay = extract_overlay(entry, staging / 'source-rag', args.source_components_dir, python, old, tools)
            index_worker(python, 'create-index', fixture, overlay, runtime, entry['pythonAbi'])
        before = fixture_fingerprint(fixture)
        with materialized_app(new_package, target, staging / 'target-app', args.unsigned_test) as resources:
            runtime, python = verify_runtime(resources, target, new_catalog)
            entry = new_components['rag']
            overlay = extract_overlay(entry, staging / 'target-rag', args.target_components_dir, python, new, tools)
            index_worker(python, 'read-index', fixture, overlay, runtime, entry['pythonAbi'])
            if 'cluster' in new_components:
                cluster = extract_overlay(new_components['cluster'], staging / 'target-cluster', args.target_components_dir,
                                          python, new, tools)
                output([str(python), '-I', '-B', '-X', 'utf8',
                        str(Path(__file__).with_name('verify-python-components.py').resolve()),
                        '--worker', 'cluster', '--overlay', str(cluster), '--runtime', str(runtime)], timeout=180)
        require(fixture_fingerprint(fixture) == before, 'Target runtime changed the persisted index fixture')

    evidence = {
        'sourceReleaseId': old['releaseId'],
        'targetReleaseId': new['releaseId'],
        'dataProfile': target['formatProfile'],
        'targetComponentsSHA256': target['release']['components']['sha256'],
        'fixtureSHA256': before,
        'sourceClasses': [{key: old_components['rag'][key]
                           for key in ('tag', 'pythonAbi', 'baseFingerprint', 'packages')}],
        'passed': True,
        'readOnly': True,
    }
    with args.output.open('x', encoding='utf-8') as stream:
        json.dump(evidence, stream, separators=(',', ':'))
        stream.write('\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--worker', choices=['read-index', 'create-index'], help=argparse.SUPPRESS)
    parser.add_argument('--fixture', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--overlay', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--runtime', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--expected-abi', help=argparse.SUPPRESS)
    parser.add_argument('--source', type=Path, help='Staged source release directory')
    parser.add_argument('--target', type=Path, help='Staged target release directory')
    parser.add_argument('--output', type=Path, help='New, immutable format evidence JSON')
    parser.add_argument('--source-components-dir', type=Path, help='Local source component bundle directory')
    parser.add_argument('--target-components-dir', type=Path, help='Local target component bundle directory')
    parser.add_argument('--unsigned-test', action='store_true', help='Verify explicitly isolated macOS test packages')
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
        result = read_index(args.fixture) if args.worker == 'read-index' else create_index(args.fixture)
        print(json.dumps(result))
        return
    if not (args.source and args.target and args.output):
        parser.error('--source, --target and --output are required')
    generate_evidence(args)


if __name__ == '__main__':
    try:
        main()
    except (subprocess.SubprocessError, zipfile.BadZipFile):
        raise SystemExit('Native update evidence process or archive failed') from None
    except (OSError, RuntimeError, ValueError, KeyError, TypeError, IndexError) as error:
        print(f'Native update evidence failed: {error}', file=sys.stderr)
        raise SystemExit(1) from None
