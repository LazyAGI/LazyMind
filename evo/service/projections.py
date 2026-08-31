from __future__ import annotations

import asyncio
import json
import uuid
from collections.abc import Mapping
from datetime import datetime, timezone
from typing import Any

from evo import artifacts as A
from evo.artifact_flow import FlowDefinition, FlowSnapshot
from evo.artifact_runtime import (
    ArtifactKey,
    ArtifactRecord,
    ArtifactRef,
    AttemptSnapshot,
    PartitionSet,
    ProgressEvent,
)
from evo.operations.repair.trace import RepairTraceStore

from .contracts import ServiceError
from .public import public_thread_state, public_value


class ProjectionService:
    def __init__(self, flow: Any, definition: FlowDefinition,
                 repair_traces: RepairTraceStore
                 ) -> None:
        self.flow = flow
        self.definition = definition
        self.repair_traces = repair_traces

    async def gates(self, thread_id: str) -> dict[str, Any]:
        snapshot = await self.flow.snapshot(thread_id)
        histories = await asyncio.gather(*(
            self.flow.history(thread_id, stage.result_key)
            for stage in self.definition.stages
        ))
        gates = []
        for stage, history in zip(self.definition.stages, histories, strict=True):
            effective = snapshot.runtime.completed_artifacts.get(stage.result_key)
            versions = [record.ref.version for record in history]
            gates.append({
                'step': stage.name,
                'artifact_id': stage.result_key.artifact_id,
                'versions': versions,
                'effective_version': None if effective is None else effective.version,
                'latest_version': max(versions, default=None),
            })
        return {'thread_id': thread_id, 'gates': gates}

    async def gate_content(self, thread_id: str, stage: str,
                           version: int
                           ) -> dict[str, Any]:
        ref = await self._gate_ref(thread_id, stage, version)
        return {
            'thread_id': thread_id,
            'step': stage,
            'version': version,
            'content': public_value(await self.flow.read(thread_id, ref)),
        }

    async def gate_download(self, thread_id: str, stage: str,
                            version: int
                            ) -> tuple[str, bytes]:
        content = (await self.gate_content(thread_id, stage, version))['content']
        return (
            f'{stage}-v{version}.json',
            json.dumps(
                content,
                ensure_ascii=False,
                indent=2,
                sort_keys=True,
            ).encode(),
        )

    async def steps(self, thread_id: str) -> dict[str, Any]:
        _, _, _, items = await _execution_projection(
            self.flow,
            self.definition,
            thread_id,
        )
        return {
            'thread_id': thread_id,
            'active_step_id': next(
                (item['step_id'] for item in items if item['active']),
                '',
            ),
            'items': [_public_step(item) for item in items],
            'total_size': len(items),
        }

    async def events(self, thread_id: str, step_id: str = '',
                     after_event_id: str = ''
                     ) -> dict[str, Any]:
        return await execution_events(
            self.flow,
            self.definition,
            thread_id,
            step_id=step_id,
            after_event_id=after_event_id,
        )

    async def event_trace(self, thread_id: str, step_id: str,
                          after_event_id: str = ''
                          ) -> dict[str, Any]:
        snapshot, attempts, _, pages = await _execution_projection(
            self.flow,
            self.definition,
            thread_id,
        )
        page = _resolve_step(snapshot, pages, step_id)
        if page['stage'] == 'repair':
            execution = _repair_execution(attempts, page)
            trace_scope = await _repair_trace_scope(self.flow, thread_id, page)
            cursor = _repair_trace_cursor(thread_id, after_event_id)
            last_sequence = 0
            if cursor:
                last_sequence = await asyncio.to_thread(
                    self.repair_traces.last_seq,
                    thread_id,
                )
                if cursor > last_sequence:
                    raise ServiceError(422, 'unknown event_id for event scope')
            rows = (
                []
                if cursor and cursor == last_sequence
                else await asyncio.to_thread(
                    self.repair_traces.read_since,
                    thread_id,
                    cursor,
                )
            )
            rows = _repair_trace_rows(rows, page, trace_scope)
            execution_id = '' if execution is None else execution.attempt_id
            terminal = _step_terminal(page)
            return {
                'thread_id': thread_id,
                'step_id': step_id,
                'execution_id': execution_id,
                'items': [
                    _repair_trace_event(thread_id, step_id, execution_id, row)
                    for row in rows
                ],
                'terminal': terminal,
                'reason': (
                    _stream_end_reason(snapshot, page)
                    if terminal
                    else ''
                ),
                **public_thread_state(snapshot),
            }

        result = await self.events(thread_id, step_id, after_event_id)
        result['items'] = [
            item for item in result['items']
            if item.get('detail') or item.get('message')
        ]
        return result

    async def abtest_case_details(self, thread_id: str, version: int,
                                  page_size: int, page_token: str,
                                  keyword: str = '', outcome: str = ''
                                  ) -> dict[str, Any]:
        value = (await self.gate_content(thread_id, 'abtest', version))['content']
        data = value if isinstance(value, Mapping) else {}
        summary = data.get('summary') if isinstance(data.get('summary'), Mapping) else {}
        rows = _rows(data.get('case_deltas') or summary.get('case_deltas'))
        rows = rows or _comparison_rows(data)
        rows = _filter(rows, keyword, ('case_id', 'query', 'outcome'))
        if outcome:
            rows = [row for row in rows if row.get('outcome') == outcome]
        return _page(rows, page_size, page_token)

    async def trace_detail(self, thread_id: str, trace_id: str) -> dict[str, Any]:
        await self.flow.snapshot(thread_id)
        from evo.traces import build_trace_detail_view

        value = await asyncio.to_thread(build_trace_detail_view, trace_id)
        return public_value(value)

    async def trace_compare(self, thread_id: str, left: str,
                            right: str
                            ) -> dict[str, Any]:
        await self.flow.snapshot(thread_id)
        from evo.traces import build_trace_compare_view

        value = await asyncio.to_thread(build_trace_compare_view, left, right)
        return public_value(value)

    async def candidates(self, thread_id: str, status: str,
                         page_size: int, page_token: str
                         ) -> dict[str, Any]:
        if thread_id and not await self.flow.has_run(thread_id):
            raise ServiceError(404, f'thread not found: {thread_id}')
        run_ids = (thread_id,) if thread_id else await self.flow.run_ids()
        items = []
        for run_id in run_ids:
            for stage in ('repair', 'abtest'):
                key = ArtifactKey.scalar(A.ROOTS[stage])
                for record in await self.flow.history(run_id, key):
                    value = await self.flow.read(run_id, record.ref)
                    items.append(_candidate(run_id, stage, record.ref, value))
        if status:
            items = [item for item in items if item['status'] == status]
        items.sort(key=lambda item: item['candidate_id'])
        if page_token:
            items = [item for item in items if item['candidate_id'] > page_token]
        page = items[:page_size]
        return {
            'items': page,
            'next_page_token': (
                page[-1]['candidate_id'] if len(page) == page_size else ''
            ),
        }

    async def candidate(self, candidate_id: str) -> dict[str, Any]:
        thread_id, artifact, version = _parse_candidate(candidate_id)
        stage = next(
            (
                stage for stage in ('repair', 'abtest')
                if A.ROOTS[stage] == artifact
            ),
            '',
        )
        if not stage:
            raise ServiceError(404, 'candidate not found')
        ref = await self._gate_ref(thread_id, stage, version)
        value = await self.flow.read(thread_id, ref)
        return _candidate(thread_id, stage, ref, value, detail=True)

    async def _gate_ref(self, thread_id: str, stage: str,
                        version: int
                        ) -> ArtifactRef:
        if stage not in A.ROOTS:
            raise ServiceError(422, f'step must be one of: {", ".join(A.STEPS)}')
        if version < 1:
            raise ServiceError(422, 'version must be positive')
        ref = ArtifactRef(ArtifactKey.scalar(A.ROOTS[stage]), version)
        if await self.flow.record(thread_id, ref) is None:
            raise ServiceError(404, 'gate artifact version not found')
        return ref


