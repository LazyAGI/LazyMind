"""Finite background execution: real service/DB/HTTP boundary, controlled clock."""
import base64
import hashlib
import hmac
import io
import json
import threading
from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta
from urllib.error import HTTPError

import pytest
from core.cloud_crypto import encrypt_json
from models import CloudAuthConnection, User
from services.github_cli_runner import GitHubCLIError
from test_github_star import STAR_PATH
from test_github_star_deferred import (
    KEY,
    LOCAL_BODY,
    OWNER,
    REPO,
    START,
    TOKEN,
    as_utc,
    current,
    intent,
    require_service,
)
from test_github_star_deferred import deferred_env as _shared_deferred_env  # noqa: F401


@pytest.fixture
def deferred_env(request):
    return request.getfixturevalue('_shared_deferred_env')


def begin(env, **kwargs):
    service = require_service(env)
    result = service.receive(owner_user_id=str(OWNER), source='local', confirmed=True, **kwargs)
    return service, intent(result)


def test_home_failure_then_company_next_due_opportunity(deferred_env):
    env = deferred_env
    service, accepted = begin(env)
    env.github.offline = True
    service.run_due_once()
    failed = current(env)
    assert failed['status'] == 'pending' and failed['remote_state'] == 'unknown'
    assert failed['attempt_count'] == 1
    assert as_utc(failed['next_attempt_at']) == START + timedelta(hours=6)
    first_calls = len(env.github.calls)
    env.clock.advance(hours=2)
    env.github.offline = False
    service.run_due_once()
    assert len(env.github.calls) == first_calls
    env.clock.advance(hours=4)
    service.run_due_once()
    completed = current(env)
    assert completed['intent_id'] == accepted['intent_id']
    assert completed['status'] == 'succeeded' and completed['remote_state'] == 'starred'
    assert completed['attempt_count'] == 2
    assert len(env.github.writes()) == 1
    env.github.starred = False
    env.clock.advance(days=10)
    calls = len(env.github.calls)
    service.run_due_once()
    assert len(env.github.calls) == calls


@pytest.mark.parametrize('failure', ['normal_failure', 'crash'])
def test_rebuilding_executor_keeps_budget_and_does_not_catch_up_missed_slots(deferred_env, monkeypatch, failure):
    env = deferred_env
    service, original = begin(env)
    env.github.offline = True

    if failure == 'crash':
        class ProcessExit(BaseException):
            pass

        def crash_before_response(*args, **kwargs):
            assert env.row()['attempt_count'] == 1
            assert as_utc(env.row()['next_attempt_at']) == START + timedelta(hours=6)
            raise ProcessExit()

        monkeypatch.setattr('services.github_star_service.request.urlopen', crash_before_response)
        with pytest.raises(ProcessExit):
            service.run_due_once()
        monkeypatch.setattr('services.github_star_service.request.urlopen', env.github)
    else:
        service.run_due_once()

    env.clock.advance(hours=2)
    restarted = env.build()
    restarted.run_due_once()
    assert current(env)['attempt_count'] == 1
    env.clock.advance(hours=23)
    restarted.run_due_once()
    restored = current(env)
    assert restored['attempt_count'] == 2
    assert restored['created_at'] == original['created_at']
    assert restored['expires_at'] == original['expires_at']
    assert as_utc(restored['next_attempt_at']) == env.clock() + timedelta(hours=6)
    restarted.run_due_once()
    assert current(env)['attempt_count'] == 2


def test_concurrent_receive_and_execution_preserve_one_intent_and_one_cycle(deferred_env):
    env = deferred_env
    service = require_service(env)
    with ThreadPoolExecutor(max_workers=2) as pool:
        submissions = [pool.submit(service.receive, owner_user_id=str(OWNER), **LOCAL_BODY) for _ in range(2)]
        received = [intent(future.result(timeout=5)) for future in submissions]
    assert received[0]['intent_id'] == received[1]['intent_id']
    env.github.release_put = threading.Event()
    with ThreadPoolExecutor(max_workers=2) as pool:
        first = pool.submit(service.run_due_once)
        try:
            assert env.github.put_entered.wait(2), 'cycle did not reach controlled HTTP PUT'
            second = pool.submit(service.run_due_once)
        finally:
            env.github.release_put.set()
        first.result(timeout=5)
        second.result(timeout=5)
    assert current(env)['attempt_count'] == 1
    assert len(env.github.writes()) == 1


