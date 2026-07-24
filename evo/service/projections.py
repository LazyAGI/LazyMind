from __future__ import annotations

import json
import re
import uuid
from collections.abc import Iterable, Mapping
from pathlib import Path
from typing import Any

from fastapi import HTTPException

from evo.artifact_flow.state import FlowRunState
from evo.artifact_runtime.evo import catalog as C
from evo.artifact_runtime.kernel import ArtifactKey, ArtifactRef
from evo.operations.abtest.comparison import compare_eval_detail_for_repair
from evo.operations.eval.materializers import build_eval_detail_summary
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
STEP_BY_ARTIFACT = {
    spec.artifact_id: step
    for step, specs in C.OUTPUTS.items()
    for spec in specs
}
STEP_ID_NAMESPACE = uuid.uuid5(uuid.NAMESPACE_URL, 'lazyrag:evo:step-events:v1')
GATE_BOUNDARY_BY_STATUS = {
    'paused': ('paused', 'pause'),
    'failed': ('failed', 'failed'),
    'cancelled': ('canceled', 'cancel'),
}
GATE_BOUNDARY_ACTIONS = tuple(action for _, action in GATE_BOUNDARY_BY_STATUS.values())
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

    def steps(self, thread_id: str) -> dict[str, Any]:
        config = self._require_thread(thread_id)
        state = self.runtime.gate_state(thread_id)
        snapshot = self.runtime.query(_num_case(config)).snapshot(thread_id)
        store = self.runtime.store()
        try:
            effective = store.effective_artifacts(thread_id)
            rows = _source_event_rows(thread_id, store, effective)
        finally:
            store.close()
        boundary_status, _ = _gate_boundary(state.status)
        boundary_step = _gate_boundary_step(state, snapshot.checkpoint.current_step, boundary_status)
        items = _step_items(thread_id, rows, state, boundary_step, boundary_status, snapshot.checkpoint.current_step)
        active = next((item['step_id'] for item in reversed(items) if item['active']), '')
        return {
            'thread_id': thread_id,
            'active_step_id': active,
            'items': items,
            'total_size': len(items),
        }

    def events(self, thread_id: str, step_id: str = '', after_event_id: str = '') -> dict[str, Any]:
        config = self._require_thread(thread_id)
        store = self.runtime.store()
        try:
            effective = store.effective_artifacts(thread_id)
            rows = _source_event_rows(thread_id, store, effective)
            state = self.runtime.gate_state(thread_id)
            snapshot = self.runtime.query(_num_case(config)).snapshot(thread_id)
            boundary_status, _ = _gate_boundary(state.status)
            boundary_step = _gate_boundary_step(state, snapshot.checkpoint.current_step, boundary_status)
            step_items = _step_items(
                thread_id,
                rows,
                state,
                boundary_step,
                boundary_status,
                snapshot.checkpoint.current_step,
            )
            gate_step_id = next((item['step_id'] for item in reversed(step_items) if item['active']), '')
            step_id = _normalized_step_id(step_id)
            current_step_ids = {item['step_id'] for item in step_items}
            if step_id and step_id not in current_step_ids and step_id in {row['step_id'] for row in rows}:
                return {'thread_id': thread_id, 'step_id': None, 'items': []}
            if step_id and step_id not in current_step_ids:
                raise HTTPException(422, 'unknown step_id for thread')
            items = _display_events(
                thread_id,
                [row for row in _visible_rows(rows) if not step_id or row['step_id'] == step_id],
                _num_case(config),
                state,
                boundary_step,
                gate_step_id,
                append_gate_boundary=not step_id or step_id == gate_step_id,
            )
            if after_event_id:
                sliced = _events_after_id(items, after_event_id)
                if sliced is None:
                    raise HTTPException(422, 'unknown event_id for event scope')
                items = sliced
        finally:
            store.close()
        return {
            'thread_id': thread_id,
            'step_id': step_id or None,
            'items': items,
        }

    def event_trace(self, thread_id: str, step_id: str, after_event_id: str = '') -> dict[str, Any]:
        config = self._require_thread(thread_id)
        step_id = _normalized_step_id(step_id)
        if not step_id:
            raise HTTPException(422, 'step_id is required')
        trace_cursor = None
        store = self.runtime.store()
        try:
            effective = store.effective_artifacts(thread_id)
            rows = _source_event_rows(thread_id, store, effective)
            visible = _visible_rows(rows)
            stages = {row['stage'] for row in visible if row['step_id'] == step_id}
            state = self.runtime.gate_state(thread_id)
            snapshot = self.runtime.query(_num_case(config)).snapshot(thread_id)
            boundary_status, _ = _gate_boundary(state.status)
            boundary_step = _gate_boundary_step(state, snapshot.checkpoint.current_step, boundary_status)
            step_items = _step_items(
                thread_id,
                rows,
                state,
                boundary_step,
                boundary_status,
                snapshot.checkpoint.current_step,
            )
            current_step_ids = {item['step_id'] for item in step_items}
            if not stages and step_id in current_step_ids:
                return {'thread_id': thread_id, 'step_id': step_id, 'items': []}
            if not stages and step_id in {row['step_id'] for row in rows}:
                return {'thread_id': thread_id, 'step_id': step_id, 'items': []}
            if not stages:
                raise HTTPException(422, 'unknown step_id for thread')
            if 'repair' in stages:
                trace_cursor = _repair_trace_cursor_for_step(thread_id, step_id, visible, store)
        finally:
            store.close()
        trace_rows = self.repair_trace.read_since(thread_id)
        if 'repair' in stages:
            trace_rows = _repair_trace_rows_for_step(visible, trace_rows, trace_cursor)
        items = _trace_items(thread_id, step_id, trace_rows) if 'repair' in stages else []
        if after_event_id:
            items = _trace_events_after_id(items, after_event_id)
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

    def eval_bad_cases(
        self,
        thread_id: str,
        version: int,
        page_size: int,
        page_token: str,
        keyword: str = '',
        failure_type: str = '',
    ) -> dict[str, Any]:
        self._require_thread(thread_id)
        store = self.runtime.store()
        try:
            record = self._gate_record(thread_id, 'eval', version, store)
            summary = _eval_detail_summary_from_record(store, thread_id, record, C.EVAL_JUDGE_RESULT)
            rows = [
                _bad_case_item(row)
                for row in _list_of_mappings(summary.get('bad_cases'))
            ]
        finally:
            store.close()
        rows = _filter_keyword(rows, keyword, ('case_id', 'question', 'defect', 'reason', 'failure_type'))
        if failure_type:
            rows = [row for row in rows if row.get('failure_type') == failure_type]
        page, next_token, total = _page(rows, page_size, page_token)
        return {'items': [public_value(row) for row in page],
                'next_page_token': next_token, 'total_size': total}

    def abtest_case_details(
        self,
        thread_id: str,
        version: int,
        page_size: int,
        page_token: str,
        keyword: str = '',
        outcome: str = '',
    ) -> dict[str, Any]:
        self._require_thread(thread_id)
        store = self.runtime.store()
        try:
            record = self._gate_record(thread_id, 'abtest', version, store)
            comparison = _abtest_detail_from_record(store, thread_id, record)
            rows = _abtest_case_detail_items(comparison)
        finally:
            store.close()
        rows = _filter_keyword(rows, keyword, ('case_id', 'query', 'outcome'))
        if outcome:
            rows = [row for row in rows if row.get('outcome') == outcome]
        page, next_token, total = _page(rows, page_size, page_token)
        return {'items': [public_value(row) for row in page], 'next_page_token': next_token, 'total_size': total}

    def _gate_record(self, thread_id: str, step: str, version: int, store: Any) -> Any:
        if step not in C.ROOTS:
            raise HTTPException(422, f'step must be one of: {", ".join(C.STEPS)}')
        if version < 1:
            raise HTTPException(422, 'version must be positive')
        ref = ArtifactRef(ArtifactKey.of(C.ROOTS[step]), version)
        record = store.get(thread_id, ref)
        if record is None or record.run_id != thread_id:
            raise HTTPException(404, 'gate artifact version not found')
        return record

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