def _stage_status(stage_status: str, flow_status: str) -> str:
    if stage_status == 'awaiting_approval':
        return 'completed'
    return 'idle' if stage_status == 'pending' and flow_status == 'idle' else stage_status


def _comparison_rows(value: Mapping[str, Any]) -> list[dict[str, Any]]:
    origin = value.get('origin') if isinstance(value.get('origin'), Mapping) else {}
    candidate = value.get('candidate') if isinstance(value.get('candidate'), Mapping) else {}
    before = {str(row.get('case_id') or ''): row for row in _rows(origin.get('cases'))}
    after = {str(row.get('case_id') or ''): row for row in _rows(candidate.get('cases'))}
    result = []
    for case_id in dict.fromkeys((*before, *after)):
        left, right = before.get(case_id, {}), after.get(case_id, {})
        old = float(left.get('overall') or left.get('overall_score') or 0)
        new = float(right.get('overall') or right.get('overall_score') or 0)
        result.append({
            'case_id': case_id,
            'outcome': (
                'improved' if new > old
                else 'regressed' if new < old
                else 'unchanged'
            ),
            'before': dict(left),
            'after': dict(right),
            'delta': {'overall_score': round(new - old, 4)},
        })
    return result


def _candidate(thread_id: str, stage: str, ref: ArtifactRef,
               value: object, *, detail: bool = False
               ) -> dict[str, Any]:
    data = value if isinstance(value, Mapping) else {}
    row = {
        'candidate_id': f'{thread_id}:{ref.key.artifact_id}@v{ref.version}',
        'thread_id': thread_id,
        'source_step': stage,
        'source_ref': f'{ref.key.artifact_id}@v{ref.version}',
        'status': str(data.get('status') or ''),
        'summary': public_value({
            key: data[key]
            for key in ('status', 'verdict', 'algo_id', 'candidate_algo_id')
            if key in data
        }),
    }
    if detail:
        diff = data.get('diff') if isinstance(data.get('diff'), Mapping) else {}
        row['files'] = [str(path) for path in diff if '/' not in str(path)]
    return row


