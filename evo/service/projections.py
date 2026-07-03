from __future__ import annotations

import json
import re
import uuid
from collections.abc import Iterable, Mapping
from pathlib import Path
from typing import Any

from fastapi import HTTPException

from evo.artifact_runtime.evo import catalog as C
from evo.artifact_runtime.kernel import ArtifactKey, ArtifactRef
from evo.operations.repair.trace import RepairTraceStore

from .runtime_port import RuntimePort

CANDIDATE_STEPS = ('repair', 'abtest')
ARTIFACT_KIND_BY_STEP = {
    'dataset': 'datasets',
    'eval': 'eval-reports',
    'analysis': 'analysis-reports',
    'repair': 'diffs',
    'abtest': 'abtests',
}
EVENT_TYPE_BY_ARTIFACT = {
    C.CORPUS_REPORT: 'dataset.load_corpus',
    C.CORPUS_SNAPSHOT: 'dataset.build_snapshot',
    C.EVAL_CASE_PREPARATION: 'dataset.prepare_case',
    C.EVAL_CASE: 'dataset.generate_case',
    C.EVAL_RAG_ANSWER: 'eval.answer',
    C.EVAL_JUDGE_RESULT: 'eval.judge',
    C.ANALYSIS_TRACE_SUMMARY: 'analysis.trace_summary',
    C.ANALYSIS_CASE_CLASSIFICATION: 'analysis.classify_case',
    C.ANALYSIS_TRACE_CLUSTERS: 'analysis.cluster',
    C.REPAIR_PLAN: 'repair.plan',
    C.REPAIR_CANDIDATE_WORKSPACE: 'repair.workspace',
    C.REPAIR_LOOP_RESULT: 'repair.loop',
    C.ABTEST_CANDIDATE_SERVICE: 'abtest.candidate_service',
    C.ABTEST_CANDIDATE_RAG_ANSWER: 'abtest.candidate_answer',
    C.ABTEST_CANDIDATE_JUDGE_RESULT: 'abtest.candidate_judge',
    C.ABTEST_CANDIDATE_EVAL_SUMMARY: 'abtest.candidate_summary',
}
REPAIR_TRACE_TYPES = {
    'repair.attempt_started': 'repair.attempt',
    'repair.base_selected': 'repair.attempt',
    'repair.decision_completed': 'repair.loop',
    'repair.loop_completed': 'repair.loop',
    'repair.patch_verified': 'repair.verify',
    'opencode.setup': 'repair.opencode',
    'opencode.process_start': 'repair.opencode',
    'opencode.heartbeat': 'repair.opencode',
    'opencode.message': 'repair.opencode',
    'opencode.code': 'repair.opencode_code',
    'opencode.tool_use.search': 'repair.opencode_tool',
    'opencode.tool_use.read_file': 'repair.opencode_tool',
    'opencode.tool_use.edit_file': 'repair.opencode_tool',
    'opencode.tool_use.run_command': 'repair.opencode_tool',
    'opencode.error': 'repair.opencode',
    'opencode.process_exit': 'repair.opencode',
    'verify.pre_validation_started': 'repair.verify',
    'verify.diff_scope_completed': 'repair.verify',
    'verify.hardcode_check_completed': 'repair.verify',
    'verify.command_started': 'repair.verify',
    'verify.command_completed': 'repair.verify',
    'verify.pre_validation_completed': 'repair.verify',
    'candidate.service_started': 'repair.candidate_eval',
    'candidate.service_ready': 'repair.candidate_eval',
    'candidate.service_failed': 'repair.candidate_eval',
    'candidate.case_started': 'repair.candidate_eval',
    'candidate.case_completed': 'repair.candidate_eval',
    'candidate.eval_summary_completed': 'repair.candidate_eval',
    'analysis.candidate_started': 'repair.candidate_eval',
    'analysis.candidate_completed': 'repair.candidate_eval',
    'analysis.delta_completed': 'repair.delta',
}
STEP_BY_ARTIFACT = {
    spec.artifact_id: step
    for step, specs in C.OUTPUTS.items()
    for spec in specs
}
STEP_ID_NAMESPACE = uuid.uuid5(uuid.NAMESPACE_URL, 'lazyrag:evo:step-events:v1')
SECRET = re.compile(r'(api[_-]?key|token|secret|password|authorization|llm_config)', re.I)
AUTH_SECRET = re.compile(
    r'(?i)\bauthorization\s*[:=]\s*(?:bearer|basic|token)?\s*[^\s,;)\]}]+'
)
INLINE_SECRET = re.compile(
    r'(?i)\b(authorization|api[_-]?key|token|secret|password)\b\s*[:=]\s*(?:bearer\s+)?[^\s,;)\]}]+'
)
FILE_URI = re.compile(r'file:///[^\n\r,;)\]}]+')
URL = re.compile(r'(?i)\bhttps?://[^\s,;)\]}]+')
ABS_PATH = re.compile(r'(?<![:/\w])/(?!/)(?:[^\s,;)\]}\"\']*/)+[^\s,;)\]}\"\']*')