def _eval_detail_summary_from_record(
    store: Any,
    thread_id: str,
    record: Any,
    judge_artifact_id: str,
) -> dict[str, Any]:
    value = record.value if isinstance(record.value, Mapping) else {}
    if 'bad_cases' in value or 'rows' in value:
        detail = dict(value)
        detail.setdefault(
            'bad_cases',
            [row for row in _list_of_mappings(value.get('rows')) if row.get('quality_label') != 'good'],
        )
        return detail

    judge_refs = _input_refs(record, judge_artifact_id, partitioned=True)
    if not judge_refs:
        raise HTTPException(409, f'{_ref_text(record.ref)} has no {judge_artifact_id} provenance')
    judges = tuple(_values_for_refs(store, thread_id, judge_refs))
    return build_eval_detail_summary(judges)


def _abtest_detail_from_record(store: Any, thread_id: str, record: Any) -> dict[str, Any]:
    value = record.value if isinstance(record.value, Mapping) else {}
    case_deltas = _comparison_case_deltas(value)
    summary = value.get('summary') if isinstance(value.get('summary'), Mapping) else {}
    if 'case_deltas' in value or 'case_deltas' in summary:
        detail = dict(value)
        detail['case_deltas'] = case_deltas
        return detail

    baseline_record = _input_record(store, thread_id, record, C.ROOTS['eval'])
    candidate_record = _input_record(store, thread_id, record, C.ABTEST_CANDIDATE_EVAL_SUMMARY)
    baseline = _eval_detail_summary_from_record(store, thread_id, baseline_record, C.EVAL_JUDGE_RESULT)
    candidate = _eval_detail_summary_from_record(
        store,
        thread_id,
        candidate_record,
        C.ABTEST_CANDIDATE_JUDGE_RESULT,
    )
    return _enrich_abtest_case_deltas(compare_eval_detail_for_repair(baseline, candidate), baseline, candidate)