@pytest.mark.parametrize('state', ['pending', 'needs_attention', 'cancelled', 'exhausted', 'succeeded'])
def test_every_history_state_is_idempotent_without_new_probe_or_budget(deferred_env, state):
    env = deferred_env
    if state == 'needs_attention':
        env.probe.return_value = None
    service, accepted = begin(env)
    if state == 'cancelled':
        service.cancel(owner_user_id=str(OWNER), intent_id=accepted['intent_id'])
    elif state == 'exhausted':
        env.clock.advance(days=7)
        service.run_due_once()
    elif state == 'succeeded':
        service.run_due_once()
    before = current(env)
    assert before['status'] == state
    env.clock.advance(days=10)
    probes, calls = env.probe.call_count, len(env.github.calls)
    repeated = intent(service.receive(owner_user_id=str(OWNER), **LOCAL_BODY))
    for field in ('intent_id', 'status', 'created_at', 'expires_at', 'attempt_count', 'next_attempt_at'):
        assert repeated[field] == before[field]
    assert (env.probe.call_count, len(env.github.calls)) == (probes, calls)


def test_first_attempt_is_counted_and_twenty_eight_failures_stop_permanently(deferred_env):
    env = deferred_env
    service, original = begin(env)
    env.github.offline = True
    for index in range(28):
        env.clock.value = START + timedelta(hours=6 * index)
        service.run_due_once()
        assert current(env)['attempt_count'] == index + 1
    exhausted = current(env)
    assert exhausted['status'] == 'exhausted' and exhausted['remote_state'] == 'unknown'
    calls, probes = len(env.github.calls), env.probe.call_count
    env.clock.advance(days=14)
    env.build().run_due_once()
    repeated = intent(service.receive(owner_user_id=str(OWNER), **LOCAL_BODY))
    assert repeated['intent_id'] == original['intent_id']
    assert repeated['created_at'] == original['created_at']
    assert repeated['expires_at'] == original['expires_at']
    assert repeated['attempt_count'] == 28 and repeated['status'] == 'exhausted'
    assert (len(env.github.calls), env.probe.call_count) == (calls, probes)


def test_deadline_before_first_execution_does_not_probe_or_call_github(deferred_env):
    env = deferred_env
    service, _ = begin(env)
    probes = env.probe.call_count
    env.clock.advance(days=7)
    service.run_due_once()
    assert current(env)['status'] == 'exhausted'
    assert current(env)['attempt_count'] == 0
    assert env.github.calls == [] and env.probe.call_count == probes


@pytest.mark.parametrize('after_response', [1, 2, 3, 4])
def test_every_http_boundary_stops_at_deadline_including_confirmation_get(deferred_env, after_response):
    env = deferred_env
    service, _ = begin(env)
    env.clock.value = START + timedelta(days=7, seconds=-1)

    def expire(count):
        if count == after_response:
            env.clock.value = START + timedelta(days=7)

    env.github.after_response = expire
    service.run_due_once()
    assert len(env.github.calls) == after_response
    stopped = current(env)
    assert stopped['status'] == 'exhausted'
    assert stopped['remote_state'] == 'unknown'
    assert [path for _, path, _ in env.github.calls] == [
        '/user', '/repos/' + REPO, STAR_PATH, STAR_PATH,
    ][:after_response]
    if after_response == 4:
        assert env.github.starred is True  # Applied PUT is still not verified success.
        assert len(env.github.writes()) == 1


