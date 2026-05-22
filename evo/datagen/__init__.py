from __future__ import annotations
import logging
from typing import Any
from evo.datagen.attempts import save_attempt, start_attempt
from evo.datagen.evaluate import create_evaluate_task
from evo.datagen.metrics import calculate_metrics
from evo.datagen.prompts import prompt_evaluate, prompt_generate_single_hop
from evo.datagen.queue import get_eval_queue
from evo.datagen.single_hop import generate_single_hop, generate_single_hop_from_chunks
from evo.datagen.corpus import build_corpus_index
from evo.datagen.multi_hop import generate_multi_hop_from_chunks
from evo.datagen.structured import generate_list_questions, generate_table_questions
from evo.datagen.validate import safe_parse_qa_json
from evo.datagen.writer import (
    build_eval_report,
    build_full_eval_set,
    ensure_eval_dir,
    extract_json,
    load_report,
    save_eval_report,
    write_full_eval_set,
)
from evo.datagen.kb_client import KBClient
from evo.datagen.langfuse import fetch_traces_for_report
from evo.harness.plan import StopRequested
from evo.runtime.config import EvoConfig
from evo.runtime.fs import atomic_write_json

_log = logging.getLogger('evo.datagen')


class DatasetGenerationEmptyError(RuntimeError):
    code = 'DATASET_EMPTY'
    kind = 'permanent'


class KBDocsEmptyError(RuntimeError):
    code = 'KB_DOCS_EMPTY'
    kind = 'permanent'


class KBChunksEmptyError(RuntimeError):
    code = 'KB_CHUNKS_EMPTY'
    kind = 'permanent'


class EvalDatasetEmptyError(RuntimeError):
    code = 'EVAL_DATASET_EMPTY'
    kind = 'permanent'


__all__ = [
    'run_generate_pipeline',
    'run_eval',
    'load_report',
    'fetch_traces_for_report',
    'generate_single_hop',
    'create_evaluate_task',
    'get_eval_queue',
    'calculate_metrics',
    'build_eval_report',
    'build_full_eval_set',
    'save_eval_report',
    'write_full_eval_set',
    'load_report',
    'ensure_eval_dir',
    'extract_json',
    'safe_parse_qa_json',
    'prompt_generate_single_hop',
    'prompt_evaluate',
    'KBClient',
    'fetch_traces_for_report',
]
_TYPE_ORDER = ('single_hop', 'multi_hop', 'table', 'list')
_TYPE_TO_QUESTION_TYPE = {'single_hop': 1, 'multi_hop': (2, 3), 'table': 4, 'list': 5}