def _parse_candidate(value: str) -> tuple[str, str, int]:
    try:
        thread_id, ref = value.split(':', 1)
        artifact, version = ref.rsplit('@v', 1)
        return thread_id, artifact, int(version)
    except ValueError as exc:
        raise ServiceError(404, 'candidate not found') from exc


def _rows(value: object) -> list[dict[str, Any]]:
    return [dict(row) for row in value] if isinstance(value, list) else []


def _filter(rows: list[dict[str, Any]], keyword: str,
            fields: tuple[str, ...]
            ) -> list[dict[str, Any]]:
    keyword = keyword.strip().lower()
    return rows if not keyword else [
        row for row in rows
        if any(keyword in str(row.get(field) or '').lower() for field in fields)
    ]


def _page(rows: list[dict[str, Any]], size: int, token: str) -> dict[str, Any]:
    if not str(token or '0').isdigit():
        raise ServiceError(422, 'page_token must be a non-negative integer offset')
    offset = int(token or 0)
    page = rows[offset:offset + size]
    return {
        'items': public_value(page),
        'next_page_token': str(offset + size) if offset + size < len(rows) else '',
        'total_size': len(rows),
    }


_TERMINAL_ATTEMPTS = frozenset({
    'cancelled', 'succeeded', 'failed', 'interrupted', 'discarded',
})
_TERMINAL_STEPS = frozenset({
    'completed', 'paused', 'cancelled', 'canceled', 'failed',
})
_STEP_ID_NAMESPACE = uuid.uuid5(
    uuid.NAMESPACE_URL,
    'lazyrag:evo:step-events:v1',
)


async def execution_events(flow: Any, definition: FlowDefinition,
                           run_id: str, *, step_id: str = '',
                           after_event_id: str = ''
                           ) -> dict[str, Any]:
    snapshot, _, items, pages = await _execution_projection(
        flow,
        definition,
        run_id,
    )
    page = None
    if step_id:
        page = _resolve_step(snapshot, pages, step_id)
        items = [item for item in items if item['step_id'] == step_id]
    if after_event_id:
        items = events_after(items, after_event_id)
    terminal = _terminal(snapshot) if page is None else _step_terminal(page)
    return {
        'thread_id': run_id,
        'step_id': step_id or None,
        'items': items,
        'terminal': terminal,
        'reason': _stream_end_reason(snapshot, page) if terminal else '',
        **public_thread_state(snapshot),
    }


async def _execution_projection(
    flow: Any,
    definition: FlowDefinition,
    run_id: str,
) -> tuple[
    FlowSnapshot,
    tuple[AttemptSnapshot, ...],
    list[dict[str, Any]],
    list[dict[str, Any]],
]:
    if not await flow.has_run(run_id):
        raise ServiceError(404, f'thread not found: {run_id}')

    snapshot, attempts, progress = await asyncio.gather(
        flow.snapshot(run_id),
        flow.attempts(run_id),
        flow.progress_events(run_id),
    )
    result_rows = await asyncio.gather(*(
        flow.history(run_id, item.result_key)
        for item in definition.stages
    ))
    approval_stages = tuple(
        item for item in definition.stages if item.approval_key is not None
    )
    approval_rows = await asyncio.gather(*(
        flow.history(run_id, item.approval_key)
        for item in approval_stages
    ))
    results = dict(zip(
        (item.name for item in definition.stages),
        result_rows,
        strict=True,
    ))
    approvals = {item.name: () for item in definition.stages}
    approvals.update(zip(
        (item.name for item in approval_stages),
        approval_rows,
        strict=True,
    ))

    items = flow_events(
        snapshot,
        attempts,
        progress,
        results,
        approvals,
        await _historical_partition_sets(flow, run_id, attempts),
        definition,
    )
    pages = _step_pages(snapshot, items)
    _link_execution_pages(snapshot, items, pages)
    return snapshot, attempts, items, pages


