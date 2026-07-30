import hashlib
import json
import logging
import math
import threading
import time
import uuid
from typing import Any, Iterable

from channel_gateway.common.database import GatewayStore
from channel_gateway.common.inbound import InboundMessage, InboundMessageProcessor
from channel_gateway.common.lazymind import LazyMindClient
from channel_gateway.common.outbound import split_outbound_parts
from channel_gateway.common.security import JsonCipher
from channel_gateway.settings import Settings
from channel_gateway.wechat.client import WeChatClient, WeChatError


_logger = logging.getLogger(__name__)
_SEND_ATTEMPTS = 3
_MIN_POLL_TIMEOUT_MS = 5_000
_MAX_POLL_TIMEOUT_MS = 60_000
_WELCOME_MESSAGE = """补充介绍一下：我是 LazyMind，你的个人 AI 助手。这里与 LazyMind 网页端使用同一账号、普通会话和历史记录。

除了刚才的对话，你还可以直接用自然语言：
1. “帮我创建一个新会话，并整理今天的周报”
2. “列出我的历史会话”或“切到第 2 个会话”
3. “这轮使用 AI学习资料 知识库”
4. “查看当前可用的知识库、Skill 和工具”
5. “帮我生成一张可爱的小狗图片”

微信端暂不支持 Plugin、SubAgent、后台 Task 和结构化 Ask。直接发消息即可开始。"""