def run_generate_pipeline(kb_id: str, algo_id: str, eval_name: str, *, dataset_source: KBClient,
                          config: EvoConfig, thread_id: str | None = None, llm_factory=None,
                          cancel=None, num_cases: int | None = None, attempt_id: str | None = None,
                          resume: bool = True, on_progress=None,
                          ) -> tuple[str, dict[str, Any]]:
    _log.info('start dataset_gen kb_id=%s algo_id=%s eval_name=%s', kb_id, algo_id, eval_name)
    _check_cancel(cancel)
    docs = _get_docs_or_raise(dataset_source, kb_id, algo_id)
    _check_cancel(cancel)
    dataset_dir = config.storage.base_dir / 'datasets' / eval_name
    attempt_dir, prev = start_attempt(dataset_dir, 'attempts', attempt_id)
    plan = prev.get('generation_plan') if resume and num_cases is None and prev.get('generation_plan') else (
        _generation_plan(num_cases, config.dataset_gen.task_settings)
    )
    prev_cases = list(prev.get('cases') or []) if resume else []
    stats: dict[str, int] = {}
    target_count = sum(plan.values())
    done_cases = prev_cases[:target_count]
    _save_dataset_attempt(attempt_dir, eval_name, kb_id, plan, stats, done_cases)

    def add(kind: str, items: list[dict]) -> None:
        nonlocal done_cases
        stats[kind] = stats.get(kind, 0) + len(items)
        done_cases = (done_cases + _cases_from_items(items, eval_name, kb_id))[:target_count]
        _save_dataset_attempt(attempt_dir, eval_name, kb_id, plan, stats, done_cases)
        if on_progress:
            on_progress(min(len(done_cases), target_count), target_count)

    workers = max(1, min(config.dataset_gen.max_workers, 8))
    corpus = build_corpus_index(
        dataset_source,
        kb_id,
        algo_id,
        cache_dir=config.storage.work_dir / 'datagen_cache',
        docs=docs,
        max_workers=workers,
    )
    _check_cancel(cancel)
    if not corpus.chunks:
        raise KBChunksEmptyError(
            f'生成评测集失败，因为知识库是空的或没有可用内容。kb_id={kb_id} algo_id={algo_id}'
        )
    need = _remaining_plan(plan, done_cases)
    if need['single_hop'] > 0:
        add(
            'single_hop',
            generate_single_hop_from_chunks(
                corpus.chunks, count=need['single_hop'], max_workers=workers, llm_factory=llm_factory, cancel=cancel
            ),
        )
    _check_cancel(cancel)
    need = _remaining_plan(plan, done_cases)
    if need['multi_hop'] > 0:
        add(
            'multi_hop',
            generate_multi_hop_from_chunks(
                corpus.chunks, count=need['multi_hop'], max_workers=workers, llm_factory=llm_factory, cancel=cancel
            ),
        )
    _check_cancel(cancel)
    need = _remaining_plan(plan, done_cases)
    if need['table'] > 0:
        add(
            'table',
            generate_table_questions(
                corpus.chunks, count=need['table'], max_workers=workers, llm_factory=llm_factory, cancel=cancel
            ),
        )
    _check_cancel(cancel)
    need = _remaining_plan(plan, done_cases)
    if need['list'] > 0:
        add(
            'list',
            generate_list_questions(
                corpus.chunks, count=need['list'], max_workers=workers, llm_factory=llm_factory, cancel=cancel
            ),
        )
    missing = target_count - len(done_cases)
    if missing > 0:
        _log.info('dataset_gen shortfall=%s; filling with single-hop', missing)
        add(
            'single_hop',
            generate_single_hop(
                dataset_source,
                kb_id,
                algo_id,
                count=missing,
                max_workers=workers,
                llm_factory=llm_factory,
                cancel=cancel,
            ),
        )
    final_data = build_full_eval_set([], eval_name=eval_name, kb_id=kb_id)
    final_data['cases'] = done_cases[:target_count]
    final_data['total_nums'] = len(final_data['cases'])
    final_data['generation_plan'] = plan
    final_data['question_type_counts'] = _question_type_counts(final_data.get('cases', []))
    final_data['generation_stats'] = stats
    cases = final_data.get('cases', [])
    if not cases:
        raise DatasetGenerationEmptyError(f'dataset generation produced no cases for {eval_name}')
    if target_count and len(cases) < target_count:
        raise DatasetGenerationEmptyError(
            f'dataset generation produced {len(cases)}/{target_count} valid cases for {eval_name}'
        )
    _save_dataset_attempt(attempt_dir, eval_name, kb_id, plan, stats, final_data['cases'])
    path = dataset_dir / 'eval_data.json'
    path.parent.mkdir(parents=True, exist_ok=True)
    atomic_write_json(path, final_data)
    _log.info('dataset_gen finished %s cases -> %s', len(final_data.get('cases', [])), path)
    return (str(path), final_data)


def _cases_from_items(items: list[dict], eval_name: str, kb_id: str) -> list[dict]:
    return build_full_eval_set(items, eval_name=eval_name, kb_id=kb_id).get('cases', [])


def _save_dataset_attempt(attempt_dir, eval_name: str, kb_id: str, plan: dict, stats: dict, cases: list[dict]) -> None:
    data = build_full_eval_set([], eval_name=eval_name, kb_id=kb_id)
    data.update({'cases': cases, 'total_nums': len(cases), 'generation_plan': plan, 'generation_stats': stats})
    data['question_type_counts'] = _question_type_counts(cases)
    save_attempt(attempt_dir, data)


def _remaining_plan(plan: dict[str, int], cases: list[dict]) -> dict[str, int]:
    counts = _question_type_counts(cases)
    return {kind: max(0, plan[kind] - counts.get(kind, 0)) for kind in _TYPE_ORDER}


def _generation_plan(num_cases: int | None, settings: dict) -> dict[str, int]:
    if num_cases is None:
        return {k: int((settings.get(k) or {}).get('num', 0)) for k in _TYPE_ORDER}
    total = num_cases
    if total <= 0:
        total = 40
    base, rem = divmod(total, len(_TYPE_ORDER))
    plan = {k: base for k in _TYPE_ORDER}
    for k in _TYPE_ORDER[:rem]:
        plan[k] += 1
    return plan


def _question_type_counts(cases: list[dict]) -> dict[str, int]:
    out = {k: 0 for k in _TYPE_ORDER}
    by_code = {
        code: kind
        for kind, codes in _TYPE_TO_QUESTION_TYPE.items()
        for code in (codes if isinstance(codes, tuple) else (codes,))
    }
    for case in cases:
        key = by_code.get(case.get('question_type'), 'unknown')
        out[key] = out.get(key, 0) + 1
    return out