def flow_events(snapshot: FlowSnapshot,
                attempts: tuple[AttemptSnapshot, ...],
                progress: tuple[ProgressEvent, ...],
                results: Mapping[str, tuple[ArtifactRecord, ...]],
                approvals: Mapping[str, tuple[ArtifactRecord, ...]],
                partition_sets: Mapping[ArtifactRef, PartitionSet],
                definition: FlowDefinition
                ) -> list[dict[str, Any]]:
    rows: list[tuple[float, int, str, dict[str, Any]]] = []
    progress_by_attempt: dict[str, list[ProgressEvent]] = {}
    for event in progress:
        progress_by_attempt.setdefault(event.attempt_id, []).append(event)
    completions: dict[ArtifactKey, list[AttemptSnapshot]] = {}

    for attempt in sorted(attempts, key=lambda item: (item.created_at, item.attempt_id)):
        stage = _operation_stage(definition, attempt.operation_id)
        if not stage:
            continue
        case = attempt_case(snapshot, attempt, partition_sets)
        base = {
            'thread_id': snapshot.run_id,
            'step_id': f'{snapshot.run_id}:{stage}',
            'stage': stage,
            'next_step_id': '',
            'next_step_run_id': '',
            'operation_id': attempt.operation_id,
            'attempt_id': attempt.attempt_id,
            **({'case': case} if case is not None else {}),
        }
        rows.append((
            attempt.created_at,
            0,
            attempt.attempt_id,
            {
                **base,
                'event_id': f'{snapshot.run_id}:{attempt.attempt_id}:start',
                'event_type': attempt.operation_id,
                'status': 'running',
                'timestamp': attempt.created_at,
            },
        ))
        for event in sorted(
            progress_by_attempt.get(attempt.attempt_id, ()),
            key=lambda item: item.sequence,
        ):
            rows.append((
                event.created_at,
                1,
                f'{attempt.attempt_id}:{event.sequence}',
                {
                    **base,
                    'event_id': (
                        f'{snapshot.run_id}:{attempt.attempt_id}:'
                        f'progress:{event.sequence}'
                    ),
                    'event_type': event.update.phase,
                    'status': 'running',
                    'timestamp': event.created_at,
                    'message': event.update.message,
                    'progress': {
                        'current': event.update.current,
                        'total': event.update.total,
                    },
                    'detail': public_value(event.update.detail),
                },
            ))
        if attempt.status not in _TERMINAL_ATTEMPTS:
            continue

        finished = attempt.finished_at or attempt.started_at or attempt.created_at
        rows.append((
            finished,
            2,
            attempt.attempt_id,
            {
                **base,
                'event_id': f'{snapshot.run_id}:{attempt.attempt_id}:terminal',
                'event_type': attempt.operation_id,
                'status': _attempt_status(attempt.status),
                'timestamp': finished,
                **(
                    {'message': attempt.error.message}
                    if attempt.error is not None
                    else {}
                ),
            },
        ))
        if attempt.status == 'succeeded':
            for key in attempt.output_keys:
                completions.setdefault(key, []).append(attempt)

    for stage, records in results.items():
        for index, record in enumerate(records):
            attempt = _matching_attempt(completions, record, index)
            timestamp = _attempt_time(attempt, index)
            rows.append((
                timestamp,
                3,
                f'{stage}:{record.ref.version}',
                {
                    'thread_id': snapshot.run_id,
                    'step_id': f'{snapshot.run_id}:{stage}',
                    'stage': stage,
                    'next_step_id': '',
                    'next_step_run_id': '',
                    'event_id': f'{snapshot.run_id}:{stage}:v{record.ref.version}',
                    'event_type': 'step.finish',
                    'status': 'completed',
                    'timestamp': timestamp,
                    'artifact': _artifact(record),
                },
            ))

    for stage, records in approvals.items():
        stage_results = results.get(stage, ())
        for index, record in enumerate(records):
            result = stage_results[index] if index < len(stage_results) else None
            attempt = (
                None if result is None else _matching_attempt(completions, result, index)
            )
            timestamp = _attempt_time(attempt, index)
            rows.append((
                timestamp,
                4,
                f'{stage}:approval:{record.ref.version}',
                {
                    'thread_id': snapshot.run_id,
                    'step_id': f'{snapshot.run_id}:{stage}',
                    'stage': stage,
                    'next_step_id': '',
                    'next_step_run_id': '',
                    'event_id': (
                        f'{snapshot.run_id}:{stage}:'
                        f'approval:v{record.ref.version}'
                    ),
                    'event_type': 'checkpoint.continue',
                    'status': 'completed',
                    'timestamp': timestamp,
                    'artifact': _artifact(record),
                },
            ))

    rows.sort(key=lambda row: (row[0], row[1], row[2]))
    items = [row[3] for row in rows]
    _assign_step_ids(snapshot.run_id, items)
    pages = _step_pages(snapshot, items)
    _link_execution_pages(snapshot, items, pages)
    return items