@pytest.mark.parametrize('after_response', [1, 2, 3, 4])
def test_cancel_between_http_responses_blocks_all_later_gets_and_puts(deferred_env, after_response):
    env = deferred_env
    service, accepted = begin(env)

    entered, release = threading.Event(), threading.Event()

    def hold_response(count):
        if count == after_response:
            entered.set()
            assert release.wait(5), 'test did not release controlled HTTP response'

    env.github.after_response = hold_response
    with ThreadPoolExecutor(max_workers=2) as pool:
        running = pool.submit(service.run_due_once)
        try:
            assert entered.wait(2), 'cycle did not reach controlled HTTP boundary'
            cancelled = pool.submit(service.cancel, owner_user_id=str(OWNER), intent_id=accepted['intent_id'])
            assert intent(cancelled.result(timeout=2))['status'] == 'cancelled'
        finally:
            release.set()
        running.result(timeout=5)
    assert len(env.github.calls) == after_response
    assert current(env)['status'] == 'cancelled'
    assert current(env)['remote_state'] == 'unknown'
    env.clock.advance(hours=6)
    service.run_due_once()
    assert len(env.github.calls) == after_response


def test_twenty_eighth_valid_cycle_can_complete_all_five_http_requests(deferred_env):
    env = deferred_env
    service, _ = begin(env)
    env.github.offline = True
    for index in range(27):
        env.clock.value = START + timedelta(hours=index * 6)
        service.run_due_once()
    env.github.offline = False
    env.github.calls.clear()
    env.clock.value = START + timedelta(hours=162)
    service.run_due_once()
    assert current(env)['attempt_count'] == 28
    assert current(env)['status'] == 'succeeded'
    assert current(env)['remote_state'] == 'starred'
    assert len(env.github.calls) == 5


def test_existing_star_finishes_without_put(deferred_env):
    env = deferred_env
    service, _ = begin(env)
    env.github.starred = True
    service.run_due_once()
    assert current(env)['status'] == 'succeeded'
    assert current(env)['remote_state'] == 'starred'
    assert env.github.writes() == []


def test_unknown_applied_write_is_checked_before_any_later_put(deferred_env):
    env = deferred_env
    service, _ = begin(env)
    env.github.put_error = TimeoutError('fixture lost PUT response')
    env.github.apply_before_error = True
    env.github.fail_read_after_put = True
    service.run_due_once()
    assert current(env)['status'] == 'pending'
    assert current(env)['remote_state'] == 'unknown'
    assert len(env.github.writes()) == 1
    env.github.put_error = None
    env.github.fail_read_after_put = False
    env.clock.advance(hours=6)
    service.run_due_once()
    assert current(env)['status'] == 'succeeded'
    assert len(env.github.writes()) == 1


@pytest.mark.parametrize('status,headers', [
    (429, {'Retry-After': '36000'}),
    (403, {'X-RateLimit-Remaining': '0', 'X-RateLimit-Reset': str(int(START.timestamp()) + 36000)}),
])
def test_rate_limit_later_than_six_hours_survives_restart(deferred_env, status, headers):
    env = deferred_env
    service, _ = begin(env)
    env.github.get_error = HTTPError('https://api.github.com' + STAR_PATH, status, 'fixture', headers, io.BytesIO(b'{}'))
    service.run_due_once()
    assert current(env)['attempt_count'] == 1
    assert as_utc(current(env)['rate_limit_not_before']) >= START + timedelta(hours=10)
    calls = len(env.github.calls)
    env.clock.advance(hours=6)
    env.build().run_due_once()
    assert len(env.github.calls) == calls
    env.github.get_error = None
    env.clock.advance(hours=4)
    env.build().run_due_once()
    assert current(env)['status'] == 'succeeded'
    assert current(env)['attempt_count'] == 2


def test_rate_limit_beyond_deadline_exhausts_without_another_request(deferred_env):
    env = deferred_env
    service, _ = begin(env)
    env.github.get_error = HTTPError('https://api.github.com' + STAR_PATH, 429, 'fixture',
                                     {'Retry-After': str(8 * 24 * 3600)}, io.BytesIO(b'{}'))
    service.run_due_once()
    assert current(env)['status'] == 'exhausted'
    calls = len(env.github.calls)
    env.clock.advance(days=8)
    env.build().run_due_once()
    assert len(env.github.calls) == calls


