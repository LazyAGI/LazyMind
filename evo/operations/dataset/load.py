import hashlib
import os
import re
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ...runtime import OperationOutput
from .utils import bounded_int, expected_ref, strings

FILTER_KEYS = {'doc_ids', 'file_name', 'filename', 'file_type'}
TYPES = {'table': 'table', 'list': 'list', 'ordered_list': 'list', 'unordered_list': 'list', 'formula': 'formula',
         'equation': 'formula', 'mixed': 'mixed', 'table_text': 'mixed', 'formula_text': 'mixed'}
_DOCUMENTS: dict[tuple[str, str], Any] = {}


class LoadCorpusOperation:
    def execute(self, ctx) -> OperationOutput:
        sources = ctx.params.get('sources', [])
        if not isinstance(sources, list):
            raise ValueError('sources must be a list')
        state = {'pages': [], 'sources': [], 'filters': [], 'loaded_refs': [], 'skipped': [],
                 'stats': {'source_count': 0, 'matched_doc_count': 0, 'scanned_doc_count': 0, 'loaded_doc_count': 0,
                           'skipped_doc_count': 0, 'document_page_count': 0, 'hit_doc_limit': False}}
        self._progress(ctx, state, 'load_corpus', 'running', 'starting corpus load', done=0, total=len(sources))
        for index, source in enumerate(sources, 1):
            ctx.check_interrupt()
            if not isinstance(source, dict):
                raise ValueError(f'source_{index} is not an object')
            source_id = str(source.get('source_id') or source.get('dataset_id') or f'source_{index}')
            state['stats']['source_count'] += 1
            driver = str(source.get('driver') or 'postgres').strip().lower()
            if source.get('type') != 'kb' or driver not in {'postgres', 'postgresql'}:
                raise ValueError(f'unsupported corpus source {source_id}: type={source.get("type")} driver={driver}')
            self._load_db(ctx, state, source_id, source)
            self._progress(ctx, state, 'load_corpus', 'running', f'loaded {source_id}', source_id, index, len(sources))
        report = {'sources': state['sources'], 'filters': {'sources': state['filters']} if state['filters'] else {},
                  'document_page_refs': [expected_ref(ctx, draft) for draft in state['pages']], 'chunk_page_refs': [],
                  'loaded_doc_refs': state['loaded_refs'], 'stats': state['stats'], 'skipped': state['skipped'],
                  'errors': []}
        self._progress(ctx, state, 'load_corpus', 'success', f"loaded {state['stats']['loaded_doc_count']} docs",
                       done=state['stats']['loaded_doc_count'], total=state['stats']['scanned_doc_count'])
        return OperationOutput([*state['pages'], ArtifactDraft('corpus_load_report', 'CorpusLoadReport', report,
                                                               ctx.operation_run_id)])

    def _doc_page(self, ctx, state, source_id, docs) -> None:
        if not docs:
            return
        index = state['stats']['document_page_count'] + 1
        payload = {'source_id': source_id, 'page_index': index, 'documents': docs}
        state['pages'].append(ArtifactDraft(f'corpus_docs_page_{index:04d}', 'CorpusDocumentPage', payload,
                                            ctx.operation_run_id))
        state['stats']['document_page_count'] = index

    def _load_db(self, ctx, state, source_id, source) -> None:
        try:
            import psycopg
            from psycopg.rows import dict_row
        except ImportError as exc:
            raise RuntimeError('psycopg is required for db corpus loading') from exc
        driver = os.getenv('LAZYMIND_READONLY_DB_DRIVER', 'postgres').strip().lower()
        if driver not in {'postgres', 'postgresql'}:
            raise RuntimeError(f'unsupported readonly db driver: {driver}')
        dsn = os.getenv('LAZYMIND_READONLY_DB_DSN', '').strip()
        schema = os.getenv('LAZYMIND_READONLY_SCHEMA', 'public').strip()
        default = 'host=db user=app password=app dbname=app port=5432 sslmode=disable connect_timeout=5'
        safe_dsn = (' '.join('password=***' if part.startswith('password=') else part for part in dsn.split())
                    if dsn else default.replace('password=app', 'password=***'))
        dataset_id, buffer, loaded, scanned = str(source.get('dataset_id') or source_id), [], 0, 0
        max_docs = bounded_int(source.get('max_docs'), 1000, 1, 100000)
        page_size = bounded_int(source.get('doc_page_size'), int(source.get('page_size') or 1000), 1, 5000)
        raw = source.get('filters') if isinstance(source.get('filters'), dict) else {}
        unsupported = sorted({key for key, value in raw.items() if value and key not in FILTER_KEYS})
        filters: dict[str, Any] = {}
        for name, key, allowed in (
                ('doc_ids', 'doc_ids', ('include', 'exclude')),
                ('file_name', ('file_name', 'filename'), ('include', 'exclude', 'prefixes', 'suffixes')),
                ('file_type', 'file_type', ('include',))):
            value = raw.get(key) if isinstance(key, str) else next((raw[item] for item in key if item in raw), None)
            if isinstance(value, dict):
                group = {key: items for key in allowed if (items := strings(value.get(key)))}
            else:
                items = strings(value)
                group = {'include': items} if items and 'include' in allowed else {}
            if group:
                filters[name] = group
        if filters or unsupported:
            state['filters'].append({'source_id': source_id, 'applied': filters, 'unsupported': unsupported})
        q = '"' + schema.replace('"', '""') + '"'
        table = f'from {q}.lazyllm_kb_documents kb join {q}.lazyllm_documents d on d.doc_id = kb.doc_id'
        where, params = ['kb.kb_id = %s'], [dataset_id]
        self._any(where, params, 'd.doc_id', filters.get('doc_ids', {}).get('include'))
        self._any(where, params, 'd.doc_id', filters.get('doc_ids', {}).get('exclude'), exclude=True)
        name_filter = filters.get('file_name', {})
        self._any(where, params, 'd.filename', name_filter.get('include'))
        self._any(where, params, 'd.filename', name_filter.get('exclude'), exclude=True)
        self._like(where, params, 'd.filename', name_filter.get('prefixes'), suffix=False)
        self._like(where, params, 'd.filename', name_filter.get('suffixes'), suffix=True)
        types = [item.lower().lstrip('.') for item in strings(filters.get('file_type', {}).get('include'))]
        if types:
            where.append("lower(coalesce(d.file_type, '')) = any(%s)")
            params.append(types)
        where_sql = ' and '.join(where)
        with psycopg.connect(dsn or default, row_factory=dict_row) as conn, conn.cursor() as cursor:
            cursor.execute(f'select count(*) as count {table} where {where_sql}', params)
            matched = int((cursor.fetchone() or {}).get('count') or 0)
            limit = min(matched, max_docs)
            state['stats']['matched_doc_count'] += matched
            state['stats']['hit_doc_limit'] = state['stats']['hit_doc_limit'] or matched > max_docs
            offset = 0
            while offset < limit:
                ctx.check_interrupt()
                cursor.execute(f'select kb.id as kb_document_id, d.* {table} where {where_sql} order by kb.id'
                               ' limit %s offset %s', [*params, min(page_size, limit - offset), offset])
                rows = cursor.fetchall()
                if not rows:
                    break
                offset += len(rows)
                for row in rows:
                    scanned += 1
                    g = row.get
                    doc_id = str(g('doc_id') or '')
                    if not doc_id:
                        state['skipped'].append({'source_id': source_id, 'reason': 'missing_doc_id'})
                        state['stats']['skipped_doc_count'] += 1
                        continue
                    filename = str(g('filename') or g('display_name') or doc_id)
                    metadata = self._json({'document_id': doc_id, 'filename': filename,
                                           'upload_status': g('upload_status', ''), 'file_type': g('file_type', ''),
                                           'size_bytes': int(g('size_bytes') or 0), 'core_document': dict(row)})
                    doc = {'doc_ref': f'{dataset_id}:{doc_id}', 'doc_id': doc_id, 'source_ref': str(g('path') or ''),
                           'filename': filename, 'file_type': str(metadata['file_type']),
                           'text_preview': filename[:240], 'char_count': len(filename), 'metadata': metadata}
                    loaded += 1
                    state['stats']['scanned_doc_count'] += 1
                    state['loaded_refs'].append(doc['doc_ref'])
                    state['stats']['loaded_doc_count'] += 1
                    buffer.append(doc)
                    if len(buffer) >= page_size:
                        self._doc_page(ctx, state, source_id, buffer)
                        buffer.clear()
                    self._progress(ctx, state, 'scan_documents', 'running',
                                   f"scanned {state['stats']['scanned_doc_count']} docs",
                                   doc['doc_ref'], state['stats']['scanned_doc_count'], limit)
        self._doc_page(ctx, state, source_id, buffer)
        extra = {'source_id': source_id, 'dataset_id': dataset_id, 'resolved_api': 'db.lazyllm_kb_documents',
                 'resolved_base_url': safe_dsn, 'readonly_schema': schema, 'fetched_document_count': loaded,
                 'matched_document_count': matched, 'scanned_document_count': scanned, 'max_docs': max_docs,
                 'doc_page_size': page_size, 'applied_filters': filters, 'unsupported_filters': unsupported}
        out = {k: self._json(v) for k, v in source.items() if k != 'filters' and not k.lower().endswith('dsn')}
        state['sources'].append(out | {key: self._json(value) for key, value in extra.items()})

    def _any(self, where, params, expression, values, exclude=False) -> None:
        if items := strings(values):
            where.append(f'{expression} <> all(%s)' if exclude else f'{expression} = any(%s)')
            params.append(items)

    def _like(self, where, params, expression, values, suffix) -> None:
        patterns = [f'%{item}' if suffix else f'{item}%' for item in strings(values)]
        if patterns:
            where.append('(' + ' or '.join(f'{expression} ilike %s' for _ in patterns) + ')')
            params.extend(patterns)

    def _json(self, value) -> Any:
        if isinstance(value, dict):
            return {str(key): self._json(item) for key, item in value.items()}
        if isinstance(value, (list, tuple)):
            return [self._json(item) for item in value]
        return value if value is None or isinstance(value, (str, int, float, bool)) else str(value)

    def _progress(self, ctx, state, phase, status, message, current_item='', done=0, total=0) -> None:
        detail = {'loaded_docs': state['stats']['loaded_doc_count'], 'skipped': state['stats']['skipped_doc_count'],
                  'document_page_count': state['stats']['document_page_count']}
        ctx.report_progress(phase=phase, status=status, message=message, current_item=current_item, done=done,
                            total=total, detail=detail)


