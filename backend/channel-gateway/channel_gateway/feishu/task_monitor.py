from __future__ import annotations

import datetime as dt
import hashlib
import json
import logging
import threading
import time
import uuid
from typing import Any
from urllib.parse import urlsplit

from channel_gateway.common.domain.channel import ClaimedOutbound
from channel_gateway.common.ports.core import (
    StaticAssetClient,
    TaskClient,
)
from channel_gateway.common.ports.providers import RuntimeCredentialStore
from channel_gateway.feishu.ports import (
    FeishuOutboundFactory,
    FeishuTaskOutboxRepository,
)
from channel_gateway.feishu.presentation import (
    FeishuPresentationRenderer,
)


_logger = logging.getLogger(__name__)
_POLL_SECONDS = 5
_PLUGIN_TERMINAL_GRACE_SECONDS = 180
_TASK_OUTBOX_LIMIT = 100
_MONITOR_STATE_VERSION = 3
_MAX_FEISHU_IMAGE_BYTES = 10 * 1024 * 1024
_TERMINAL_STATUSES = {
    'completed',
    'succeeded',
    'success',
    'failed',
    'cancelled',
    'canceled',
    'stopped',
    'interrupted',
}
_NON_RETRYABLE_TERMINAL_STATUSES = {
    'cancelled',
    'canceled',
    'stopped',
}


