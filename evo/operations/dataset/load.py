from __future__ import annotations

import os
from typing import Any

from ...artifacts import ArtifactDraft
from ...runtime import OperationOutput
from .utils import strings

FILTER_KEYS = {'doc_ids', 'file_name', 'filename', 'file_type'}


class LoadCorpusOperation:
    def execute(self, ctx) -> OperationOutput:
        sources = ctx.params.get('sources', [])
        if not isinstance(sources, list):
            raise ValueError('sources must be a list')
        state = _state()
        _progress(ctx, state, 'load_corpus', 'running', 'starting corpus load', done=0, total=len(sources))
        for index, source in enumerate(sources, 1):
            ctx.check_interrupt()
            if not isinstance(source, dict):
                _skip(state, f'source_{index}', 'invalid_source')
                continue
            source_id = str(source.get('source_id') or source.get('dataset_id') or f'source_{index}')
            _load_source(ctx, state, source_id, source)
            _progress(ctx, state, 'load_corpus', 'running', f'loaded {source_id}', source_id, index, len(sources))
        report = {'sources': state['sources'], 'filters': {'sources': state['filters']} if state['filters'] else {},
                  'document_page_refs': [_expected_ref(ctx, draft) for draft in state['pages']],
                  'chunk_page_refs': [], 'loaded_doc_refs': state['loaded_refs'], 'stats': state['stats'],
                  'skipped': state['skipped'], 'errors': []}
        _progress(ctx, state, 'load_corpus', 'success', f"loaded {state['stats']['loaded_doc_count']} docs",
                  done=state['stats']['loaded_doc_count'], total=state['stats']['scanned_doc_count'])
        return OperationOutput([*state['pages'], ArtifactDraft(
            'corpus_load_report', 'CorpusLoadReport', report, ctx.operation_run_id
        )])


def _state() -> dict[str, Any]:
    return {'pages': [], 'sources': [], 'filters': [], 'loaded_refs': [], 'skipped': [],
            'stats': {'source_count': 0, 'matched_doc_count': 0, 'scanned_doc_count': 0,
                      'loaded_doc_count': 0, 'skipped_doc_count': 0, 'document_page_count': 0,
                      'hit_doc_limit': False}}


def _page(ctx, state, source_id: str, docs: list[dict[str, Any]]) -> None:
    if not docs:
        return
    index = state['stats']['document_page_count'] + 1
    payload = {'source_id': source_id, 'page_index': index, 'documents': docs}
    state['pages'].append(ArtifactDraft(f'corpus_docs_page_{index:04d}', 'CorpusDocumentPage', payload,
                                        ctx.operation_run_id))
    state['stats']['document_page_count'] = index


def _skip(state, source_id: str, reason: str, **extra: Any) -> None:
    state['skipped'].append({'source_id': source_id, 'reason': reason, **extra})
    state['stats']['skipped_doc_count'] += 1


def _load_source(ctx, state, source_id: str, source: dict[str, Any]) -> None:
    state['stats']['source_count'] += 1
    driver = str(source.get('driver') or 'postgres').strip().lower()
    if source.get('type') == 'kb' and driver in {'postgres', 'postgresql'}:
        _load_db(ctx, state, source_id, source)
        return
    reason = 'unsupported_source_driver' if source.get('type') == 'kb' else 'unsupported_source_type'
    _skip(state, source_id, reason, type=source.get('type'), driver=driver)
    state['sources'].append(_source_summary(source, source_id=source_id, reason=reason))


