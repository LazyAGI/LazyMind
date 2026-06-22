from __future__ import annotations

import hashlib
import os
import re
from collections import Counter
from typing import Any, Mapping

from evo.operations.common import as_list, chunks, first_text, int_between, json_safe, text, unique_texts


DEFAULT_KB_GROUPS = ('block', 'line', 'doc-summary')
_DOCUMENTS: dict[tuple[str, str], Any] = {}


def load_source_documents(source_config: Mapping[str, Any]) -> dict[str, Any]:
    dataset_id = text(source_config.get('dataset_id') or source_config.get('kb_id') or 'algo')
    docs = _input_documents(source_config, dataset_id)
    if docs:
        return {'documents': docs, 'load_mode': 'inline', 'errors': []}
    return {'documents': _kb_documents(source_config, dataset_id), 'load_mode': 'lazyllm_document', 'errors': []}


def build_corpus_load_report(source_config: Mapping[str, Any], documents: list[Mapping[str, Any]], *,
                             load_mode: str, errors: list[Mapping[str, Any]] | None = None) -> dict[str, Any]:
    dataset_id = text(source_config.get('dataset_id') or source_config.get('kb_id') or 'algo')
    docs = [dict(doc) for doc in documents if isinstance(doc, Mapping)]
    if not docs:
        raise ValueError(f'dataset {dataset_id} has no usable source units')
    unique_docs = sorted({text(doc.get('doc_id')) for doc in docs if text(doc.get('doc_id'))})
    page_size = int_between(source_config.get('document_page_size') or source_config.get('page_size'), 200, 1, 5000)
    pages = [{'source_id': dataset_id, 'page_index': index, 'documents': page}
             for index, page in enumerate(chunks(docs, page_size), 1)]
    return {
        'dataset_id': dataset_id,
        'sources': [{'source_id': dataset_id, 'type': load_mode, 'document_count': len(unique_docs)}],
        'document_pages': pages,
        'stats': {
            'source_count': 1,
            'loaded_doc_count': len(unique_docs),
            'source_unit_count': len(docs),
            'document_page_count': len(pages),
        },
        'skipped': [],
        'errors': list(errors or []),
    }


def build_corpus_snapshot(report: Mapping[str, Any], source_config: Mapping[str, Any]) -> dict[str, Any]:
    raw_docs = [
        doc
        for page in report.get('document_pages', [])
        for doc in page.get('documents', [])
        if isinstance(doc, Mapping)
    ]
    units = [_unit(doc, index) for index, doc in enumerate(raw_docs, 1)]
    if not units:
        raise ValueError('corpus load report has no loaded documents')
    by_type = Counter(unit['unit_type'] for unit in units)
    return {
        'dataset_id': text(report.get('dataset_id') or source_config.get('dataset_id') or source_config.get('kb_id')
                           or 'algo'),
        'source_units': units,
        'source_unit_count': len(units),
        'unit_type_counts': dict(by_type),
        'source_report': {'stats': dict(report.get('stats') or {})},
    }


def _unit(doc: Mapping[str, Any], index: int) -> dict[str, str]:
    content = text(doc.get('content'))
    doc_id = text(doc.get('doc_id') or f'doc_{index}')
    filename = text(doc.get('filename') or f'{doc_id}.txt')
    metadata = doc.get('metadata') if isinstance(doc.get('metadata'), Mapping) else {}
    return {
        'source_unit_ref': text(doc.get('source_unit_ref')) or f'source_unit:{doc_id}:{index}',
        'doc_ref': text(doc.get('doc_ref')) or f'doc:{doc_id}',
        'doc_id': doc_id,
        'filename': filename,
        'chunk_id': text(doc.get('chunk_id') or doc.get('segment_id') or f'{doc_id}:chunk:{index}'),
        'unit_type': text(doc.get('unit_type')) or _unit_type(content, metadata),
        'content': content,
    }


