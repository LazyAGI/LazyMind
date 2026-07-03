import hashlib
import re
import threading
from collections.abc import Iterable, Mapping
from typing import Any

from .csv_loader import DEFAULT_MIN_CASE_COUNT, as_list, as_text, case_source
from .csv_loader import load_eval_dataset_csv_report, normalize_eval_case, warning

KB_GROUPS = ('block', 'line', 'doc-summary')
_DOCUMENTS: dict[tuple[str, str], Any] = {}
_DOCUMENT_LOCK = threading.Lock()


def _sid(config: Mapping[str, Any], default: str = '') -> str:
    kb_id = config.get('kb_id')
    if isinstance(kb_id, list | tuple):
        kb_id = kb_id[0] if kb_id else ''
    return as_text(kb_id or config.get('source_id') or config.get('dataset_id')) or default


def _csv_path(config: Mapping[str, Any]) -> str:
    return as_text(config.get('csv_path') or config.get('eval_dataset_path'))


def load_corpus(source_config: Mapping[str, Any], case_ids: Iterable[str] | None = None) -> dict[str, Any]:
    dataset_id = as_text(source_config.get('dataset_id')) or _sid(source_config, 'dataset')
    partitions = None if case_ids is None else tuple(case_id for case_id in case_ids if as_text(case_id))
    imported = _imported_cases(source_config)
    cases, warnings, has_csv = imported['cases'], imported['warnings'], imported['has_csv']
    raw_target = source_config.get('min_case_count') or source_config.get('target_case_count')
    target = int(raw_target) if raw_target not in (None, '') else (
        DEFAULT_MIN_CASE_COUNT if has_csv else len(partitions or ()) or 1
    )
    need_units = not cases or len(cases) < target or bool(partitions and len(cases) < len(partitions))
    units = _source_units(source_config, dataset_id) if need_units else []
    if not units:
        if cases and not need_units:
            pass
        elif cases and need_units:
            raise ValueError(f'eval dataset has {len(cases)} cases; supplementing to {target} needs source units')
        else:
            raise ValueError('eval dataset csv has no usable cases and source units are empty' if has_csv
                             else f'dataset {dataset_id} has no source units or eval cases')
    if partitions is not None and len(partitions) < max(len(cases), target):
        raise ValueError(f'eval dataset needs at least {max(len(cases), target)} partitions')
    mode = 'hybrid' if has_csv and len(cases) < target else 'eval_dataset' if cases and has_csv else 'corpus'
    if mode == 'hybrid':
        warnings = [*warnings, warning('csv_supplemented', f'supplementing cases to {target}')]
    return {'dataset_id': dataset_id, 'mode': mode, 'source_units': units, 'cases': cases, 'warnings': warnings,
            'case_provenance': [case_source(case) for case in cases],
            'stats': {'case_count': len(cases), 'min_case_count': target, 'source_unit_count': len(units)}}


def build_corpus_snapshot(report: Mapping[str, Any], source_config: Mapping[str, Any]) -> dict[str, Any]:
    dataset_id = as_text(report.get('dataset_id') or source_config.get('dataset_id')) or _sid(source_config, 'dataset')
    cases = [normalize_eval_case(case, default_id=f'case_{index:04d}')
             for index, case in enumerate(report.get('cases') or [], 1)]
    units = _normalize_units(report.get('source_units') or [], dataset_id)
    if not cases and not units:
        raise ValueError('corpus snapshot needs source units or eval cases')
    return {'dataset_id': dataset_id, 'mode': as_text(report.get('mode')) or ('eval_dataset' if cases else 'corpus'),
            'source_units': units, 'source_unit_count': len(units), 'cases': cases, 'case_count': len(cases),
            'stats': dict(report.get('stats') or {}), 'warnings': list(report.get('warnings') or []),
            'case_provenance': list(report.get('case_provenance') or [])}