class FeishuTaskCardMonitor:
    """Keeps Feishu task cards aligned with Core's async task state."""

    def __init__(
        self,
        *,
        store: FeishuTaskOutboxRepository,
        credentials: RuntimeCredentialStore,
        channels: FeishuOutboundFactory,
        tasks: TaskClient,
        assets: StaticAssetClient,
    ):
        self._store = store
        self._credentials = credentials
        self._channels = channels
        self._tasks = tasks
        self._assets = assets
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None

    def start(self) -> None:
        if self._thread is not None:
            return
        self._stop.clear()
        self._thread = threading.Thread(
            target=self._run,
            name='feishu-task-cards',
            daemon=True,
        )
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()
        if self._thread is not None:
            self._thread.join(timeout=2)
            self._thread = None

    def _run(self) -> None:
        while not self._stop.is_set():
            try:
                outbounds = self._store.list_sent_task_outbounds(
                    provider='feishu',
                    limit=_TASK_OUTBOX_LIMIT,
                )
                for outbound in outbounds:
                    if self._stop.is_set():
                        return
                    try:
                        self._refresh_outbound(outbound)
                    except Exception:
                        _logger.exception(
                            'feishu_task_card_refresh_failed '
                            'outbox_id=%s',
                            outbound.outbox_id,
                        )
            except Exception:
                _logger.exception('feishu_task_card_monitor_failed')
            self._stop.wait(_POLL_SECONDS)

    def _refresh_outbound(self, outbound: ClaimedOutbound) -> None:
        bindings = _task_bindings(outbound)
        if not bindings:
            return
        account = self._credentials.load_runtime_account(
            outbound.account_id
        )
        owner_user_id = str(account['owner_user_id'])
        for part_index, anchor_task_id, conversation_id in bindings:
            saved_state = dict(
                outbound.provider_state.get(str(part_index)) or {}
            )
            monitor_state = dict(
                saved_state.get('task_monitor') or {}
            )
            if (
                monitor_state.get('terminal') is True
                and monitor_state.get('artifacts_complete') is True
                and int(monitor_state.get('version') or 1)
                >= _MONITOR_STATE_VERSION
            ):
                continue
            if not conversation_id:
                continue
            tasks = self._tasks.list_conversation_tasks(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=(
                    f'channel_feishu_task_'
                    f'{outbound.outbox_id[-16:]}_{part_index}'
                ),
            )
            workflow = _workflow_tasks(tasks, anchor_task_id)
            if not workflow:
                continue
            now = time.time()
            waiting, terminal, terminal_since = _workflow_state(
                workflow,
                monitor_state,
                now,
            )
            signature = _workflow_signature(
                workflow,
                waiting_for_next_step=waiting,
                terminal=terminal,
            )
            message_id = str(saved_state.get('message_id') or '')
            if signature != str(
                monitor_state.get('signature') or ''
            ) or not message_id:
                card = FeishuPresentationRenderer.task_workflow_card(
                    workflow,
                    waiting_for_next_step=waiting,
                )
                message_id = self._publish_card(
                    outbound=outbound,
                    account=account,
                    message_id=message_id,
                    card=card,
                    part_index=part_index,
                )
            delivered_artifacts = self._deliver_images(
                outbound=outbound,
                account=account,
                workflow=workflow,
                delivered=monitor_state.get('delivered_artifacts'),
                part_index=part_index,
            )
            artifact_keys = {
                artifact_key
                for task in workflow
                for artifact_key, _, _ in _task_images(task)
            }
            next_state = {
                **saved_state,
                'message_id': message_id,
                'task_monitor': {
                    'version': _MONITOR_STATE_VERSION,
                    'signature': signature,
                    'terminal': terminal,
                    'artifacts_complete': (
                        terminal
                        and artifact_keys.issubset(delivered_artifacts)
                    ),
                    'terminal_since': terminal_since,
                    'latest_status': str(
                        workflow[-1].get('status') or ''
                    ).lower(),
                    'task_ids': [
                        str(task.get('task_id') or '')
                        for task in workflow
                    ],
                    'delivered_artifacts': sorted(delivered_artifacts),
                },
            }
            if next_state != saved_state:
                if not self._store.save_sent_outbound_part_state(
                    outbox_id=outbound.outbox_id,
                    part_index=part_index,
                    state=next_state,
                ):
                    raise RuntimeError(
                        'Cannot persist Feishu task card state'
                    )

    def _deliver_images(
        self,
        *,
        outbound: ClaimedOutbound,
        account: dict[str, Any],
        workflow: list[dict[str, Any]],
        delivered: Any,
        part_index: int,
    ) -> set[str]:
        delivered_keys = {
            str(value)
            for value in (
                delivered if isinstance(delivered, list) else []
            )
            if value
        }
        pending = [
            (artifact_key, source, caption)
            for task in workflow
            for artifact_key, source, caption in _task_images(task)
            if artifact_key not in delivered_keys
        ]
        if not pending:
            return delivered_keys
        chat_id = str(
            outbound.provider_context.get('chat_id')
            or outbound.recipient_id
        )
        if not chat_id:
            raise RuntimeError('Feishu destination chat is missing')
        owner_user_id = str(account['owner_user_id'])
        sender = self._channels.create_sender(account['credentials'])
        try:
            for artifact_key, source, caption in pending:
                try:
                    content = self._assets.download_static_image(
                        source=source,
                        owner_user_id=owner_user_id,
                    )
                    if len(content) > _MAX_FEISHU_IMAGE_BYTES:
                        raise RuntimeError('飞书图片不能超过 10 MB')
                    sender.send_image(
                        chat_id=chat_id,
                        content=content,
                        caption=caption,
                        idempotency_key=str(
                            uuid.uuid5(
                                uuid.NAMESPACE_URL,
                                (
                                    f'lazymind:{outbound.outbox_id}:'
                                    f'task-artifact:{part_index}:'
                                    f'{artifact_key}'
                                ),
                            )
                        ),
                    )
                    delivered_keys.add(artifact_key)
                except Exception:
                    _logger.exception(
                        'feishu_task_image_delivery_failed '
                        'outbox_id=%s artifact_key=%s',
                        outbound.outbox_id,
                        artifact_key,
                    )
        finally:
            sender.close()
        return delivered_keys

    def _publish_card(
        self,
        *,
        outbound: ClaimedOutbound,
        account: dict[str, Any],
        message_id: str,
        card: dict[str, Any],
        part_index: int,
    ) -> str:
        sender = self._channels.create_sender(account['credentials'])
        try:
            if message_id:
                sender.update_card(
                    message_id=message_id,
                    card=card,
                )
                return message_id
            chat_id = str(
                outbound.provider_context.get('chat_id')
                or outbound.recipient_id
            )
            return sender.send_card(
                chat_id=chat_id,
                card=card,
                idempotency_key=str(
                    uuid.uuid5(
                        uuid.NAMESPACE_URL,
                        (
                            f'lazymind:{outbound.outbox_id}:'
                            f'task-monitor:{part_index}'
                        ),
                    )
                ),
            )
        finally:
            sender.close()