def _unit_type(content: str, metadata: Mapping[str, Any] | None = None) -> str:
    node_type = text((metadata or {}).get('type') or (metadata or {}).get('node_type')).lower()
    if node_type in {'table', 'list', 'ordered_list', 'unordered_list', 'formula', 'equation'}:
        return {'ordered_list': 'list', 'unordered_list': 'list', 'equation': 'formula'}.get(node_type, node_type)
    if '|' in content and '\n' in content:
        return 'table'
    if re.search(r'\b(sum|average|formula|equation|=)\b', content, re.I):
        return 'formula'
    return 'paragraph'


def _input_documents(config: Mapping[str, Any], dataset_id: str) -> list[dict[str, str]]:
    docs = []
    for index, item in enumerate(as_list(config.get('documents') or config.get('docs')), 1):
        if isinstance(item, Mapping):
            content = text(item.get('content') or item.get('text'))
            if content:
                docs.append({
                    'doc_id': text(item.get('doc_id') or item.get('id') or f'{dataset_id}_doc_{index}'),
                    'filename': text(item.get('filename') or item.get('file_name') or f'{dataset_id}_{index}.txt'),
                    'chunk_id': text(item.get('chunk_id') or item.get('segment_id')),
                    'unit_type': text(item.get('unit_type')),
                    'content': content,
                    'metadata': item.get('metadata') if isinstance(item.get('metadata'), Mapping) else {},
                })
    for index, source in enumerate(as_list(config.get('sources')), len(docs) + 1):
        if isinstance(source, Mapping):
            content = text(source.get('content') or source.get('text') or source.get('summary'))
            if content:
                docs.append({
                    'doc_id': text(source.get('doc_id') or source.get('source_id')
                                   or f'{dataset_id}_source_{index}'),
                    'filename': text(source.get('filename') or source.get('file_name')
                                     or f'{dataset_id}_source_{index}.txt'),
                    'chunk_id': text(source.get('chunk_id') or source.get('segment_id')),
                    'unit_type': text(source.get('unit_type')),
                    'content': content,
                    'metadata': source.get('metadata') if isinstance(source.get('metadata'), Mapping) else {},
                })
    return docs


def _kb_documents(config: Mapping[str, Any], dataset_id: str) -> list[dict[str, Any]]:
    rows = _kb_document_rows(config, dataset_id)
    doc = _document_client()
    groups = tuple(unique_texts(config.get('segment_groups') or config.get('groups'))) or DEFAULT_KB_GROUPS
    max_units = int_between(config.get('max_source_units') or config.get('max_units'), 200, 1, 10000)
    page_size = int_between(config.get('kb_page_size') or config.get('node_page_size'), 100, 1, 1000)
    min_chars = int_between(config.get('min_segment_chars'), 80, 1, 100000)
    units, seen = [], set()
    for row in rows:
        for group in groups:
            offset = 0
            while len(units) < max_units:
                nodes, total = doc.get_nodes(
                    doc_ids=[row['doc_id']],
                    kb_id=dataset_id,
                    group=group,
                    limit=min(page_size, max_units - len(units)),
                    offset=offset,
                    return_total=True,
                    sort_by_number=True,
                )
                if not nodes:
                    break
                for node in nodes:
                    unit = _node_unit(dataset_id, group, node, row)
                    content = text(unit.get('content'))
                    if len(content) < min_chars:
                        continue
                    key = text(unit.get('chunk_id')) or hashlib.sha256(content.encode('utf-8')).hexdigest()
                    if key in seen:
                        continue
                    seen.add(key)
                    units.append(unit)
                    if len(units) >= max_units:
                        break
                offset += len(nodes)
                if offset >= int(total or offset):
                    break
            if len(units) >= max_units:
                break
        if len(units) >= max_units:
            break
    return units


