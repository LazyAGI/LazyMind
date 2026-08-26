from __future__ import annotations

import datetime as dt
import logging
import queue
import threading
import uuid
from typing import Any

from channel_gateway.common.application.ask_text import render_text_ask
from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
)
from channel_gateway.common.domain.chat import (
    CoreStreamUpdate,
    CoreToolProgress,
)
from channel_gateway.common.domain.outbound import OutboundRenderer
from channel_gateway.common.ports.messaging import ReplyStream
from channel_gateway.common.ports.repository import NavigationRepository
from channel_gateway.wechat.domain import WeChatError
from channel_gateway.wechat.ports import WeChatDeliveryClient


_logger = logging.getLogger(__name__)
_TYPING_REFRESH_SECONDS = 5.0
_STREAM_CLOSE_SECONDS = 25.0
_ASK_TTL = dt.timedelta(minutes=10)
_ASK_FALLBACK = 'LazyMind 正在等待补充信息。'
_TASK_FALLBACK = 'LazyMind 已创建后台任务。'
_STOP = object()


class WeChatPresentationRenderer:
    """Adds WeChat's plain-text interaction surfaces to common output parts."""

    def __init__(
        self,
        base: OutboundRenderer,
        store: NavigationRepository,
    ):
        self._base = base
        self._store = store

    def render(self, message: ClaimedOutbound) -> list[dict[str, Any]]:
        parts: list[dict[str, Any]] = list(self._base.render(message))
        presentations = self._presentations(message)
        ask = next(
            (item for item in reversed(presentations) if item.get('kind') == 'ask'),
            None,
        )
        task = next(
            (item for item in reversed(presentations) if item.get('kind') == 'task'),
            None,
        )
        if ask is not None:
            prompt = render_text_ask(ask)
            if prompt:
                self._store.save_selection_snapshot(
                    message.account_id,
                    message.order_key,
                    'ask',
                    [ask],
                    dt.datetime.now(dt.timezone.utc) + _ASK_TTL,
                )
                parts = self._without_fallback(parts, _ASK_FALLBACK)
                parts.extend(self._base.text_parts(prompt))
        if task is not None:
            status = render_wechat_task(task)
            if status:
                parts = self._without_fallback(parts, _TASK_FALLBACK)
                parts.extend(self._base.text_parts(status))
        return parts

    @staticmethod
    def _presentations(message: ClaimedOutbound) -> list[dict[str, Any]]:
        raw = message.metadata.get('presentations')
        return [
            dict(item)
            for item in (raw if isinstance(raw, list) else [])
            if isinstance(item, dict)
        ]

    @staticmethod
    def _without_fallback(
        parts: list[dict[str, Any]],
        fallback: str,
    ) -> list[dict[str, Any]]:
        return [
            part for part in parts
            if not (
                part.get('kind') == 'text'
                and str(part.get('text') or '').strip() == fallback
            )
        ]