def _input_record(store: Any, thread_id: str, record: Any, artifact_id: str) -> Any:
    refs = _input_refs(record, artifact_id, partitioned=False)
    if not refs:
        raise HTTPException(409, f'{_ref_text(record.ref)} has no {artifact_id} provenance')
    result = store.get(thread_id, refs[0])
    if result is None:
        raise HTTPException(409, f'{_ref_text(refs[0])} provenance payload is missing')
    return result


def _input_refs(record: Any, artifact_id: str, *, partitioned: bool) -> list[ArtifactRef]:
    refs = [
        ref for key, ref in record.input_refs.items()
        if key.artifact_id == artifact_id and bool(key.partition) == partitioned
    ]
    return sorted(refs, key=lambda ref: ref.key.partition)


def _values_for_refs(store: Any, thread_id: str, refs: list[ArtifactRef]) -> list[Mapping[str, Any]]:
    values: list[Mapping[str, Any]] = []
    for ref in refs:
        record = store.get(thread_id, ref)
        if record is not None and isinstance(record.value, Mapping):
            values.append(record.value)
    return values


def _list_of_mappings(value: object) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    return [dict(item) for item in value if isinstance(item, Mapping)]


def _bad_case_item(row: Mapping[str, Any]) -> dict[str, Any]:
    score_source = (
        row.get('answer_correctness')
        if row.get('answer_correctness') is not None
        else row.get('overall_score')
    )
    score = _number(score_source)
    payload = dict(row)
    payload.update({
        'query': row.get('query') or row.get('question'),
        'reference': row.get('reference') or row.get('ground_truth'),
        'answer': row.get('answer') or row.get('rag_answer'),
        'score': score,
        'Defect': row.get('Defect') or row.get('defect'),
        'Reason': row.get('Reason') or row.get('reason'),
        'trace_status': row.get('trace_status') or ('linked' if row.get('trace_id') else ''),
        'failure_detail': row.get('failure_detail') or row.get('chat_error_message')
        or row.get('reason') or row.get('failure_type'),
    })
    return payload


def _abtest_case_detail_items(comparison: Mapping[str, Any]) -> list[dict[str, Any]]:
    return [_abtest_case_detail_item(row) for row in _comparison_case_deltas(comparison)]


def _comparison_case_deltas(comparison: Mapping[str, Any]) -> list[dict[str, Any]]:
    case_deltas = _list_of_mappings(comparison.get('case_deltas'))
    if case_deltas:
        return case_deltas
    summary = comparison.get('summary') if isinstance(comparison.get('summary'), Mapping) else {}
    return _list_of_mappings(summary.get('case_deltas'))


def _abtest_case_detail_item(row: Mapping[str, Any]) -> dict[str, Any]:
    before = dict(row.get('before') if isinstance(row.get('before'), Mapping) else {})
    after = dict(row.get('after') if isinstance(row.get('after'), Mapping) else {})
    payload = dict(row)
    payload['before'] = before
    payload['after'] = after
    payload.setdefault('baseline', before)
    payload.setdefault('candidate', after)
    payload.setdefault('query', row.get('question') or '')
    payload.setdefault('a_trace_id', row.get('baseline_trace_id') or before.get('trace_id'))
    payload.setdefault('b_trace_id', row.get('candidate_trace_id') or after.get('trace_id'))
    payload.setdefault('baseline_trace_id', payload.get('a_trace_id'))
    payload.setdefault('candidate_trace_id', payload.get('b_trace_id'))
    return payload


