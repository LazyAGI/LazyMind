"""Exercise the installed official SDK; only its WebSocket transport is replaced."""
import asyncio
import json
import threading
import uuid

import pytest
from aibot import ws
from websockets.protocol import State

from channel_gateway.wecom.runtime import verify_credentials


class BotSocket:
    def __init__(self, *, auth_error=0):
        self.state = State.OPEN
        self.frames = asyncio.Queue()
        self.sent = []
        self.auth_error = auth_error
        self.loop = asyncio.get_running_loop()
        self.authenticated = threading.Event()

    async def send(self, text):
        frame = json.loads(text)
        self.sent.append(frame)
        error = self.auth_error if frame['cmd'] == 'aibot_subscribe' else 0
        await self.frames.put(json.dumps({'headers': frame['headers'], 'errcode': error,
                                         'errmsg': 'synthetic-private' if error else 'ok'}))
        if frame['cmd'] == 'aibot_subscribe':
            self.authenticated.set()

    def __aiter__(self):
        return self

    async def __anext__(self):
        return await self.frames.get()

    async def close(self, **_kwargs):
        self.state = State.CLOSED


@pytest.mark.parametrize('auth_error', [0, 40001])
def test_official_sdk_authentication_and_shutdown(monkeypatch, caplog, auth_error):
    sockets = []

    async def connect(url, **kwargs):
        assert url == 'wss://openws.work.weixin.qq.com'
        socket = BotSocket(auth_error=auth_error)
        sockets.append(socket)
        return socket

    monkeypatch.setattr(ws.websockets, 'connect', connect)
    credentials = {'bot_id': 'synthetic-bot', 'secret': uuid.uuid4().hex}
    if auth_error:
        with pytest.raises(RuntimeError, match='WECOM_AUTH_FAILED'):
            asyncio.run(verify_credentials(credentials))
    else:
        asyncio.run(verify_credentials(credentials))
    assert len(sockets) == 1
    assert sockets[0].sent[0]['body'] == credentials
    assert sockets[0].state == State.CLOSED
    assert credentials['secret'] not in caplog.text and 'synthetic-private' not in caplog.text


def test_official_sdk_inbound_dedupe_and_proactive_send(gateway, account, monkeypatch):
    row = account('wecom')
    service = gateway.components.delivery_worker._providers.delivery('wecom')
    runtime = service._runtime
    ready = threading.Event()
    ingested = threading.Event()
    sockets = []

    async def connect(_url, **_kwargs):
        socket = BotSocket()
        sockets.append(socket)
        return socket

    # Observes durable receipt; it neither replaces nor changes storage behavior.
    original_ingest = gateway.store.ingest_batch

    def observe(*args, **kwargs):
        result = original_ingest(*args, **kwargs)
        ingested.set()
        return result

    original_status = gateway.store.set_runtime_status

    def status(*args, **kwargs):
        result = original_status(*args, **kwargs)
        if args[1] == 'running':
            ready.set()
        return result

    monkeypatch.setattr(ws.websockets, 'connect', connect)
    monkeypatch.setattr(gateway.store, 'ingest_batch', observe)
    monkeypatch.setattr(gateway.store, 'set_runtime_status', status)
    runtime.restart_account(row['id'])
    try:
        assert ready.wait(5), 'SDK did not authenticate'
        frame = {'cmd': 'aibot_msg_callback', 'headers': {'req_id': 'callback'},
                 'body': {'msgid': 'message-1', 'msgtype': 'text', 'from': {'userid': 'recipient-a'},
                          'text': {'content': '请整理今日简报'}}}
        socket = sockets[0]
        socket.loop.call_soon_threadsafe(socket.frames.put_nowait, json.dumps(frame))
        assert ingested.wait(5)
        inbound = gateway.store.claim_next_inbound('sdk-test', lease_seconds=120)
        assert inbound is not None and inbound.text == '请整理今日简报' and inbound.provider == 'wecom'
        ingested.clear()
        socket.loop.call_soon_threadsafe(socket.frames.put_nowait, json.dumps(frame))
        assert ingested.wait(5)
        assert gateway.store.claim_next_inbound('duplicate', lease_seconds=120) is None
        runtime.send(row['id'], 'recipient-a', '今日简报结果')
        sent = [item for item in socket.sent if item['cmd'] == 'aibot_send_msg']
        assert len(sent) == 1 and sent[0]['body']['chatid'] == 'recipient-a'
        assert sent[0]['body']['markdown']['content'] == '今日简报结果'
    finally:
        runtime.stop_account(row['id'])
    assert sockets[0].state == State.CLOSED