def _task_bindings(
    outbound: ClaimedOutbound,
) -> list[tuple[int, str, str]]:
    presentations = [
        dict(item)
        for item in (
            outbound.metadata.get('presentations')
            if isinstance(
                outbound.metadata.get('presentations'),
                list,
            )
            else []
        )
        if isinstance(item, dict)
        and item.get('kind') == 'task'
        and item.get('task_id')
    ]
    event_conversations = {
        str(event.get('payload', {}).get('task_id') or ''): str(
            event.get('payload', {}).get('conversation_id') or ''
        )
        for event in (
            outbound.metadata.get('core_events')
            if isinstance(
                outbound.metadata.get('core_events'),
                list,
            )
            else []
        )
        if isinstance(event, dict)
        and isinstance(event.get('payload'), dict)
    }
    bindings: list[tuple[int, str, str]] = []
    claimed: set[str] = set()
    for part_index, part in enumerate(outbound.rendered_parts):
        task_id = str(part.get('task_id') or '')
        presentation = next(
            (
                item
                for item in presentations
                if str(item.get('task_id') or '') == task_id
            ),
            None,
        )
        if not task_id:
            title = _card_title(part)
            presentation = next(
                (
                    item
                    for item in presentations
                    if str(item.get('task_id') or '') not in claimed
                    and str(item.get('title') or '') == title
                ),
                None,
            )
            task_id = str(
                presentation.get('task_id') if presentation else ''
            )
        if not task_id or presentation is None:
            continue
        conversation_id = str(
            part.get('conversation_id')
            or presentation.get('conversation_id')
            or event_conversations.get(task_id)
            or ''
        )
        bindings.append((part_index, task_id, conversation_id))
        claimed.add(task_id)
    return bindings


def _card_title(part: dict[str, Any]) -> str:
    card = part.get('card')
    if not isinstance(card, dict):
        return ''
    header = card.get('header')
    if not isinstance(header, dict):
        return ''
    title = header.get('title')
    if not isinstance(title, dict):
        return ''
    return str(title.get('content') or '')


def _task_images(
    task: dict[str, Any],
) -> list[tuple[str, str, str]]:
    task_id = str(task.get('task_id') or '')
    if not task_id:
        return []
    images: list[tuple[str, str, str]] = []
    for artifact in (
        task.get('artifacts')
        if isinstance(task.get('artifacts'), list)
        else []
    ):
        if (
            not isinstance(artifact, dict)
            or str(artifact.get('content_type') or '').lower()
            != 'image'
        ):
            continue
        value = artifact.get('value')
        if not isinstance(value, dict):
            continue
        source = str(value.get('url') or '').strip()
        if not _is_lazymind_static_file(source):
            continue
        slot = str(artifact.get('slot') or 'image')
        sequence = str(artifact.get('seq') or 0)
        images.append(
            (
                f'{task_id}:{slot}:{sequence}',
                source,
                str(value.get('caption') or '').strip(),
            )
        )
    return images


def _is_lazymind_static_file(source: str) -> bool:
    return urlsplit(source).path.startswith('/static-files/')


