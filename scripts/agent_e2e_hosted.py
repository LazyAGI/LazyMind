#!/usr/bin/env python3
"""Bootstrap a disposable, source-built LazyMind on a GitHub-hosted VM."""
import argparse
import hashlib
import json
import os
import queue
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import threading
import time
import urllib.request
import zipfile
from types import SimpleNamespace

import agent_e2e as e2e
import agent_e2e_platform as platform

ROOT = Path(__file__).resolve().parents[1]


def run(command, *, cwd=ROOT, env=None, timeout=1800):
    print('Running ' + Path(command[0]).name + ' ' + command[1], flush=True)
    subprocess.run(command, cwd=cwd, env=env, check=True, timeout=timeout)


def paths(root):
    logs = Path(os.environ['LOCALAPPDATA']) / 'LazyMind/Logs' if platform.IS_WINDOWS else Path.home() / 'Library/Logs/LazyMind'
    return SimpleNamespace(root=root, runtime=root / 'runtime', build=root / 'build',
                           home=root / 'connector', output=root / 'results',
                           logs=logs,
                           manager=root / 'bin' / platform.executable_name('local-runtime-manager'))


def runtime_args(p):
    return ['--profile', 'local', '--repo-root', str(ROOT), '--runtime-root', str(p.runtime),
            '--build-root', str(p.build), '--resources-root', str(ROOT)]


