#!/usr/bin/env python3
"""Exercise a slim Python runtime and its real RAG bundle without an installer.

Run with the staged algorithm Python. Uses temporary data and child processes;
does not install into the base environment or touch the user's knowledge bases.
"""

import argparse
import hashlib
import importlib
import importlib.metadata
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import signal
import site
import socket
import stat
import subprocess
import sys
import sysconfig
import tempfile
import time
from urllib.parse import urlparse
from urllib.request import HTTPRedirectHandler, build_opener
import zipfile


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


class HTTPSRedirects(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        require(urlparse(newurl).scheme == 'https', 'Refusing an HTTP downgrade')
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def signed_team_id(binary, adhoc=False):
    subprocess.run(['/usr/bin/codesign', '--verify', '--strict', str(binary)],
                   check=True, capture_output=True, text=True)
    result = subprocess.run(['/usr/bin/codesign', '-dv', '--verbose=4', str(binary)],
                            check=True, capture_output=True, text=True)
    team = next((line.split('=', 1)[1].strip() for line in result.stderr.splitlines()
                 if line.startswith('TeamIdentifier=')), '')
    if adhoc:
        require('Signature=adhoc' in result.stderr and team in ('', 'not set'),
                f'Expected ad-hoc signature: {binary}')
        return 'adhoc'
    require(team and team != 'not set', f'Missing signing TeamIdentifier: {binary}')
    return team


def verify_macos_team_id(overlay, python=None, adhoc=False):
    require(sys.platform == 'darwin', 'macOS signature verification requires native macOS')
    expected = signed_team_id(Path(python or sys.executable).resolve(), adhoc=adhoc)
    magics = {bytes.fromhex(value) for value in (
        'feedface', 'feedfacf', 'cefaedfe', 'cffaedfe',
        'cafebabe', 'bebafeca', 'cafebabf', 'bfbafeca')}
    checked = 0
    for binary in sorted(overlay.rglob('*')):
        if not binary.is_file():
            continue
        with binary.open('rb') as stream:
            if stream.read(4) not in magics:
                continue
        actual = signed_team_id(binary, adhoc=adhoc)
        require(actual == expected,
                f'RAG signing TeamIdentifier mismatch: {binary}: {actual}, Python: {expected}')
        checked += 1
    require(checked > 0, 'RAG bundle contains no Mach-O binaries to verify')
    print(f'RAG_SIGNATURE_OK TeamIdentifier={expected} binaries={checked}', flush=True)


def base_imports(without_grpc=False):
    forbidden = ('spacy', 'pymilvus', 'milvus_lite')
    if without_grpc:
        forbidden += ('grpc', 'opensearchpy', 'opensearch_protobufs', 'volcenginesdkarkruntime')
    for name in forbidden:
        require(importlib.util.find_spec(name) is None, f'{name} still exists in base; this is not a slim runtime')
    for name in ('dashscope', 'numpy', 'pandas', 'sklearn', 'umap', 'fitz', 'docx', 'pptx', 'openpyxl',
                 'bm25s', 'Stemmer', 'jieba'):
        importlib.import_module(name)
        print(f'BASE_IMPORT_OK {name}', flush=True)
    importlib.import_module('lazyllm.tools.rag.readers.ocrReader')
    require('lazyllm.tools.rag.document' not in sys.modules, 'OCR imported knowledge-base document management')
    require('lazyllm.tools.rag.store' not in sys.modules, 'OCR imported knowledge-base storage')
    print('BASE_OCR_IMPORT_OK', flush=True)
    from lazymind.chat.engine.tools.algo.search_temp import _make_retriever
    with tempfile.TemporaryDirectory(prefix='base-bm25-check-') as temporary:
        source = Path(temporary) / 'notes.txt'
        source.write_text('苹果香蕉水果采购清单。', encoding='utf-8')
        nodes = _make_retriever(use_embed=False)([str(source)], '苹果水果', topk=1)
        require(nodes and '苹果' in nodes[0].text, 'Base BM25 retrieval returned no matching text')
    require('lazymind.chat.engine.tools.algo.search_kb' not in sys.modules,
            'Temporary retrieval imported knowledge-base service wiring')
    print('BASE_BM25_RETRIEVAL_OK', flush=True)


def overlay_imports(overlay):
    site.addsitedir(str(overlay))
    for name in ('grpc', 'lazyllm.tools.rag', 'spacy', 'pymilvus', 'milvus_lite', 'bm25s', 'Stemmer', 'sentencepiece', 'nltk'):
        importlib.import_module(name)
        print(f'OVERLAY_IMPORT_OK {name}', flush=True)


def serve(overlay, data, port):
    site.addsitedir(str(overlay))
    entry = next(e for e in importlib.metadata.distribution('milvus-lite').entry_points
                 if e.group == 'console_scripts' and e.name == 'milvus-lite')
    sys.argv = ['milvus-lite', 'server', '--data-dir', str(data), '--host', '127.0.0.1', '--port', str(port)]
    entry.load()()


def milvus_roundtrip(overlay, data, logs):
    site.addsitedir(str(overlay))
    from pymilvus import MilvusClient

    for cycle in (1, 2):
        with socket.socket() as reservation:
            reservation.bind(('127.0.0.1', 0))
            port = reservation.getsockname()[1]
        with (logs / f'milvus-server-{cycle}.log').open('w', encoding='utf-8') as log:
            command = [sys.executable, '-I', '-B', '-X', 'utf8', str(Path(__file__).resolve()),
                       '--worker', 'server', '--overlay', str(overlay), '--data', str(data), '--port', str(port)]
            server = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT)
            client = None
            try:
                deadline = time.monotonic() + 40
                while time.monotonic() < deadline:
                    require(server.poll() is None, f'Milvus server exited; see {log.name}')
                    try:
                        with socket.create_connection(('127.0.0.1', port), timeout=.2):
                            break
                    except OSError:
                        time.sleep(.2)
                else:
                    raise RuntimeError('Milvus server readiness timeout')
                client = MilvusClient(uri=f'http://127.0.0.1:{port}', timeout=20)
                print(f'MILVUS_CYCLE_{cycle}_CONNECTED', flush=True)
                if cycle == 1:
                    client.create_collection(collection_name='component_check', dimension=2, timeout=20)
                    print('MILVUS_COLLECTION_CREATED', flush=True)
                    client.insert(collection_name='component_check',
                                  data=[{'id': 1, 'vector': [1., 0.], 'text': 'durable component data'}], timeout=20)
                    print('MILVUS_INSERT_OK', flush=True)
                else:
                    print('MILVUS_RESTART_LOADING', flush=True)
                    client.load_collection(collection_name='component_check', timeout=20)
                result = client.search(collection_name='component_check', data=[[1., 0.]], limit=1,
                                       output_fields=['text'], timeout=20)
                require(result and result[0] and result[0][0]['id'] == 1, f'Unexpected search result: {result}')
                print('MILVUS_INSERT_SEARCH_OK' if cycle == 1 else 'MILVUS_RESTART_SEARCH_OK', flush=True)
                if cycle == 1:
                    client.flush(collection_name='component_check', timeout=20)
                    print('MILVUS_FLUSH_OK', flush=True)
                else:
                    client.drop_collection(collection_name='component_check', timeout=20)
                    print('MILVUS_DROP_OK', flush=True)
            finally:
                try:
                    if client is not None:
                        client.close()
                finally:
                    server.terminate()
                    try:
                        server.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        server.kill()
                        server.wait()


