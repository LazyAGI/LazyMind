"""Retry-chain guarantees through the real HTTP service and transactional store."""
from concurrent.futures import ThreadPoolExecutor
from threading import Barrier
import json

import pytest

from test_notification_delivery import enqueue, notification, outbox, PREFIX


def terminal(gateway, record, status):
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_outbox SET status = %s WHERE id = %s',
                           (status, record['notification_id']))


def retry(gateway, record, key, confirmed=False):
    return gateway.client.post(f'{PREFIX}/task-notifications/{record["notification_id"]}:retry',
                               json={'idempotency_key': key, 'confirm_duplicate_risk': confirmed})


def failed_notice(gateway, account, incoming):
    row = account()
    incoming(row, context='synthetic-context')
    original = enqueue(gateway, notification(row))
    terminal(gateway, original, 'dead')
    return original


def test_failed_ancestor_cannot_bypass_unknown_child_confirmation(gateway, account, incoming):
    original = failed_notice(gateway, account, incoming)
    first = retry(gateway, original, 'first')
    assert first.status_code == 201, first.text
    child = first.json()
    terminal(gateway, child, 'unknown')
    before = outbox(gateway, original['outbox_id'])
    response = retry(gateway, original, 'second')
    assert response.status_code == 409, response.text
    assert response.json()['error']['code'] == 'NOTIFICATION_CONFIRMATION_REQUIRED'
    response = retry(gateway, original, 'second', True)
    assert response.status_code == 201, response.text
    assert response.json()['retry_of'] == original['notification_id']
    assert outbox(gateway, original['outbox_id']) == before
    assert outbox(gateway, child['outbox_id'])['status'] == 'unknown'
    assert len(gateway.store.notification_history('owner', original['payload']['task_id'])['items']) == 3


@pytest.mark.parametrize('status', ['pending', 'retry_wait', 'sending', 'sent', 'skipped'])
def test_chain_state_blocks_retry_of_failed_ancestor(gateway, account, incoming, status):
    original = failed_notice(gateway, account, incoming)
    child = retry(gateway, original, 'first').json()
    terminal(gateway, child, status)
    response = retry(gateway, original, 'second', True)
    assert response.status_code == 409, response.text
    assert response.json()['error']['code'] == 'NOTIFICATION_STATE_CHANGED'
    assert len(gateway.store.notification_history('owner', original['payload']['task_id'])['items']) == 2


@pytest.mark.parametrize('same_key', [False, True])
def test_concurrent_retry_requests_have_one_effective_attempt(gateway, account, incoming, same_key):
    original = failed_notice(gateway, account, incoming)
    barrier = Barrier(2)

    def invoke(index):
        barrier.wait(timeout=10)
        return retry(gateway, original, 'same' if same_key else f'key-{index}')

    with ThreadPoolExecutor(max_workers=2) as executor:
        results = list(executor.map(invoke, range(2)))
    assert sorted(item.status_code for item in results) == ([201, 201] if same_key else [201, 409])
    ids = {item.json()['notification_id'] for item in results if item.status_code == 201}
    assert len(ids) == 1
    assert len(gateway.store.notification_history('owner', original['payload']['task_id'])['items']) == 2


def test_receipt_replay_survives_child_completion_and_closed_delivery_gate(gateway, account, incoming):
    original = failed_notice(gateway, account, incoming)
    child = retry(gateway, original, 'same').json()
    terminal(gateway, child, 'sent')
    gateway.core_events.enabled = False
    replay = retry(gateway, original, 'same')
    assert replay.status_code == 201, replay.text
    assert replay.json()['notification_id'] == child['notification_id']
    assert replay.json()['status'] == 'sent'
    replay = gateway.client.post(f'{PREFIX}/task-notifications/{original["notification_id"]}:retry',
                                 headers={'X-User-Id': 'other'}, json={'idempotency_key': 'same'})
    assert replay.status_code == 404


def test_retry_of_ancestor_inherits_latest_confirmed_parts(gateway, account, incoming):
    original = failed_notice(gateway, account, incoming)
    child = retry(gateway, original, 'first').json()
    parts = [{'text': 'already sent'}, {'text': 'remaining'}]
    state = {'provider_checkpoint': 'synthetic'}
    with gateway.store._connect() as connection:
        connection.execute('''UPDATE channel_outbox SET status = 'unknown', rendered_parts = %s::jsonb,
                              next_part_index = 1, provider_state = %s::jsonb WHERE id = %s''',
                           (json.dumps(parts), json.dumps(state), child['outbox_id']))
    before = outbox(gateway, child['outbox_id'])
    response = retry(gateway, original, 'second', True)
    assert response.status_code == 201, response.text
    latest = response.json()
    stored = outbox(gateway, latest['outbox_id'])
    assert stored['next_part_index'] == 1
    assert gateway.store._list(stored['rendered_parts']) == parts
    assert gateway.store._dict(stored['provider_state']) == state
    assert outbox(gateway, child['outbox_id']) == before
    # The confirmed attempt supersedes the unknown ancestor; an explicit
    # subsequent failure can be retried without replaying already sent parts.
    terminal(gateway, latest, 'dead')
    again = retry(gateway, original, 'third')
    assert again.status_code == 201, again.text
    assert outbox(gateway, again.json()['outbox_id'])['next_part_index'] == 1


def test_separate_events_do_not_share_retry_chain(gateway, account, incoming):
    first = failed_notice(gateway, account, incoming)
    assert retry(gateway, first, 'first').status_code == 201
    payload = {**first['payload'], 'event_id': 'different-event'}
    second = enqueue(gateway, payload)
    terminal(gateway, second, 'dead')
    assert retry(gateway, second, 'first').status_code == 201


def test_preexisting_unknown_branch_cannot_be_hidden_by_newer_failed_branch(gateway, account, incoming):
    original = failed_notice(gateway, account, incoming)
    child = retry(gateway, original, 'first').json()
    terminal(gateway, child, 'unknown')
    # Model a branch written by the old implementation, bypassing the corrected
    # API only to seed historical state. The request under test is real HTTP.
    branch_id = 'b' * 64
    with gateway.store._connect() as connection:
        connection.execute('''
            INSERT INTO channel_outbox(id, account_id, dedupe_key, provider, order_key, sequence,
                recipient_id, provider_context, text, intent_kind, purpose, metadata, status)
            SELECT %s, account_id, %s, provider, order_key, sequence, recipient_id, provider_context,
                text, intent_kind, purpose, metadata, 'dead' FROM channel_outbox WHERE id = %s
        ''', (branch_id, 'notification:' + branch_id, original['outbox_id']))
    response = retry(gateway, original, 'after-branch')
    assert response.status_code == 409, response.text
    assert response.json()['error']['code'] == 'NOTIFICATION_CONFIRMATION_REQUIRED'
    assert retry(gateway, original, 'after-branch', True).status_code == 201
