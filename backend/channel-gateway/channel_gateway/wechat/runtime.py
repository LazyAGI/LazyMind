from __future__ import annotations

import hashlib
import json
import logging
import math
import os
import threading
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from channel_gateway.common.domain.channel import (
    InboundEnvelope,
    ReceiverCheckpoint,
)
from channel_gateway.common.ports.providers import (
    ReceiverRepository,
    RuntimeCredentialStore,
    RuntimeLease,
)
from channel_gateway.common.domain.chat import (
    ChannelAttachment,
    ChannelExecutionContext,
)
from channel_gateway.wechat.domain import (
    WeChatAddressFactory,
    WeChatConfig,
    WeChatError,
)
from channel_gateway.wechat.ports import WeChatReceiverClient


_logger = logging.getLogger(__name__)
_MIN_POLL_TIMEOUT_MS = 5_000
_MAX_POLL_TIMEOUT_MS = 60_000


@dataclass(slots=True)
class _AccountWorker:
    account_id: str
    revision: int
    stop_event: threading.Event
    thread: threading.Thread | None = None
    lease: RuntimeLease | None = None


def _message_key(message: dict[str, Any]) -> str:
    message_id = message.get('message_id')
    if message_id is not None and str(message_id).strip():
        raw = str(message_id).strip()
    else:
        raw = json.dumps(
            message,
            ensure_ascii=False,
            sort_keys=True,
            separators=(',', ':'),
        )
    return hashlib.sha256(raw.encode('utf-8')).hexdigest()


def _message_text(message: dict[str, Any]) -> str:
    values: list[str] = []
    for item in message.get('item_list') or []:
        if not isinstance(item, dict):
            continue
        if item.get('type') == 1:
            text_item = item.get('text_item') or {}
            if isinstance(text_item, dict) and text_item.get('text') is not None:
                text = str(text_item['text']).strip()
                if text:
                    values.append(text)
    return '\n'.join(values)


