"""Drive a published A -> B through the installed Desktop UI's normal update bridge.

Only on a disposable GitHub-hosted native runner. With --provision-only, verify
and install A into a fresh account. Run again without that switch to open A, set
its requested components through its public API and drive its Settings update
actions. B is never installed or launched by this script. Use Python >=3.11
and websockets>=15.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import runpy
import secrets
import socket
import subprocess
import sys
import tempfile
import time
from urllib.error import URLError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener


def require(ok, message):
    if not ok:
        raise RuntimeError(message)


def read_json(path):
    return json.loads(path.read_bytes())


def poll(description, operation, timeout=300):
    limit = time.monotonic() + timeout
    while time.monotonic() < limit:
        try:
            value = operation()
            if value:
                return value
        except (OSError, URLError, json.JSONDecodeError):
            pass  # A stopped and B is bringing up its local gateway.
        time.sleep(1)
    raise RuntimeError('Timed out waiting for ' + description)


def local_json(url, *, data=None, token=None, timeout=10):
    require(urlsplit(url).hostname == '127.0.0.1', 'Expected the locally installed application')
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = Request(url, data=None if data is None else json.dumps(data).encode(), headers=headers)
    # Send the local admin token only to the gateway itself, without a proxy or redirect.
    class NoRedirect(HTTPRedirectHandler):
        def redirect_request(self, *args):
            return None
    with build_opener(ProxyHandler({}), NoRedirect()).open(req, timeout=timeout) as response:
        return json.load(response)


def gateway_api(state):
    proxy = state['config']['localProxy']
    port = int(proxy.get('port') or proxy.get('Port'))
    require(0 < port <= 65535, 'Invalid local gateway port')
    base = f'http://127.0.0.1:{port}'
    session = local_json(base + '/_local/admin-session', data={})
    token = session.get('data', session)['token']
    def call(route, data=None, timeout=10):
        result = local_json(base + '/api/core/' + route, data=data, token=token, timeout=timeout)
        return result.get('data', result)
    return call


def check_components(api, expected):
    rows = api('system-dependencies/python')
    return {v['id'] for v in rows if v['installed']} == expected and {v['id'] for v in rows if v['active']} == expected


def evaluate(ws, expression, timeout=60):
    request_id = secrets.randbelow(1 << 30)
    ws.send(json.dumps({'id': request_id, 'method': 'Runtime.evaluate', 'params': {
        'expression': expression, 'awaitPromise': True, 'returnByValue': True}}))
    limit = time.monotonic() + timeout
    while True:
        reply = json.loads(ws.recv(timeout=max(0, limit - time.monotonic())))
        if reply.get('id') != request_id:
            continue
        require('error' not in reply and 'exceptionDetails' not in reply.get('result', {}), 'Renderer rejected the product update action')
        return reply['result']['result'].get('value')


def renderer(port, source_version):
    from websockets.exceptions import WebSocketException
    from websockets.sync.client import connect
    for page in local_json(f'http://127.0.0.1:{port}/json/list'):
        if page.get('type') != 'page' or urlsplit(page['url']).hostname not in ('localhost', '127.0.0.1', '::1'):
            continue
        address = urlsplit(page['webSocketDebuggerUrl'])
        if address.hostname != '127.0.0.1' or address.port != port or address.scheme != 'ws':
            continue
        ws = connect(page['webSocketDebuggerUrl'], open_timeout=5, close_timeout=2, proxy=None)
        try:
            status = evaluate(ws, 'window.lazymindDesktop?.updateStatus()', timeout=5)
        except (RuntimeError, TimeoutError, WebSocketException):
            status = None
        if status and status.get('currentVersion') == source_version:
            return ws
        ws.close()
    return None


def run(args):
    require(os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted' and os.environ.get('GITHUB_ACTIONS') == 'true',
            'Run only on a fresh disposable GitHub-hosted runner')
    require(sys.platform in ('darwin', 'win32'), 'Requires native macOS or Windows')
    a, b = (read_json(directory / 'release.json') for directory in (args.source, args.target))
    ca, cb = a['config'], b['config']
    expected_platform = 'darwin' if sys.platform == 'darwin' else 'windows'
    for cfg in (ca, cb):
        require(cfg['policyURL'] == args.policy_url and cfg['signingIdentity'] == args.signer and cfg['platform'] == expected_platform,
                'Published identity differs from the expected channel, signer or native runner')
        require(cfg['updaterProtocol'] == 2, 'Durable installation confirmation requires v2')
    require(ca['releaseId'] != cb['releaseId'] and ca['version'] != cb['version'], 'A and B must be distinct')
    require(ca['mode'] == cb['mode'] and ca['trustDomain'] == cb['trustDomain'], 'A/B channel mismatch')
    require(urlsplit(args.policy_url).scheme == 'https' and urlsplit(args.policy_url).hostname == 'modelscope.cn', 'Expected the public ModelScope policy')
    expected = set() if args.selected == 'none' else set(args.selected.split(','))
    require(expected <= {'rag', 'cluster'}, 'Unknown optional component')
    require(args.report.parent.is_dir() and not args.report.exists(), 'Choose a new result file in an existing directory')
    root = Path.home() / 'Library/Application Support' if sys.platform == 'darwin' else Path(os.environ['LOCALAPPDATA'])
    runtime_root = (args.runtime_root or root / 'LazyMind').resolve()
    state_file = runtime_root / 'state/runtime-state.json'
    destinations = [Path.home() / 'Applications/LazyMind.app', Path('/Applications/LazyMind.app')] if sys.platform == 'darwin' else [root / 'Programs/LazyMind/LazyMind.exe']
    app = args.app.resolve()
    require(any(app == p.resolve() for p in destinations), 'Use the installed A in its normal destination')
    if args.provision_only:
        require(not args.app.is_symlink() and not any(os.path.lexists(p) for p in destinations) and
                not runtime_root.exists() and not (root / 'LazyMindUpdater').exists(), 'Refusing to provision over an existing install or user state')
        probe = runpy.run_path(str(Path(__file__).with_name('verify-update-format.py')))
        _, package, _, _ = probe['release_inputs'](args.source, ca['mode'] == 'unsigned-test')
        if sys.platform == 'darwin':
            app.parent.mkdir(parents=True, exist_ok=True)
            with tempfile.TemporaryDirectory() as temp, probe['materialized_app'](
                    package, a['release'], Path(temp) / 'source', ca['mode'] == 'unsigned-test') as resources:
                subprocess.run(['/usr/bin/ditto', str(resources.parent.parent), str(app)], check=True, timeout=900)
        else:
            require(not app.parent.exists(), 'Refusing to install into an existing Windows application directory')
            probe['verify_windows_signature'](package, args.signer)
            require(not any(c.isspace() for c in str(app.parent)), 'NSIS scratch installation path must have no whitespace')
            probe['run_windows_installer'](package, '/S --full-install /D=' + str(app.parent), timeout=1950)
        return
    require(app.exists(), 'Provision A before starting the upgrade smoke')
    exe = app / 'Contents/MacOS/LazyMind' if sys.platform == 'darwin' else app
    helper = app / 'Contents/Resources/runtime/bin/lazymind-update-helper' if sys.platform == 'darwin' else app.parent / 'resources/runtime/bin/lazymind-update-helper.exe'
    def installed(cfg):
        result = subprocess.run([str(helper), 'verify-identity'], capture_output=True, check=True, timeout=90)
        require(json.loads(result.stdout) == {'status': 'enabled', 'config': cfg}, 'Installed helper identity differs from its release record')
    def ready(release=None):
        state = read_json(state_file)
        if state.get('overallStatus') != 'ready' or state.get('profile') != 'desktop' or state['config'].get('maintenanceMode'):
            return None
        if release:
            committed = state['config'].get('nativeCommittedTx', '')
            if (state['config'].get('nativeCommittedReleaseId') != release or len(committed) != 32 or
                    (root / 'LazyMindUpdater/native-v2/active.json').exists()):
                return None
        require(Path(state['runtimeRoot']).resolve() == runtime_root, 'Application switched runtime roots')
        return state

    installed(ca)
    descriptor = b['release']['package']
    digest = descriptor['sha256']
    require(len(digest) == 64 and all(c in '0123456789abcdef' for c in digest), 'Invalid target package digest')
    cache = root / 'LazyMindUpdater/downloads'
    extension = '.zip' if sys.platform == 'darwin' else '.exe'
    def cached(): return list(cache.glob('*/' + digest + extension))
    require(not cached(), 'A fresh download is required; B was already cached')
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    # Start A only. Its own native installer is responsible for replacing and launching B.
    subprocess.Popen([str(exe), '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=' + str(port)])
    state_a = poll('A ready', ready, 900)
    api = gateway_api(state_a)
    initial = {v['id'] for v in api('system-dependencies/python') if v['installed']}
    require(initial <= expected, 'A already has an unexpected component; prepare a fresh baseline for this selection')
    for component in sorted(expected - initial):
        api('system-dependencies/python:install', {'id': component}, timeout=1200)
    if not check_components(api, expected):
        with poll('A renderer for component restart', lambda: renderer(port, ca['version'])) as ws:
            evaluate(ws, 'window.lazymindDesktop.restartRuntime({reload:false})', 900)
        def source_selection():
            state = ready()
            if state:
                candidate = gateway_api(state)
                if check_components(candidate, expected):
                    return candidate
            return None
        api = poll('requested A components installed and active', source_selection, 900)
    name = 'Published-upgrade-smoke-' + secrets.token_hex(8)
    data = api('datasets', {'display_name': name, 'processing_level': 'stored'})
    dataset = data.get('id') or data.get('dataset_id') or data.get('name', '').removeprefix('datasets/')
    require(dataset and '/' not in dataset and api('datasets/' + dataset)['display_name'] == name, 'Source dataset was not created')
    target_id = json.dumps(cb['releaseId'])
    with poll('trusted A renderer', lambda: renderer(port, ca['version'])) as ws:
        offered = evaluate(ws, 'window.lazymindDesktop.updateCheck()', 120)
        require(offered['currentVersion'] == ca['version'] and offered['phase'] == 'available' and offered['releaseId'] == cb['releaseId'],
                'Installed A did not discover B as its next release')
        downloaded = evaluate(ws, f'window.lazymindDesktop.updateDownload({target_id})', 3600)
        require(downloaded['phase'] == 'downloaded' and downloaded['releaseId'] == cb['releaseId'], 'A did not finish downloading B')
        files = cached()
        require(len(files) == 1 and files[0].is_file() and not files[0].is_symlink() and files[0].stat().st_size == descriptor['size'], 'Invalid downloaded B cache')
        with files[0].open('rb') as package:
            require(hashlib.file_digest(package, 'sha256').hexdigest() == digest, 'A cached bytes other than published B')
        # Dispatch the same renderer action as Install. A may exit before its promise settles.
        from websockets.exceptions import ConnectionClosed
        try:
            evaluate(ws, f'void window.lazymindDesktop.updateInstall({target_id})')
        except ConnectionClosed:
            pass  # Only B's committed state and live data check below can pass the smoke.
    state_b = poll('restarted B ready with its durable commit', lambda: ready(cb['releaseId']), 2400)
    installed(cb)
    def recovered():
        live = ready(cb['releaseId'])
        if not live:
            return False
        current = gateway_api(live)
        return check_components(current, expected) and current('datasets/' + dataset)['display_name'] == name
    poll('B serving the old dataset and selected components', recovered, 1800)
    with args.report.open('x') as out:
        json.dump({'sourceReleaseId': ca['releaseId'], 'targetReleaseId': cb['releaseId'], 'downloadSha256': digest,
                   'committedTransaction': state_b['config']['nativeCommittedTx'], 'datasetId': dataset,
                   'activeComponents': sorted(expected), 'passed': True}, out)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for argument in ('source', 'target', 'app', 'report'):
        parser.add_argument('--' + argument, type=Path, required=True)
    parser.add_argument('--runtime-root', type=Path)
    parser.add_argument('--provision-only', action='store_true', help='Only install the verified A in a clean runner; do not launch B')
    parser.add_argument('--policy-url', required=True)
    parser.add_argument('--signer', required=True)
    parser.add_argument('--selected', required=True, help='Expected preinstalled A selection: none, rag, cluster or rag,cluster')
    run(parser.parse_args())
