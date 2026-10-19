"""Real HTTP adapter/worker behavior with synthetic provider responses only."""
import copy
import pickle

import httpx
import pytest

from channel_gateway.wechat.client import WeChatClient
from channel_gateway.wechat.domain import WeChatRejectedError

from test_notification_delivery import OneIteration, enqueue, notification, outbox


def send_text(client):
    client.send_text(base_url='https://wechat.test.invalid', token='private-token',
                     to_user_id='private-recipient', context_token='private-context',
                     text='private-body', client_id='synthetic-client-id', run_id='synthetic-run-id')


@pytest.mark.parametrize('payload', [
    {'ret': 0, 'errcode': 0, 'errmsg': 'ok'},
    {'ret': 0, 'errmsg': 'ok'},
    {'errcode': 0, 'errmsg': 'ok'},
    {'ret': 0, 'errmsg': ''},
])
def test_explicit_numeric_success_is_not_rejected_for_informational_errmsg(monkeypatch, payload):
    monkeypatch.setattr(httpx, 'post', lambda *args, **kwargs: httpx.Response(200, json=payload))
    send_text(WeChatClient('https://wechat.test.invalid', 40))


@pytest.mark.parametrize('payload', [
    {'ret': -2}, {'ret': 0, 'errcode': 40001},
    {'errmsg': 'private-provider-text'},
    {'ret': '0'}, {'ret': False}, {'ret': []}, {'ret': {}},
    {'ret': 0, 'errcode': '0'}, {'ret': 0, 'errcode': True},
])
def test_missing_success_or_nonzero_or_malformed_codes_remain_rejected(monkeypatch, payload):
    monkeypatch.setattr(httpx, 'post', lambda *args, **kwargs: httpx.Response(200, json=payload))
    with pytest.raises(WeChatRejectedError) as rejected:
        send_text(WeChatClient('https://wechat.test.invalid', 40))
    assert rejected.value.operation == 'sendmessage'
    assert rejected.value.http_status == 200
    assert rejected.value.retryable is False
    assert 'private-provider-text' not in str(rejected.value)


@pytest.mark.parametrize('status,payload,expected_status,expected_reason', [
    (200, {'ret': -2, 'errcode': 40001, 'errmsg': 'private-provider-text'},
     'dead', 'NOTIFICATION_DELIVERY_FAILED'),
    (401, {'errmsg': 'private-provider-text'}, 'dead', 'NOTIFICATION_DELIVERY_FAILED'),
    (429, {'errmsg': 'private-provider-text'}, 'retry_wait', 'NOTIFICATION_DELIVERY_FAILED'),
])
def test_worker_correlates_safe_provider_codes_without_changing_delivery_contract(
        gateway, account, incoming, monkeypatch, caplog, status, payload, expected_status, expected_reason):
    row = account()
    incoming(row, context='private-context')
    queued = enqueue(gateway, notification(row))
    credentials = gateway.cipher.decrypt('owner', row['credentials_ciphertext'])
    monkeypatch.setattr(httpx, 'post', lambda *args, **kwargs: httpx.Response(status, json=payload))
    worker = gateway.components.delivery_worker
    monkeypatch.setattr(worker, '_stop', OneIteration())
    worker._run('diagnostic-worker')

    stored = outbox(gateway, queued['outbox_id'])
    assert stored['status'] == expected_status
    assert stored['last_error'] == expected_reason
    assert stored['attempt_count'] == 1
    entries = [record.getMessage() for record in caplog.records if record.msg.startswith('wechat_outbound_rejected ')]
    assert len(entries) == 1
    assert f'outbox_id={queued["outbox_id"]} operation=sendmessage http_status={status}' in entries[0]
    assert f'ret={payload.get("ret")} errcode={payload.get("errcode")}' in entries[0]
    for secret in ('private-provider-text', 'private-context', 'https://wechat.test.invalid', credentials['token']):
        assert secret not in caplog.text


@pytest.mark.parametrize('retryable', [False, True])
def test_rejection_diagnostics_survive_copy_and_pickle(retryable):
    error = WeChatRejectedError(retryable=retryable, operation='sendmessage', http_status=200, ret=-2, errcode=40001)
    for restored in (
        WeChatRejectedError(*error.args), copy.copy(error), copy.deepcopy(error), pickle.loads(pickle.dumps(error)),
    ):
        assert restored.args == error.args
        assert str(restored) == 'Provider rejected request'
        assert restored.operation == 'sendmessage'
        assert restored.http_status == 200
        assert restored.ret == -2
        assert restored.errcode == 40001
        assert restored.retryable is retryable


@pytest.mark.parametrize('operation', ['private-token', {'token': 'private-token'}, ['private-token']])
def test_diagnostic_fields_retain_only_allowlisted_operations_and_bounded_numbers(operation):
    error = WeChatRejectedError(
        operation=operation, http_status='private-token', ret='private-token', errcode=2 ** 100,
    )
    for restored in (
        error, WeChatRejectedError(*error.args), copy.copy(error),
        copy.deepcopy(error), pickle.loads(pickle.dumps(error)),
    ):
        assert restored.operation == 'unknown'
        assert restored.http_status is None
        assert restored.ret is None
        assert restored.errcode is None
        assert 'private-token' not in repr(restored)


def test_getupdates_keeps_safe_operation_and_http_diagnostics(monkeypatch):
    monkeypatch.setattr(httpx, 'post', lambda *args, **kwargs: httpx.Response(200, json={'ret': -14}))
    with pytest.raises(WeChatRejectedError) as rejected:
        WeChatClient('https://wechat.test.invalid', 40).get_updates(
            base_url='https://wechat.test.invalid', token='private-token', cursor='', timeout_ms=5000)
    assert rejected.value.operation == 'getupdates'
    assert rejected.value.http_status == 200
    assert rejected.value.ret == -14