def _assign_step_ids(run_id: str, items: list[dict[str, Any]]) -> None:
    counts: dict[str, int] = {}
    last_by_stage: dict[str, str] = {}
    current_stage = ''
    current_step_id = ''
    closed = True

    for item in items:
        stage = item['stage']
        if item['event_type'] == 'checkpoint.continue' and stage in last_by_stage:
            item['step_id'] = last_by_stage[stage]
            continue
        if stage != current_stage or closed:
            counts[stage] = counts.get(stage, 0) + 1
            current_stage = stage
            current_step_id = _step_id(run_id, stage, counts[stage])
            last_by_stage[stage] = current_step_id
            closed = False
        item['step_id'] = current_step_id
        if item['event_type'] == 'step.finish':
            closed = True


def _step_pages(snapshot: FlowSnapshot,
                items: list[dict[str, Any]]
                ) -> list[dict[str, Any]]:
    pages: list[dict[str, Any]] = []
    by_id: dict[str, dict[str, Any]] = {}
    for item in items:
        step_id = item['step_id']
        page = by_id.get(step_id)
        if page is None:
            page = {
                'thread_id': snapshot.run_id,
                'step_id': step_id,
                'stage': item['stage'],
                'title': item['stage'],
                'order_index': len(pages),
                'event_count': 0,
                'next_step_id': '',
                'next_step_run_id': '',
                'version': None,
                'status': 'running',
                'continues_previous': bool(
                    pages and pages[-1]['stage'] == item['stage']
                ),
                'active': False,
                '_closed': False,
                '_started_at': item['timestamp'],
                '_ended_at': item['timestamp'],
                '_next_started_at': None,
                '_next_stage': '',
            }
            by_id[step_id] = page
            pages.append(page)

        page['event_count'] += 1
        page['_started_at'] = min(page['_started_at'], item['timestamp'])
        page['_ended_at'] = max(page['_ended_at'], item['timestamp'])
        if item['event_type'] == 'step.finish':
            page['status'] = 'completed'
            page['version'] = item['artifact']['version']
            page['_closed'] = True
        elif item['status'] in {'failed', 'canceled'}:
            page['status'] = item['status']
        elif item['status'] == 'running':
            page['status'] = 'running'

    _append_current_page(snapshot, pages)
    current = next(
        (page for page in reversed(pages) if page['stage'] == snapshot.current_stage),
        None,
    )
    if current is not None and not current['_closed']:
        progress = next(
            stage for stage in snapshot.stages
            if stage.stage == snapshot.current_stage
        )
        current['status'] = _stage_status(progress.status, snapshot.status)

    for index, page in enumerate(pages):
        page['order_index'] = index
        page['continues_previous'] = bool(
            index and pages[index - 1]['stage'] == page['stage']
        )
        page['active'] = (
            index == len(pages) - 1
            and page['status'] in {'running', 'pausing', 'cancelling'}
        )
    return pages