def _load_db(ctx, state, source_id: str, source: dict[str, Any]) -> None:
    try:
        import psycopg
        from psycopg.rows import dict_row
    except ImportError as exc:
        raise RuntimeError('psycopg is required for db corpus loading') from exc
    dataset_id, db = str(source.get('dataset_id') or source_id), _db_config()
    max_docs = _limit(source, 'max_docs', 1000, 1, 100000)
    page_size = _limit(source, 'doc_page_size', int(source.get('page_size') or 1000), 1, 5000)
    filters, unsupported = _filters(source)
    if filters or unsupported:
        state['filters'].append({'source_id': source_id, 'applied': filters, 'unsupported': unsupported})
    buffer, loaded, scanned = [], 0, 0
    with psycopg.connect(db['dsn'], row_factory=dict_row) as conn, conn.cursor() as cursor:
        table, where, params = _query(db['schema'], dataset_id, filters)
        cursor.execute(f'select count(*) as count {table} where {where}', params)
        matched = int((cursor.fetchone() or {}).get('count') or 0)
        limit = min(matched, max_docs)
        state['stats']['matched_doc_count'] += matched
        state['stats']['hit_doc_limit'] = state['stats']['hit_doc_limit'] or matched > max_docs
        for rows in _pages(ctx, cursor, table, where, params, page_size, limit):
            for row in rows:
                scanned += 1
                doc = _document(dataset_id, row)
                if not doc:
                    _skip(state, source_id, 'missing_doc_id')
                    continue
                loaded += 1
                _scan(ctx, state, source_id, doc, buffer, page_size, limit)
    _page(ctx, state, source_id, buffer)
    state['sources'].append(_source_summary(
        source, source_id=source_id, dataset_id=dataset_id, resolved_api='db.lazyllm_kb_documents',
        resolved_base_url=db['safe_dsn'], readonly_schema=db['schema'], fetched_document_count=loaded,
        matched_document_count=matched, scanned_document_count=scanned, max_docs=max_docs,
        doc_page_size=page_size, applied_filters=filters, unsupported_filters=unsupported,
    ))


def _pages(ctx, cursor, table: str, where: str, params: list[Any], page_size: int, limit: int):
    offset = 0
    while offset < limit:
        ctx.check_interrupt()
        row_limit = min(page_size, limit - offset)
        cursor.execute(
            f'select kb.id as kb_document_id, d.* {table} where {where} order by kb.id limit %s offset %s',
            [*params, row_limit, offset],
        )
        rows = cursor.fetchall()
        if not rows:
            break
        yield rows
        offset += len(rows)


def _scan(ctx, state, source_id: str, doc: dict[str, Any], buffer: list[dict[str, Any]], page_size: int, total: int):
    state['stats']['scanned_doc_count'] += 1
    state['loaded_refs'].append(doc['doc_ref'])
    state['stats']['loaded_doc_count'] += 1
    buffer.append(doc)
    if len(buffer) >= page_size:
        _page(ctx, state, source_id, buffer)
        buffer.clear()
    _progress(ctx, state, 'scan_documents', 'running', f"scanned {state['stats']['scanned_doc_count']} docs",
              doc['doc_ref'], state['stats']['scanned_doc_count'], total)


def _document(dataset_id: str, row: dict[str, Any]) -> dict[str, Any]:
    doc_id = str(row.get('doc_id') or '')
    if not doc_id:
        return {}
    filename = str(row.get('filename') or row.get('display_name') or doc_id)
    metadata = _json({'document_id': doc_id, 'filename': filename, 'upload_status': row.get('upload_status', ''),
                      'file_type': row.get('file_type', ''), 'size_bytes': int(row.get('size_bytes') or 0),
                      'core_document': dict(row)})
    return {'doc_ref': f'{dataset_id}:{doc_id}', 'doc_id': doc_id, 'source_ref': str(row.get('path') or ''),
            'filename': filename, 'file_type': str(metadata['file_type']), 'text_preview': filename[:240],
            'char_count': len(filename), 'metadata': metadata}


def _filters(source: dict[str, Any]) -> tuple[dict[str, Any], list[str]]:
    raw = source.get('filters') if isinstance(source.get('filters'), dict) else {}
    unsupported = {key for key, value in raw.items() if value and key not in FILTER_KEYS}
    out = {}
    for name, key, allowed in (
        ('doc_ids', 'doc_ids', ('include', 'exclude')),
        ('file_name', ('file_name', 'filename'), ('include', 'exclude', 'prefixes', 'suffixes')),
        ('file_type', 'file_type', ('include',)),
    ):
        if group := _group(_first(raw, key), allowed):
            out[name] = group
    return out, sorted(unsupported)