def _enrich_abtest_case_deltas(
    comparison: Mapping[str, Any],
    baseline: Mapping[str, Any],
    candidate: Mapping[str, Any],
) -> dict[str, Any]:
    baseline_rows = _rows_by_case(baseline.get('rows'))
    candidate_rows = _rows_by_case(candidate.get('rows'))
    detail = dict(comparison)
    detail['case_deltas'] = [
        _enrich_abtest_case_delta(row, baseline_rows, candidate_rows)
        for row in _comparison_case_deltas(detail)
    ]
    summary = dict(detail.get('summary') if isinstance(detail.get('summary'), Mapping) else {})
    if summary:
        summary['case_deltas'] = detail['case_deltas']
        detail['summary'] = summary
    return detail


def _enrich_abtest_case_delta(
    row: Mapping[str, Any],
    baseline_rows: Mapping[str, Mapping[str, Any]],
    candidate_rows: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    case_id = _text(row.get('case_id'))
    baseline = baseline_rows.get(case_id, {})
    candidate = candidate_rows.get(case_id, {})
    before = dict(row.get('before') if isinstance(row.get('before'), Mapping) else {})
    after = dict(row.get('after') if isinstance(row.get('after'), Mapping) else {})
    before.setdefault('trace_id', baseline.get('trace_id'))
    after.setdefault('trace_id', candidate.get('trace_id'))
    before.setdefault('quality_label', baseline.get('quality_label'))
    after.setdefault('quality_label', candidate.get('quality_label'))
    return {
        **dict(row),
        'query': baseline.get('query') or baseline.get('question')
        or candidate.get('query') or candidate.get('question') or '',
        'before': before,
        'after': after,
        'baseline': before,
        'candidate': after,
        'baseline_trace_id': baseline.get('trace_id'),
        'candidate_trace_id': candidate.get('trace_id'),
        'a_trace_id': baseline.get('trace_id'),
        'b_trace_id': candidate.get('trace_id'),
        'question_type': baseline.get('question_type') or candidate.get('question_type'),
    }


def _rows_by_case(value: object) -> dict[str, Mapping[str, Any]]:
    return {
        _text(row.get('case_id')): row
        for row in _list_of_mappings(value)
        if _text(row.get('case_id'))
    }


def _filter_keyword(rows: list[dict[str, Any]], keyword: str, fields: tuple[str, ...]) -> list[dict[str, Any]]:
    text = keyword.strip().lower()
    if not text:
        return rows
    return [
        row for row in rows
        if any(text in str(row.get(field) or '').lower() for field in fields)
    ]


def _page(rows: list[dict[str, Any]], page_size: int, page_token: str) -> tuple[list[dict[str, Any]], str, int]:
    offset = int(page_token or 0) if str(page_token or '0').isdigit() else -1
    if offset < 0:
        raise HTTPException(422, 'page_token must be an integer offset')
    page = rows[offset:offset + page_size]
    next_token = str(offset + page_size) if offset + page_size < len(rows) else ''
    return page, next_token, len(rows)


def _number(value: object) -> float:
    try:
        return round(float(value or 0.0), 4)
    except (TypeError, ValueError):
        return 0.0


def _text(value: object) -> str:
    return str(value or '').strip()


def _normalized_step_id(value: str) -> str:
    value = value.strip()
    if not value:
        return ''
    try:
        return str(uuid.UUID(value))
    except ValueError as exc:
        raise HTTPException(422, 'invalid step_id') from exc


def _source_event_rows(
    thread_id: str,
    store: Any,
    effective: Mapping[ArtifactKey, ArtifactRef] | None = None,
) -> list[dict[str, Any]]:
    filter_effective = effective is not None
    effective_map = effective or {}
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
                    'visible': not filter_effective or effective_map.get(ref.key) == ref,
                })
            if any(ref.key.artifact_id == C.ROOTS[step] for ref in refs):
                closed = True
    next_by_id = {span_id: span_ids[index + 1] for index, span_id in enumerate(span_ids[:-1])}
    return [
        dict(
            row,
            next_step_id=next_by_id.get(row['step_id']),
        )
        for row in rows
    ]