def load_kb_doc_nodes(kb_doc: Any, *, kb_id: str, doc_id: str = '', group: str = 'block',
                      page_size: int = 100) -> Iterable[Any]:
    offset = 0
    while True:
        doc_filter = {'doc_ids': {doc_id}} if doc_id else {}
        nodes, total = kb_doc.get_nodes(**doc_filter, kb_id=kb_id, group=group, limit=page_size, offset=offset,
                                        return_total=True, sort_by_number=True)
        if not nodes:
            break
        yield from nodes
        offset += len(nodes)
        if offset >= int(total or offset):
            break


def kb_doc_exists(kb_doc: Any, *, kb_id: str, doc_id: str) -> bool:
    for group in KB_GROUPS:
        nodes, total = kb_doc.get_nodes(doc_ids={doc_id}, kb_id=kb_id, group=group, limit=1,
                                        offset=0, return_total=True, sort_by_number=True)
        if total or nodes:
            return True
    return False


def verify_kb_evidence(kb_id: str, doc_ids: Iterable[str], chunk_ids: Iterable[str]) -> tuple[bool, str]:
    docs = [item.removeprefix(f'{kb_id}:') for item in (as_text(doc_id) for doc_id in doc_ids) if item]
    chunks = {item for item in (as_text(chunk_id) for chunk_id in chunk_ids) if item}
    if not kb_id:
        return False, 'kb_id is empty'
    if not docs or not chunks:
        return False, 'reference doc ids or chunk ids are empty'
    try:
        kb_doc, matched = _document_client(), set()
        for doc_id in docs:
            if not kb_doc_exists(kb_doc, kb_id=kb_id, doc_id=doc_id):
                return False, f'doc_id not found in kb: {doc_id}'
            for group in KB_GROUPS:
                for node in load_kb_doc_nodes(kb_doc, kb_id=kb_id, doc_id=doc_id, group=group):
                    uid = as_text(getattr(node, 'uid', '')) or as_text(getattr(node, '_uid', ''))
                    matched.update(chunks & {uid, f'{kb_id}:{doc_id}:{uid}'})
                if chunks <= matched:
                    return True, ''
        return False, f'chunk_id not found in kb: {", ".join(sorted(chunks - matched))}'
    except Exception as exc:  # noqa: BLE001 - verification failures are report warnings.
        return False, str(exc)


def _imported_cases(source_config: Mapping[str, Any]) -> dict[str, Any]:
    rows, warnings, has_csv = [], [], False
    cache: dict[tuple[str, tuple[str, ...], tuple[str, ...]], tuple[bool, str]] = {}
    disabled_kbs: dict[str, str] = {}
    for config in _configs(source_config):
        path = _csv_path(config)
        if not path:
            continue
        has_csv = True
        kb_id = _sid(config)
        report = load_eval_dataset_csv_report(path, kb_id=kb_id)
        warnings.extend(report['warnings'])
        for row in report['cases']:
            source = case_source(row)
            source.update({'final_id': f'case_{len(rows) + 1:04d}', 'kb_id': kb_id or source['kb_id']})
            prep = {**dict(row.get('source_preparation') or {}), 'case_source': source}
            remapped = normalize_eval_case({**dict(row), 'id': source['final_id'], 'source_preparation': prep},
                                           default_id=source['final_id'])
            identity_error = _scope_import_refs(remapped, source['kb_id'])
            if identity_error:
                warnings.append(warning('csv_row_invalid', identity_error, case_id=remapped['id'],
                                        original_id=source['original_id'], kb_id=source['kb_id']))
                continue
            ok, message = False, disabled_kbs.get(source['kb_id'], '')
            if not message:
                key = (source['kb_id'], tuple(remapped.get('reference_doc_ids') or ()),
                       tuple(remapped.get('reference_chunk_ids') or ()))
                timeout = float(config.get('kb_verify_timeout') or 3)
                if key not in cache:
                    cache[key] = _verify_kb_evidence_with_timeout(key[0], key[1], key[2], timeout)
                ok, message = cache[key]
                if not ok and 'timed out' in message:
                    disabled_kbs[source['kb_id']] = message
            if not ok:
                item = warning('csv_evidence_unverified',
                               message or 'csv evidence could not be verified in knowledge base',
                               case_id=remapped['id'], original_id=source['original_id'], kb_id=source['kb_id'])
                warnings.append(item)
                remapped['source_preparation']['warnings'] = [
                    *list(remapped['source_preparation'].get('warnings') or []), item,
                ]
            rows.append(remapped)
    return {'cases': rows, 'warnings': warnings, 'has_csv': has_csv}