@pytest.mark.parametrize('status', [401, 403])
def test_github_auth_or_permission_failure_stops_without_another_cycle(deferred_env, status):
    env = deferred_env
    service, original = begin(env)
    env.github.repo_error = HTTPError('https://api.github.com/repos/' + REPO, status, 'fixture', {}, io.BytesIO(b'{}'))
    service.run_due_once()
    assert current(env)['status'] == 'needs_attention'
    assert env.github.writes() == []
    calls, probes = len(env.github.calls), env.probe.call_count
    env.clock.advance(hours=6)
    service.run_due_once()
    repeated = intent(service.receive(owner_user_id=str(OWNER), **LOCAL_BODY))
    assert repeated['intent_id'] == original['intent_id'] and repeated['status'] == 'needs_attention'
    assert (len(env.github.calls), env.probe.call_count) == (calls, probes)


@pytest.mark.parametrize('scope', [None, 'read:user'])
def test_background_cannot_write_with_missing_or_read_only_github_scope(deferred_env, monkeypatch, scope):
    env = deferred_env
    service, _ = begin(env)

    def github(req, *args, **kwargs):
        response = env.github(req, *args, **kwargs)
        if req.full_url.endswith('/user'):
            if scope is None:
                response.headers.pop('X-OAuth-Scopes', None)
            else:
                response.headers['X-OAuth-Scopes'] = scope
        return response

    monkeypatch.setattr('services.github_star_service.request.urlopen', github)
    service.run_due_once()
    assert current(env)['status'] == 'needs_attention'
    assert env.github.writes() == []


@pytest.mark.parametrize('change', ['disabled', 'permission_removed', 'deleted'])
def test_background_rechecks_real_owner_status_and_permissions(deferred_env, change):
    env = deferred_env
    service, _ = begin(env)
    if change == 'permission_removed':
        env.set_permissions(['model.read'])
    else:
        with env.sessions() as db:
            user = db.get(User, OWNER)
            if change == 'disabled':
                user.disabled = True
            else:
                db.delete(user)
            db.commit()
    probes = env.probe.call_count
    service.run_due_once()
    assert current(env)['status'] == 'needs_attention'
    assert env.github.calls == [] and env.probe.call_count == probes


def test_low_entropy_input_is_bound_with_purpose_separated_hmac(deferred_env, caplog):
    env = deferred_env
    env.probe.return_value = {'access_token': 'short-fixture-password'}
    _, accepted = begin(env)
    private = env.row()
    subkey = hmac.new(base64.urlsafe_b64decode(KEY), b'lazymind/github-star-intent/key/v1', hashlib.sha256).digest()
    expected = hmac.new(subkey, b'short-fixture-password', hashlib.sha256).hexdigest()
    assert private['credential_fingerprint'] == expected
    assert hashlib.sha256(b'short-fixture-password').hexdigest() not in json.dumps(private, default=str)
    assert 'short-fixture-password' not in json.dumps(private, default=str)
    assert 'short-fixture-password' not in json.dumps(accepted) and 'short-fixture-password' not in caplog.text
    assert 'credential_fingerprint' not in accepted


@pytest.mark.parametrize('key', [None, 'dev-ragscan-secret-key-change-me'])
def test_missing_or_public_key_keeps_only_non_executable_intent(deferred_env, monkeypatch, key):
    env = deferred_env
    if key is None:
        monkeypatch.delenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY')
    else:
        monkeypatch.setenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', key)
    service, accepted = begin(env)
    assert accepted['status'] == 'needs_attention'
    assert not env.row().get('credential_fingerprint')
    env.probe.return_value = {'access_token': 'later-other-credential'}
    monkeypatch.setenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', KEY)
    calls = env.probe.call_count
    env.clock.advance(hours=6)
    service.run_due_once()
    assert current(env)['status'] == 'needs_attention'
    assert env.probe.call_count == calls and env.github.calls == []