def run_child(name, arguments, logs):
    log_path = logs / f'{name}.log'
    command = [sys.executable, '-I', '-B', '-X', 'utf8', str(Path(__file__).resolve()), '--worker', name, *arguments]
    options = ({'creationflags': subprocess.CREATE_NEW_PROCESS_GROUP} if os.name == 'nt'
               else {'start_new_session': True})
    # -B suppresses .pyc but not Numba's JIT cache. Keep signed apps read-only.
    options['env'] = {**os.environ, 'NUMBA_CACHE_DIR': str(logs / 'numba-cache'),
                      'LAZYLLM_HOME': str(logs / 'lazyllm'), 'LAZYLLM_INIT_DOC': 'False'}
    if name == 'base':
        options['env']['LAZYMIND_RAG_ENABLED'] = '0'
    with log_path.open('w', encoding='utf-8') as log:
        child = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT, **options)
        try:
            result = child.wait(timeout=240)
        except subprocess.TimeoutExpired:
            if os.name == 'nt':
                subprocess.run(['taskkill', '/PID', str(child.pid), '/T', '/F'], stdout=log, stderr=log)
            else:
                os.killpg(child.pid, signal.SIGKILL)
            child.wait()
            raise RuntimeError(f'{name} timed out; see {log_path}')
    if result != 0:
        lines = log_path.read_text(encoding='utf-8', errors='replace').strip().splitlines()
        detail = '\n'.join(lines[-60:])[-12000:] if lines else 'No child output'
        raise RuntimeError(f'{name} failed (exit {result}): {detail}; see {log_path}')