class ProjectionService:
    def __init__(self, root: Path, runtime: RuntimePort) -> None:
        self.root = root
        self.runtime = runtime
        self.repair_trace = RepairTraceStore(root)
        self.download_root = root / 'downloads'
        self.download_root.mkdir(parents=True, exist_ok=True)

    def gates(self, thread_id: str) -> dict[str, Any]:
        self._require_thread(thread_id)
        store = self.runtime.store()
        try:
            effective = store.effective_artifacts(thread_id)
            items = []
            for step in C.STEPS:
                artifact_id = C.ROOTS[step]
                key = ArtifactKey.of(artifact_id)
                versions = sorted(record.ref.version for record in store.history(thread_id, key))
                effective_ref = effective.get(key)
                items.append({
                    'step': step,
                    'artifact_id': artifact_id,
                    'versions': versions,
                    'effective_version': None if effective_ref is None else effective_ref.version,
                    'latest_version': max(versions, default=None),
                })
            return {'thread_id': thread_id, 'gates': items}
        finally:
            store.close()

    def gate_content(self, thread_id: str, step: str, version: int) -> dict[str, Any]:
        value = public_value(self._gate_value(thread_id, step, version))
        return {'thread_id': thread_id, 'step': step, 'version': version, 'content': value}

    def gate_download(self, thread_id: str, step: str, version: int, file_format: str) -> Path:
        if file_format != 'json':
            raise HTTPException(422, 'format must be json')
        value = public_value(self._gate_value(thread_id, step, version))
        target = self.download_root / thread_id / f'{step}-v{version}.{file_format}'
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True), encoding='utf-8')
        return target

    def events(self, thread_id: str, step_id: str = '', after_event_id: str = '') -> dict[str, Any]:
        config = self._require_thread(thread_id)
        store = self.runtime.store()
        try:
            rows = _source_event_rows(thread_id, store)
            step_id = _normalized_step_id(step_id)
            if step_id and step_id not in {row['step_id'] for row in rows}:
                raise HTTPException(422, 'unknown step_id for thread')
            items = _display_events(
                [row for row in rows if not step_id or row['step_id'] == step_id],
                _num_case(config),
            )
            if after_event_id:
                ids = [str(item['event_id']) for item in items]
                if after_event_id not in ids:
                    raise HTTPException(422, 'unknown event_id for event scope')
                items = items[ids.index(after_event_id) + 1:]
        finally:
            store.close()
        return {
            'thread_id': thread_id,
            'step_id': step_id or None,
            'items': items,
        }

    def event_trace(self, thread_id: str, step_id: str, after_event_id: str = '') -> dict[str, Any]:
        self._require_thread(thread_id)
        step_id = _normalized_step_id(step_id)
        if not step_id:
            raise HTTPException(422, 'step_id is required')
        store = self.runtime.store()
        try:
            rows = _source_event_rows(thread_id, store)
        finally:
            store.close()
        repair_step_ids = {row['step_id'] for row in rows if row['stage'] == 'repair'}
        stages = {row['stage'] for row in rows if row['step_id'] == step_id}
        if not stages:
            raise HTTPException(422, 'unknown step_id for thread')
        if 'repair' in stages and len(repair_step_ids) > 1:
            raise HTTPException(409, 'repair trace is ambiguous for repeated repair scopes')
        items = _trace_items(thread_id, step_id, self.repair_trace.read_since(thread_id)) if 'repair' in stages else []
        if after_event_id:
            ids = [str(item['event_id']) for item in items]
            if after_event_id not in ids:
                raise HTTPException(422, 'unknown event_id for event trace scope')
            items = items[ids.index(after_event_id) + 1:]
        return {'thread_id': thread_id, 'step_id': step_id, 'items': items}

    def candidates(self, thread_id: str, status: str, page_size: int, page_token: str) -> dict[str, Any]:
        run_ids = [thread_id] if thread_id else self.runtime.run_ids()
        if thread_id:
            self._require_thread(thread_id)
        items = []
        for run_id in run_ids:
            items.extend(self._candidate_items(run_id))
        if status:
            items = [item for item in items if item.get('status') == status]
        items.sort(key=lambda item: item['candidate_id'])
        items = [item for item in items if not page_token or item['candidate_id'] > page_token]
        page = items[:page_size]
        next_token = page[-1]['candidate_id'] if len(page) == page_size else ''
        return {'items': page, 'next_page_token': next_token}

    def candidate(self, candidate_id: str) -> dict[str, Any]:
        thread_id, step, version = _parse_candidate_id(candidate_id)
        detail = self.gate_content(thread_id, step, version)
        content = detail['content'] if isinstance(detail['content'], Mapping) else {}
        return _candidate_row(thread_id, step, version, content, detail=True)

    def _candidate_items(self, thread_id: str) -> list[dict[str, Any]]:
        rows = []
        store = self.runtime.store()
        try:
            for step in CANDIDATE_STEPS:
                key = ArtifactKey.of(C.ROOTS[step])
                for record in store.history(thread_id, key):
                    rows.append(
                        _candidate_row(thread_id, step, record.ref.version, public_value(record.value))
                    )
        finally:
            store.close()
        return rows

    def _gate_value(self, thread_id: str, step: str, version: int) -> object:
        self._require_thread(thread_id)
        if step not in C.ROOTS:
            raise HTTPException(422, f'step must be one of: {", ".join(C.STEPS)}')
        if version < 1:
            raise HTTPException(422, 'version must be positive')
        store = self.runtime.store()
        try:
            record = store.get(thread_id, ArtifactRef(ArtifactKey.of(C.ROOTS[step]), version))
            if record is None or record.run_id != thread_id:
                raise HTTPException(404, 'gate artifact version not found')
            return record.value
        finally:
            store.close()

    def _require_thread(self, thread_id: str) -> Mapping[str, Any]:
        config = self.runtime.run_config(thread_id)
        if config is None:
            raise HTTPException(404, f'thread not found: {thread_id}')
        return config