@pytest.mark.parametrize('change', ['token', 'key', 'missing_key'])
def test_binding_change_pauses_and_restoring_it_cannot_revive_intent(deferred_env, monkeypatch, change):
    env = deferred_env
    service, _ = begin(env)
    if change == 'token':
        env.probe.return_value = {'access_token': 'different-fixture-token'}
    elif change == 'key':
        monkeypatch.setenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', base64.urlsafe_b64encode(b'x' * 32).decode())
    else:
        monkeypatch.delenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY')
    service.run_due_once()
    assert current(env)['status'] == 'needs_attention'
    assert env.github.calls == []
    env.probe.return_value = {'access_token': TOKEN}
    monkeypatch.setenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', KEY)
    env.clock.advance(hours=6)
    service.run_due_once()
    assert current(env)['status'] == 'needs_attention'
    assert env.github.calls == []


def test_local_account_verified_before_failed_star_lookup_remains_bound(deferred_env):
    env = deferred_env
    service, _ = begin(env)
    env.github.get_error = TimeoutError('fixture Star GET unavailable after identity verification')
    service.run_due_once()
    assert current(env)['account_id'] == '10001'
    assert current(env)['status'] == 'pending'
    env.github.get_error = None
    env.github.account_id = '20002'
    env.clock.advance(hours=6)
    service.run_due_once()
    assert current(env)['status'] == 'needs_attention'
    assert env.github.writes() == []


@pytest.mark.parametrize('failure', [None, 'GITHUB_ACCOUNT_SELECTION_REQUIRED', 'GITHUB_CREDENTIAL_UNREADABLE'])
def test_uncaptured_credential_is_never_filled_from_later_default_account(deferred_env, failure):
    env = deferred_env
    env.probe.return_value = None
    if failure:
        env.probe.side_effect = GitHubCLIError(failure)
    service, original = begin(env)
    assert original['status'] == 'needs_attention'
    env.probe.side_effect = None
    env.probe.return_value = {'access_token': TOKEN}
    probes = env.probe.call_count
    env.clock.advance(hours=6)
    service.run_due_once()
    repeated = intent(service.receive(owner_user_id=str(OWNER), **LOCAL_BODY))
    assert repeated['intent_id'] == original['intent_id'] and repeated['status'] == 'needs_attention'
    assert env.probe.call_count == probes and env.github.calls == []


def test_initial_probe_abort_leaves_independently_committed_non_executable_history(deferred_env):
    env = deferred_env
    service = require_service(env)

    class CaptureInterrupted(BaseException):
        pass

    def interrupted_probe():
        # A second DB connection must see a committed record before capture.
        saved = env.row()
        assert saved['status'] == 'needs_attention'
        assert not saved.get('credential_fingerprint')
        assert saved['attempt_count'] == 0
        assert TOKEN not in json.dumps(saved, default=str)
        assert KEY not in json.dumps(saved, default=str)
        raise CaptureInterrupted()

    env.probe.side_effect = interrupted_probe
    with pytest.raises(CaptureInterrupted):
        service.receive(owner_user_id=str(OWNER), **LOCAL_BODY)
    saved = env.row()
    env.probe.side_effect = None
    env.probe.return_value = {'access_token': 'later-credential-not-covered-by-original-consent'}
    probes = env.probe.call_count
    env.clock.advance(hours=6)
    restarted = env.build()
    restarted.run_due_once()
    repeated = intent(restarted.receive(owner_user_id=str(OWNER), **LOCAL_BODY))
    assert repeated['intent_id'] == saved['intent_id']
    assert repeated['status'] == 'needs_attention' and repeated['attempt_count'] == 0
    assert not env.row().get('credential_fingerprint')
    assert env.probe.call_count == probes and env.github.calls == []