def check_archive(archive, entry):
    require(archive.stat().st_size == entry['sizeBytes'], 'ZIP size does not match catalog')
    digest = hashlib.sha256()
    with archive.open('rb') as stream:
        while chunk := stream.read(1024 * 1024):
            digest.update(chunk)
    require(digest.hexdigest() == entry['sha256'], 'ZIP SHA-256 does not match catalog')


def resolve_catalog_entry(entry):
    address = entry.get('catalogUrl')
    if not address:
        return entry
    parsed = urlparse(address)
    require(parsed.scheme == 'https' and parsed.hostname and not parsed.username
            and not parsed.password and not parsed.fragment, 'Catalog URL must use HTTPS without credentials')
    require(re.fullmatch(r'[a-f0-9]{64}', entry.get('tag', '')), 'Invalid component tag')
    with build_opener(HTTPSRedirects()).open(address, timeout=30) as response:
        data = response.read(1024 * 1024 + 1)
    require(len(data) <= 1024 * 1024, 'Catalog exceeds 1 MiB')
    catalog = json.loads(data)
    require(catalog.get('schemaVersion') == 1, 'Unsupported online catalog schema')
    require(catalog.get('platform') == entry['platform'] and catalog.get('arch') == entry['arch'],
            'Online catalog platform/architecture mismatch')
    resolved = catalog['components'][entry['id']]
    for key in ('id', 'platform', 'arch', 'pythonAbi', 'baseFingerprint', 'packages'):
        require(resolved.get(key) == entry.get(key), f'Online catalog incompatible: {key}')
    require(not resolved.get('tag') or resolved['tag'] == entry['tag'], 'Online catalog tag mismatch')
    for key in ('revision', 'sha256'):
        require(re.fullmatch(r'[a-f0-9]{64}', resolved.get(key, '')), f'Invalid online {key}')
    for key in ('sizeBytes', 'unpackedBytes'):
        require(type(resolved.get(key)) is int and resolved[key] > 0, f'Invalid online {key}')
    filename = resolved.get('filename', '')
    require(filename not in ('', '.', '..') and Path(filename).name == filename
            and not any(c in filename for c in '\\:'), 'Unsafe online ZIP filename')
    for url in (resolved.get('url', ''), resolved.get('fallbackUrl') or resolved.get('url', '')):
        parsed = urlparse(url)
        require(parsed.scheme == 'https' and parsed.hostname and not parsed.username
                and not parsed.password and not parsed.fragment, 'Invalid online ZIP URL')
    return {**resolved, 'tag': entry['tag'], 'catalogUrl': address}