class WeChatRuntime:
    """Receives iLink events and durably enqueues them without calling Core."""

    def __init__(
        self,
        *,
        config: WeChatConfig,
        store: ReceiverRepository,
        credentials: RuntimeCredentialStore,
        client: WeChatReceiverClient,
        addresses: WeChatAddressFactory,
    ):
        self._config = config
        self._store = store
        self._credentials = credentials
        self._client = client
        self._addresses = addresses
        self._shutdown = threading.Event()
        self._lock = threading.Lock()
        self._workers: dict[str, _AccountWorker] = {}

    def reconcile_accounts(
        self,
        accounts: list[dict[str, Any]],
    ) -> None:
        desired = {
            str(account['id']): int(account['credential_revision'])
            for account in accounts
        }
        with self._lock:
            current = {
                account_id: worker.revision
                for account_id, worker in self._workers.items()
            }
        for account_id in current.keys() - desired.keys():
            self.stop_account(account_id)
        for account_id, revision in desired.items():
            if current.get(account_id) != revision:
                self.start_account(account_id, revision=revision)

    def stop(self) -> None:
        self._shutdown.set()
        with self._lock:
            workers = list(self._workers.values())
        for worker in workers:
            worker.stop_event.set()
            if worker.lease:
                try:
                    self._store.set_runtime_status(
                        worker.account_id,
                        'stopped',
                        runtime_fence=worker.lease.fence,
                    )
                except Exception:
                    _logger.exception(
                        'wechat_runtime_stop_status_failed '
                        'account_id=%s',
                        worker.account_id,
                    )
                worker.lease.close()
        for worker in workers:
            if worker.thread:
                worker.thread.join(timeout=1.0)

    def start_account(
        self,
        account_id: str,
        *,
        revision: int = 0,
    ) -> None:
        old_worker = None
        with self._lock:
            existing = self._workers.get(account_id)
            if (
                existing
                and existing.thread
                and existing.thread.is_alive()
                and (revision == 0 or existing.revision == revision)
            ):
                return
            if existing:
                self._workers.pop(account_id, None)
                existing.stop_event.set()
                if existing.lease:
                    existing.lease.close()
                old_worker = existing
            stop_event = threading.Event()
            worker = _AccountWorker(
                account_id=account_id,
                revision=revision,
                stop_event=stop_event,
            )
            thread = threading.Thread(
                target=self._run_account,
                args=(worker,),
                name=f'channel-wechat-receiver-{account_id[-8:]}',
                daemon=True,
            )
            worker.thread = thread
            self._workers[account_id] = worker
            thread.start()
        if old_worker and old_worker.thread:
            old_worker.thread.join(timeout=1.0)

    def restart_account(self, account_id: str) -> None:
        try:
            account = self._credentials.load_runtime_account(account_id)
        except Exception:
            _logger.exception(
                'wechat_account_reload_failed account_id=%s',
                account_id,
            )
            return
        self.start_account(
            account_id,
            revision=int(account['credential_revision']),
        )

    def stop_account(self, account_id: str) -> None:
        with self._lock:
            worker = self._workers.pop(account_id, None)
        if not worker:
            return
        worker.stop_event.set()
        if worker.lease:
            worker.lease.close()
        if worker.thread:
            worker.thread.join(timeout=1.0)

    def _run_account(
        self,
        worker: _AccountWorker,
    ) -> None:
        account_id = worker.account_id
        stop_event = worker.stop_event
        failures = 0
        try:
            while not self._shutdown.is_set() and not stop_event.is_set():
                lease = None
                try:
                    lease = self._store.acquire_runtime_lease(account_id)
                    if lease is None:
                        stop_event.wait(5)
                        continue
                    with self._lock:
                        worker.lease = lease
                    account = self._credentials.load_runtime_account(account_id)
                    if account.get('status') != 'connected':
                        return
                    credentials = dict(account['credentials'])
                    self._store.set_runtime_status(
                        account_id,
                        'starting',
                        runtime_fence=lease.fence,
                    )
                    self._notify_start(account_id, credentials)
                    failures = 0
                    self._poll(account, credentials, stop_event, lease)
                except Exception as exc:
                    failures += 1
                    delay = min(30, 2 ** min(failures, 5))
                    _logger.exception(
                        'wechat_receiver_failed account_id=%s retry_in=%s',
                        account_id,
                        delay,
                    )
                    if lease is not None:
                        try:
                            self._store.set_runtime_status(
                                account_id,
                                'failed',
                                str(exc)[:500],
                                runtime_fence=lease.fence,
                            )
                        except Exception:
                            pass
                    stop_event.wait(delay)
                finally:
                    if lease is not None:
                        lease.close()
                        with self._lock:
                            if worker.lease is lease:
                                worker.lease = None
                if not self._shutdown.is_set() and not stop_event.is_set():
                    stop_event.wait(2)
        finally:
            with self._lock:
                current = self._workers.get(account_id)
                if current is worker:
                    self._workers.pop(account_id, None)

    def _poll(
        self,
        account: dict[str, Any],
        credentials: dict[str, str],
        stop_event: threading.Event,
        lease: RuntimeLease,
    ) -> None:
        account_id = str(account['id'])
        checkpoint = self._store.get_checkpoint(account_id)
        cursor = str(checkpoint.get('cursor') or '')
        timeout_ms = int(checkpoint.get('longpoll_timeout_ms') or 35000)
        failures = 0
        self._store.set_runtime_status(
            account_id,
            'running',
            runtime_fence=lease.fence,
        )
        _logger.info('wechat_receiver_started account_id=%s', account_id)

        while not self._shutdown.is_set() and not stop_event.is_set():
            lease.keepalive()
            try:
                result = self._client.get_updates(
                    base_url=credentials['base_url'],
                    token=credentials['token'],
                    cursor=cursor,
                    timeout_ms=timeout_ms,
                )
                if self._shutdown.is_set() or stop_event.is_set():
                    return
                lease.keepalive()
                failures = 0
                timeout_ms = self._next_timeout(result, timeout_ms)
                next_cursor = str(result.get('get_updates_buf') or cursor)
                envelopes = [
                    envelope
                    for message in (result.get('msgs') or [])
                    if isinstance(message, dict)
                    for envelope in [
                        self._normalize(account, credentials, message)
                    ]
                    if envelope is not None
                ]
                self._store.ingest_batch(
                    account_id,
                    envelopes,
                    ReceiverCheckpoint(
                        cursor=next_cursor,
                        metadata={'longpoll_timeout_ms': timeout_ms},
                    ),
                    lease.fence,
                )
                cursor = next_cursor
            except WeChatError as exc:
                failures += 1
                delay = (
                    30
                    if failures
                    >= self._config.max_consecutive_errors
                    else 2
                )
                self._store.set_runtime_status(
                    account_id,
                    'degraded',
                    f'{exc.__class__.__name__}: {exc}'[:500],
                    lease.fence,
                )
                _logger.warning(
                    'wechat_getupdates_failed account_id=%s attempt=%s retry_in=%s',
                    account_id,
                    failures,
                    delay,
                )
                stop_event.wait(delay)

    def _normalize(
        self,
        account: dict[str, Any],
        credentials: dict[str, str],
        message: dict[str, Any],
    ) -> InboundEnvelope | None:
        if message.get('message_type') not in (None, 1):
            return None
        sender_id = str(message.get('from_user_id') or '')
        if sender_id != credentials['authorized_user_id']:
            return None
        context_token = str(message.get('context_token') or '')
        if not sender_id or not context_token:
            return None
        account_id = str(account['id'])
        message_key = _message_key(message)
        attachments: list[ChannelAttachment] = []
        media_failed = False
        media_bytes = 0
        for index, item in enumerate((message.get('item_list') or [])[:10]):
            if not isinstance(item, dict) or item.get('type') not in (2, 4):
                continue
            kind = 'image' if item.get('type') == 2 else 'file'
            payload = item.get(f'{kind}_item') or {}
            media = payload.get('media') if isinstance(payload, dict) else None
            if not isinstance(media, dict):
                media_failed = True
                continue
            try:
                content = self._client.download_media(
                    media=media,
                    aes_key_hint=(
                        str(payload.get('aeskey') or '')
                        if kind == 'image'
                        else ''
                    ),
                    max_bytes=(
                        self._config.max_inbound_media_bytes
                        - media_bytes
                    ),
                )
                media_bytes += len(content)
                path = self._save_media(
                    owner_user_id=str(account['owner_user_id']),
                    message_key=message_key,
                    index=index,
                    kind=kind,
                    filename=str(payload.get('file_name') or ''),
                    content=content,
                )
            except (OSError, WeChatError):
                media_failed = True
                _logger.exception(
                    'wechat_inbound_media_failed account_id=%s message_key=%s index=%s',
                    account_id,
                    message_key,
                    index,
                )
                continue
            attachment = ChannelAttachment.from_dict({
                'input_type': kind,
                'uri': path,
            })
            if attachment is not None:
                attachments.append(attachment)

        text = _message_text(message)
        if not text and attachments:
            text = (
                '请处理用户发送的图片和文档。'
                if len(attachments) > 1
                else '请处理用户发送的图片。'
                if attachments[0].input_type == 'image'
                else '请处理用户发送的文档。'
            )
        if not text and media_failed:
            text = '微信图片或文档读取失败，请重新发送。'
        if not text:
            return None
        address_hash = self._addresses.direct(
            account_id,
            sender_id,
        ).route_hash
        execution = ChannelExecutionContext(
            attachments=tuple(attachments),
        )
        return InboundEnvelope(
            provider='wechat',
            account_id=account_id,
            message_key=message_key,
            order_key=address_hash,
            external_address_hash=address_hash,
            owner_user_id=str(account['owner_user_id']),
            recipient_id=sender_id,
            text=text,
            provider_context={
                'context_token': context_token,
                'session_id': str(message.get('session_id') or ''),
                'channel_error': (
                    '微信图片或文档读取失败，请重新发送。'
                    if media_failed
                    else ''
                ),
            },
            sensitive_context=(
                {'channel_execution': execution.to_dict()}
                if attachments
                else {}
            ),
        )

    def _save_media(
        self,
        *,
        owner_user_id: str,
        message_key: str,
        index: int,
        kind: str,
        filename: str,
        content: bytes,
    ) -> str:
        root = Path(self._config.upload_root).resolve()
        owner = hashlib.sha256(
            owner_user_id.encode('utf-8')
        ).hexdigest()[:32]
        directory = (root / 'channels' / 'wechat' / owner / message_key).resolve()
        if root not in directory.parents:
            raise OSError('Invalid WeChat media directory')
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        name = self._safe_filename(filename)
        if not name:
            name = (
                f'image-{index}{self._image_extension(content)}'
                if kind == 'image'
                else f'document-{index}.bin'
            )
        target = (directory / name).resolve()
        if directory not in target.parents:
            raise OSError('Invalid WeChat media filename')
        if target.exists() and target.read_bytes() == content:
            return str(target)
        temporary = directory / f'.{name}.{os.getpid()}.tmp'
        try:
            with temporary.open('wb') as handle:
                os.chmod(temporary, 0o600)
                handle.write(content)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(temporary, target)
        finally:
            temporary.unlink(missing_ok=True)
        return str(target)

    @staticmethod
    def _safe_filename(value: str) -> str:
        name = Path(value.replace('\\', '/')).name.strip()
        name = ''.join(
            character
            for character in name
            if character.isprintable() and character not in {'/', '\\', '\x00'}
        )
        return name[:180]

    @staticmethod
    def _image_extension(content: bytes) -> str:
        if content.startswith(b'\x89PNG\r\n\x1a\n'):
            return '.png'
        if content.startswith(b'\xff\xd8\xff'):
            return '.jpg'
        if content.startswith((b'GIF87a', b'GIF89a')):
            return '.gif'
        if len(content) >= 12 and content[:4] == b'RIFF' and content[8:12] == b'WEBP':
            return '.webp'
        return '.img'

    def _notify_start(
        self,
        account_id: str,
        credentials: dict[str, str],
    ) -> None:
        try:
            self._client.notify_start(
                base_url=credentials['base_url'],
                token=credentials['token'],
            )
        except WeChatError:
            _logger.warning(
                'wechat_notify_start_failed account_id=%s',
                account_id,
            )

    @staticmethod
    def _next_timeout(result: dict[str, Any], current: int) -> int:
        suggested = result.get('longpolling_timeout_ms')
        if not isinstance(suggested, (int, float)):
            return current
        if not math.isfinite(suggested):
            return current
        return min(
            _MAX_POLL_TIMEOUT_MS,
            max(_MIN_POLL_TIMEOUT_MS, int(suggested)),
        )