def _scope_import_refs(row: dict[str, Any], kb_id: str) -> str:
    if not kb_id:
        return ''
    refs = row['source_preparation'].get('context_reference')
    refs = refs if isinstance(refs, list) else []
    raw_docs = [as_text(doc).removeprefix(f'{kb_id}:') for doc in row['reference_doc_ids']]
    scoped_chunks = []
    for index, chunk in enumerate(row['reference_chunk_ids']):
        ref_doc = refs[index].get('doc_ref') or refs[index].get('doc_id') \
            if index < len(refs) and isinstance(refs[index], Mapping) else ''
        doc = as_text(ref_doc).removeprefix(f'{kb_id}:') or (
            raw_docs[0] if len(raw_docs) == 1 else raw_docs[index] if index < len(raw_docs) else ''
        )
        if chunk.startswith(f'{kb_id}:'):
            rest = chunk.removeprefix(f'{kb_id}:')
            if ':' in rest and rest.split(':', 1)[0]:
                scoped_chunks.append(chunk)
                continue
            chunk = rest
        if not doc:
            return f'case {row["id"]} reference chunk cannot be mapped to doc_id: {chunk}'
        scoped_chunks.append(f'{kb_id}:{doc}:{chunk}')
    row['reference_doc_ids'] = [
        doc if doc.startswith(f'{kb_id}:') else f'{kb_id}:{doc}' for doc in row['reference_doc_ids']
    ]
    row['reference_chunk_ids'] = scoped_chunks
    if refs and scoped_chunks:
        row['source_preparation']['context_reference'] = [
            {**dict(ref), 'source_id': kb_id,
             'doc_ref': row['reference_doc_ids'][min(index, len(row['reference_doc_ids']) - 1)],
             'source_unit_ref': scoped_chunks[min(index, len(scoped_chunks) - 1)]}
            for index, ref in enumerate(refs) if isinstance(ref, Mapping)
        ]
    return ''


def _source_units(source_config: Mapping[str, Any], dataset_id: str) -> list[dict[str, Any]]:
    units, configs = [], _configs(source_config)
    for config in ([source_config, *configs] if configs else [source_config]):
        source_id = _sid(config, dataset_id)
        force_source = bool(as_text(config.get('kb_id') or config.get('source_id')))
        for key in ('source_units', 'documents'):
            for item in config.get(key) or ():
                if isinstance(item, Mapping) and as_text(item.get('content') or item.get('text')):
                    units.append({**dict(item), 'source_id': source_id if force_source else
                                  as_text(item.get('source_id')) or source_id})
    for config in (configs or [source_config]):
        try:
            units.extend(_direct_kb_units(config))
        except Exception:  # noqa: BLE001 - direct KB absence falls through to corpus validation.
            continue
    return _normalize_units(units, dataset_id)


def _normalize_units(raw_units: Iterable[Mapping[str, Any]], dataset_id: str) -> list[dict[str, Any]]:
    units = []
    for index, raw in enumerate(raw_units, 1):
        content = as_text(raw.get('content') or raw.get('text')) if isinstance(raw, Mapping) else ''
        if not content:
            continue
        source_id = as_text(raw.get('source_id') or raw.get('kb_id') or raw.get('dataset_id')) or dataset_id
        doc_id = as_text(raw.get('doc_id') or f'{dataset_id}_doc_{index}')
        chunk_id = as_text(raw.get('chunk_id')) or hashlib.sha256(
            f'{source_id}:{doc_id}:{index}:{content}'.encode()
        ).hexdigest()
        unit_type = as_text(raw.get('unit_type') or raw.get('type')) or (
            'table' if '|' in content and '\n' in content else
            'formula' if '=' in content or re.search(r'\b(sum|average|formula|equation)\b', content, re.I)
            else 'paragraph'
        )
        units.append({'source_id': source_id, 'source_unit_ref': f'{source_id}:{doc_id}:{chunk_id}',
                      'doc_ref': f'{source_id}:{doc_id}', 'doc_id': doc_id,
                      'filename': as_text(raw.get('filename') or f'{doc_id}.txt'),
                      'chunk_id': chunk_id, 'unit_type': unit_type, 'content': content})
    return units


