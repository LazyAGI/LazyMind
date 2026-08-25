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
from channel_gateway.common.ports.messaging import (
    TaskArtifactOutboxRepository,
)
from channel_gateway.common.ports.providers import RuntimeCredentialStore


_logger = logging.getLogger(__name__)
_POLL_SECONDS = 2
_OUTBOX_BATCH_SIZE = 100
_MAX_ARTIFACTS = 20
TASK_ARTIFACT_MONITOR_VERSION = 1
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


def task_terminal(task: dict[str, Any]) -> bool:
    return str(task.get('status') or '').lower() in _TERMINAL_STATUSES


def artifact_manifest_hash(artifacts: list[dict[str, str]]) -> str:
    return hashlib.sha256(
        '\0'.join(
            str(artifact.get('artifact_key') or '')
            for artifact in artifacts
        ).encode()
    ).hexdigest()


def task_artifact_manifest(
    *,
    parent_outbox_id: str,
    part_index: int,
    tasks: list[dict[str, Any]],
    allowed_kinds: set[str],
    limit: int = _MAX_ARTIFACTS,
) -> tuple[list[dict[str, str]], int]:
    projected: dict[str, dict[str, str]] = {}
    for task in tasks:
        task_id = str(task.get('task_id') or '')
        if not task_id:
            continue
        raw_artifacts = task.get('artifacts')
        for artifact in (
            raw_artifacts if isinstance(raw_artifacts, list) else []
        ):
            if not isinstance(artifact, dict):
                continue
            content_type = str(
                artifact.get('content_type') or ''
            ).lower()
            value = _artifact_value(artifact.get('value'))
            if value is None:
                continue
            sources: list[tuple[str, str]] = []
            kind = ''
            if is_image_content_type(content_type):
                kind = 'image'
                source = str(value.get('url') or '')
                if _is_static_file(source):
                    sources.append((source, ''))
            elif content_type == 'file':
                kind = 'file'
                source = str(value.get('url') or '')
                if _is_static_file(source):
                    sources.append((source, _artifact_filename(value, source)))
            elif content_type == 'file_list':
                kind = 'file'
                paths = value.get('paths')
                for raw_source in (
                    paths if isinstance(paths, list) else []
                ):
                    source = str(raw_source or '')
                    if _is_static_file(source):
                        sources.append((source, _artifact_filename({}, source)))
            if kind not in allowed_kinds:
                continue
            slot = str(artifact.get('slot') or kind)
            sequence = str(artifact.get('seq') or 0)
            caption = str(
                value.get('caption')
                or artifact.get('caption')
                or ''
            ).strip()[:300]
            for source_index, (source, filename) in enumerate(sources):
                source_key = _artifact_source_key(source)
                artifact_key = hashlib.sha256(
                    (
                        f'{task_id}\0{slot}\0{sequence}\0'
                        f'{source_index}\0{kind}\0{source_key}'
                    ).encode()
                ).hexdigest()
                projected[artifact_key] = {
                    'artifact_key': artifact_key,
                    'kind': kind,
                    'source': source,
                    'filename': filename,
                    'caption': caption,
                    'delivery_id': _delivery_id(
                        parent_outbox_id,
                        part_index,
                        artifact_key,
                    ),
                }
    values = list(projected.values())
    bounded_limit = max(1, limit)
    return values[-bounded_limit:], max(0, len(values) - bounded_limit)


def _artifact_value(value: Any) -> dict[str, Any] | None:
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except json.JSONDecodeError:
            return None
    return dict(value) if isinstance(value, dict) else None


def _is_static_file(source: str) -> bool:
    return bool(
        source
        and len(source) <= 2048
        and urlsplit(source).path.startswith('/static-files/')
    )


def _artifact_source_key(source: str) -> str:
    """Return the stable resource identity behind a refreshed signed URL."""
    parsed = urlsplit(source)
    return parsed.path


def _artifact_filename(value: dict[str, Any], source: str) -> str:
    return str(
        value.get('filename')
        or value.get('name')
        or urlsplit(source).path.rsplit('/', 1)[-1]
        or 'lazymind-output'
    )[:255]


def _delivery_id(
    parent_outbox_id: str,
    part_index: int,
    artifact_key: str,
) -> str:
    return str(
        uuid.uuid5(
            uuid.NAMESPACE_URL,
            (
                f'lazymind:{parent_outbox_id}:'
                f'task-artifact:{part_index}:{artifact_key}'
            ),
        )
    )


