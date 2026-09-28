"""Account lifecycle through the official SDK with an isolated socket transport."""
import json
import threading
import uuid

from aibot import ws
from websockets.protocol import State

from test_wecom_protocol import BotSocket
from test_notification_delivery import PREFIX


def test_two_accounts_keep_messages_and_reconnection_isolated(gateway, account, monkeypatch):
    rows = [account('wecom', identity='synthetic-bot-a'), account('wecom', identity='synthetic-bot-b')]
    runtime = gateway.components.delivery_worker._providers.delivery('wecom')._runtime
    ready = {row['id']: threading.Event() for row in rows}
    ingested = {row['id']: threading.Event() for row in rows}
    sockets = []
    original_status, original_ingest = gateway.store.set_runtime_status, gateway.store.ingest_batch

    async def connect(_url, **_kwargs):
        socket = BotSocket()
        sockets.append(socket)
        return socket

    def status(*args, **kwargs):
        result = original_status(*args, **kwargs)
        if args[1] == 'running':
            ready[args[0]].set()
        return result

    def ingest(*args, **kwargs):
        result = original_ingest(*args, **kwargs)
        ingested[args[0]].set()
        return result

    def receive(socket, row, message_id):
        ingested[row['id']].clear()
        frame = {'cmd': 'aibot_msg_callback', 'headers': {'req_id': message_id},
                 'body': {'msgid': message_id, 'msgtype': 'text', 'from': {'userid': 'same-recipient'},
                          'text': {'content': row['id']}}}
        socket.loop.call_soon_threadsafe(socket.frames.put_nowait, json.dumps(frame))
        assert ingested[row['id']].wait(5)

    monkeypatch.setattr(ws.websockets, 'connect', connect)
    monkeypatch.setattr(gateway.store, 'set_runtime_status', status)
    monkeypatch.setattr(gateway.store, 'ingest_batch', ingest)
    try:
        # Start sequentially to associate transport observations without timing guesses.
        for row in rows:
            runtime.restart_account(row['id'])
            assert ready[row['id']].wait(5)
        assert len(sockets) == 2
        for row, socket in zip(rows, sockets):
            receive(socket, row, 'same-platform-message-id')
        messages = [gateway.store.claim_next_inbound(f'reader-{i}', lease_seconds=120) for i in range(2)]
        assert {message.account_id for message in messages} == {row['id'] for row in rows}
        assert all(message.text == message.account_id for message in messages)
        for index, message in enumerate(messages):
            assert gateway.store.complete_inbound(message.inbox_id, f'reader-{index}', [], {})
        for row, socket in zip(rows, sockets):
            receive(socket, row, 'same-platform-message-id')
        assert gateway.store.claim_next_inbound('duplicates', lease_seconds=120) is None

        assert gateway.store.disconnect_account('owner', rows[0]['id'])
        runtime.reconcile_accounts(gateway.store.list_accounts('owner', 'wecom'))
        assert sockets[0].state == State.CLOSED
        assert sockets[1].state == State.OPEN
        runtime.send(rows[1]['id'], 'same-recipient', 'second account stays online')
        assert len([frame for frame in sockets[1].sent if frame['cmd'] == 'aibot_send_msg']) == 1

        ready[rows[0]['id']].clear()
        response = gateway.client.post(f'{PREFIX}/connection-sessions', json={
            'provider': 'wecom', 'account_id': rows[0]['id'],
            'credentials': {'bot_id': 'synthetic-bot-a', 'secret': uuid.uuid4().hex},
        })
        assert response.status_code == 201, response.text
        assert response.json()['status'] == 'connected'
        reconnected = gateway.store.get_account('owner', rows[0]['id'])
        assert reconnected['id'] == rows[0]['id']
        assert reconnected['credential_revision'] > rows[0]['credential_revision']
        runtime.reconcile_accounts(gateway.store.list_accounts('owner', 'wecom'))
        assert ready[rows[0]['id']].wait(5)
        # The connection API verifies with a short-lived SDK client, then starts its receiver.
        assert len(sockets) == 4 and sockets[2].state == State.CLOSED
        receive(sockets[3], reconnected, 'same-platform-message-id')
        assert gateway.store.claim_next_inbound('reconnect-duplicate', lease_seconds=120) is None
        receive(sockets[3], reconnected, 'new-platform-message-id')
        assert gateway.store.claim_next_inbound('new-message', lease_seconds=120).account_id == rows[0]['id']
        runtime.send(rows[0]['id'], 'same-recipient', 'reconnected account result')
        assert not [frame for frame in sockets[0].sent if frame['cmd'] == 'aibot_send_msg']
        sent = [frame for frame in sockets[3].sent if frame['cmd'] == 'aibot_send_msg']
        assert len(sent) == 1 and sent[0]['body']['markdown']['content'] == 'reconnected account result'
    finally:
        runtime.stop()
    assert all(socket.state == State.CLOSED for socket in sockets)