def _check_cancel(cancel) -> None:
    if cancel and cancel():
        raise StopRequested(at_step='case')


def _get_docs_or_raise(dataset_source: KBClient, kb_id: str, algo_id: str) -> list[dict]:
    docs = dataset_source.get_doc_list(kb_id, algo_id)
    if docs:
        return docs
    hint = ''
    if ',' in kb_id and kb_id.split(',', 1)[0].startswith(('http://', 'https://')):
        hint = (
            ' URL_MAP document_url datasets are not enumerable through /v1/docs; '
            'use a local ds_* kb_id or add a remote enumeration adapter.'
        )
    raise KBDocsEmptyError(f'生成评测集失败，因为知识库是空的。kb_id={kb_id} algo_id={algo_id}.{hint}')


def run_eval(dataset_id: str, target_chat_url: str, *, cfg: EvoConfig, llm_factory=None, max_workers: int = 10,
             rag_max_workers: int | None = None, judge_max_workers: int | None = None, dataset_name: str = '',
             filters: dict[str, Any] | None = None, require_trace: bool = True,
             model_config: dict[str, Any] | None = None, persist_report: bool = True, attempt_id: str | None = None,
             resume: bool = True, cancel=None, on_progress=None, on_judge_progress=None,
             ) -> dict[str, Any]:
    _log.info('start eval dataset_id=%s target=%s', dataset_id, target_chat_url)
    rag_workers = max(1, int(rag_max_workers or max_workers))
    judge_workers = max(1, int(judge_max_workers or max_workers))
    attempt_dir, prev = start_attempt(cfg.storage.base_dir / 'datasets' / dataset_id, 'eval_attempts', attempt_id)
    done = (
        [row for row in prev.get('case_details') or [] if row.get('case_id') and 'error' not in row]
        if resume else []
    )
    queued = [row for row in prev.get('eval_queue') or [] if row.get('case_id')] if resume else []
    meta: dict[str, Any] = {'eval_name': dataset_id, 'eval_set_id': '', 'kb_id': ''}

    def save_eval_attempt() -> None:
        report = build_eval_report(done, meta)
        report['eval_queue'] = [row for row in queued if row.get('case_id') not in {x.get('case_id') for x in done}]
        save_attempt(attempt_dir, report, indent=None)

    eval_data = get_eval_queue(
        dataset_id,
        dataset_name=dataset_name,
        base_dir=cfg.storage.base_dir,
        target_chat_url=target_chat_url,
        max_workers=rag_workers,
        filters=filters or {},
        require_trace=require_trace,
        model_config=model_config,
        skip_case_ids={row.get('case_id') for row in done + queued},
        on_item=lambda item: (queued.append(item), save_eval_attempt()),
        cancel=cancel,
        on_progress=_offset_progress(on_progress, _case_count(done + queued)),
    )
    _check_cancel(cancel)
    meta.update(eval_data)
    save_eval_attempt()
    eval_queue = [row for row in _unique_by_case(queued) if row.get('case_id') not in {x.get('case_id') for x in done}]
    if not eval_queue and not done:
        raise RuntimeError(f'eval dataset {dataset_id} completed no cases; retry will resume remaining cases')
    create_evaluate_task(
        eval_queue,
        llm_factory=llm_factory,
        max_workers=judge_workers,
        on_item=lambda item: (done.append(item), save_eval_attempt()),
        cancel=cancel,
        on_progress=_offset_progress(on_judge_progress, _case_count(done), int(eval_data.get('total_cases') or 0)),
    )
    _check_cancel(cancel)
    done = _unique_by_case(done)
    report = build_eval_report(done, eval_data)
    save_attempt(attempt_dir, report)
    total = int(eval_data.get('total_cases') or len(done))
    if len(done) < total:
        raise RuntimeError(f'eval finished {len(done)}/{total} cases; retry will resume remaining cases')
    if persist_report:
        path = save_eval_report(dataset_id, report, cfg.storage.base_dir)
        _log.info('eval %s done -> %s', dataset_id, path)
    else:
        _log.info('eval %s done', dataset_id)
    return report


def _unique_by_case(rows: list[dict]) -> list[dict]:
    seen = set()
    out = []
    for row in rows:
        case_id = row.get('case_id')
        if not case_id or case_id in seen:
            continue
        seen.add(case_id)
        out.append(row)
    return out


def _case_count(rows: list[dict]) -> int:
    return len(_unique_by_case(rows))


def _offset_progress(callback, offset: int, total: int = 0):
    if callback is None:
        return None

    def report(current: int, remaining_total: int) -> None:
        full_total = total or offset + remaining_total
        callback(min(offset + current, full_total), full_total)

    return report
