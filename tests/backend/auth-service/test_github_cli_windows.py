"""Windows host credentials use Git's protocol without starting authorization."""
import os
import subprocess
import importlib
import json
from types import SimpleNamespace
from unittest.mock import patch

import pytest

from api import cloud_oauth as cloud_api
from core.errors import AppException, ErrorCodes
from models import CloudAuthConnection
from services.github_cli_auth import GitHubCLIAuthService
from services import github_cli_runner as runner_module
from test_github_star import ROOT, TOKEN, star_env  # noqa: F401


GIT = '/fixture/Git/cmd/git.exe'
GIT_FILL = [GIT, '-c', 'credential.interactive=false', '-c', 'core.askPass=', 'credential', 'fill']
GIT_QUERY = 'protocol=https\nhost=github.com\n\n'
MAC_GIT_GET = ['/usr/bin/git', 'credential-osxkeychain', 'get']


@pytest.fixture
def mac_absent_probe():
    # Reuse the existing fake RAM-disk/login commands, never a real Keychain or gh process.
    case = importlib.import_module('test_github_cli_runner').GitHubCLIRunnerTest()
    case.setUp()
    config = case.root / 'existing-gh'
    config.mkdir()
    (config / 'hosts.yml').write_text('github.com:\n  user: fixture-user\n')
    probe_calls = []

    def command(args, **kwargs):
        if args == ['/usr/bin/security', 'find-generic-password', '-s', 'gh:github.com', '-wa', 'fixture-user']:
            probe_calls.append(args)
            return subprocess.CompletedProcess(args, 44, '', '')
        if args == MAC_GIT_GET or args == GIT_FILL:
            assert kwargs['input'] == GIT_QUERY
            probe_calls.append(args)
            # These distinct missing-credential results are part of the two external protocols.
            return subprocess.CompletedProcess(args, 0 if args == MAC_GIT_GET else 128, '', '')
        result = case.run_command(args, **kwargs)
        if 'auth' in args and 'token' in args and case.started:
            result.stdout = TOKEN
        return result

    try:
        with patch.dict(os.environ, {
            'HOME': str(case.root), 'GH_CONFIG_DIR': str(config),
            'LAZYMIND_AUTH_CLOUD_SECRET_KEY': 'fixture-windows-encryption-key',
        }, clear=True), patch.object(runner_module.sys, 'platform', 'darwin'), \
                patch.object(runner_module.shutil, 'which', return_value=GIT), \
                patch.object(runner_module.subprocess, 'run', side_effect=command), \
                patch.object(runner_module.subprocess, 'Popen', side_effect=case.start_process):
            yield SimpleNamespace(case=case, runner=case.runner, calls=probe_calls)
    finally:
        case.doCleanups()


def test_mac_missing_credentials_preserve_the_native_get_protocol(mac_absent_probe):
    assert mac_absent_probe.runner.probe() is None
    assert mac_absent_probe.calls[-1] == MAC_GIT_GET
    assert mac_absent_probe.case.login_calls == []


@pytest.fixture
def windows_probe(tmp_path):
    host, service = tmp_path / 'host', tmp_path / 'service'
    host.mkdir()
    service.mkdir()
    git_config = host / '.gitconfig'
    git_config.write_text('[credential]\n    helper = manager\n')
    config_before = git_config.read_bytes()
    gh_config = host / 'gh'
    gh_config.mkdir()
    (gh_config / 'hosts.yml').write_text('invalid: [yaml')
    runner = runner_module.GitHubCLIRunner(
        binary_path=tmp_path / 'unused-gh.exe', expected_sha256='', runtime_dir=tmp_path / 'state',
    )
    probe = SimpleNamespace(
        runner=runner, host=host, service=service, calls=[], error=None, returncode=0,
        stdout=f'username=fixture-user\npassword={TOKEN}\n', stderr='',
    )

    def command(args, **kwargs):
        assert args == GIT_FILL
        assert kwargs['input'] == GIT_QUERY
        assert not kwargs.get('shell')
        probe.calls.append((args, kwargs))
        if probe.error:
            raise probe.error
        return subprocess.CompletedProcess(args, probe.returncode, probe.stdout, probe.stderr)

    environment = {
        'HOME': str(service), 'USERPROFILE': str(host), 'LAZYMIND_HOST_HOME': str(host),
        'GH_CONFIG_DIR': str(gh_config), 'GIT_ASKPASS': 'fixture-askpass', 'SSH_ASKPASS': 'fixture-ssh-askpass',
        'GIT_TERMINAL_PROMPT': '1', 'GCM_INTERACTIVE': '1',
        'LAZYMIND_AUTH_CLOUD_SECRET_KEY': 'fixture-windows-encryption-key',
    }
    with patch.dict(os.environ, environment, clear=True), \
            patch.object(runner_module.sys, 'platform', 'win32'), \
            patch.object(runner_module.shutil, 'which', return_value=GIT) as which, \
            patch.object(runner_module.subprocess, 'run', side_effect=command), \
            patch.object(runner_module.subprocess, 'Popen') as login:
        probe.which, probe.login = which, login
        yield probe
        login.assert_not_called()
    assert git_config.read_bytes() == config_before