def _append_current_page(snapshot: FlowSnapshot,
                         pages: list[dict[str, Any]]
                         ) -> None:
    if snapshot.status == 'completed':
        return
    progress = next(
        stage for stage in snapshot.stages
        if stage.stage == snapshot.current_stage
    )
    latest = next(
        (page for page in reversed(pages) if page['stage'] == progress.stage),
        None,
    )
    settled = progress.status in {'awaiting_approval', 'completed'}
    if settled and latest is not None:
        return
    if latest is not None and latest is pages[-1] and not latest['_closed']:
        return

    count = sum(page['stage'] == progress.stage for page in pages) + 1
    pages.append({
        'thread_id': snapshot.run_id,
        'step_id': _step_id(snapshot.run_id, progress.stage, count),
        'stage': progress.stage,
        'title': progress.stage,
        'order_index': len(pages),
        'event_count': 0,
        'next_step_id': '',
        'next_step_run_id': '',
        'version': (
            None if progress.result_ref is None else progress.result_ref.version
        ),
        'status': _stage_status(progress.status, snapshot.status),
        'continues_previous': bool(
            pages and pages[-1]['stage'] == progress.stage
        ),
        'active': False,
        '_closed': False,
        '_started_at': None,
        '_ended_at': None,
        '_next_started_at': None,
        '_next_stage': '',
    })


def _link_execution_pages(snapshot: FlowSnapshot,
                          items: list[dict[str, Any]],
                          pages: list[dict[str, Any]]
                          ) -> None:
    for index, page in enumerate(pages):
        next_page = pages[index + 1] if index + 1 < len(pages) else None
        if next_page is not None:
            page['next_step_id'] = next_page['step_id']
            page['next_step_run_id'] = next_page['step_id']
            page['_next_started_at'] = next_page['_started_at']
            page['_next_stage'] = next_page['stage']
            continue

        next_stage = _next_stage(snapshot, page['stage']) if page['_closed'] else ''
        page['_next_stage'] = next_stage
        if next_stage:
            count = sum(item['stage'] == next_stage for item in pages) + 1
            page['next_step_run_id'] = _step_id(
                snapshot.run_id,
                next_stage,
                count,
            )

    by_id = {page['step_id']: page for page in pages}
    for item in items:
        page = by_id[item['step_id']]
        item['next_step_id'] = page['next_step_id']
        item['next_step_run_id'] = page['next_step_run_id']


def _next_stage(snapshot: FlowSnapshot, stage: str) -> str:
    index = next(
        index for index, progress in enumerate(snapshot.stages)
        if progress.stage == stage
    )
    return (
        snapshot.stages[index + 1].stage
        if index + 1 < len(snapshot.stages)
        else ''
    )


def _step_id(run_id: str, stage: str, ordinal: int) -> str:
    return str(uuid.uuid5(
        _STEP_ID_NAMESPACE,
        f'{run_id}:{stage}:{ordinal}',
    ))


def _resolve_step(snapshot: FlowSnapshot,
                  pages: list[dict[str, Any]],
                  step_id: str
                  ) -> dict[str, Any]:
    page = next((item for item in pages if item['step_id'] == step_id), None)
    if page is not None:
        return page
    if pages and pages[-1]['next_step_run_id'] == step_id:
        source = pages[-1]
        return {
            'thread_id': snapshot.run_id,
            'step_id': step_id,
            'stage': source['_next_stage'],
            'title': source['_next_stage'],
            'order_index': len(pages),
            'event_count': 0,
            'next_step_id': '',
            'next_step_run_id': '',
            'version': None,
            'status': 'pending',
            'continues_previous': source['stage'] == source['_next_stage'],
            'active': False,
            '_closed': False,
            '_started_at': None,
            '_ended_at': None,
            '_next_started_at': None,
            '_next_stage': '',
        }
    raise ServiceError(422, 'unknown step_id for thread')


def _public_step(page: Mapping[str, Any]) -> dict[str, Any]:
    return {
        key: value for key, value in page.items()
        if not key.startswith('_') and key != 'thread_id'
    }


def _step_terminal(page: Mapping[str, Any]) -> bool:
    return bool(page['next_step_id']) or page['status'] in _TERMINAL_STEPS


def _stream_end_reason(
    snapshot: FlowSnapshot,
    page: Mapping[str, Any] | None,
) -> str:
    if page is not None:
        page_status = str(page['status'])
        if page_status == 'completed' or page['next_step_id']:
            pending = snapshot.pending_approval
            if pending is not None and pending.stage == page['stage']:
                return 'checkpoint_wait'
            if (
                snapshot.status == 'completed'
                and snapshot.current_stage == page['stage']
            ):
                return 'flow_completed'
            return 'step_completed'
        if page_status in {'cancelled', 'canceled'}:
            return 'cancelled'
        if page_status == 'failed':
            return 'failed'
        if page_status == 'paused':
            return 'user_paused'

    if snapshot.pending_approval is not None:
        return 'checkpoint_wait'
    return {
        'paused': 'user_paused',
        'cancelled': 'cancelled',
        'failed': 'failed',
        'completed': 'flow_completed',
    }.get(snapshot.status, 'step_completed')


