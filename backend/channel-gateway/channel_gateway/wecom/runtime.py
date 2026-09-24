"""Official SDK transport plugged into the existing account supervisor and inbox."""
import asyncio
from dataclasses import dataclass
import hashlib
import logging
import re
import threading

from aibot import WSClient, WSClientOptions

from channel_gateway.common.domain.channel import InboundEnvelope
from channel_gateway.common.errors import GatewayError, ProviderRejectedError


_logger = logging.getLogger(__name__)


class SDKLogger:
    # SDK messages can contain authentication frames and provider response bodies.
    # Lifecycle and errors are recorded by this adapter using fixed event names.
    def debug(self, _message, *_args):
        pass

    def info(self, _message, *_args):
        pass

    def warn(self, _message, *_args):
        _logger.debug('wecom_sdk_warning')

    def error(self, _message, *_args):
        _logger.warning('wecom_sdk_error')


def sdk_client(credentials):
    return WSClient(WSClientOptions(
        bot_id=credentials['bot_id'], secret=credentials['secret'],
        max_reconnect_attempts=-1, logger=SDKLogger(),
    ))


async def authenticate(client):
    ready = asyncio.get_running_loop().create_future()

    def authenticated():
        if not ready.done():
            ready.set_result(True)

    def failed(_error):
        if not ready.done():
            ready.set_exception(RuntimeError('WECOM_AUTH_FAILED'))

    client.on('authenticated', authenticated)
    client.on('error', failed)
    try:
        async with asyncio.timeout(10):
            await client.connect()
            await ready
    finally:
        client.remove_listener('authenticated', authenticated)
        client.remove_listener('error', failed)


async def verify_credentials(credentials):
    client = sdk_client(credentials)
    try:
        await authenticate(client)
    finally:
        await close_client(client)


async def close_client(client):
    client.disconnect()
    # Each account owns its event loop. Drain the SDK's scheduled socket shutdown
    # before asyncio.run cancels remaining tasks and destroys that loop.
    pending = [task for task in asyncio.all_tasks() if task is not asyncio.current_task()]
    if pending:
        _, unfinished = await asyncio.wait(pending, timeout=1)
        for task in unfinished:
            task.cancel()
        await asyncio.gather(*pending, return_exceptions=True)


@dataclass
class Worker:
    stop: threading.Event
    revision: int
    thread: threading.Thread | None = None
    loop: asyncio.AbstractEventLoop | None = None
    client: WSClient | None = None
    ready: bool = False
    lease: object = None