def _message_key(message: dict[str, Any]) -> str:
    message_id = message.get('message_id')
    if message_id is not None and str(message_id).strip():
        raw = str(message_id).strip()
    else:
        raw = json.dumps(message, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    return hashlib.sha256(raw.encode('utf-8')).hexdigest()


def _message_text(message: dict[str, Any]) -> str:
    for item in message.get('item_list') or []:
        if not isinstance(item, dict):
            continue
        if item.get('type') == 1:
            text_item = item.get('text_item') or {}
            if isinstance(text_item, dict) and text_item.get('text') is not None:
                return str(text_item['text']).strip()
        if item.get('type') == 3:
            voice_item = item.get('voice_item') or {}
            if isinstance(voice_item, dict) and voice_item.get('text'):
                return str(voice_item['text']).strip()
    return ''


def _split_text(text: str, size: int) -> Iterable[str]:
    remaining = text.strip()
    while remaining:
        if len(remaining) <= size:
            yield remaining
            return
        cut = remaining.rfind('\n', 0, size + 1)
        if cut < size // 2:
            cut = remaining.rfind('。', 0, size + 1)
            if cut >= size // 2:
                cut += 1
        if cut < size // 2:
            cut = size
        yield remaining[:cut].strip()
        remaining = remaining[cut:].strip()


class WeChatRuntime:
    def __init__(
        self,
        *,
        settings: Settings,
        store: GatewayStore,
        cipher: JsonCipher,
        inbound: InboundMessageProcessor,
        lazymind: LazyMindClient,
    ):
        self._settings = settings
        self._store = store
        self._cipher = cipher
        self._wechat = WeChatClient(
            settings.wechat_ilink_base_url,
            settings.wechat_poll_timeout_seconds,
        )
        self._inbound = inbound
        self._lazymind = lazymind
        self._shutdown = threading.Event()
        self._lock = threading.Lock()
        self._workers: dict[str, tuple[threading.Thread, threading.Event]] = {}

    def start(self) -> None:
        for account in self._store.connected_accounts():
            self.start_account(account['id'])

    def stop(self) -> None:
        self._shutdown.set()
        with self._lock:
            workers = list(self._workers.items())
        for _, (_, stop_event) in workers:
            stop_event.set()
        for _, (thread, _) in workers:
            thread.join(timeout=1.0)

    def start_account(self, account_id: str) -> None:
        with self._lock:
            existing = self._workers.get(account_id)
            if existing and existing[0].is_alive():
                return
            stop_event = threading.Event()
            thread = threading.Thread(
                target=self._run_account,
                args=(account_id, stop_event),
                name=f'channel-wechat-{account_id[-8:]}',
                daemon=True,
            )
            self._workers[account_id] = (thread, stop_event)
            thread.start()

    def stop_account(self, account_id: str) -> None:
        with self._lock:
            worker = self._workers.get(account_id)
        if not worker:
            return
        thread, stop_event = worker
        stop_event.set()
        thread.join(timeout=1.0)

    def _run_account(self, account_id: str, stop_event: threading.Event) -> None:
        try:
            startup_failures = 0
            while not self._shutdown.is_set() and not stop_event.is_set():
                lease = None
                try:
                    lease = self._store.acquire_runtime_lease(account_id)
                    if lease is None:
                        stop_event.wait(5)
                        continue
                    account = self._store.get_account_internal(account_id)
                    if not account or account['status'] != 'connected':
                        return
                    if account['provider'] != 'wechat':
                        self._store.set_runtime_status(account_id, 'unsupported')
                        return
                    credentials = self._credentials(account)
                    self._store.set_runtime_status(account_id, 'starting')
                    try:
                        self._wechat.notify_start(
                            base_url=credentials['base_url'],
                            token=credentials['token'],
                        )
                    except WeChatError as exc:
                        _logger.warning(
                            'wechat_notify_start_failed account_id=%s error=%s',
                            account_id,
                            exc,
                        )
                    startup_failures = 0
                    self._poll(
                        account,
                        credentials,
                        stop_event,
                        claim_owner=f'gateway_{uuid.uuid4().hex}',
                        lease=lease,
                    )
                except Exception as exc:
                    startup_failures += 1
                    delay = min(30, 2 ** min(startup_failures, 5))
                    _logger.exception(
                        'channel_runtime_failed account_id=%s retry_in=%s',
                        account_id,
                        delay,
                    )
                    try:
                        self._store.set_runtime_status(
                            account_id,
                            'failed',
                            str(exc)[:500],
                        )
                    except Exception:
                        pass
                    stop_event.wait(delay)
                finally:
                    if lease is not None:
                        if self._shutdown.is_set() or stop_event.is_set():
                            try:
                                self._store.set_runtime_status(
                                    account_id,
                                    'stopped',
                                )
                            except Exception:
                                pass
                        self._store.release_runtime_lease(lease)
                if not self._shutdown.is_set() and not stop_event.is_set():
                    stop_event.wait(2)
        finally:
            with self._lock:
                self._workers.pop(account_id, None)

    def _poll(
        self,
        account: dict[str, Any],
        credentials: dict[str, str],
        stop_event: threading.Event,
        claim_owner: str,
        lease: Any,
    ) -> None:
        account_id = str(account['id'])
        checkpoint = self._store.get_checkpoint(account_id)
        cursor = str(checkpoint.get('cursor') or '')
        timeout_ms = int(checkpoint.get('longpoll_timeout_ms') or 35000)
        failures = 0
        self._store.set_runtime_status(account_id, 'running')
        _logger.info('wechat_message_runtime_started account_id=%s', account_id)

        while not self._shutdown.is_set() and not stop_event.is_set():
            lease.execute('SELECT 1')
            try:
                self._inbound.retry_pending(
                    account_id,
                    send=lambda to_user_id, context_token, text, message_key: self._reply_then_welcome(
                        account,
                        credentials,
                        to_user_id,
                        context_token,
                        text,
                        message_key,
                    ),
                )
                result = self._wechat.get_updates(
                    base_url=credentials['base_url'],
                    token=credentials['token'],
                    cursor=cursor,
                    timeout_ms=timeout_ms,
                )
                if self._shutdown.is_set() or stop_event.is_set():
                    return
                failures = 0
                suggested_timeout = result.get('longpolling_timeout_ms')
                if (
                    isinstance(suggested_timeout, (int, float))
                    and math.isfinite(suggested_timeout)
                ):
                    timeout_ms = min(
                        _MAX_POLL_TIMEOUT_MS,
                        max(_MIN_POLL_TIMEOUT_MS, int(suggested_timeout)),
                    )
                for message in result.get('msgs') or []:
                    if isinstance(message, dict):
                        self._handle_message(
                            account,
                            credentials,
                            message,
                            claim_owner,
                        )
                next_cursor = str(result.get('get_updates_buf') or '')
                if next_cursor:
                    cursor = next_cursor
                self._store.save_checkpoint(account_id, cursor, timeout_ms)
            except WeChatError as exc:
                failures += 1
                delay = 30 if failures >= self._settings.wechat_max_consecutive_errors else 2
                error = f'{exc.__class__.__name__}: {exc}'
                self._store.set_runtime_status(account_id, 'degraded', error[:500])
                _logger.warning(
                    'wechat_getupdates_failed account_id=%s attempt=%s retry_in=%s',
                    account_id,
                    failures,
                    delay,
                )
                stop_event.wait(delay)
            except Exception as exc:
                failures += 1
                delay = 30 if failures >= self._settings.wechat_max_consecutive_errors else 2
                try:
                    self._store.set_runtime_status(
                        account_id,
                        'degraded',
                        exc.__class__.__name__,
                    )
                except Exception:
                    pass
                _logger.exception(
                    'channel_runtime_iteration_failed account_id=%s retry_in=%s',
                    account_id,
                    delay,
                )
                claim_owner = f'gateway_{uuid.uuid4().hex}'
                stop_event.wait(delay)

    def _handle_message(
        self,
        account: dict[str, Any],
        credentials: dict[str, str],
        message: dict[str, Any],
        claim_owner: str,
    ) -> None:
        account_id = str(account['id'])
        key = _message_key(message)

        sender_id = str(message.get('from_user_id') or '')
        if sender_id != credentials['authorized_user_id']:
            self._store.mark_message_processed(account_id, key, 'ignored_unauthorized')
            return
        if message.get('message_type') not in (None, 1):
            self._store.mark_message_processed(account_id, key, 'ignored_type')
            return
        context_token = str(message.get('context_token') or '')
        text = _message_text(message)
        if not text or not context_token:
            self._store.mark_message_processed(account_id, key, 'ignored_empty')
            return
        address_hash = hashlib.sha256(
            f'wechat:{account_id}:{sender_id}'.encode('utf-8')
        ).hexdigest()
        self._inbound.process(
            InboundMessage(
                provider='wechat',
                account_id=account_id,
                external_address_hash=address_hash,
                owner_user_id=str(account['owner_user_id']),
                sender_id=sender_id,
                context_token=context_token,
                text=text,
                message_key=key,
            ),
            claim_owner=claim_owner,
            send=lambda to_user_id, reply_context, reply_text, message_key: self._reply_then_welcome(
                account,
                credentials,
                to_user_id,
                reply_context,
                reply_text,
                message_key,
            ),
        )

    def _reply_then_welcome(
        self,
        account: dict[str, Any],
        credentials: dict[str, str],
        to_user_id: str,
        context_token: str,
        text: str,
        idempotency_seed: str,
    ) -> None:
        self._reply(
            credentials,
            str(account['id']),
            str(account['owner_user_id']),
            to_user_id,
            context_token,
            text,
            idempotency_seed,
        )
        self._send_pending_welcome(
            account=account,
            credentials=credentials,
            sender_id=to_user_id,
            context_token=context_token,
        )

    def _send_pending_welcome(
        self,
        *,
        account: dict[str, Any],
        credentials: dict[str, str],
        sender_id: str,
        context_token: str,
    ) -> None:
        if not account.get('welcome_pending'):
            return
        account_id = str(account['id'])
        try:
            self._reply(
                credentials,
                account_id,
                str(account['owner_user_id']),
                sender_id,
                context_token,
                _WELCOME_MESSAGE,
                f'account:{account_id}:welcome:v1',
            )
            self._store.mark_welcome_sent(account_id)
            account['welcome_pending'] = False
            _logger.info('wechat_welcome_sent account_id=%s', account_id)
        except Exception:
            # A welcome failure must never block the user's actual chat. The
            # stable idempotency seed makes a later retry safe.
            _logger.exception('wechat_welcome_send_failed account_id=%s', account_id)

    def _reply(
        self,
        credentials: dict[str, str],
        account_id: str,
        owner_user_id: str,
        to_user_id: str,
        context_token: str,
        text: str,
        idempotency_seed: str,
    ) -> None:
        run_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f'lazymind:{idempotency_seed}:run'))
        for part_index, part in enumerate(split_outbound_parts(text)):
            if part.kind == 'image':
                self._send_image_part(
                    credentials=credentials,
                    account_id=account_id,
                    owner_user_id=owner_user_id,
                    to_user_id=to_user_id,
                    context_token=context_token,
                    source=part.source,
                    idempotency_seed=idempotency_seed,
                    part_index=part_index,
                    run_id=run_id,
                )
                continue
            self._send_text_part(
                credentials=credentials,
                to_user_id=to_user_id,
                context_token=context_token,
                text=part.text,
                idempotency_seed=idempotency_seed,
                part_index=part_index,
                run_id=run_id,
            )

    def _send_text_part(
        self,
        *,
        credentials: dict[str, str],
        to_user_id: str,
        context_token: str,
        text: str,
        idempotency_seed: str,
        part_index: int,
        run_id: str,
    ) -> None:
        for chunk_index, chunk in enumerate(
            _split_text(text, self._settings.wechat_text_chunk_size)
        ):
            client_id = str(
                uuid.uuid5(
                    uuid.NAMESPACE_URL,
                    f'lazymind:{idempotency_seed}:part:{part_index}:chunk:{chunk_index}',
                )
            )
            self._retry_send(
                lambda chunk=chunk, client_id=client_id: self._wechat.send_text(
                    base_url=credentials['base_url'],
                    token=credentials['token'],
                    to_user_id=to_user_id,
                    context_token=context_token,
                    text=chunk,
                    client_id=client_id,
                    run_id=run_id,
                )
            )

    def _send_image_part(
        self,
        *,
        credentials: dict[str, str],
        account_id: str,
        owner_user_id: str,
        to_user_id: str,
        context_token: str,
        source: str,
        idempotency_seed: str,
        part_index: int,
        run_id: str,
    ) -> None:
        client_id = str(
            uuid.uuid5(
                uuid.NAMESPACE_URL,
                f'lazymind:{idempotency_seed}:part:{part_index}:image',
            )
        )
        media_state = self._load_media_state(
            account_id,
            idempotency_seed,
            owner_user_id,
        )
        part_key = str(part_index)
        stored = media_state.get(part_key)
        image_item = (
            stored.get('item')
            if isinstance(stored, dict)
            and stored.get('source') == source
            and isinstance(stored.get('item'), dict)
            else None
        )
        if image_item is None:
            image = self._lazymind.download_static_image(source=source)
            image_item = self._wechat.upload_image(
                base_url=credentials['base_url'],
                token=credentials['token'],
                to_user_id=to_user_id,
                image=image,
                idempotency_key=client_id,
            )
            media_state[part_key] = {'source': source, 'item': image_item}
            saved = self._store.save_reply_media(
                account_id,
                idempotency_seed,
                self._cipher.encrypt(owner_user_id, {'parts': media_state}),
            )
            if not saved:
                raise RuntimeError('Cannot persist the prepared channel image')
        self._retry_send(
            lambda: self._wechat.send_image(
                base_url=credentials['base_url'],
                token=credentials['token'],
                to_user_id=to_user_id,
                context_token=context_token,
                image_item=image_item,
                client_id=client_id,
                run_id=run_id,
            )
        )

    def _load_media_state(
        self,
        account_id: str,
        message_key: str,
        owner_user_id: str,
    ) -> dict[str, Any]:
        ciphertext = self._store.get_reply_media(account_id, message_key)
        if not ciphertext:
            return {}
        value = self._cipher.decrypt(owner_user_id, ciphertext)
        parts = value.get('parts')
        return dict(parts) if isinstance(parts, dict) else {}

    @staticmethod
    def _retry_send(send: Any) -> None:
        last_error: WeChatError | None = None
        for attempt in range(_SEND_ATTEMPTS):
            try:
                send()
                last_error = None
                break
            except WeChatError as exc:
                last_error = exc
                if attempt + 1 < _SEND_ATTEMPTS:
                    time.sleep(2)
        if last_error is not None:
            raise last_error

    def _credentials(self, account: dict[str, Any]) -> dict[str, str]:
        ciphertext = str(account['credentials_ciphertext'])
        try:
            raw = self._cipher.decrypt(
                str(account['owner_user_id']),
                ciphertext,
            )
        except Exception as exc:
            raise RuntimeError('Cannot decrypt channel credentials') from exc
        credentials = {
            'token': str(raw.get('token') or ''),
            'account_id': str(raw.get('account_id') or ''),
            'authorized_user_id': str(raw.get('authorized_user_id') or ''),
            'base_url': str(raw.get('base_url') or '').rstrip('/'),
        }
        if not all(credentials.values()):
            raise RuntimeError('Channel credentials are incomplete')
        if self._cipher.needs_migration(ciphertext):
            self._store.update_account_credentials(
                str(account['id']),
                self._cipher.encrypt(str(account['owner_user_id']), raw),
            )
        return credentials