def test_owned_active_saved_connection_completes_without_default_local_probe(deferred_env):
    env = deferred_env
    service = require_service(env)
    env.seed()
    accepted = intent(service.receive(owner_user_id=str(OWNER), source='connection', confirmed=True,
                                      connection_id='conn-deferred', expected_account_id='10001'))
    assert accepted['connection_id'] == 'conn-deferred' and accepted['account_id'] == '10001'
    assert env.github.calls == []
    service.run_due_once()
    assert current(env)['status'] == 'succeeded'
    assert current(env)['remote_state'] == 'starred'
    assert [method for method, path, _ in env.github.calls if path == STAR_PATH] == ['GET', 'PUT', 'GET']
    env.probe.assert_not_called()


def test_same_saved_account_can_rotate_its_existing_connection_token(deferred_env, monkeypatch):
    env = deferred_env
    service = require_service(env)
    env.seed()
    service.receive(owner_user_id=str(OWNER), source='connection', confirmed=True,
                    connection_id='conn-deferred', expected_account_id='10001')
    rotated = 'fixture-rotated-token-for-the-same-account'
    with env.sessions() as db:
        row = db.get(CloudAuthConnection, 'conn-deferred')
        row.auth_state_ciphertext = encrypt_json({'token_source': 'github_cli', 'access_token': rotated})
        db.commit()

    def github(req, *args, **kwargs):
        method, path = req.get_method(), req.full_url.removeprefix('https://api.github.com')
        assert req.full_url.startswith('https://api.github.com/')
        assert req.get_header('Authorization') == 'Bearer ' + rotated
        env.github.calls.append((method, path, req))
        headers = {'X-OAuth-Scopes': 'repo'}
        if method == 'GET' and path == '/user':
            return env.github.response(200, {'id': 10001, 'login': 'fixture-user'}, headers)
        if method == 'GET' and path == '/repos/' + REPO:
            return env.github.response(200, {'full_name': REPO}, headers)
        if path == STAR_PATH and method == 'PUT':
            env.github.starred = True
            return env.github.response(204, {}, headers)
        assert path == STAR_PATH and method == 'GET'
        if not env.github.starred:
            raise HTTPError(req.full_url, 404, 'fixture', headers, io.BytesIO(b'{}'))
        return env.github.response(204, {}, headers)

    monkeypatch.setattr('services.github_star_service.request.urlopen', github)
    service.run_due_once()
    assert current(env)['status'] == 'succeeded' and current(env)['account_id'] == '10001'
    assert len(env.github.writes()) == 1
    env.probe.assert_not_called()


def test_deleted_saved_connection_keeps_history_and_cannot_fall_back_to_local_token(deferred_env):
    env = deferred_env
    service = require_service(env)
    env.seed()
    accepted = intent(service.receive(owner_user_id=str(OWNER), source='connection', confirmed=True,
                                      connection_id='conn-deferred', expected_account_id='10001'))
    with env.sessions() as db:
        db.delete(db.get(CloudAuthConnection, 'conn-deferred'))
        db.commit()
    service.run_due_once()
    stopped = current(env)
    assert stopped['intent_id'] == accepted['intent_id'] and stopped['status'] == 'needs_attention'
    assert stopped['source'] == 'connection'
    assert env.row()['intent_id'] == accepted['intent_id']
    env.probe.assert_not_called()
    assert env.github.calls == []


@pytest.mark.parametrize('change', ['revoked', 'stored_account', 'actual_account'])
def test_saved_connection_identity_cannot_change_under_background_consent(deferred_env, change):
    env = deferred_env
    service = require_service(env)
    env.seed()
    service.receive(owner_user_id=str(OWNER), source='connection', confirmed=True,
                    connection_id='conn-deferred', expected_account_id='10001')
    with env.sessions() as db:
        row = db.get(CloudAuthConnection, 'conn-deferred')
        if change == 'revoked':
            row.status = 'REVOKED'
        elif change == 'stored_account':
            row.provider_account_id = '20002'
        db.commit()
    if change == 'actual_account':
        env.github.account_id = '20002'
    service.run_due_once()
    assert current(env)['status'] == 'needs_attention'
    assert env.github.writes() == []
    env.probe.assert_not_called()