class BuildCorpusSnapshotOperation:
    def execute(self, ctx) -> OperationOutput:
        raw = str(ctx.params.get('source_report_ref') or '')
        report_ref = ctx.input_refs[0] if ctx.input_refs else ArtifactRef.parse(raw)
        report = ctx.artifact_graph.get(report_ref)
        page_refs = [ArtifactRef.parse(ref) for ref in report.get('document_page_refs', [])]
        loaded_refs = set(report.get('loaded_doc_refs') or [])
        docs = [doc for ref in page_refs for doc in ctx.artifact_graph.get(ref).get('documents', [])
                if not loaded_refs or doc.get('doc_ref') in loaded_refs]
        if not docs:
            raise ValueError('corpus load report has no loaded documents')
        groups = strings(ctx.params.get('segment_groups')) or ['block', 'line']
        page_size = bounded_int(ctx.params.get('segment_page_size'), 50, 1, 1000)
        min_chars = bounded_int(ctx.params.get('min_segment_chars'), 80, 1, 100000)
        preview_chars = bounded_int(ctx.params.get('preview_chars'), 240, 20, 2000)
        state = {'buffer': [], 'pages': [], 'documents': [], 'skipped': [], 'errors': [],
                 'stats': {'document_count': 0, 'document_with_units_count': 0, 'source_unit_count': 0,
                           'source_unit_page_count': 0, 'total_char_count': 0, 'skipped_document_count': 0,
                           'error_count': 0, 'unit_type_counts': {}}}
        self._snap_progress(ctx, state, 'build_corpus_snapshot', 'running', 'starting corpus snapshot', done=0,
                            total=len(docs))
        for index, doc in enumerate(docs, 1):
            ctx.check_interrupt()
            try:
                units = self._doc_units(groups, doc, page_size, min_chars, preview_chars)
                self._add(ctx, state, report_ref, page_refs, doc, units, page_size)
            except Exception as exc:
                state['stats']['document_count'] += 1
                state['stats']['error_count'] += 1
                state['errors'].append({'doc_ref': doc.get('doc_ref', ''), 'message': str(exc)})
            self._snap_progress(ctx, state, 'read_segments', 'running', f'snapshotted {index}/{len(docs)} docs',
                                str(doc.get('doc_ref') or ''), index, len(docs))
        self._unit_page(ctx, state, report_ref, page_refs, state['buffer'])
        if state['stats']['source_unit_count'] == 0:
            detail = '; '.join(f"{e.get('doc_ref')}: {e.get('message')}" for e in state['errors'][:3])
            raise ValueError(f"corpus snapshot has no usable source units{'; ' + detail if detail else ''}")
        snapshot = {'snapshot_id': 'corpus_snapshot', 'source_report_ref': str(report_ref),
                    'document_page_refs': [str(ref) for ref in page_refs],
                    'source_unit_page_refs': [expected_ref(ctx, draft) for draft in state['pages']],
                    'documents': state['documents'], 'stats': state['stats'], 'skipped': state['skipped'],
                    'errors': state['errors']}
        self._snap_progress(ctx, state, 'build_corpus_snapshot', 'success',
                            f"snapshot built from {state['stats']['document_with_units_count']} docs and "
                            f"{state['stats']['source_unit_count']} source units", done=len(docs), total=len(docs))
        return OperationOutput([*state['pages'], ArtifactDraft(
            'corpus_snapshot', 'CorpusSnapshot', snapshot, ctx.operation_run_id, input_refs=[report_ref, *page_refs]
        )])

    def _add(self, ctx, state, report_ref, page_refs, doc, units, page_size):
        state['stats']['document_count'] += 1
        if not units:
            state['skipped'].append({'doc_ref': doc.get('doc_ref', ''), 'reason': 'no_usable_segments'})
            state['stats']['skipped_document_count'] += 1
            return
        chars = sum(int(unit.get('char_count') or 0) for unit in units)
        keys = [hashlib.sha256(re.sub(r'\s+', ' ', u['content']).strip().encode('utf-8')).hexdigest() for u in units]
        state['documents'].append({'doc_ref': doc.get('doc_ref', ''), 'filename': doc.get('filename', ''),
                                   'file_type': doc.get('file_type', ''), 'source_unit_count': len(units),
                                   'char_count': chars,
                                   'content_checksum': hashlib.sha256(''.join(keys).encode('ascii')).hexdigest(),
                                   'text_preview': units[0].get('text_preview', '')})
        state['stats']['document_with_units_count'] += 1
        state['stats']['source_unit_count'] += len(units)
        state['stats']['total_char_count'] += chars
        counts = state['stats']['unit_type_counts']
        for t in (str(u.get('unit_type') or 'paragraph') for u in units):
            counts[t] = int(counts.get(t, 0)) + 1
        state['buffer'].extend(units)
        while len(state['buffer']) >= page_size:
            self._unit_page(ctx, state, report_ref, page_refs, state['buffer'][:page_size])
            state['buffer'] = state['buffer'][page_size:]

    def _unit_page(self, ctx, state, report_ref, page_refs, units) -> None:
        if not units:
            return
        index = state['stats']['source_unit_page_count'] + 1
        payload = {'snapshot_id': 'corpus_snapshot', 'page_index': index, 'source_units': units}
        state['pages'].append(ArtifactDraft(f'corpus_source_units_page_{index:04d}', 'CorpusSourceUnitPage',
                                            payload, ctx.operation_run_id, input_refs=[report_ref, *page_refs]))
        state['stats']['source_unit_page_count'] = index

    def _doc_units(self, groups, doc, page_size, min_chars, preview_chars) -> list[dict[str, Any]]:
        """Merge units across segment groups; earlier groups win on duplicate content."""
        parts = str(doc.get('doc_ref') or '').split(':', 1)
        if len(parts) != 2:
            raise ValueError(f"invalid doc_ref: {doc.get('doc_ref')}")
        dataset_id, doc_id, last_error = parts[0].strip(), parts[1].strip(), ''
        merged, seen = [], set()
        for group in groups:
            try:
                units = [self._unit(dataset_id, doc_id, doc, group, row, index, preview_chars)
                         for index, row in enumerate(self._chunks(dataset_id, doc_id, group, page_size), 1)
                         if len(str(row.get('content') or '').strip()) >= min_chars]
            except Exception as exc:
                last_error = str(exc)
                continue
            for unit in units:
                key = hashlib.sha256(re.sub(r'\s+', ' ', unit['content']).strip().encode('utf-8')).hexdigest()
                if key in seen:
                    continue
                seen.add(key)
                merged.append(unit)
        if not merged and last_error:
            raise RuntimeError(last_error)
        return merged

    def _unit(self, dataset_id, doc_id, doc, group, row, index, preview_chars) -> dict[str, Any]:
        content = str(row.get('content') or '').strip()
        segment_id = str(row.get('uid') or row.get('id') or row.get('number') or index)
        unit_type = TYPES.get(str(row.get('type') or (row.get('metadata') or {}).get('type') or '').lower(),
                              'paragraph')
        return {'source_unit_ref': f'{dataset_id}:{doc_id}:segment:{segment_id}', 'doc_ref': doc.get('doc_ref', ''),
                'doc_id': doc_id, 'filename': doc.get('filename', ''), 'segment_id': segment_id,
                'group': str(row.get('group') or group), 'unit_type': unit_type, 'content': content,
                'text_preview': content[:preview_chars], 'char_count': len(content),
                'metadata': {key: row.get(key) for key in ('number', 'type', 'parent', 'metadata',
                                                           'global_metadata')}}

    def _chunks(self, dataset_id, doc_id, group, page_size) -> list[dict[str, Any]]:
        rows, offset, doc = [], 0, self._document()
        while True:
            nodes, total = doc.get_nodes(doc_ids=[doc_id], group=group, kb_id=dataset_id, limit=page_size,
                                         offset=offset, return_total=True, sort_by_number=True)
            items = [{'uid': getattr(n, 'uid', ''), 'number': getattr(n, 'number', None),
                      'group': getattr(n, 'group', None), 'parent': getattr(n, '_parent', None),
                      'type': (m := getattr(n, 'metadata', {}) or {}).get('type') or m.get('node_type'),
                      'content': getattr(n, 'text', '') or '', 'metadata': m,
                      'global_metadata': getattr(n, 'global_metadata', {}) or {}} for n in nodes]
            rows.extend(items)
            if not items or len(rows) >= int(total or len(rows)):
                return rows
            offset += len(items)

    def _document(self):
        from lazyllm import Document
        from lazymind.config import config
        key = (config['agentic_kb_url'].rstrip('/'), config['agentic_kb_name'])
        if key not in _DOCUMENTS:
            _DOCUMENTS[key] = Document(url=f'{key[0]}/_call', name=key[1])
        return _DOCUMENTS[key]

    def _snap_progress(self, ctx, state, phase, status, message, current_item='', done=0, total=0) -> None:
        detail = {'source_unit_count': state['stats']['source_unit_count'],
                  'source_unit_page_count': state['stats']['source_unit_page_count'],
                  'skipped': state['stats']['skipped_document_count'], 'errors': state['stats']['error_count']}
        ctx.report_progress(phase=phase, status=status, message=message, current_item=current_item, done=done,
                            total=total, detail=detail)