@pytest.mark.parametrize('host_marker', [True, False])
def test_windows_reuses_git_credentials_without_mac_commands(windows_probe, host_marker):
    if not host_marker:
        os.environ.pop('LAZYMIND_HOST_HOME')
    before = dict(os.environ)
    assert windows_probe.runner.probe() == {'access_token': TOKEN}
    assert dict(os.environ) == before
    windows_probe.which.assert_called_once_with('git')
    assert len(windows_probe.calls) == 1
    _, options = windows_probe.calls[0]
    assert options['timeout'] == 35
    assert options['capture_output'] is True and options['text'] is True
    child_env = options['env']
    assert child_env['HOME'] == child_env['USERPROFILE'] == str(windows_probe.host)
    assert child_env['GIT_TERMINAL_PROMPT'] == '0'
    assert child_env['GCM_INTERACTIVE'] == '0'
    assert 'GIT_ASKPASS' not in child_env and 'SSH_ASKPASS' not in child_env


@pytest.mark.parametrize('name', ['GH_TOKEN', 'GITHUB_TOKEN'])
def test_windows_environment_token_precedes_git(windows_probe, name):
    os.environ[name] = TOKEN
    before = dict(os.environ)
    assert windows_probe.runner.probe() == {'access_token': TOKEN}
    assert dict(os.environ) == before
    assert windows_probe.calls == []
    windows_probe.which.assert_not_called()


def test_windows_missing_git_is_not_a_login_request(windows_probe):
    windows_probe.which.return_value = None
    with pytest.raises(runner_module.GitHubCLIError) as error:
        windows_probe.runner.probe()
    assert error.value.code == 'GITHUB_CREDENTIAL_UNREADABLE'
    assert windows_probe.calls == []


@pytest.mark.parametrize('returncode,stdout', [
    (0, ''), (128, ''), (0, 'malformed response\n'), (0, 'username=fixture-user\n'), (0, 'password=\n'),
])
def test_windows_missing_or_invalid_credentials_never_start_login(windows_probe, returncode, stdout, caplog):
    windows_probe.returncode, windows_probe.stdout = returncode, stdout
    windows_probe.stderr = TOKEN
    if returncode == 0 and not stdout:
        assert windows_probe.runner.probe() is None
    else:
        with pytest.raises(runner_module.GitHubCLIError) as error:
            windows_probe.runner.probe()
        assert error.value.code == 'GITHUB_CREDENTIAL_UNREADABLE'
        assert TOKEN not in str(error.value)
    assert TOKEN not in caplog.text
    assert len(windows_probe.calls) == 1


@pytest.mark.parametrize('failure', [OSError(TOKEN), subprocess.TimeoutExpired('fixture-git', 35, stderr=TOKEN)])
def test_windows_command_errors_keep_the_existing_safe_timeout_boundary(windows_probe, failure, caplog):
    windows_probe.error = failure
    with pytest.raises(runner_module.GitHubCLIError) as error:
        windows_probe.runner.probe()
    assert error.value.code == 'GITHUB_CLI_UNAVAILABLE'
    assert TOKEN not in str(error.value) and TOKEN not in caplog.text


class QueuedExecutor:
    def __init__(self):
        self.pending = []

    def submit(self, function, *args):
        self.pending.append((function, args))

    def run(self):
        function, args = self.pending.pop(0)
        function(*args)


@pytest.fixture
def windows_session(star_env, windows_probe, monkeypatch):  # noqa: F811
    provider = importlib.import_module('services.providers.github_oauth_provider')
    monkeypatch.setattr(provider.request, 'urlopen', star_env.github)
    executor = QueuedExecutor()
    service = GitHubCLIAuthService(
        runner=windows_probe.runner, connections=star_env.connections, executor=executor,
        host_credentials_enabled=True, web_authorization_enabled=False,
    )
    return SimpleNamespace(service=service, executor=executor, probe=windows_probe, env=star_env)


def test_windows_host_only_connection_reuses_verified_credentials(windows_session):
    fixture = windows_session
    pending = fixture.service.start(owner_user_id='star-owner', attempt_id='fixture-windows-connection')
    fixture.executor.run()
    result = fixture.service.get(pending['session_id'], owner_user_id='star-owner')
    assert result['status'] == 'COMPLETED', result
    assert result['connection_id']
    assert TOKEN not in json.dumps([pending, result])
    with fixture.env.sessions() as db:
        row = db.query(CloudAuthConnection).one()
        assert row.owner_user_id == 'star-owner' and row.status == 'ACTIVE'
        assert TOKEN not in row.auth_state_ciphertext
    assert [(method, path) for method, path, _ in fixture.env.github.calls] == [('GET', '/user')]
    assert fixture.env.github.writes() == []


