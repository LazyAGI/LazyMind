from __future__ import annotations

from typing import Any, Mapping

from evo.operations.common import as_list, first_text, text, unique_texts
from .judge_answer import judge_answer


def rag_answer(case: Mapping[str, Any], target_config: Mapping[str, Any], services: Any) -> dict[str, Any]:
    return normalize_rag_answer(case, services.answer_case(case, target_config))


def answer_and_judge(case: Mapping[str, Any], target_config: Mapping[str, Any],
                     policy: Mapping[str, Any], services: Any) -> tuple[dict[str, Any], dict[str, Any]]:
    answer = rag_answer(case, target_config, services)
    return answer, judge_answer(answer, policy, services)


def normalize_rag_answer(case: Mapping[str, Any], result: Mapping[str, Any]) -> dict[str, Any]:
    case_id = text(case.get('id') or result.get('case_id'))
    value = result.get('value') if isinstance(result.get('value'), Mapping) else result
    status = text(result.get('status') or value.get('status') or 'completed')
    chat_error = None if status in {'completed', 'ok'} else {
        'type': text(result.get('error_type') or value.get('error_type') or 'ChatError'),
        'message': text(result.get('error_message') or value.get('error_message') or 'RAG call failed'),
    }
    answer = text(value.get('answer') or value.get('text'))
    source_doc_ids, source_chunk_ids = _source_ids([*as_list(value.get('sources')), *as_list(value.get('contexts'))])
    return {
        'case_id': case_id,
        'case': case,
        'question': text(value.get('question') or case.get('question')),
        'answer': answer,
        'status': 'ok' if answer and chat_error is None else 'failed',
        'chat_error': chat_error,
        'tool_errors': unique_texts(value.get('kb_errors') or value.get('tool_errors')),
        'contexts': [str(item) for item in value.get('contexts') or value.get('sources') or []],
        'doc_ids': unique_texts([*as_list(value.get('doc_ids') or value.get('document_ids')), *source_doc_ids]),
        'chunk_ids': unique_texts([*as_list(value.get('chunk_ids') or value.get('segment_ids')
                                            or value.get('segement_ids')), *source_chunk_ids]),
        'trace_id': text(value.get('trace_id')),
        'evidence_status': 'found' if source_doc_ids or source_chunk_ids or value.get('contexts') else 'no_evidence',
        'target': dict(value.get('target') or {}) if isinstance(value.get('target'), Mapping) else {},
    }


def _source_ids(items: Any) -> tuple[list[str], list[str]]:
    doc_ids, chunk_ids = [], []
    for item in as_list(items):
        if isinstance(item, Mapping):
            doc = first_text(item, 'doc_id', 'document_id', 'file_id', 'docid')
            chunk = first_text(item, 'chunk_id', 'segment_id', 'segement_id', 'node_id', 'uid', 'source_unit_ref')
            if doc:
                doc_ids.append(doc)
            if chunk:
                chunk_ids.append(chunk)
    return list(dict.fromkeys(doc_ids)), list(dict.fromkeys(chunk_ids))