def public_value(value: object) -> object:
    if isinstance(value, Mapping):
        return {
            str(key): '<redacted>' if SECRET.search(str(key)) else public_value(item)
            for key, item in value.items()
        }
    if isinstance(value, list):
        return [public_value(item) for item in value]
    if isinstance(value, tuple):
        return [public_value(item) for item in value]
    if isinstance(value, str):
        text = AUTH_SECRET.sub('authorization=<redacted>', value)
        text = INLINE_SECRET.sub(lambda match: f'{match.group(1)}=<redacted>', text)
        return ABS_PATH.sub('<redacted-path>', URL.sub('<redacted-url>', FILE_URI.sub('file://<redacted-path>', text)))
    return value


def _candidate_row(thread_id: str, step: str, version: int, content: object, *, detail: bool = False) -> dict[str, Any]:
    data = content if isinstance(content, Mapping) else {}
    diff = data.get('diff') if isinstance(data.get('diff'), Mapping) else {}
    files = [str(item) for item in diff if _safe_file(str(item))]
    row = {
        'candidate_id': f'{thread_id}:{C.ROOTS[step]}@v{version}',
        'thread_id': thread_id,
        'source_step': step,
        'source_ref': f'{C.ROOTS[step]}@v{version}',
        'status': str(data.get('status') or ''),
        'summary': _summary(data),
    }
    if detail:
        row['files'] = files
    return row


def _summary(data: Mapping[str, Any]) -> dict[str, Any]:
    diff = data.get('diff') if isinstance(data.get('diff'), Mapping) else {}
    return {
        **{key: public_value(data[key]) for key in ('status', 'verdict', 'algo_id',
                                                    'candidate_algo_id') if key in data},
        **({'diff_files': [item for item in diff if _safe_file(str(item))]} if diff else {}),
    }