def acquire_bundle(entry, directory, bundle_dir):
    if bundle_dir:
        archive = bundle_dir / entry['filename']
        check_archive(archive, entry)
        return archive
    addresses = [entry['url']]
    if entry.get('fallbackUrl') and entry['fallbackUrl'] != entry['url']:
        addresses.append(entry['fallbackUrl'])
    for address in addresses:
        parsed = urlparse(address)
        require(parsed.scheme == 'https' and parsed.hostname and not parsed.username
                and not parsed.password and not parsed.fragment,
                'Catalog must contain a direct HTTPS download URL without credentials')
        if len(addresses) > 1:
            require(Path(parsed.path).name == entry['filename'], 'Mirror filename differs from catalog')
    archive = directory / 'download.zip'
    for index, address in enumerate(addresses):
        primary = index == 0 and len(addresses) > 1
        try:
            size = 0
            window_start = time.monotonic()
            window_bytes = 0
            with build_opener(HTTPSRedirects()).open(address, timeout=10 if primary else 45) as response:
                with archive.open('wb') as target:
                    while chunk := response.read1(64 * 1024):
                        size += len(chunk)
                        require(size <= entry['sizeBytes'], 'Download exceeds catalog size')
                        target.write(chunk)
                        elapsed = time.monotonic() - window_start
                        if primary and elapsed >= 15:
                            require((size - window_bytes) / elapsed >= 64 * 1024,
                                    'Primary download is slower than 64 KiB/s')
                            window_start = time.monotonic()
                            window_bytes = size
            check_archive(archive, entry)
            return archive
        except (OSError, RuntimeError, ValueError) as exc:
            archive.unlink(missing_ok=True)
            if index == len(addresses) - 1:
                raise
            print(f'Primary component source failed; using fallback: {exc}', flush=True)


