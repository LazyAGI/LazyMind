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
_WORKFLOW_TERMINAL_GRACE_SECONDS = 180
_TASK_OUTBOX_LIMIT = 100
_MAX_WORKFLOW_TASKS = 20
_MAX_TASK_IMAGES = 20
_MONITOR_STATE_VERSION = 4
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
                monitor_state.get('workflow_terminal') is True
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
            workflow = workflow_tasks(tasks, anchor_task_id)
            if not workflow:
                continue
            visible_workflow = workflow[-_MAX_WORKFLOW_TASKS:]
            now = time.time()
            waiting, terminal, terminal_since = _workflow_state(
                workflow,
                monitor_state,
                now,
            )
            artifacts, omitted_artifacts = _task_artifact_manifest(
                outbound,
                part_index,
                workflow,
            )
            delivery = self._store.sync_task_artifact_outbounds(
                parent=outbound,
                part_index=part_index,
                artifacts=artifacts,
            )
            signature = _workflow_signature(
                visible_workflow,
                waiting_for_next_step=waiting,
                terminal=terminal,
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
                    card=FeishuPresentationRenderer.task_workflow_card(
                        visible_workflow,
                        waiting_for_next_step=waiting,
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
                    'workflow_terminal': terminal,
                    'delivery_settled': delivery_settled,
                    'artifacts_complete': artifacts_complete,
                    'failed_count': delivery['dead'],
                    'omitted_count': omitted_artifacts,
                    'manifest_hash': _artifact_manifest_hash(artifacts),
                    'terminal_since': terminal_since,
                    'latest_status': str(
                        workflow[-1].get('status') or ''
                    ).lower(),
                    'task_lineage': _task_lineage(workflow),
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
    workflow: list[dict[str, Any]],
) -> tuple[list[dict[str, str]], int]:
    artifacts: list[dict[str, str]] = []
    omitted = 0
    for task in workflow:
        images, task_omitted = _task_image_projection(task)
        omitted += task_omitted
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


def workflow_tasks(
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
    workflow_prefix = (
        anchor_title.split(':', 1)[0]
        if anchor_type == 'workflow_step' and ':' in anchor_title
        else ''
    )
    candidates = sorted(
        [
            task
            for task in tasks
            if int(task.get('seq_in_conversation') or 0) >= anchor_seq
            and (
                (
                    workflow_prefix
                    and str(task.get('agent_type') or '') == 'workflow_step'
                    and str(task.get('title') or '').startswith(
                        f'{workflow_prefix}:'
                    )
                )
                or (
                    not workflow_prefix
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
            > _WORKFLOW_TERMINAL_GRACE_SECONDS
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
    if str(latest.get('agent_type') or '') != 'workflow_step':
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
    current_lineage = _task_lineage(tasks)
    terminal_since = float(
        previous.get('terminal_since') or 0
    )
    if (
        str(previous.get('task_lineage') or '') != current_lineage
        or str(previous.get('latest_status') or '') != status
        or terminal_since <= 0
    ):
        terminal_since = now
    waiting = (
        now - terminal_since
        < _WORKFLOW_TERMINAL_GRACE_SECONDS
    )
    return waiting, not waiting, terminal_since


def _task_lineage(tasks: list[dict[str, Any]]) -> str:
    return hashlib.sha256(
        '\0'.join(
            str(task.get('task_id') or '')
            for task in tasks
        ).encode()
    ).hexdigest()


def _workflow_signature(
    tasks: list[dict[str, Any]],
    *,
    waiting_for_next_step: bool,
    terminal: bool,
    image_delivery: dict[str, int],
    omitted_images: int,
) -> str:
    payload = {
        'waiting': waiting_for_next_step,
        'terminal': terminal,
        'image_delivery': image_delivery,
        'omitted_images': omitted_images,
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