def _visible_rows(rows: Iterable[Mapping[str, Any]]) -> list[dict[str, Any]]:
    return [dict(row) for row in rows if row.get('visible', True)]


def _refs_by_step(refs: Iterable[ArtifactRef]) -> dict[str, list[ArtifactRef]]:
    grouped = {step: [] for step in C.STEPS}
    for ref in refs:
        step = STEP_BY_ARTIFACT.get(ref.key.artifact_id)
        if step:
            grouped[step].append(ref)
    return {step: grouped[step] for step in C.STEPS if grouped[step]}


def _display_events(
    thread_id: str,
    rows: list[dict[str, Any]],
    num_case: int,
    state: FlowRunState,
    boundary_step: str,
    boundary_step_id: str,
    *,
    append_gate_boundary: bool,
) -> list[dict[str, Any]]:
    items: list[dict[str, Any]] = []
    case_counts: dict[str, int] = {}
    ordered = sorted(rows, key=lambda item: item['order'])
    for row in ordered:
        item = _artifact_event(row, num_case, case_counts)
        if item:
            items.append(item)
        if _is_root_row(row):
            _append_step_event(
                items,
                row,
                action='finish',
                status='completed',
                progress={'percent': 100},
            )
    if append_gate_boundary:
        _append_gate_boundary(thread_id, items, ordered, state, boundary_step, boundary_step_id)
    return items


def _step_items(
    thread_id: str,
    rows: list[dict[str, Any]],
    state: FlowRunState,
    boundary_step: str = '',
    boundary_status: str = '',
    current_step: str = '',
) -> list[dict[str, Any]]:
    by_id: dict[str, dict[str, Any]] = {}
    for row in sorted(_visible_rows(rows), key=lambda item: item['order']):
        step_id = row['step_id']
        item = by_id.setdefault(
            step_id,
            {
                'thread_id': thread_id,
                'step_id': step_id,
                'stage': row['stage'],
                'title': row['stage'],
                'order_index': len(by_id),
                'event_count': 0,
                'next_step_id': row.get('next_step_id') or '',
                'version': None,
                'status': 'running',
                'continues_previous': False,
            },
        )
        item['event_count'] += 1
        item['next_step_id'] = row.get('next_step_id') or ''
        ref = row['ref']
        if ref.key.artifact_id == C.ROOTS[row['stage']]:
            item['version'] = ref.version
            item['status'] = 'completed'
    result = []
    for item in by_id.values():
        completed = item['status'] == 'completed'
        item['active'] = not completed and not item['next_step_id']
        if not item['next_step_id']:
            item['next_step_id'] = ''
        result.append(item)
    _refresh_step_links(result)
    for index in range(1, len(result)):
        result[index]['continues_previous'] = result[index - 1].get('stage') == result[index].get('stage')
    _apply_gate_step_status(thread_id, rows, result, state, boundary_step, boundary_status)
    _apply_current_step_status(thread_id, rows, result, current_step, boundary_status)
    return result


def _refresh_step_links(items: list[dict[str, Any]]) -> None:
    for index, item in enumerate(items):
        item['next_step_id'] = items[index + 1]['step_id'] if index + 1 < len(items) else ''
        item['active'] = item.get('status') != 'completed' and not item['next_step_id']


def _apply_current_step_status(
    thread_id: str,
    rows: list[dict[str, Any]],
    items: list[dict[str, Any]],
    current_step: str,
    boundary_status: str,
) -> None:
    if boundary_status or current_step not in C.STEPS:
        return
    existing = next((item for item in reversed(items) if item.get('stage') == current_step), None)
    if existing is not None and existing.get('status') != 'completed':
        for item in items:
            item['active'] = item is existing
        existing['active'] = True
        return
    if existing is not None and existing.get('status') == 'completed':
        return
    item = _synthetic_step_item(thread_id, rows, items, current_step, 'running')
    for current in items:
        current['active'] = False
    if items:
        items[-1]['next_step_id'] = item['step_id']
    item['active'] = True
    items.append(item)


