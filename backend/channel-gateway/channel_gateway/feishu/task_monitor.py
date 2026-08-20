from __future__ import annotations

import hashlib
import json
import logging
import threading
import uuid
from typing import Any
from urllib.parse import urlsplit

from channel_gateway.common.domain.channel import ClaimedOutbound
from channel_gateway.common.domain.outbound import is_image_content_type
from channel_gateway.common.ports.core import TaskClient
from channel_gateway.common.ports.providers import RuntimeCredentialStore
from channel_gateway.feishu.domain import workspace_card_expired
from channel_gateway.feishu.ports import (
    FeishuOutboundFactory,
    FeishuTaskOutboxRepository,
)
from channel_gateway.feishu.presentation import (
    FeishuPresentationRenderer,
)


_logger = logging.getLogger(__name__)
_POLL_SECONDS = 5
_TASK_OUTBOX_LIMIT = 100
_MAX_TASK_IMAGES = 20
_MONITOR_STATE_VERSION = 5
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


class FeishuTaskCardMonitor:
    """Keeps Feishu task cards aligned with Core's async task state."""

    def __init__(
        self,
        *,
        store: FeishuTaskOutboxRepository,
        credentials: RuntimeCredentialStore,
        channels: FeishuOutboundFactory,
        tasks: TaskClient,
    ):
        self._store = store
        self._credentials = credentials
        self._channels = channels
        self._tasks = tasks
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
                after_sequence = 0
                while not self._stop.is_set():
                    outbounds = self._store.list_sent_task_outbounds(
                        provider='feishu',
                        limit=_TASK_OUTBOX_LIMIT,
                        after_sequence=after_sequence,
                    )
                    if not outbounds:
                        break
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
                    after_sequence = outbounds[-1].created_sequence
                    if len(outbounds) < _TASK_OUTBOX_LIMIT:
                        break
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
                monitor_state.get('task_terminal') is True
                and monitor_state.get('delivery_settled') is True
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
            task = find_task(tasks, anchor_task_id)
            if task is None:
                continue
            terminal = _task_terminal(task)
            artifacts, omitted_artifacts = _task_artifact_manifest(
                outbound,
                part_index,
                task,
            )
            delivery = self._store.sync_task_artifact_outbounds(
                parent=outbound,
                part_index=part_index,
                artifacts=artifacts,
            )
            signature = _task_signature(
                task,
                image_delivery=delivery,
                omitted_images=omitted_artifacts,
            )
            message_id = str(saved_state.get('message_id') or '')
            replacement_message_id = ''
            if (
                signature != str(monitor_state.get('signature') or '')
                or not message_id
            ):
                message_id, replacement_message_id = self._publish_card(
                    outbound=outbound,
                    account=account,
                    message_id=message_id,
                    card=FeishuPresentationRenderer.task_card(
                        task,
                        inflight_image_count=delivery['inflight'],
                        failed_image_count=delivery['dead'],
                        omitted_image_count=omitted_artifacts,
                    ),
                    part_index=part_index,
                )
            delivery_settled = terminal and delivery['inflight'] == 0
            artifacts_complete = bool(
                delivery_settled
                and delivery['dead'] == 0
                and omitted_artifacts == 0
            )
            expected_revision = int(
                monitor_state.get('monitor_revision') or 0
            )
            next_state = {
                **saved_state,
                'message_id': message_id,
                'task_monitor': {
                    'version': _MONITOR_STATE_VERSION,
                    'signature': signature,
                    'task_terminal': terminal,
                    'delivery_settled': delivery_settled,
                    'artifacts_complete': artifacts_complete,
                    'failed_count': delivery['dead'],
                    'omitted_count': omitted_artifacts,
                    'manifest_hash': _artifact_manifest_hash(artifacts),
                    'latest_status': str(task.get('status') or '').lower(),
                },
            }
            persisted = (
                self._store.compare_and_save_sent_task_monitor_state(
                    outbox_id=outbound.outbox_id,
                    part_index=part_index,
                    expected_revision=expected_revision,
                    state=next_state,
                    complete=delivery_settled,
                )
            )
            authoritative_message_id = str(
                (persisted or {}).get('message_id') or ''
            )
            if (
                replacement_message_id
                and authoritative_message_id != replacement_message_id
            ):
                self._expire_replacement_card(
                    account=account,
                    message_id=replacement_message_id,
                )

    def _publish_card(
        self,
        *,
        outbound: ClaimedOutbound,
        account: dict[str, Any],
        message_id: str,
        card: dict[str, Any],
        part_index: int,
    ) -> tuple[str, str]:
        sender = self._channels.create_sender(account['credentials'])
        try:
            if message_id:
                try:
                    sender.update_card(
                        message_id=message_id,
                        card=card,
                    )
                    return message_id, ''
                except Exception as exc:
                    if not workspace_card_expired(exc):
                        raise
            chat_id = str(
                outbound.provider_context.get('chat_id')
                or outbound.recipient_id
            )
            replacement = sender.send_card(
                chat_id=chat_id,
                card=card,
                idempotency_key=str(
                    uuid.uuid5(
                        uuid.NAMESPACE_URL,
                        (
                            f'lazymind:{outbound.outbox_id}:'
                            f'task-monitor:{part_index}:'
                            f'{message_id or "initial"}'
                        ),
                    )
                ),
            )
            if not replacement or replacement == message_id:
                raise RuntimeError(
                    'Feishu task card replacement was not created'
                )
            return replacement, replacement if message_id else ''
        finally:
            sender.close()

    def _expire_replacement_card(
        self,
        *,
        account: dict[str, Any],
        message_id: str,
    ) -> None:
        sender = self._channels.create_sender(account['credentials'])
        try:
            sender.update_card(
                message_id=message_id,
                card=FeishuPresentationRenderer.task_replaced_card(),
            )
        except Exception as exc:
            if not workspace_card_expired(exc):
                _logger.warning(
                    'feishu_task_replacement_expire_failed message_id=%s',
                    message_id,
                    exc_info=True,
                )
        finally:
            sender.close()