def extract_bundle(archive, directory, entry):
    with zipfile.ZipFile(archive) as bundle:
        manifest = json.loads(bundle.read('bundle-manifest.json'))
        for key in ('id', 'revision', 'baseFingerprint', 'platform', 'arch', 'pythonAbi', 'packages'):
            require(manifest.get(key) == entry.get(key), f'Bundle manifest mismatch: {key}')
        total = 0
        for item in bundle.infolist():
            name = item.filename
            require('\\' not in name and ':' not in name and '..' not in name.split('/'), 'Unsafe ZIP path')
            require(name == 'bundle-manifest.json' or name.startswith('site-packages/'), 'Unexpected ZIP entry')
            require(not stat.S_ISLNK(item.external_attr >> 16), 'ZIP symlinks are not supported')
            target = directory / name
            require(target.resolve().is_relative_to(directory.resolve()), 'ZIP path escapes temporary directory')
            total += item.file_size
            require(total <= entry['unpackedBytes'] + 1024 * 1024, 'ZIP exceeds declared expanded size')
            bundle.extract(item, directory)
            if os.name != 'nt' and not item.is_dir():
                target.chmod((item.external_attr >> 16) & 0o777 or 0o600)
    return directory / 'site-packages'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--runtime', type=Path, help='Staged slim runtime root (contains config/python-components.json)')
    parser.add_argument('--bundle-dir', type=Path, help='Use local bundle instead of downloading the catalog URL')
    parser.add_argument('--report', type=Path, help='JSON report; defaults to desktop/dist/component-check/<platform>/report.json')
    parser.add_argument('--require-windows-milvus-patch', action='store_true',
                        help='Reject Windows bundles without the reviewed manifest replacement fix')
    parser.add_argument('--require-macos-team-id', action='store_true',
                        help='Require valid RAG Mach-O signatures matching the running Python Team ID')
    parser.add_argument('--worker', choices=['base', 'overlay', 'milvus', 'server'], help=argparse.SUPPRESS)
    parser.add_argument('--overlay', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--data', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--logs', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--port', type=int, help=argparse.SUPPRESS)
    parser.add_argument('--without-grpc', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.worker:
        if args.runtime:
            sys.path.insert(0, str(args.runtime / 'app/algorithm'))
            source = args.runtime / 'app/algorithm/lazyllm'
            if (source / 'lazyllm').is_dir():
                sys.path.insert(0, str(source))
        if args.worker == 'base':
            base_imports(args.without_grpc)
        elif args.worker == 'overlay':
            overlay_imports(args.overlay)
        elif args.worker == 'milvus':
            milvus_roundtrip(args.overlay, args.data, args.logs)
        else:
            serve(args.overlay, args.data, args.port)
        return 0
    if not args.runtime:
        parser.error('--runtime is required')
    target_platform = 'windows' if sys.platform == 'win32' else sys.platform
    arch = {'amd64': 'amd64', 'x86_64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}.get(platform.machine().lower())
    output = (args.report or Path(__file__).resolve().parents[1] / 'dist/component-check' /
              f'{target_platform}-{arch}' / 'report.json').resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    logs = output.parent / ('run-' + time.strftime('%Y%m%d-%H%M%S') + '-' + str(os.getpid()))
    logs.mkdir()
    report = {'platform': target_platform, 'arch': arch, 'python': sys.executable,
              'runtime': str(args.runtime.resolve()), 'logs': str(logs), 'checks': [], 'passed': False}

    def check(name, action):
        print(f'RUN {name}', flush=True)
        try:
            value = action()
        except Exception as error:
            report['checks'].append({'name': name, 'passed': False, 'error': str(error)})
            raise
        report['checks'].append({'name': name, 'passed': True})
        print(f'PASS {name}', flush=True)
        return value

    try:
        def preflight():
            require(Path(sysconfig.get_path('purelib')).resolve().is_relative_to(
                (args.runtime / 'deps/python/algorithm').resolve()), 'Run using this runtime\'s algorithm Python')
            catalog = json.loads((args.runtime / 'config/python-components.json').read_text())
            entry = catalog['components']['rag']
            for key, value in [('platform', target_platform), ('arch', arch),
                               ('pythonAbi', f'cp{sys.version_info.major}{sys.version_info.minor}')]:
                require(entry[key] == value, f'Wrong {key}: catalog {entry[key]}, interpreter {value}')
            require(Path(entry['filename']).name == entry['filename'], 'Invalid bundle filename')
            report.update(filename=entry['filename'], sha256=entry['sha256'],
                          source=str(args.bundle_dir) if args.bundle_dir else entry['url'])
            return entry
        entry = check('runtime compatibility', preflight)
        if not args.bundle_dir and entry.get('catalogUrl'):
            entry = check('online tag catalog', lambda: resolve_catalog_entry(entry))
            report.update(filename=entry['filename'], sha256=entry['sha256'], source=entry['url'])
        if args.require_windows_milvus_patch:
            require(target_platform == 'windows', 'Windows patch verification requires native Windows')
        base_args = ['--runtime', str(args.runtime.resolve())]
        if 'grpcio' in entry['packages']:
            base_args.append('--without-grpc')
        check('slim base imports', lambda: run_child('base', base_args, logs))
        with tempfile.TemporaryDirectory(prefix='lazymind-component-check-') as temporary:
            root = Path(temporary)
            archive = check('bundle download/local file + SHA256', lambda: acquire_bundle(entry, root, args.bundle_dir))
            overlay = check('bundle extraction + manifest', lambda: extract_bundle(archive, root / 'payload', entry))
            if args.require_macos_team_id:
                check('macOS RAG signatures match Python Team ID', lambda: verify_macos_team_id(overlay))
            if args.require_windows_milvus_patch:
                spec = importlib.util.spec_from_file_location(
                    'windows_milvus_patch', Path(__file__).with_name('patch-windows-milvus.py'))
                patch = importlib.util.module_from_spec(spec)
                spec.loader.exec_module(patch)
                check('Windows Milvus storage fixes + RECORD', lambda: patch.verify_site(overlay))
            check('RAG overlay imports', lambda: run_child(
                'overlay', ['--overlay', str(overlay), '--runtime', str(args.runtime.resolve())], logs))
            check('Milvus insert + flush + restart + search + drop', lambda: run_child(
                'milvus', ['--overlay', str(overlay), '--data', str(root / 'milvus'), '--logs', str(logs)], logs))
        report['passed'] = True
    except Exception as error:
        report['error'] = str(error)
        print(f'FAIL {error}', file=sys.stderr, flush=True)
    finally:
        output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
        print(f'REPORT {output}', flush=True)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