class WeComRuntime:
    def __init__(self, store, cipher):
        self._store, self._cipher = store, cipher
        self._lock = threading.Lock()
        self._workers = {}

    def load_runtime_account(self, account_id):
        row = self._store.get_account_internal(account_id)
        if not row or row['provider'] != 'wecom' or row['status'] != 'connected':
            raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '企业微信账号不可用')
        credentials = self._cipher.decrypt(row['owner_user_id'], row['credentials_ciphertext'])
        if not credentials.get('bot_id') or not credentials.get('secret'):
            raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '企业微信凭据不可用')
        return {**row, 'credentials': credentials}

    def reconcile_accounts(self, accounts):
        wanted = {row['id']: row for row in accounts if row['status'] == 'connected'}
        with self._lock:
            active = dict(self._workers)
        for account_id, worker in active.items():
            row = wanted.get(account_id)
            if not row or worker.revision != row['credential_revision']:
                self.stop_account(account_id)
        for row in wanted.values():
            with self._lock:
                worker = self._workers.get(row['id'])
            if not worker or not worker.thread.is_alive():
                self.restart_account(row['id'])

    def restart_account(self, account_id):
        self.stop_account(account_id)
        row = self.load_runtime_account(account_id)
        worker = Worker(threading.Event(), int(row['credential_revision']))
        worker.thread = threading.Thread(target=self._run, args=(row, worker), daemon=True,
                                         name=f'wecom-{account_id}')
        with self._lock:
            self._workers[account_id] = worker
        worker.thread.start()

    def stop_account(self, account_id):
        with self._lock:
            worker = self._workers.pop(account_id, None)
        if worker:
            worker.stop.set()
            if worker.loop and worker.client and worker.loop.is_running():
                worker.loop.call_soon_threadsafe(worker.client.disconnect)
            if worker.thread is not threading.current_thread():
                worker.thread.join(timeout=3)

    def stop(self):
        with self._lock:
            ids = list(self._workers)
        for account_id in ids:
            self.stop_account(account_id)

    def _run(self, account, worker):
        lease = self._store.acquire_runtime_lease(account['id'])
        if lease is None:
            return
        worker.lease = lease
        try:
            self._store.set_runtime_status(account['id'], 'starting', runtime_fence=lease.fence)
            asyncio.run(self._serve(account, worker, lease))
        except Exception:
            _logger.warning('wecom_runtime_failed account_id=%s', account['id'])
            if not worker.stop.is_set():
                self._store.set_runtime_status(
                    account['id'], 'failed', 'WECOM_CONNECTION_FAILED', runtime_fence=lease.fence,
                )
        finally:
            worker.ready = False
            lease.close()

    async def _serve(self, account, worker, lease):
        worker.loop = asyncio.get_running_loop()
        client = worker.client = sdk_client(account['credentials'])

        async def changed(status):
            worker.ready = status == 'running'
            if not worker.stop.is_set():
                await asyncio.to_thread(self._store.set_runtime_status, account['id'], status,
                                        runtime_fence=lease.fence)

        client.on('disconnected', lambda _reason: asyncio.create_task(changed('degraded')))
        client.on('authenticated', lambda: asyncio.create_task(changed('running')))
        client.on('error', lambda _error: _logger.warning('wecom_transport_error account_id=%s', account['id']))

        async def received(frame):
            if not worker.ready or worker.stop.is_set():
                return
            try:
                body = frame.get('body') or {}
                sender = (body.get('from') or {}).get('userid')
                recipient = body.get('chatid') or sender
                message_id = body.get('msgid')
                text = (body.get('text') or {}).get('content')
                if not all(isinstance(value, str) and value.strip() for value in (sender, recipient, message_id, text)):
                    return
                if len(text.encode()) > 16384 or max(len(sender), len(recipient), len(message_id)) > 256:
                    return
                route = f'wecom:{account["id"]}:{recipient}:{sender}'
                envelope = InboundEnvelope(provider='wecom', account_id=account['id'], message_key=message_id,
                                           order_key=recipient,
                                           external_address_hash=hashlib.sha256(route.encode()).hexdigest(),
                                           owner_user_id=account['owner_user_id'], recipient_id=recipient, text=text)
                await asyncio.to_thread(self._store.ingest_batch, account['id'], [envelope], None, lease.fence)
            except Exception:
                _logger.warning('wecom_inbound_rejected account_id=%s', account['id'])

        client.on('message.text', received)
        try:
            await authenticate(client)
            self._store.set_runtime_status(account['id'], 'running', runtime_fence=lease.fence)
            while not worker.stop.is_set():
                if await asyncio.to_thread(worker.stop.wait, 30):
                    break
                await asyncio.to_thread(lease.keepalive)
        finally:
            worker.ready = False
            await close_client(client)

    def send(self, account_id, recipient_id, text):
        row = self.load_runtime_account(account_id)
        with self._lock:
            worker = self._workers.get(account_id)
        if (not worker or not worker.ready or not worker.loop or not worker.loop.is_running()
                or worker.revision != row['credential_revision']):
            raise GatewayError(503, 'WECOM_NOT_CONNECTED', '企业微信连接暂不可用', retryable=True)
        worker.lease.keepalive()
        future = asyncio.run_coroutine_threadsafe(
            worker.client.send_message(recipient_id, {'msgtype': 'markdown', 'markdown': {'content': text}}),
            worker.loop,
        )
        try:
            return future.result(timeout=10)
        except TimeoutError:
            future.cancel()
            raise TimeoutError('WECOM_DELIVERY_UNKNOWN') from None
        except Exception as error:
            # The pinned SDK reports explicit negative acknowledgements with
            # this stable prefix, but exposes no typed error code. Discard errmsg.
            if re.match(r'^Reply ack error: errcode=-?\d+,', str(error)):
                raise ProviderRejectedError() from None
            raise