def environment(p, create=False):
    private = p.root / 'runtime-environment.json'
    if create and not private.exists():
        values = {name: secrets.token_urlsafe(32) for name in
                  ('LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD', 'LAZYMIND_JWT_SECRET',
                   'LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'LAZYMIND_FILE_WATCHER_AGENT_TOKEN')}
        e2e.private_config(private, json.dumps(values))
    values = json.loads(private.read_text(encoding='utf-8'))
    for value in values.values():
        print('::add-mask::' + value, flush=True)
    env = dict(os.environ)
    env.update(values)
    env.update(LAZYMIND_RAG_ENABLED='false', LAZYMIND_LOCAL_ENABLE_EVO='false',
               LAZYMIND_BOOTSTRAP_ADMIN_USERNAME='ci-admin', LAZYMIND_LOCAL_UP_TIMEOUT='600',
               LAZYMIND_RUNTIME_ROOT=str(p.runtime), LAZYMIND_LOCAL_BUILD_ROOT=str(p.build),
               CGO_ENABLED='0' if platform.IS_WINDOWS else '1', PYTHONUTF8='1', PYTHONIOENCODING='utf-8')
    if (p.root / 'fonts/NotoSansSC.ttf').exists():
        env['LAZYMIND_MEME_FONT_PATH'] = str(p.root / 'fonts/NotoSansSC.ttf')
    return env


def start(p):
    p.manager.parent.mkdir(parents=True, exist_ok=True)
    install_font(p)
    env = environment(p, create=True)
    run(['go', 'build', '-o', str(p.manager), '.'], cwd=ROOT / 'local/local-runtime-manager', env=env)
    if platform.IS_WINDOWS:
        # Chat waits five minutes for Core. A cold Go build on a two-CPU VM
        # can take longer, so populate the normal compiler cache before services
        # start. The runtime manager still builds and runs its own Core binary.
        print('Preparing Windows Core before the service readiness window.', flush=True)
        run([sys.executable, str(ROOT / 'frontend/scripts/generate_upstream_model_catalog.py'),
             '--backend-out', str(ROOT / 'backend/core/config/upstream_model_catalog.json'),
             '--frontend-out', str(ROOT / 'frontend/src/modules/modelProvider/upstreamModelCatalog.json')],
            env=env, timeout=240)
        run(['go', 'build', '-buildvcs=false', '-o', str(p.manager.parent / 'core-build-check.exe'), '.'],
            cwd=ROOT / 'backend/core', env=env)
    finished = threading.Event()
    def startup_errors():
        seen = {}
        while not finished.wait(20):
            for path in p.logs.glob('*.log'):
                if path.name not in ('chat.log', 'core.log', 'auth-service.log', 'process-compose.log'):
                    continue
                try:
                    with path.open('rb') as stream:
                        stream.seek(max(0, path.stat().st_size - 16384))
                        tail = stream.read().decode('utf-8', errors='replace')
                    if tail != seen.get(str(path)) and any(marker in tail for marker in
                            ('Traceback', 'ModuleNotFoundError', 'ImportError', 'ERROR', 'failed:')):
                        seen[str(path)] = tail
                        print('Startup diagnostic: ' + path.name, flush=True)
                        for line in tail.splitlines()[-20:]:
                            print(e2e.safe_diagnostic(line), flush=True)
                except OSError:
                    continue
    monitor = threading.Thread(target=startup_errors, daemon=True)
    monitor.start()
    try:
        run([str(p.manager), 'up', *runtime_args(p)], env=env, timeout=2700)
    finally:
        finished.set()
        monitor.join(timeout=2)
    args = SimpleNamespace(output=str(p.output))
    e2e.build_cli(args)
    cli = p.output / 'bin' / platform.executable_name('lazymind')
    # The normal connector credential store obtains a real local admin session.
    # Never manufacture JWTs or copy credentials from another runtime.
    p.home.mkdir(mode=0o700, exist_ok=True)
    client = e2e.MCP(str(cli), e2e.clean_env(p.home, 'http://localhost:8090', 'codex'), timeout=120)
    try:
        listing = client.call('workflow.list')
        workflows = [{k: item.get(k) for k in ('workflow_id', 'revision_id', 'revision_no')}
                     for item in listing.get('workflows', [])
                     if item.get('workflow_id') in {case[0] for case in e2e.CASES.values()}]
        if len(workflows) != 3:
            raise RuntimeError('Expected all three built-in workflows in fresh runtime')
        for item in workflows:
            client.call('workflow.get', workflow_id=item['workflow_id'], revision_id=item['revision_id'])
    finally:
        client.close()
    # Catalog reads do not allocate an Agent Host identity. Initialize it via
    # the product's normal adapter constructor, using a separate CI Codex home.
    identity_env = e2e.clean_env(p.home, 'http://localhost:8090', 'codex')
    identity_env['CODEX_HOME'] = str(p.root / 'identity-bootstrap/codex')
    Path(identity_env['CODEX_HOME']).mkdir(parents=True, mode=0o700, exist_ok=True)
    subprocess.run([str(cli), 'internal', 'agent', 'codex', 'status'], env=identity_env,
                   stdout=subprocess.DEVNULL, check=True, timeout=60)
    if not (p.home / 'connector-host-id').is_file():
        raise RuntimeError('Normal connector initialization did not create its host identity')
    e2e.write_json(p.output / 'hosted-runtime.json', {
        'status': 'passed', 'platform': sys.platform, 'authentication': 'normal local admin-session bootstrap',
        'source_sha': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
        'workflows': workflows, 'business_cases_executed': False})
    print('Fresh LazyMind runtime and all three MCP workflow revisions verified.', flush=True)
    with open(os.environ['GITHUB_ENV'], 'a', encoding='utf-8') as stream:
        for key, value in {'HOSTED_ROOT': p.root, 'LAZYMIND_HOME': p.home,
                           'LAZYMIND_CI_RUNTIME_PROVENANCE': p.output / 'hosted-runtime.json',
                           'LAZYMIND_SERVER_URL': 'http://localhost:8090'}.items():
            stream.write(f'{key}={value}\n')


def stop(p):
    if p.manager.exists() and (p.root / 'runtime-environment.json').exists():
        run([str(p.manager), 'down', *runtime_args(p)], env=environment(p), timeout=180)


def diagnose(p):
    """Expose bounded, redacted startup errors, never the private runtime tree."""
    for path in sorted(set(p.logs.glob('*.log')) | set(p.runtime.rglob('*.log'))):
        if path.is_file():
            lines = path.read_text(encoding='utf-8', errors='replace').splitlines()[-15:]
            print(path.name, flush=True)
            for line in lines:
                print(e2e.safe_diagnostic(line), flush=True)


def install_font(p):
    from PIL import ImageFont
    destination = p.root / 'fonts'
    destination.mkdir(exist_ok=True)
    commit = '5bd87dfc525c8a0d1d8529d3684716e00c3fc59a'
    base = f'https://raw.githubusercontent.com/google/fonts/{commit}/ofl/notosanssc/'
    font = destination / 'NotoSansSC.ttf'
    with urllib.request.urlopen(base + 'NotoSansSC%5Bwght%5D.ttf', timeout=120) as response:
        data = response.read(20 * 1024 * 1024)
    if hashlib.sha256(data).hexdigest() != 'a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da':
        raise RuntimeError('Noto Sans SC font SHA-256 mismatch')
    font.write_bytes(data)
    with urllib.request.urlopen(base + 'OFL.txt', timeout=60) as response:
        (destination / 'OFL.txt').write_bytes(response.read(32768))
    rendered = ImageFont.truetype(str(font), size=48)
    if bytes(rendered.getmask('收')) == bytes(rendered.getmask('到')):
        raise RuntimeError('Chinese caption font glyph check failed')
    if platform.IS_WINDOWS:
        import ctypes
        from ctypes import wintypes
        import winreg
        installed = Path(os.environ['LOCALAPPDATA']) / 'Microsoft/Windows/Fonts/NotoSansSC.ttf'
        installed.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(font, installed)
        with winreg.CreateKey(winreg.HKEY_CURRENT_USER, r'Software\Microsoft\Windows NT\CurrentVersion\Fonts') as key:
            winreg.SetValueEx(key, 'Noto Sans SC (TrueType)', 0, winreg.REG_SZ, str(installed))
        register = ctypes.windll.gdi32.AddFontResourceExW
        register.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, ctypes.c_void_p]
        register.restype = ctypes.c_int
        if not register(str(installed), 0, None):
            raise RuntimeError('Could not register the CI Chinese font for Chromium')
    else:
        installed = Path.home() / 'Library/Fonts/NotoSansSC.ttf'
        installed.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(font, installed)
    print('Verified pinned Noto Sans SC and Chinese caption glyphs.', flush=True)


def setup_codex_windows_sandbox(p, command):
    """Use Codex's normal setup API; no unsandboxed Agent fallback."""
    home = p.root / 'codex-home'
    home.mkdir(mode=0o700, exist_ok=True)
    env = e2e.clean_env(p.home, 'http://localhost:8090')
    env['CODEX_HOME'] = str(home)
    tree = platform.ProcessTree(command + ['--config', 'windows.sandbox="elevated"',
        '--config', 'sandbox_mode="workspace-write"', '--config', 'sandbox_workspace_write.network_access=true',
        'app-server'], cwd=p.output, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    messages = queue.Queue()
    def read():
        for line in tree.process.stdout:
            try:
                messages.put(json.loads(line))
            except ValueError:
                pass
        messages.put({'error': {'message': 'Codex setup process exited'}})
    reader = threading.Thread(target=read, daemon=True)
    reader.start()
    def send(message):
        tree.process.stdin.write((json.dumps(message) + '\n').encode())
        tree.process.stdin.flush()
    def receive(*, request_id=None, method=None):
        deadline = time.monotonic() + 180
        while time.monotonic() < deadline:
            try:
                message = messages.get(timeout=max(.01, deadline - time.monotonic()))
            except queue.Empty:
                raise RuntimeError('Codex Windows sandbox setup timed out') from None
            if message.get('error'):
                raise RuntimeError('Codex Windows sandbox setup RPC failed: ' + e2e.safe_diagnostic(message['error']))
            if (request_id is not None and message.get('id') == request_id) or (method and message.get('method') == method):
                return message.get('result', message.get('params', {}))
        raise RuntimeError('Codex Windows sandbox setup timed out')
    try:
        send({'id': 1, 'method': 'initialize', 'params': {'clientInfo': {'name': 'lazymind-ci', 'version': '1'},
                                                     'capabilities': {'experimentalApi': True}}})
        receive(request_id=1)
        send({'method': 'initialized', 'params': {}})
        send({'id': 2, 'method': 'windowsSandbox/readiness', 'params': {}})
        state = receive(request_id=2)
        if state.get('status') != 'ready':
            print('Initializing the official elevated Codex Windows sandbox.', flush=True)
            send({'id': 3, 'method': 'windowsSandbox/setupStart', 'params': {'mode': 'elevated', 'cwd': str(p.output)}})
            if not receive(request_id=3).get('started'):
                raise RuntimeError('Codex Windows sandbox setup did not start')
            completion = receive(method='windowsSandbox/setupCompleted')
            if not completion.get('success'):
                raise RuntimeError('Codex Windows sandbox setup failed: ' + e2e.safe_diagnostic(completion.get('error')))
        print('Official Codex Windows sandbox setup passed.', flush=True)
    finally:
        tree.process.stdin.close()
        tree.close()
        tree.process.wait(timeout=15)
        reader.join(timeout=2)
        tree.process.stdout.close()
    return home


def install_agents(p):
    """Install pinned vendor distributions, without a desktop login dependency."""
    p.output.mkdir(parents=True, exist_ok=True)
    tools = p.root / 'agent-tools'
    tools.mkdir(exist_ok=True)
    for name in ('package.json', 'package-lock.json'):
        shutil.copy2(ROOT / 'scripts/ci-agent-tools' / name, tools / name)
    npm = shutil.which('npm.cmd' if platform.IS_WINDOWS else 'npm')
    node = shutil.which('node')
    npm_command = [node, str(Path(npm).parent / 'node_modules/npm/bin/npm-cli.js')] if platform.IS_WINDOWS else [npm]
    run([*npm_command, 'ci', '--no-audit', '--no-fund'], cwd=tools, timeout=900)
    modules = tools / 'node_modules'
    commands = {'codex': [node, str(modules / '@openai/codex/bin/codex.js')],
                'deepseek-harness': [node, str(modules / '@deepseek-ai/dsh/lib/bin.js')]}
    # These URLs and hashes are the vendor's public /v2/update metadata.
    # Keep a fixed version; do not silently follow a new latest installer.
    distributions = {
        'darwin-x64': ('zip', 'f17292ee0a9702670d739679b76d3a84a012dbd232f45c6479bf37cc00cf402c'),
        'win32-x64-user': ('exe', '38841a16a766e488eeba9adf322b92239e69cbb7a309dac7a35fee495496441c')}
    target = 'win32-x64-user' if platform.IS_WINDOWS else 'darwin-x64'
    ext, digest = distributions[target]
    url = f'https://download.codebuddy.ai/workbuddy/saas/{target}/WorkBuddy-{target}-5.11.4.44121853-0026dff0.{ext}'
    archive = p.root / ('workbuddy-download.' + ext)
    print('Downloading official WorkBuddy 5.11.4.44121853 for ' + target, flush=True)
    with urllib.request.urlopen(url, timeout=120) as response, archive.open('wb') as stream:
        shutil.copyfileobj(response, stream)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != digest:
        raise RuntimeError('Official WorkBuddy installer SHA-256 mismatch')
    buddy = p.root / 'workbuddy'
    if platform.IS_WINDOWS:
        # Verified in the vendor executable: Nullsoft.NSIS.exehead / NSIS 3.11.
        # NSIS requires /D= last; the hosted runner's temp root has no spaces.
        run([str(archive), '/S', '/D=' + str(buddy)], timeout=600)
    else:
        with zipfile.ZipFile(archive) as source:
            # Only the genuine bundled CLI is needed for headless CI.
            for entry in source.infolist():
                if '/app.asar.unpacked/cli/' in entry.filename and not entry.is_dir():
                    rel = Path(entry.filename)
                    if rel.is_absolute() or '..' in rel.parts:
                        raise RuntimeError('Unsafe WorkBuddy archive entry')
                    source.extract(entry, buddy)
    launchers = list(buddy.rglob('codebuddy'))
    launchers = [path for path in launchers if path.is_file() and path.parent.name == 'bin'
                 and (path.parent.parent / 'product.json').is_file()]
    if len(launchers) != 1:
        # Give public layout clues, without dumping app files or user data.
        print('WorkBuddy CLI candidates: ' + json.dumps([str(x.relative_to(buddy)) for x in buddy.rglob('*codebuddy*')][:20]))
        raise RuntimeError('Expected one genuine WorkBuddy bundled CLI')
    commands['workbuddy'] = [node, str(launchers[0])]
    archive.unlink()
    env = dict(os.environ)
    env.update(DSH_HOME=str(p.root / 'dsh'), WORKBUDDY_CONFIG_DIR=str(p.root / 'workbuddy-config'))
    subprocess.run(commands['deepseek-harness'] + ['--profile', 'lazymind-ci', '--from-default-profile', 'headless', '--dump-config'],
                   env=env, timeout=120, check=True, stdout=subprocess.DEVNULL)
    e2e.private_config(p.root / 'agent-commands.json', json.dumps(commands))
    versions = {name: e2e.version(command) for name, command in commands.items()}
    sandbox_home = setup_codex_windows_sandbox(p, commands['codex']) if platform.IS_WINDOWS else None
    e2e.write_json(p.output / 'installed-agents.json', {'versions': versions, 'workbuddy_distribution_sha256': digest,
                   'codex_windows_sandbox': 'elevated setup passed' if sandbox_home else 'not applicable'})
    if any(value == 'unavailable' for value in versions.values()):
        raise RuntimeError('An installed Agent could not report its version')
    with open(os.environ['GITHUB_ENV'], 'a', encoding='utf-8') as stream:
        if sandbox_home:
            stream.write(f'LAZYMIND_CI_CODEX_HOME={sandbox_home}\n')
        for key, value in {'HOSTED_ROOT': p.root, 'LAZYMIND_CI_AGENT_COMMANDS': p.root / 'agent-commands.json',
                           'DSH_HOME': p.root / 'dsh', 'WORKBUDDY_CONFIG_DIR': p.root / 'workbuddy-config'}.items():
            stream.write(f'{key}={value}\n')


def main():
    if os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted':
        raise SystemExit('This bootstrap runs only on a disposable GitHub Actions VM.')
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('start', 'stop', 'diagnose', 'install-agents'))
    parser.add_argument('--root', type=Path, default=Path(os.environ['RUNNER_TEMP']) / 'lazymind-hosted')
    args = parser.parse_args()
    root = args.root.resolve()
    if root.is_relative_to(ROOT):
        raise SystemExit('Runtime secrets and credentials must be outside the checkout.')
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    for stream in (sys.stdout, sys.stderr):
        stream.reconfigure(encoding='utf-8')
    {'start': start, 'stop': stop, 'diagnose': diagnose, 'install-agents': install_agents}[args.action](paths(root))


if __name__ == '__main__':
    main()