def _normalized_step_id(value: str) -> str:
    value = value.strip()
    if not value:
        return ''
    try:
        return str(uuid.UUID(value))
    except ValueError as exc:
        raise HTTPException(422, 'invalid step_id') from exc


def _source_event_rows(thread_id: str, store: Any) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    span_ids: list[str] = []
    current_step, current_id, closed = '', '', True
    span_counts = {step: 0 for step in C.STEPS}
    for event in store.events_since(0, thread_id):
        refs_by_step = _refs_by_step(event.refs)
        if event.kind != 'committed':
            if current_step in refs_by_step:
                closed = True
            continue
        for step, refs in refs_by_step.items():
            if step != current_step or closed:
                current_step, closed = step, False
                span_counts[step] += 1
                current_id = str(uuid.uuid5(STEP_ID_NAMESPACE, f'{thread_id}:{step}:{span_counts[step]}'))
                span_ids.append(current_id)
            for ref in refs:
                rows.append({
                    'order': event.seq * 1000 + len(rows),
                    'event_id': str(uuid.uuid5(STEP_ID_NAMESPACE, f'{thread_id}:event:{event.seq}:{_ref_text(ref)}')),
                    'step_id': current_id,
                    'next_step_id': None,
                    'stage': step,
                    'ref': ref,
                })
            if any(ref.key.artifact_id == C.ROOTS[step] for ref in refs):
                closed = True
    first_by_id = {}
    for row in rows:
        first_by_id.setdefault(row['step_id'], row)
    next_by_id = {span_id: span_ids[index + 1] for index, span_id in enumerate(span_ids[:-1])}
    return [
        dict(
            row,
            next_step_id=next_by_id.get(row['step_id']),
            _next_row=first_by_id.get(next_by_id.get(row['step_id'], '')),
        )
        for row in rows
    ]


def _refs_by_step(refs: Iterable[ArtifactRef]) -> dict[str, list[ArtifactRef]]:
    grouped = {step: [] for step in C.STEPS}
    for ref in refs:
        step = STEP_BY_ARTIFACT.get(ref.key.artifact_id)
        if step:
            grouped[step].append(ref)
    return {step: grouped[step] for step in C.STEPS if grouped[step]}


def _display_events(rows: list[dict[str, Any]], num_case: int) -> list[dict[str, Any]]:
    items: list[dict[str, Any]] = []
    case_counts: dict[str, int] = {}
    ordered = sorted(rows, key=lambda item: item['order'])
    last_order_by_step = {}
    for row in ordered:
        last_order_by_step[row['step_id']] = row['order']
    for row in ordered:
        item = _artifact_event(row, num_case, case_counts)
        if item:
            items.append(item)
        if row['order'] == last_order_by_step[row['step_id']]:
            _append_transition(items, row)
    return items


def _append_transition(items: list[dict[str, Any]], row: Mapping[str, Any]) -> None:
    next_row = row.get('_next_row')
    if not row.get('next_step_id') or not isinstance(next_row, Mapping):
        return
    event_id = str(uuid.uuid5(STEP_ID_NAMESPACE, f"{row['step_id']}:transition:{row['next_step_id']}"))
    if any(item['event_id'] == event_id for item in items):
        return
    items.append({
        'event_id': event_id,
        'step_id': row['step_id'],
        'next_step_id': row['next_step_id'],
        'stage': row['stage'],
        'event_type': 'step.transition',
        'action': 'completed',
        'summary': {'next_stage': next_row['stage']},
    })


def _artifact_event(row: Mapping[str, Any], num_case: int, case_counts: dict[str, int]) -> dict[str, Any] | None:
    ref = row['ref']
    artifact_id = ref.key.artifact_id
    event_type = (
        'artifact.committed'
        if C.ROOTS.get(row['stage']) == artifact_id
        else EVENT_TYPE_BY_ARTIFACT.get(artifact_id)
    )
    if not event_type:
        return None
    item = _base_event(row, event_type, 'completed')
    if ref.key.partition:
        case_counts[artifact_id] = case_counts.get(artifact_id, 0) + 1
        total = num_case or case_counts[artifact_id]
        item['case'] = {'id': ref.key.partition, 'index': _case_index(ref.key.partition), 'total': total}
        item['progress'] = _progress(case_counts[artifact_id], total)
    elif artifact_id in C.ROOTS.values():
        item['progress'] = {'percent': 100}
    item['artifact'] = _artifact_locator(str(row['stage']), ref)
    return _clean_empty(item)