def _kb_document_rows(config: Mapping[str, Any], dataset_id: str) -> list[dict[str, str]]:
    try:
        import psycopg
        from psycopg.rows import dict_row
    except ImportError as exc:
        raise RuntimeError('psycopg is required for LazyRAG dataset loading') from exc

    schema = text(config.get('db_schema') or os.getenv('LAZYMIND_READONLY_SCHEMA') or 'public')
    max_docs = int_between(config.get('max_docs'), 1000, 1, 100000)
    quoted_schema = schema.replace(chr(34), chr(34) + chr(34))
    table = (
        f'from "{quoted_schema}".lazyllm_kb_documents kb '
        f'join "{quoted_schema}".lazyllm_documents d on d.doc_id = kb.doc_id'
    )
    sql = f'select d.doc_id, d.filename, d.file_type {table} where kb.kb_id = %s order by kb.id limit %s'
    with psycopg.connect(_db_dsn(), row_factory=dict_row) as conn, conn.cursor() as cursor:
        cursor.execute(sql, (dataset_id, max_docs))
        rows = [
            {
                'doc_id': text(row.get('doc_id')),
                'filename': text(row.get('filename') or row.get('doc_id')),
                'file_type': text(row.get('file_type')),
            }
            for row in cursor.fetchall()
            if text(row.get('doc_id'))
        ]
    if not rows:
        raise ValueError(f'dataset {dataset_id} has no registered documents')
    return rows


def _db_dsn() -> str:
    raw = text(os.getenv('LAZYMIND_READONLY_DB_DSN') or os.getenv('LAZYMIND_DATABASE_URL'))
    if raw.startswith('postgresql+psycopg://'):
        return 'postgresql://' + raw.removeprefix('postgresql+psycopg://')
    if raw.startswith('postgres+psycopg://'):
        return 'postgres://' + raw.removeprefix('postgres+psycopg://')
    return raw or 'host=db user=app password=app dbname=app port=5432 sslmode=disable connect_timeout=5'


def _document_client() -> Any:
    from lazyllm import Document
    from lazymind.config import config

    url = _config_value(config, 'agentic_kb_url').rstrip('/')
    name = _config_value(config, 'agentic_kb_name')
    if not url or not name:
        raise RuntimeError('LazyRAG document service config is missing')
    key = (url, name)
    if key not in _DOCUMENTS:
        _DOCUMENTS[key] = Document(url=f'{url}/_call', name=name)
    return _DOCUMENTS[key]


def _node_unit(dataset_id: str, group: str, node: Any, doc_row: Mapping[str, Any]) -> dict[str, Any]:
    metadata = getattr(node, 'metadata', {}) or {}
    global_metadata = getattr(node, 'global_metadata', {}) or {}
    if not isinstance(metadata, Mapping):
        metadata = {}
    if not isinstance(global_metadata, Mapping):
        global_metadata = {}
    doc_id = text(doc_row.get('doc_id'))
    filename = text(doc_row.get('filename')) or first_text(
        global_metadata, 'file_name', 'display_name', 'filename') or f'{doc_id}.txt'
    content = text(getattr(node, 'text', ''))
    chunk_id = text(getattr(node, 'uid', '')) or text(metadata.get('uid')) or hashlib.sha256(
        content.encode('utf-8')).hexdigest()
    return {
        'source_unit_ref': f'{dataset_id}:{doc_id}:segment:{chunk_id}',
        'doc_ref': f'{dataset_id}:{doc_id}',
        'doc_id': doc_id,
        'filename': filename,
        'chunk_id': chunk_id,
        'group': text(getattr(node, 'group', '')) or group,
        'unit_type': _unit_type(content, metadata),
        'content': content,
        'metadata': json_safe({
            'node': metadata,
            'document': global_metadata,
            'number': getattr(node, 'number', None),
        }),
    }


def _config_value(config: Any, key: str) -> str:
    try:
        return text(config[key])
    except Exception:
        return text(getattr(config, key, ''))