def events_after(items: list[dict[str, Any]], event_id: str
                 ) -> list[dict[str, Any]]:
    for index, item in enumerate(items):
        if item['event_id'] == event_id:
            return items[index + 1:]
    raise ServiceError(422, 'unknown event_id for event scope')


_REPAIR_EXECUTION_OPERATIONS = frozenset({
    'repair.plan',
    'repair.candidate_workspace',
    'repair.loop_result',
})


def _repair_execution(attempts: tuple[AttemptSnapshot, ...],
                      page: Mapping[str, Any]
                      ) -> AttemptSnapshot | None:
    started_at = page['_started_at']
    next_started_at = page['_next_started_at']
    if started_at is None:
        return None
    return max(
        (
            attempt for attempt in attempts
            if attempt.operation_id in _REPAIR_EXECUTION_OPERATIONS
            and attempt.created_at >= started_at
            and (
                next_started_at is None
                or attempt.created_at < next_started_at
            )
        ),
        key=lambda attempt: (attempt.created_at, attempt.attempt_id),
        default=None,
    )


async def _repair_trace_scope(flow: Any, run_id: str,
                              page: Mapping[str, Any]
                              ) -> tuple[int, int] | None:
    version = page['version']
    if version is None:
        return None
    root_ref = ArtifactRef(
        ArtifactKey.scalar(A.REPAIR_VERIFIED_PATCH),
        version,
    )
    record = await flow.record(run_id, root_ref)
    if record is None:
        return None
    loop_ref = next(
        (
            ref for ref in record.input_refs
            if ref.key.artifact_id == A.REPAIR_LOOP_RESULT
        ),
        None,
    )
    if loop_ref is None:
        return None
    value = await flow.read(run_id, loop_ref)
    if not isinstance(value, Mapping):
        return None
    cursor = value.get('trace_cursor')
    if not isinstance(cursor, Mapping):
        return None
    try:
        start = int(cursor['seq_start'])
        end = int(cursor['seq_end'])
    except (KeyError, TypeError, ValueError):
        return None
    return (start, end) if 0 < start <= end else None


def _repair_trace_rows(rows: list[dict[str, Any]],
                       page: Mapping[str, Any],
                       scope: tuple[int, int] | None
                       ) -> list[dict[str, Any]]:
    if scope is not None:
        start, end = scope
        next_started_at = page['_next_started_at']
        scoped = [
            row for row in rows
            if start <= int(row.get('seq') or 0) <= end
        ]
        verified = next(
            (
                row for row in rows
                if int(row.get('seq') or 0) > end
                and row.get('type') == 'repair.patch_verified'
                and (
                    next_started_at is None
                    or _event_time(row) < next_started_at
                )
            ),
            None,
        )
        return scoped + ([] if verified is None else [verified])

    started_at = page['_started_at']
    next_started_at = page['_next_started_at']
    if started_at is None:
        return []
    return [
        row for row in rows
        if _event_time(row) >= started_at
        and (
            next_started_at is None
            or _event_time(row) < next_started_at
        )
    ]


def _repair_trace_event(thread_id: str, step_id: str,
                        execution_id: str, row: Mapping[str, Any]
                        ) -> dict[str, Any]:
    sequence = int(row['seq'])
    payload = row.get('payload')
    summary = dict(payload) if isinstance(payload, Mapping) else {}
    if row.get('attempt') is not None:
        summary.setdefault('attempt', row['attempt'])
    if row.get('message'):
        summary.setdefault('message', row['message'])
    timestamp = _event_timestamp(row.get('created_at'))
    return {
        'thread_id': thread_id,
        'step_id': step_id,
        'execution_id': str(row.get('execution_id') or execution_id),
        'stage': 'repair',
        'event_id': _repair_trace_event_id(thread_id, sequence),
        'event_type': str(row.get('type') or 'repair.trace'),
        'status': str(row.get('status') or 'running'),
        'timestamp': timestamp,
        'created_at': timestamp,
        'seq': sequence,
        'trace_id': str(row.get('trace_id') or ''),
        'materialization_key': str(row.get('materialization_key') or ''),
        'source': str(row.get('source') or 'repair'),
        'attempt': row.get('attempt'),
        'message': public_value(row.get('message') or ''),
        'payload': public_value(payload if isinstance(payload, Mapping) else {}),
        'summary': public_value(summary),
    }