@pytest.mark.parametrize('returncode,code', [(0, 'GITHUB_CLI_DISABLED'), (128, 'GITHUB_CREDENTIAL_UNREADABLE')])
def test_windows_host_only_missing_credentials_cannot_fall_back_to_authorization(windows_session, returncode, code):
    fixture = windows_session
    fixture.probe.stdout = ''
    fixture.probe.returncode = returncode
    pending = fixture.service.start(owner_user_id='star-owner', attempt_id='fixture-windows-missing')
    fixture.executor.run()
    result = fixture.service.get(pending['session_id'], owner_user_id='star-owner')
    assert result['status'] == 'FAILED'
    assert result['error_code'] == code
    assert 'user_code' not in result and 'verification_uri' not in result
    assert fixture.env.github.calls == []
    with fixture.env.sessions() as db:
        assert db.query(CloudAuthConnection).count() == 0


def test_mac_missing_credentials_can_continue_with_explicit_authorization(
    star_env, mac_absent_probe, monkeypatch,  # noqa: F811
):
    provider = importlib.import_module('services.providers.github_oauth_provider')
    monkeypatch.setattr(provider.request, 'urlopen', star_env.github)
    executor = QueuedExecutor()
    service = GitHubCLIAuthService(
        runner=mac_absent_probe.runner, connections=star_env.connections, executor=executor,
        host_credentials_enabled=True, web_authorization_enabled=True,
    )
    pending = service.start(owner_user_id='star-owner', attempt_id='fixture-mac-explicit-connection')
    executor.run()
    result = service.get(pending['session_id'], owner_user_id='star-owner')
    assert result['status'] == 'COMPLETED', result
    assert mac_absent_probe.calls[-1] == MAC_GIT_GET
    assert len(mac_absent_probe.case.login_calls) == 1
    assert [(method, path) for method, path, _ in star_env.github.calls] == [('GET', '/user')]
    assert star_env.github.writes() == []
    with star_env.sessions() as db:
        assert db.query(CloudAuthConnection).count() == 1


def test_windows_host_only_explicit_web_request_is_rejected_before_probe(windows_session):
    fixture = windows_session
    with pytest.raises(AppException) as error:
        fixture.service.start(owner_user_id='star-owner', attempt_id='fixture-windows-web', force_web=True)
    assert error.value.code == ErrorCodes.GITHUB_CLI_DISABLED[1]
    assert fixture.executor.pending == [] and fixture.probe.calls == []
    assert fixture.env.github.calls == []


def test_windows_capability_uses_real_probe_and_read_only_verification(windows_session, monkeypatch):
    fixture = windows_session
    monkeypatch.setattr(cloud_api, 'github_cli_auth_service', fixture.service)
    response = fixture.env.client.get(ROOT + '/local')
    assert response.status_code == 200
    assert response.json()['data'] == {'repository': 'LazyAGI/LazyMind', 'eligible': True}
    assert 'no-store' in response.headers['cache-control']
    assert TOKEN not in response.text
    assert [(method, path) for method, path, _ in fixture.env.github.calls] == [
        ('GET', '/user'), ('GET', '/repos/LazyAGI/LazyMind'),
    ]
    assert fixture.env.github.writes() == []
    assert fixture.executor.pending == []
    with fixture.env.sessions() as db:
        assert db.query(CloudAuthConnection).count() == 0


@pytest.mark.parametrize('platform,mode,workers,container,host,web', [
    ('win32', 'local', '1', '0', True, False),
    ('win32', 'remote', '1', '0', False, False),
    ('win32', 'local', '2', '0', False, False),
    ('darwin', 'local', '1', '0', True, True),
    ('darwin', 'local', '2', '0', False, False),
    ('darwin', 'remote', '1', '0', False, False),
    ('linux', 'local', '1', '1', False, True),
    ('linux', 'local', '1', '0', False, False),
])
def test_runtime_enables_windows_only_for_local_single_worker(
    tmp_path, platform, mode, workers, container, host, web,
):
    module = importlib.import_module('services.github_cli_auth')
    try:
        with patch.dict(os.environ, {
            'LAZYMIND_RUNTIME_MODE': mode, 'WEB_CONCURRENCY': workers,
            'LAZYMIND_GITHUB_CLI_CONTAINER_ENABLED': container,
            'LAZYMIND_GITHUB_CLI_PATH': str(tmp_path / 'unused-gh'),
            'LAZYMIND_STATE_SQLITE_DIR': str(tmp_path / 'state'),
        }, clear=True), patch.object(module.sys, 'platform', platform):
            configured = importlib.reload(module).github_cli_auth_service
            assert configured.host_credentials_enabled is host
            assert configured.web_authorization_enabled is web
    finally:
        importlib.reload(module)