def _task_bindings(
    outbound: ClaimedOutbound,
) -> list[tuple[int, str, str]]:
    return [
        (
            part_index,
            str(part.get('task_id') or ''),
            str(part.get('conversation_id') or ''),
        )
        for part_index, part in enumerate(outbound.rendered_parts)
        if str(part.get('task_id') or '')
        and str(part.get('conversation_id') or '')
    ]


def _task_image_projection(
    task: dict[str, Any],
) -> tuple[list[tuple[str, str, str]], int]:
    task_id = str(task.get('task_id') or '')
    if not task_id:
        return [], 0
    images: dict[str, tuple[str, str, str]] = {}
    for artifact in (
        task.get('artifacts')
        if isinstance(task.get('artifacts'), list)
        else []
    ):
        if (
            not isinstance(artifact, dict)
            or not is_image_content_type(artifact.get('content_type'))
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
        artifact_key = hashlib.sha256(
            f'{task_id}\0{slot}\0{sequence}'.encode()
        ).hexdigest()
        images.pop(artifact_key, None)
        images[artifact_key] = (
            artifact_key,
            source,
            str(value.get('caption') or '').strip()[:300],
        )
    values = list(images.values())
    return (
        values[-_MAX_TASK_IMAGES:],
        max(0, len(values) - _MAX_TASK_IMAGES),
    )


def _task_artifact_manifest(
    outbound: ClaimedOutbound,
    part_index: int,
    task: dict[str, Any],
) -> tuple[list[dict[str, str]], int]:
    artifacts: list[dict[str, str]] = []
    images, omitted = _task_image_projection(task)
    for artifact_key, source, caption in images:
        artifacts.append({
            'artifact_key': artifact_key,
            'source': source,
            'caption': caption,
            'delivery_id': str(
                uuid.uuid5(
                    uuid.NAMESPACE_URL,
                    (
                        f'lazymind:{outbound.outbox_id}:'
                        f'task-artifact:{part_index}:{artifact_key}'
                    ),
                )
            ),
        })
    return artifacts, omitted


def _artifact_manifest_hash(artifacts: list[dict[str, str]]) -> str:
    return hashlib.sha256(
        '\0'.join(
            str(artifact.get('artifact_key') or '')
            for artifact in artifacts
        ).encode()
    ).hexdigest()


def _is_lazymind_static_file(source: str) -> bool:
    return urlsplit(source).path.startswith('/static-files/')


def find_task(
    tasks: list[dict[str, Any]],
    task_id: str,
) -> dict[str, Any] | None:
    return next(
        (
            task
            for task in tasks
            if str(task.get('task_id') or '') == task_id
        ),
        None,
    )


def _task_terminal(task: dict[str, Any]) -> bool:
    return str(task.get('status') or '').lower() in _TERMINAL_STATUSES


def _task_signature(
    task: dict[str, Any],
    *,
    image_delivery: dict[str, int],
    omitted_images: int,
) -> str:
    payload = {
        'image_delivery': image_delivery,
        'omitted_images': omitted_images,
        'task': {
            key: task.get(key)
            for key in (
                'task_id',
                'title',
                'agent_type',
                'status',
                'progress_pct',
                'current_phase',
                'estimated_sec',
                'summary',
                'updated_at',
            )
        },
    }
    encoded = json.dumps(
        payload,
        ensure_ascii=False,
        sort_keys=True,
        separators=(',', ':'),
        default=str,
    ).encode()
    return hashlib.sha256(encoded).hexdigest()