def _append_gate_boundary(
    thread_id: str,
    items: list[dict[str, Any]],
    rows: list[dict[str, Any]],
    state: FlowRunState,
    boundary_step: str,
    boundary_step_id: str,
) -> None:
    status, action = _gate_boundary(state.status)
    if not action:
        return
    row = _gate_boundary_row(thread_id, rows, state, boundary_step, boundary_step_id)
    if row is None:
        return
    summary = {'flow_status': state.status}
    if state.last_error:
        summary['message'] = state.last_error
    if state.pending_checkpoint is not None:
        summary['checkpoint'] = {
            'step': state.pending_checkpoint.step,
            'ref': _ref_text(state.pending_checkpoint.ref),
        }
    _append_step_event(
        items,
        row,
        action=action,
        status=status,
        summary=summary,
    )


def _append_step_event(
    items: list[dict[str, Any]],
    row: Mapping[str, Any],
    *,
    action: str,
    status: str,
    progress: Mapping[str, Any] | None = None,
    summary: Mapping[str, Any] | None = None,
) -> None:
    event_id = _step_event_id(str(row['step_id']), action)
    if any(item['event_id'] == event_id for item in items):
        return
    item = _base_event(row, f'step.{action}', action)
    item['event_id'] = event_id
    item['status'] = status
    if progress:
        item['progress'] = dict(progress)
    if summary:
        item['summary'] = dict(summary)
    items.append(_clean_empty(item))


def _apply_gate_step_status(
    thread_id: str,
    rows: list[dict[str, Any]],
    items: list[dict[str, Any]],
    state: FlowRunState,
    boundary_step: str,
    boundary_status: str,
) -> None:
    if not boundary_status:
        return
    item = _gate_step_item(items, state, boundary_step)
    if item is None:
        if not boundary_step:
            return
        item = _synthetic_step_item(thread_id, rows, items, boundary_step, boundary_status)
        if items:
            items[-1]['next_step_id'] = item['step_id']
        items.append(item)
    for current in items:
        current['active'] = current is item
    item['status'] = boundary_status
    item['active'] = True


def _gate_step_item(
    items: list[dict[str, Any]],
    state: FlowRunState,
    boundary_step: str,
) -> dict[str, Any] | None:
    if state.pending_checkpoint is not None:
        step = state.pending_checkpoint.step
        version = state.pending_checkpoint.ref.version
        match = next(
            (
                item for item in reversed(items)
                if item.get('stage') == step and item.get('version') == version
            ),
            None,
        )
        if match is not None:
            return match
    if boundary_step:
        match = next((item for item in reversed(items) if item.get('stage') == boundary_step), None)
        if match is not None and (match.get('status') != 'completed' or not match.get('next_step_id')):
            return match
    running = next((item for item in reversed(items) if item.get('status') == 'running'), None)
    if running is not None:
        return running
    if boundary_step:
        return None
    return items[-1] if items else None


def _gate_boundary_row(
    thread_id: str,
    rows: list[dict[str, Any]],
    state: FlowRunState,
    boundary_step: str,
    boundary_step_id: str,
) -> dict[str, Any] | None:
    if state.pending_checkpoint is not None:
        checkpoint = state.pending_checkpoint
        match = next(
            (
                row for row in reversed(rows)
                if row['stage'] == checkpoint.step and row['ref'] == checkpoint.ref
            ),
            None,
        )
        if match is not None:
            return match
    if boundary_step:
        match = next((row for row in reversed(rows) if row['stage'] == boundary_step), None)
        if match is not None:
            return match
        return _synthetic_boundary_row(thread_id, rows, boundary_step, boundary_step_id)
    return next(
        (row for row in reversed(rows) if not _is_root_row(row)),
        None,
    ) or (rows[-1] if rows else None)


def _events_after_id(items: list[dict[str, Any]], after_event_id: str) -> list[dict[str, Any]] | None:
    for index, item in enumerate(items):
        if str(item['event_id']) == after_event_id:
            return items[index + 1:]
    # A gate boundary event can disappear after resume/retry; resume from the end of that step.
    for index in range(len(items) - 1, -1, -1):
        item_step_id = str(items[index].get('step_id') or '')
        if item_step_id and any(
            _step_event_id(item_step_id, action) == after_event_id
            for action in GATE_BOUNDARY_ACTIONS
        ):
            return items[index + 1:]
    return None


def _trace_events_after_id(items: list[dict[str, Any]], after_event_id: str) -> list[dict[str, Any]]:
    ids = [str(item['event_id']) for item in items]
    return items[ids.index(after_event_id) + 1:] if after_event_id in ids else items