def _query(schema: str, dataset_id: str, filters: dict[str, Any]) -> tuple[str, str, list[Any]]:
    table = f'from {_q(schema)}.lazyllm_kb_documents kb join {_q(schema)}.lazyllm_documents d on d.doc_id = kb.doc_id'
    where, params = ['kb.kb_id = %s'], [dataset_id]
    _any(where, params, 'd.doc_id', filters.get('doc_ids', {}).get('include'))
    _any(where, params, 'd.doc_id', filters.get('doc_ids', {}).get('exclude'), exclude=True)
    name = filters.get('file_name', {})
    _any(where, params, 'd.filename', name.get('include'))
    _any(where, params, 'd.filename', name.get('exclude'), exclude=True)
    _like(where, params, 'd.filename', name.get('prefixes'), suffix=False)
    _like(where, params, 'd.filename', name.get('suffixes'), suffix=True)
    _equals(where, params, "lower(coalesce(d.file_type, ''))", [
        item.lower().lstrip('.') for item in strings(filters.get('file_type', {}).get('include'))
    ])
    return table, ' and '.join(where), params


def _group(value: Any, allowed: tuple[str, ...]) -> dict[str, list[str]]:
    if isinstance(value, dict):
        return {key: items for key in allowed if (items := strings(value.get(key)))}
    items = strings(value)
    return {'include': items} if items and 'include' in allowed else {}


def _any(where: list[str], params: list[Any], expression: str, values: Any, exclude: bool = False) -> None:
    if items := strings(values):
        where.append(f'{expression} <> all(%s)' if exclude else f'{expression} = any(%s)')
        params.append(items)


def _equals(where: list[str], params: list[Any], expression: str, items: list[str]) -> None:
    if items:
        where.append(f'{expression} = any(%s)')
        params.append(items)


def _like(where: list[str], params: list[Any], expression: str, values: Any, suffix: bool) -> None:
    patterns = [f'%{item}' if suffix else f'{item}%' for item in strings(values)]
    if patterns:
        where.append('(' + ' or '.join(f'{expression} ilike %s' for _ in patterns) + ')')
        params.extend(patterns)


def _db_config() -> dict[str, str]:
    driver = os.getenv('LAZYMIND_READONLY_DB_DRIVER', 'postgres').strip().lower()
    if driver not in {'postgres', 'postgresql'}:
        raise RuntimeError(f'unsupported readonly db driver: {driver}')
    dsn = os.getenv('LAZYMIND_READONLY_DB_DSN', '').strip()
    default = 'host=db user=app password=app dbname=app port=5432 sslmode=disable connect_timeout=5'
    return {'driver': driver, 'dsn': dsn or default, 'schema': os.getenv('LAZYMIND_READONLY_SCHEMA', 'public').strip(),
            'safe_dsn': _redact_dsn(dsn) if dsn else default.replace('password=app', 'password=***')}


def _limit(source: dict[str, Any], key: str, default: int, minimum: int, maximum: int) -> int:
    value = _int(source.get(key))
    return max(minimum, min(maximum, value if value is not None else default))


def _int(value: Any) -> int | None:
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def _q(identifier: str) -> str:
    return f'"{identifier.replace(chr(34), chr(34) * 2)}"'


def _redact_dsn(dsn: str) -> str:
    return ' '.join('password=***' if part.startswith('password=') else part for part in dsn.split())


def _json(value: Any) -> Any:
    if isinstance(value, dict):
        return {str(key): _json(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_json(item) for item in value]
    return value if value is None or isinstance(value, (str, int, float, bool)) else str(value)


def _progress(ctx, state, phase: str, status: str, message: str, current_item: str = '', done=0, total=0) -> None:
    stats = state['stats']
    detail = {'loaded_docs': stats['loaded_doc_count'], 'skipped': stats['skipped_doc_count'],
              'document_page_count': stats['document_page_count']}
    ctx.report_progress(phase=phase, status=status, message=message, current_item=current_item, done=done,
                        total=total, detail=detail)


def _expected_ref(ctx, draft: ArtifactDraft) -> str:
    try:
        version = ctx.artifact_graph.latest_ref(draft.artifact_id).version + 1
    except KeyError:
        version = 1
    return f'{draft.artifact_id}@v{version}'


def _source_summary(source: dict[str, Any], **extra: Any) -> dict[str, Any]:
    out = {key: _json(value) for key, value in source.items() if key != 'filters' and not key.lower().endswith('dsn')}
    return out | {key: _json(value) for key, value in extra.items()}


def _first(data: dict[str, Any], key: str | tuple[str, ...]) -> Any:
    return data.get(key) if isinstance(key, str) else next((data[item] for item in key if item in data), None)