def _workflow_tasks(
    tasks: list[dict[str, Any]],
    anchor_task_id: str,
) -> list[dict[str, Any]]:
    anchor = next(
        (
            task
            for task in tasks
            if str(task.get('task_id') or '') == anchor_task_id
        ),
        None,
    )
    if anchor is None:
        return []
    anchor_seq = int(anchor.get('seq_in_conversation') or 0)
    anchor_type = str(anchor.get('agent_type') or '')
    anchor_title = str(anchor.get('title') or '')
    plugin_prefix = (
        anchor_title.split(':', 1)[0]
        if anchor_type == 'plugin_step' and ':' in anchor_title
        else ''
    )
    candidates = sorted(
        [
            task
            for task in tasks
            if int(task.get('seq_in_conversation') or 0) >= anchor_seq
            and (
                (
                    plugin_prefix
                    and str(task.get('agent_type') or '') == 'plugin_step'
                    and str(task.get('title') or '').startswith(
                        f'{plugin_prefix}:'
                    )
                )
                or (
                    not plugin_prefix
                    and str(task.get('task_id') or '') == anchor_task_id
                )
            )
        ],
        key=lambda task: int(
            task.get('seq_in_conversation') or 0
        ),
    )
    workflow: list[dict[str, Any]] = []
    for task in candidates:
        if workflow and (
            str(workflow[-1].get('status') or '').lower()
            in _TERMINAL_STATUSES
            and _task_gap_seconds(workflow[-1], task)
            > _PLUGIN_TERMINAL_GRACE_SECONDS
        ):
            break
        workflow.append(task)
    return workflow


def _task_gap_seconds(
    previous: dict[str, Any],
    current: dict[str, Any],
) -> float:
    previous_at = _parse_task_time(previous.get('updated_at'))
    current_at = _parse_task_time(current.get('created_at'))
    if previous_at is None or current_at is None:
        return 0
    return max(0, (current_at - previous_at).total_seconds())


def _parse_task_time(value: Any) -> dt.datetime | None:
    text = str(value or '').strip()
    if not text:
        return None
    try:
        parsed = dt.datetime.fromisoformat(
            text.replace('Z', '+00:00')
        )
    except ValueError:
        return None
    if parsed.tzinfo is None:
        return parsed.replace(tzinfo=dt.timezone.utc)
    return parsed


def _workflow_state(
    tasks: list[dict[str, Any]],
    previous: dict[str, Any],
    now: float,
) -> tuple[bool, bool, float]:
    latest = tasks[-1]
    status = str(latest.get('status') or '').lower()
    if status not in _TERMINAL_STATUSES:
        return False, False, 0
    if status in _NON_RETRYABLE_TERMINAL_STATUSES:
        return False, True, now
    if str(latest.get('agent_type') or '') != 'plugin_step':
        return False, True, now
    if (
        status in {'completed', 'succeeded', 'success'}
        and str(latest.get('title') or '')
        .split(':', 1)[-1]
        .strip()
        .lower() in {
            'generate_image',
            'enhance_image',
        }
    ):
        return False, True, now
    previous_ids = previous.get('task_ids')
    current_ids = [
        str(task.get('task_id') or '')
        for task in tasks
    ]
    terminal_since = float(
        previous.get('terminal_since') or 0
    )
    if (
        previous_ids != current_ids
        or str(previous.get('latest_status') or '') != status
        or terminal_since <= 0
    ):
        terminal_since = now
    waiting = (
        now - terminal_since
        < _PLUGIN_TERMINAL_GRACE_SECONDS
    )
    return waiting, not waiting, terminal_since


def _workflow_signature(
    tasks: list[dict[str, Any]],
    *,
    waiting_for_next_step: bool,
    terminal: bool,
) -> str:
    payload = {
        'waiting': waiting_for_next_step,
        'terminal': terminal,
        'tasks': [
            {
                key: task.get(key)
                for key in (
                    'task_id',
                    'seq_in_conversation',
                    'title',
                    'agent_type',
                    'status',
                    'progress_pct',
                    'current_phase',
                    'estimated_sec',
                    'summary',
                    'updated_at',
                )
            }
            for task in tasks
        ],
    }
    encoded = json.dumps(
        payload,
        ensure_ascii=False,
        sort_keys=True,
        separators=(',', ':'),
        default=str,
    ).encode()
    return hashlib.sha256(encoded).hexdigest()