def _is_root_row(row: Mapping[str, Any]) -> bool:
    ref = row['ref']
    return ref.key.artifact_id == C.ROOTS[row['stage']]


def _step_event_id(step_id: str, action: str) -> str:
    return str(uuid.uuid5(STEP_ID_NAMESPACE, f'{step_id}:step:{action}'))


def _gate_boundary(status: str) -> tuple[str, str]:
    return GATE_BOUNDARY_BY_STATUS.get(status, ('', ''))


def _gate_boundary_step(state: FlowRunState, current_step: str, boundary_status: str) -> str:
    if not boundary_status:
        return ''
    if state.pending_checkpoint is not None:
        return state.pending_checkpoint.step
    return current_step if current_step in C.STEPS else ''


def _synthetic_step_item(
    thread_id: str,
    rows: list[dict[str, Any]],
    items: list[dict[str, Any]],
    step: str,
    status: str,
) -> dict[str, Any]:
    return {
        'thread_id': thread_id,
        'step_id': _projected_step_id(thread_id, rows, step),
        'stage': step,
        'title': step,
        'order_index': len(items),
        'event_count': 0,
        'next_step_id': '',
        'version': None,
        'status': status,
        'continues_previous': bool(items and items[-1].get('stage') == step),
        'active': True,
    }


def _synthetic_boundary_row(
    thread_id: str,
    rows: list[dict[str, Any]],
    step: str,
    step_id: str = '',
) -> dict[str, Any]:
    step_id = step_id or _projected_step_id(thread_id, rows, step)
    return {
        'order': (rows[-1]['order'] + 1) if rows else 0,
        'event_id': str(uuid.uuid5(STEP_ID_NAMESPACE, f'{step_id}:synthetic-boundary')),
        'step_id': step_id,
        'next_step_id': None,
        'stage': step,
    }


def _projected_step_id(thread_id: str, rows: list[dict[str, Any]], step: str) -> str:
    spans = {row['step_id'] for row in rows if row['stage'] == step}
    return str(uuid.uuid5(STEP_ID_NAMESPACE, f'{thread_id}:{step}:{len(spans) + 1}'))


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


def _repair_trace_rows_for_step(
    source_rows: list[dict[str, Any]],
    trace_rows: list[dict[str, Any]],
    trace_cursor: tuple[int, int] | None,
) -> list[dict[str, Any]]:
    if trace_cursor is not None:
        start, end = trace_cursor
        scoped = [row for row in trace_rows if start <= int(row.get('seq') or 0) <= end]
        verified = next(
            (
                row for row in trace_rows
                if int(row.get('seq') or 0) > end and row.get('type') == 'repair.patch_verified'
            ),
            None,
        )
        return scoped + ([verified] if verified else [])
    return _latest_repair_loop_trace_rows(trace_rows)