def _direct_kb_units(config: Mapping[str, Any]) -> list[dict[str, Any]]:
    kb_id = _sid(config)
    doc_ids = as_list(config.get('doc_ids'))
    for item in config.get('documents') or ():
        if isinstance(item, Mapping) and not as_text(item.get('content') or item.get('text')):
            doc_ids.append(as_text(item.get('doc_id')))
    if not kb_id: return []
    units, kb_doc = [], _document_client()
    for doc_id in (tuple(dict.fromkeys(item for item in doc_ids if item)) or ('',)):
        for group in KB_GROUPS:
            before = len(units)
            for node in load_kb_doc_nodes(kb_doc, kb_id=kb_id, doc_id=doc_id, group=group,
                                          page_size=int(config.get('kb_page_size') or 100)):
                meta, gmeta = getattr(node, 'metadata', {}) or {}, getattr(node, 'global_metadata', {}) or {}
                content = as_text(getattr(node, 'text', ''))
                if content:
                    node_doc_id = as_text(gmeta.get('docid') or doc_id)
                    chunk_id = as_text(getattr(node, 'uid', '')) or as_text(getattr(node, '_uid', ''))
                    if not node_doc_id or not chunk_id:
                        continue
                    filename = as_text(gmeta.get('file_name') or gmeta.get('filename') or f'{node_doc_id}.txt')
                    units.append({'source_id': kb_id, 'doc_id': node_doc_id,
                                  'filename': filename, 'chunk_id': chunk_id,
                                  'unit_type': as_text(meta.get('type') or meta.get('node_type') or group),
                                  'content': content})
            if len(units) > before:
                break
    return units


def _configs(source_config: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    configs = [item for item in source_config.get('knowledge_bases') or () if isinstance(item, Mapping)]
    base = {key: value for key, value in dict(source_config).items()
            if key not in {'kb_id', 'csv_data', 'csv_path', 'eval_dataset_path', 'dataset_id'}}
    csv_kbs, csv_seen = set(), set()
    for item in source_config.get('csv_data') or ():
        if isinstance(item, Mapping) and len(item) == 1:
            kb_id, csv_path = next(iter(item.items()))
            kb_id, csv_path = as_text(kb_id), as_text(csv_path)
            if (kb_id, csv_path) in csv_seen:
                continue
            csv_kbs.add(kb_id)
            csv_seen.add((kb_id, csv_path))
            configs.append({**base, 'kb_id': kb_id, 'dataset_id': kb_id, 'csv_path': csv_path})
    configs.extend(
        {**base, 'kb_id': kb_id, 'dataset_id': kb_id}
        for kb_id in as_list(source_config.get('kb_id')) if kb_id not in csv_kbs
    )
    return configs


def _document_client() -> Any:
    from lazymind.config import config
    from lazymind.parsing.service.build_document import build_document

    algo_id = as_text(config['algo_id'] or config['agentic_kb_name'])
    if not algo_id:
        raise ValueError('agentic kb algorithm id is empty')
    key = ('local', algo_id)
    with _DOCUMENT_LOCK:
        if key not in _DOCUMENTS:
            _DOCUMENTS[key] = build_document(algo_id, serve=False)
    return _DOCUMENTS[key]


def _verify_kb_evidence_with_timeout(
    kb_id: str, doc_ids: Iterable[str], chunk_ids: Iterable[str], timeout: float,
) -> tuple[bool, str]:
    del timeout
    return verify_kb_evidence(kb_id, doc_ids, chunk_ids)
