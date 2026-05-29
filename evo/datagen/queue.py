from __future__ import annotations
import json
import logging
from pathlib import Path
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, wait
from typing import Any
from evo.datagen.rag_client import call_rag_chat, RAGTargetRequiredError
from evo.datagen.validate import require_valid_eval_case
from evo.harness.plan import StopRequested

_log = logging.getLogger('evo.datagen.queue')


def get_eval_queue(
    eval_name: str,
    *,
    case_id: str = '',
    dataset_name: str = '',
    base_dir: str | Path,
    target_chat_url: str = '',
    max_workers: int = 8,
    filters: dict[str, Any] | None = None,
    require_trace: bool = True,
    model_config: dict[str, Any] | None = None,
    skip_case_ids: set[str] | None = None,
    on_item=None,
    cancel=None,
    on_progress=None,
) -> dict[str, Any]:
    base = Path(base_dir) / 'datasets' / eval_name
    eval_file = base / 'eval_data.json'
    with open(eval_file, 'r', encoding='utf-8') as f:
        eval_data = json.load(f)
    cases = eval_data.get('cases', [])
    eval_filters = _eval_filters(filters or {}, eval_data)
    if case_id:
        cases = [c for c in cases if c.get('case_id') == case_id]
    if skip_case_ids:
        cases = [c for c in cases if c.get('case_id') not in skip_case_ids]
    for case in cases:
        require_valid_eval_case(case)
    if not target_chat_url:
        raise RAGTargetRequiredError(
            f'No target_chat_url provided for eval {eval_name}. '
            'Pass target_chat_url explicitly when overriding the internal chat service.'
        )
    workers = max(1, min(max_workers, len(cases) or 1))
    eval_queue: list[dict] = []
    done = 0
    total = len(cases)
    executor = ThreadPoolExecutor(max_workers=workers)
    try:
        pending = {}
        iterator = iter(enumerate(cases))

        def submit(case: dict, attempt: int = 1) -> None:
            pending[executor.submit(
                _build_eval_item,
                case,
                target_chat_url,
                dataset_name,
                eval_filters,
                require_trace,
                model_config,
            )] = (case, attempt)

        def submit_next() -> bool:
            if cancel and cancel():
                return False
            try:
                i, case = next(iterator)
            except StopIteration:
                return False
            submit({**case, '_order': i})
            return True

        while len(pending) < workers and submit_next():
            pass
        while pending:
            if cancel and cancel():
                raise StopRequested(at_step='case')
            done_futures, _ = wait(pending, timeout=0.2, return_when=FIRST_COMPLETED)
            if not done_futures:
                continue
            future = done_futures.pop()
            case, attempt = pending.pop(future)
            try:
                item = future.result()
            except Exception as exc:
                if attempt < 3 and not (cancel and cancel()):
                    _log.warning(
                        'rag eval item failed case_id=%s retry %s/3: %s',
                        case.get('case_id'), attempt + 1,
                        exc
                    )
                    submit(case, attempt + 1)
                    continue
                _log.warning('rag eval item failed: %s', exc)
                item = None
            if item:
                eval_queue.append(item)
                if on_item:
                    on_item(item)
            done += 1
            if on_progress:
                on_progress(done, total)
            submit_next()
    finally:
        executor.shutdown(wait=False, cancel_futures=True)
    eval_queue.sort(key=lambda row: row.get('_order', 0))
    for row in eval_queue:
        row.pop('_order', None)
    return {
        'eval_queue': eval_queue,
        'eval_set_id': eval_data.get('eval_set_id', ''),
        'kb_id': eval_data.get('kb_id', ''),
        'eval_name': eval_name,
        'total_cases': eval_data.get('total_nums') or len(eval_data.get('cases') or []),
    }


def _eval_filters(filters: dict[str, Any], eval_data: dict[str, Any]) -> dict[str, Any]:
    out = dict(filters)
    kb_id = str(eval_data.get('kb_id') or '').strip()
    if kb_id and not out.get('kb_id'):
        out['kb_id'] = kb_id
    return out


def _build_eval_item(
    case: dict,
    target_chat_url: str,
    dataset_name: str,
    filters: dict[str, Any],
    require_trace: bool,
    model_config: dict[str, Any] | None,
) -> dict:
    question = case['question']
    ground_truth = case['ground_truth']
    rag_result = call_rag_chat(
        question,
        target_chat_url,
        dataset_name,
        filters,
        require_trace=require_trace,
        model_config=model_config,
    )
    metrics = _calculate_metrics(
        case.get('reference_chunk_ids', []),
        case.get('reference_doc_ids', []),
        rag_result['chunk_ids'],
        rag_result['doc_ids'],
    )
    return {
        '_order': int(case.get('_order') or 0),
        'case_id': case['case_id'],
        'key_points': case.get('key_points', []),
        'question': question,
        'question_type': case.get('question_type', 1),
        'reference_chunk_ids': case.get('reference_chunk_ids', []),
        'reference_doc_ids': case.get('reference_doc_ids', []),
        'ground_truth': ground_truth,
        'rag_answer': rag_result['answer'],
        'retrieve_contexts': rag_result['contexts'],
        'retrieve_doc': rag_result['docs'],
        'rag_response': rag_result['raw'],
        'retrieve_chunk_ids': rag_result['chunk_ids'],
        'retrieve_doc_ids': rag_result['doc_ids'],
        'trace_id': rag_result['trace_id'],
        'rag_trace': rag_result.get('trace'),
        'context_recall': metrics['context_recall'],
        'doc_recall': metrics['doc_recall'],
    }


def _calculate_metrics(
    reference_chunk_ids, reference_doc_ids, retrieve_chunk_ids, retrieve_doc_ids
) -> dict[str, float]:
    ref_chunks = set(reference_chunk_ids)
    ref_docs = set(reference_doc_ids)
    ret_chunks = set(retrieve_chunk_ids)
    ret_docs = set(retrieve_doc_ids)
    hit_chunks = len(ref_chunks & ret_chunks)
    hit_docs = len(ref_docs & ret_docs)
    context_recall = hit_chunks / len(ref_chunks) if ref_chunks else 0.0
    doc_recall = hit_docs / len(ref_docs) if ref_docs else 0.0
    return {'context_recall': round(context_recall, 4), 'doc_recall': round(doc_recall, 4)}