class WeChatReplyStream(ReplyStream):
    """Sends typing keepalives and ordered iLink tool-progress items."""

    def __init__(
        self,
        *,
        message: ClaimedInbound,
        client: WeChatDeliveryClient,
        credentials: dict[str, str],
    ):
        self._message = message
        self._client = client
        self._credentials = credentials
        self._run_id = str(uuid.uuid5(
            uuid.NAMESPACE_URL,
            f'lazymind:{message.inbox_id}:stream',
        ))
        self._events: queue.Queue[CoreToolProgress | object] = queue.Queue()
        self._sent: set[tuple[str, str]] = set()
        self._closed = False
        self._thread: threading.Thread | None = None

    def update(self, snapshot: CoreStreamUpdate) -> None:
        if self._closed:
            return
        self._start()
        for progress in snapshot.tool_progress:
            key = (progress.tool_call_id, progress.phase)
            if key in self._sent:
                continue
            self._sent.add(key)
            self._events.put(progress)

    def finish(self, final_text: str) -> bool:
        del final_text
        self._close()
        return False

    def abort(self) -> None:
        self._close()

    def _close(self) -> None:
        if self._closed:
            return
        self._closed = True
        if self._thread is None:
            return
        self._events.put(_STOP)
        self._thread.join(timeout=_STREAM_CLOSE_SECONDS)
        if self._thread.is_alive():
            _logger.warning(
                'wechat_interaction_close_timeout inbox_id=%s',
                self._message.inbox_id,
            )

    def _start(self) -> None:
        if self._thread is not None:
            return
        self._thread = threading.Thread(
            target=self._run,
            name=f'wechat-interaction-{self._message.inbox_id[-8:]}',
            daemon=True,
        )
        self._thread.start()

    def _run(self) -> None:
        typing_ticket = self._typing_ticket()
        if typing_ticket:
            self._update_typing(typing_ticket, True)
        try:
            while True:
                try:
                    event = self._events.get(timeout=_TYPING_REFRESH_SECONDS)
                except queue.Empty:
                    if typing_ticket:
                        self._update_typing(typing_ticket, True)
                    continue
                try:
                    if event is _STOP:
                        return
                    if isinstance(event, CoreToolProgress):
                        self._send_progress(event)
                finally:
                    self._events.task_done()
        finally:
            if typing_ticket:
                self._update_typing(typing_ticket, False)

    def _typing_ticket(self) -> str:
        try:
            config = self._client.get_config(
                base_url=self._credentials['base_url'],
                token=self._credentials['token'],
                to_user_id=self._message.recipient_id,
                context_token=str(
                    self._message.provider_context.get('context_token') or ''
                ),
            )
        except WeChatError as exc:
            _logger.warning('wechat_getconfig_failed error=%s', exc)
            return ''
        return str(config.get('typing_ticket') or '').strip()

    def _update_typing(self, typing_ticket: str, typing: bool) -> None:
        try:
            self._client.send_typing(
                base_url=self._credentials['base_url'],
                token=self._credentials['token'],
                to_user_id=self._message.recipient_id,
                typing_ticket=typing_ticket,
                typing=typing,
            )
        except WeChatError as exc:
            _logger.warning('wechat_typing_failed typing=%s error=%s', typing, exc)

    def _send_progress(self, progress: CoreToolProgress) -> None:
        phase = progress.phase
        client_id = str(uuid.uuid5(
            uuid.NAMESPACE_URL,
            (
                f'lazymind:{self._message.inbox_id}:tool:'
                f'{progress.tool_call_id}:{phase}'
            ),
        ))
        try:
            self._client.send_tool_progress(
                base_url=self._credentials['base_url'],
                token=self._credentials['token'],
                to_user_id=self._message.recipient_id,
                context_token=str(
                    self._message.provider_context.get('context_token') or ''
                ),
                tool_name=progress.tool_name,
                tool_call_id=progress.tool_call_id,
                status=progress.status,
                started=phase == 'start',
                client_id=client_id,
                run_id=self._run_id,
            )
        except WeChatError as exc:
            _logger.warning(
                'wechat_tool_progress_failed tool_call_id=%s phase=%s error=%s',
                progress.tool_call_id,
                phase,
                exc,
            )


def render_wechat_task(task: dict[str, Any]) -> str:
    title = str(task.get('title') or '后台任务').strip()
    status = str(task.get('status') or '已创建').strip()
    lines = [f'⏳ {title}', f'状态：{status}']
    progress = task.get('progress')
    if isinstance(progress, int) and not isinstance(progress, bool):
        lines.append(f'进度：{max(0, min(100, progress))}%')
    phase = str(task.get('current_phase') or '').strip()
    if phase:
        lines.append(f'当前阶段：{phase}')
    lines.append('任务会在后台继续，完成或失败后将在微信中通知。')
    return '\n'.join(lines)