def _trace_items(thread_id: str, step_id: str, trace_rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    items = []
    for row in trace_rows:
        item = _trace_item(thread_id, step_id, row)
        if item:
            items.append(item)
    return items


def _trace_item(thread_id: str, step_id: str, trace: Mapping[str, Any]) -> dict[str, Any] | None:
    event_type = REPAIR_TRACE_TYPES.get(str(trace.get('type') or ''))
    if not event_type:
        return None
    payload = trace.get('payload') if isinstance(trace.get('payload'), Mapping) else {}
    item = {
        'event_id': str(uuid.uuid5(STEP_ID_NAMESPACE, f"{thread_id}:event-trace:{trace.get('seq')}")),
        'step_id': step_id,
        'stage': 'repair',
        'event_type': event_type,
        'action': _action(str(trace.get('status') or 'running')),
    }
    case_id = str(payload.get('case_id') or '')
    if case_id:
        item['case'] = {'id': case_id}
    summary = {
        key: value
        for key, value in {
            'attempt': trace.get('attempt'),
            'tool_kind': payload.get('tool'),
            'exit_code': payload.get('returncode'),
            'decision': _scalar(payload.get('decision') or payload.get('decision_status')),
        }.items()
        if value not in ('', None, [], {})
    }
    if summary:
        item['summary'] = public_value(summary)
    return _clean_empty(item)


def _base_event(row: Mapping[str, Any], event_type: str, action: str) -> dict[str, Any]:
    return {
        'event_id': row['event_id'],
        'step_id': row['step_id'],
        'next_step_id': row.get('next_step_id'),
        'stage': row['stage'],
        'event_type': event_type,
        'action': action,
    }


def _artifact_locator(step: str, ref: ArtifactRef) -> dict[str, Any]:
    return {
        'id': ref.key.artifact_id,
        'kind': ARTIFACT_KIND_BY_STEP[step],
        'version': ref.version,
        'ref': _ref_text(ref),
    }


def _progress(current: int, total: int) -> dict[str, Any]:
    progress = {'current': current, 'total': total}
    if total > 0:
        progress['percent'] = round(min(100.0, current * 100.0 / total), 2)
    return progress


def _case_index(case_id: str) -> int | None:
    match = re.search(r'(\d+)$', case_id)
    return int(match.group(1)) if match else None


def _action(status: str) -> str:
    return {
        'started': 'running',
        'running': 'running',
        'completed': 'completed',
        'failed': 'failed',
        'skipped': 'skipped',
        'cancelled': 'canceled',
        'canceled': 'canceled',
        'paused': 'paused',
    }.get(status, 'running')


def _scalar(value: object) -> object:
    return value if isinstance(value, (str, int, float, bool)) else None


def _clean_empty(item: dict[str, Any]) -> dict[str, Any]:
    for key in list(item):
        if key != 'next_step_id' and item[key] in ({}, [], None):
            del item[key]
    return item


def _num_case(config: Mapping[str, Any]) -> int:
    return int(config.get('num_case') or (config.get('inputs') or {}).get('num_case') or 0)


def _parse_candidate_id(candidate_id: str) -> tuple[str, str, int]:
    thread_id, _, raw = candidate_id.partition(':')
    artifact, _, raw_version = raw.partition('@v')
    step = next((name for name in CANDIDATE_STEPS if C.ROOTS[name] == artifact), '')
    if not thread_id or not step or not raw_version.isdigit():
        raise HTTPException(404, 'candidate not found')
    return thread_id, step, int(raw_version)


def _safe_file(value: str) -> bool:
    return bool(value and not value.startswith('/') and '..' not in Path(value).parts)


def _ref_text(ref: ArtifactRef) -> str:
    key = (
        ref.key.artifact_id
        if not ref.key.partition
        else f'{ref.key.artifact_id}[{ref.key.partition}]'
    )
    return f'{key}@v{ref.version}'


__all__ = ['ProjectionService']