def _latest_repair_loop_trace_rows(trace_rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    trace_id = next(
        (
            str(row.get('trace_id') or '')
            for row in reversed(trace_rows)
            if row.get('materialization_key') == C.REPAIR_LOOP_RESULT and row.get('trace_id')
        ),
        '',
    )
    if not trace_id:
        return []
    return [row for row in trace_rows if str(row.get('trace_id') or '') == trace_id]


def _repair_trace_cursor_for_step(
    thread_id: str,
    step_id: str,
    rows: list[dict[str, Any]],
    store: Any,
) -> tuple[int, int] | None:
    loop_ref = next(
        (
            row['ref']
            for row in rows
            if row['step_id'] == step_id
            and row['stage'] == 'repair'
            and row['ref'].key.artifact_id == C.REPAIR_LOOP_RESULT
        ),
        None,
    )
    if loop_ref is None:
        return None
    record = store.get(thread_id, loop_ref)
    value = record.value if record is not None and isinstance(record.value, Mapping) else {}
    cursor = value.get('trace_cursor') if isinstance(value.get('trace_cursor'), Mapping) else {}
    start = _positive_int(cursor.get('seq_start'))
    end = _positive_int(cursor.get('seq_end'))
    return (start, end) if start and end and start <= end else None


def _ordered_stage_step_ids(rows: list[dict[str, Any]], stage: str) -> list[str]:
    result = []
    for row in sorted(rows, key=lambda item: item['order']):
        if row['stage'] == stage and row['step_id'] not in result:
            result.append(row['step_id'])
    return result


def _trace_item(thread_id: str, step_id: str, trace: Mapping[str, Any]) -> dict[str, Any] | None:
    event_type = str(trace.get('type') or '')
    if not event_type:
        return None
    payload = trace.get('payload') if isinstance(trace.get('payload'), Mapping) else {}
    status = str(trace.get('status') or 'running')
    item = {
        'event_id': str(uuid.uuid5(STEP_ID_NAMESPACE, f"{thread_id}:event-trace:{trace.get('seq')}")),
        'step_id': step_id,
        'stage': 'repair',
        'event_type': event_type,
        'action': _action(status),
        'status': status,
        'source': str(trace.get('source') or ''),
        'raw': public_value(dict(trace)),
    }
    lifecycle = _trace_lifecycle(thread_id, step_id, trace, event_type, status, payload)
    if lifecycle:
        item['lifecycle'] = lifecycle
    case_id = str(payload.get('case_id') or '')
    if case_id:
        item['case'] = {'id': case_id}
    summary = {
        key: value
        for key, value in {
            'seq': trace.get('seq'),
            'created_at': trace.get('created_at'),
            'attempt': trace.get('attempt'),
            'message': trace.get('message'),
            'execution_type': payload.get('execution_type'),
            'tool_kind': payload.get('tool'),
            'paths': payload.get('paths'),
            'command': payload.get('command'),
            'exit_code': payload.get('returncode'),
            'decision': _scalar(payload.get('decision') or payload.get('decision_status')),
        }.items()
        if value not in ('', None, [], {})
    }
    if summary:
        item['summary'] = public_value(summary)
    return _clean_empty(item)


def _trace_lifecycle(
    thread_id: str,
    step_id: str,
    trace: Mapping[str, Any],
    event_type: str,
    status: str,
    payload: Mapping[str, Any],
) -> dict[str, Any]:
    attempt = _positive_int(trace.get('attempt'))
    phase = _trace_lifecycle_phase(event_type)
    name = _trace_lifecycle_name(event_type, payload)
    if not name or not phase:
        return {}
    scope = _trace_lifecycle_scope(event_type, payload)
    parts = [thread_id, step_id, name]
    if attempt:
        parts.append(f'attempt-{attempt}')
    if scope:
        parts.append(scope)
    result = {
        'id': ':'.join(parts),
        'name': name,
        'phase': phase,
        'status': status,
        'action': _action(status),
    }
    if attempt:
        result['attempt'] = attempt
    if phase in {'finish', 'terminal'}:
        result['terminal'] = True
    return result


def _trace_lifecycle_name(event_type: str, payload: Mapping[str, Any]) -> str:
    if event_type in {'repair.attempt_started', 'repair.attempt_completed'}:
        return 'repair.attempt'
    if event_type in {'opencode.process_start', 'opencode.process_exit'}:
        return 'opencode.process'
    if event_type == 'opencode.error' and payload.get('execution_type') in {
        'configuration_error',
        'process_failed',
        'process_start_failed',
        'prompt_write_failed',
        'timeout',
    }:
        return 'opencode.process'
    if event_type in {'verify.pre_validation_started', 'verify.pre_validation_completed'}:
        return 'verify.pre_validation'
    if event_type in {'verify.command_started', 'verify.command_completed'}:
        return 'verify.command'
    if event_type in {'candidate.service_started', 'candidate.service_ready', 'candidate.service_failed'}:
        return 'candidate.service'
    if event_type in {'candidate.case_started', 'candidate.case_completed'}:
        return 'candidate.case'
    if event_type in {'analysis.candidate_started', 'analysis.candidate_completed'}:
        return 'analysis.candidate'
    return ''


def _trace_lifecycle_phase(event_type: str) -> str:
    if event_type.endswith('_started') or event_type == 'opencode.process_start':
        return 'start'
    if event_type.endswith('_completed') or event_type in {
        'opencode.process_exit',
        'candidate.service_ready',
        'candidate.service_failed',
        'opencode.error',
    }:
        return 'finish'
    return ''


def _trace_lifecycle_scope(event_type: str, payload: Mapping[str, Any]) -> str:
    if event_type in {'candidate.case_started', 'candidate.case_completed'}:
        return _text(payload.get('case_id'))
    if event_type in {'verify.command_started', 'verify.command_completed'}:
        return _text(payload.get('command')) or 'command'
    return ''


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


def _positive_int(value: object) -> int:
    try:
        number = int(value)
    except (TypeError, ValueError):
        return 0
    return number if number > 0 else 0


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