def _repair_trace_cursor(thread_id: str, event_id: str) -> int:
    if not event_id:
        return 0
    prefix = f'{thread_id}:repair:trace:'
    sequence = event_id.removeprefix(prefix)
    if not event_id.startswith(prefix) or not sequence.isdigit() or int(sequence) < 1:
        raise ServiceError(422, 'unknown event_id for event scope')
    return int(sequence)


def _repair_trace_event_id(thread_id: str, sequence: int) -> str:
    return f'{thread_id}:repair:trace:{sequence}'


def _event_timestamp(value: object) -> str:
    try:
        return datetime.fromtimestamp(float(value), timezone.utc).isoformat()
    except (TypeError, ValueError, OSError):
        return ''


def _event_time(row: Mapping[str, Any]) -> float:
    try:
        return float(row.get('created_at') or 0)
    except (TypeError, ValueError):
        return 0


def attempt_case(snapshot: FlowSnapshot, attempt: AttemptSnapshot,
                 historical: Mapping[ArtifactRef, PartitionSet] | None = None
                 ) -> dict[str, Any] | None:
    partition_key = attempt.partition_key
    if not partition_key:
        return None
    output = next((key for key in attempt.output_keys if key.partition_key), None)
    if output is None:
        return {'id': partition_key}
    partition_set_id = A.PARTITION_SET_BY_ARTIFACT.get(output.artifact_id)
    if partition_set_id is None:
        return {'id': partition_key}

    partitions = next(
        (
            (historical or {}).get(ref)
            for ref in attempt.input_refs
            if ref.key == ArtifactKey.scalar(partition_set_id)
        ),
        None,
    )
    if partitions is None:
        partitions = snapshot.runtime.partition_sets.get(
            ArtifactKey.scalar(partition_set_id)
        )
    if partitions is None or partition_key not in partitions:
        return {'id': partition_key}
    return {
        'id': partition_key,
        'index': partitions.keys.index(partition_key) + 1,
        'total': len(partitions.keys),
    }


async def _historical_partition_sets(flow: Any, run_id: str,
                                     attempts: tuple[AttemptSnapshot, ...]
                                     ) -> Mapping[ArtifactRef, PartitionSet]:
    partition_ids = frozenset(A.PARTITION_SET_BY_ARTIFACT.values())
    refs = tuple(dict.fromkeys(
        ref
        for attempt in attempts
        for ref in attempt.input_refs
        if ref.key.artifact_id in partition_ids
    ))
    if not refs:
        return {}
    values = await flow.read_many(run_id, refs)
    return {
        ref: value
        for ref, value in values.items()
        if isinstance(value, PartitionSet)
    }


def _operation_stage(definition: FlowDefinition, operation_id: str) -> str:
    index = definition.stage_index_for_operation(operation_id)
    return '' if index is None else definition.stages[index].name


def _terminal(snapshot: FlowSnapshot) -> bool:
    if snapshot.status in {
        'paused', 'awaiting_approval', 'cancelled', 'failed', 'completed',
    }:
        return True
    return False


def _matching_attempt(completions: Mapping[ArtifactKey, list[AttemptSnapshot]],
                      record: ArtifactRecord, index: int
                      ) -> AttemptSnapshot | None:
    attempts = completions.get(record.ref.key, ())
    return attempts[index] if index < len(attempts) else None


def _attempt_time(attempt: AttemptSnapshot | None, fallback: int) -> float:
    if attempt is None:
        return float(fallback)
    return attempt.finished_at or attempt.started_at or attempt.created_at


def _attempt_status(status: str) -> str:
    return {
        'succeeded': 'completed',
        'cancelled': 'canceled',
        'interrupted': 'canceled',
        'discarded': 'canceled',
    }.get(status, status)


def _artifact(record: ArtifactRecord) -> dict[str, Any]:
    return {
        'artifact_id': record.ref.key.artifact_id,
        'partition_key': record.ref.key.partition_key,
        'version': record.ref.version,
        'ref': f'{record.ref.key.artifact_id}@v{record.ref.version}',
    }


__all__ = [
    'ProjectionService', 'attempt_case', 'events_after', 'flow_events',
]