def task_bindings(
    outbound: ClaimedOutbound,
) -> list[tuple[str, str]]:
    raw_presentations = outbound.metadata.get('presentations')
    seen: set[tuple[str, str]] = set()
    result: list[tuple[str, str]] = []
    for presentation in (
        raw_presentations if isinstance(raw_presentations, list) else []
    ):
        if not isinstance(presentation, dict):
            continue
        if presentation.get('kind') != 'task':
            continue
        binding = (
            str(presentation.get('task_id') or ''),
            str(presentation.get('conversation_id') or ''),
        )
        if not all(binding) or binding in seen:
            continue
        seen.add(binding)
        result.append(binding)
    return result


class TaskArtifactMonitor:
    """Projects Core task artifacts into the durable channel Outbox."""

    def __init__(
        self,
        *,
        provider: str,
        store: TaskArtifactOutboxRepository,
        credentials: RuntimeCredentialStore,
        tasks: TaskClient,
    ):
        self._provider = provider
        self._store = store
        self._credentials = credentials
        self._tasks = tasks
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None

    def start(self) -> None:
        if self._thread is not None:
            return
        self._stop.clear()
        self._thread = threading.Thread(
            target=self._run,
            name=f'{self._provider}-task-artifacts',
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
                self._scan()
            except Exception:
                _logger.exception(
                    'channel_task_artifact_monitor_failed provider=%s',
                    self._provider,
                )
            self._stop.wait(_POLL_SECONDS)

    def _scan(self) -> None:
        after_sequence = 0
        while not self._stop.is_set():
            outbounds = self._store.list_sent_task_artifact_outbounds(
                provider=self._provider,
                limit=_OUTBOX_BATCH_SIZE,
                after_sequence=after_sequence,
                monitor_version=TASK_ARTIFACT_MONITOR_VERSION,
            )
            if not outbounds:
                return
            for outbound in outbounds:
                if self._stop.is_set():
                    return
                try:
                    self._refresh(outbound)
                except Exception:
                    _logger.exception(
                        'channel_task_artifact_refresh_failed '
                        'provider=%s outbox_id=%s',
                        self._provider,
                        outbound.outbox_id,
                    )
            after_sequence = outbounds[-1].created_sequence
            if len(outbounds) < _OUTBOX_BATCH_SIZE:
                return

    def _refresh(self, outbound: ClaimedOutbound) -> None:
        bindings = task_bindings(outbound)
        if not bindings:
            return
        monitor_index = len(outbound.rendered_parts)
        saved_state = dict(
            outbound.provider_state.get(str(monitor_index)) or {}
        )
        monitor_state = dict(saved_state.get('task_monitor') or {})
        if (
            monitor_state.get('task_terminal') is True
            and monitor_state.get('delivery_settled') is True
            and int(monitor_state.get('version') or 0)
            >= TASK_ARTIFACT_MONITOR_VERSION
        ):
            return
        account = self._credentials.load_runtime_account(
            outbound.account_id
        )
        owner_user_id = str(account['owner_user_id'])
        tasks_by_conversation: dict[str, list[dict[str, Any]]] = {}
        selected: list[dict[str, Any]] = []
        for task_id, conversation_id in bindings:
            if conversation_id not in tasks_by_conversation:
                tasks_by_conversation[conversation_id] = (
                    self._tasks.list_conversation_tasks(
                        owner_user_id=owner_user_id,
                        conversation_id=conversation_id,
                        request_id=(
                            f'channel_{self._provider}_task_artifacts_'
                            f'{outbound.outbox_id[-16:]}'
                        ),
                    )
                )
            task = find_task(tasks_by_conversation[conversation_id], task_id)
            if task is not None:
                selected.append(task)
        artifacts, omitted = task_artifact_manifest(
            parent_outbox_id=outbound.outbox_id,
            part_index=monitor_index,
            tasks=selected,
            allowed_kinds={'image', 'file'},
        )
        delivery = self._store.sync_task_artifact_outbounds(
            parent=outbound,
            part_index=monitor_index,
            artifacts=artifacts,
            provider_context=dict(outbound.provider_context),
        )
        terminal = bool(
            len(selected) == len(bindings)
            and selected
            and all(task_terminal(task) for task in selected)
        )
        delivery_settled = terminal and delivery['inflight'] == 0
        expected_revision = int(
            monitor_state.get('monitor_revision') or 0
        )
        self._store.compare_and_save_sent_task_monitor_state(
            outbox_id=outbound.outbox_id,
            part_index=monitor_index,
            expected_revision=expected_revision,
            state={
                **saved_state,
                'task_monitor': {
                    'version': TASK_ARTIFACT_MONITOR_VERSION,
                    'task_terminal': terminal,
                    'delivery_settled': delivery_settled,
                    'artifacts_complete': bool(
                        delivery_settled
                        and delivery['dead'] == 0
                        and omitted == 0
                    ),
                    'failed_count': delivery['dead'],
                    'omitted_count': omitted,
                    'manifest_hash': artifact_manifest_hash(artifacts),
                },
            },
            complete=delivery_settled,
        )
