from __future__ import annotations
import logging
import os
from dataclasses import replace
from typing import Any
from lazyllm import AutoModel
from evo.datagen import run_eval, load_report, fetch_traces_for_report
from evo.harness.plan import StopRequested
from evo.runtime.fs import atomic_write_json
from evo.runtime.model_gateway import ModelGateway
from evo.runtime.config import (
    EVO_EVAL_JUDGE_MAX_RETRIES,
    EVO_EVAL_JUDGE_MAX_WORKERS,
    EVO_EVAL_JUDGE_TIMEOUT_S,
    EVO_EVAL_MAX_WORKERS,
    EVO_EVAL_RAG_MAX_WORKERS,
)
from evo.runtime.model_config import require_thread_model_config, thread_model_config, wrap_model_call
from evo.service.core import store as _store
from evo.service.threads.workspace import EventLog, ThreadWorkspace
from .context import CancelToken, ExecCtx

log = logging.getLogger('evo.service.executors.eval')


def execute(ctx: ExecCtx, tid: str) -> None:
    cur = _store.get(ctx.store, tid)
    if cur is None:
        return
    if cur['status'] == 'queued':
        ctx.report_start(tid)
    thread_id = cur.get('thread_id')
    if not thread_id:
        ctx.on_failure(tid, _store.StateError('EVAL_NO_THREAD', 'eval flow requires a thread_id'))
        return
    payload = cur.get('payload') or {}
    eval_id = payload.get('eval_id')
    dataset_id = payload.get('dataset_id')
    target_chat_url = ctx.cfg.eval_run.target_chat_url
    eval_options = payload.get('eval_options') or payload.get('options') or {}
    ws = ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id)
    filters = dict(eval_options.get('filters') or {})
    elog = EventLog(ws.events_path)
    token = CancelToken(ctx, tid)
    model_config = (
        require_thread_model_config(ctx.cfg.storage.base_dir, thread_id, ctx.cfg.model_config.llm_role)
        if dataset_id
        else thread_model_config(ctx.cfg.storage.base_dir, thread_id)
    )
    try:
        if dataset_id:
            elog.append_event(
                'eval.start', task_id=tid, payload={'dataset_id': dataset_id, 'target_chat_url': target_chat_url}
            )
            report = run_eval(
                dataset_id=dataset_id,
                target_chat_url=target_chat_url,
                cfg=ctx.cfg,
                llm_factory=_eval_judge_llm_factory(ctx, model_config=model_config, session_id=f'evo:{tid}'),
                max_workers=_eval_max_workers(payload),
                rag_max_workers=_eval_phase_workers(payload, 'rag_max_workers', EVO_EVAL_RAG_MAX_WORKERS),
                judge_max_workers=_eval_phase_workers(payload, 'judge_max_workers', EVO_EVAL_JUDGE_MAX_WORKERS),
                dataset_name=eval_options.get('dataset_name', ''),
                filters=filters,
                require_trace=_trace_enabled(),
                model_config=model_config,
                persist_report=False,
                attempt_id=tid,
                resume=bool(payload.get('resume', True)),
                cancel=token.requested,
                on_progress=lambda current, total: elog.append_event(
                    'eval.progress',
                    task_id=tid,
                    payload={'phase': 'rag', 'current': current, 'total': total, 'dataset_id': dataset_id},
                ),
                on_judge_progress=lambda current, total: elog.append_event(
                    'eval.progress',
                    task_id=tid,
                    payload={'phase': 'judge', 'current': current, 'total': total, 'dataset_id': dataset_id},
                ),
            )
            upstream_id = report.get('report_id')
            eval_id = upstream_id or eval_id or tid
            if not upstream_id:
                log.warning('eval %s upstream report_id missing, using %s', tid, eval_id)
            report['report_id'] = eval_id
        else:
            if not eval_id:
                raise _store.StateError('EVAL_NO_TARGET', 'need eval_id or dataset_id')
            elog.append_event('eval.start', task_id=tid, payload={'eval_id': eval_id})
            report = _load_existing_report(ws, eval_id, ctx.cfg.storage.base_dir)
        atomic_write_json(ws.eval_path(eval_id), report)
        ctx.update_payload(tid, {'eval_id': eval_id})
        ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id).attach_artifact('eval_ids', eval_id)
        traces = _fetch_traces(tid, elog, report, token) if _trace_enabled() else {}
        if token.requested():
            elog.append_event('eval.cancel', task_id=tid, payload={'eval_id': eval_id})
            ctx.on_stop(tid, 'fetch_traces')
            return
        atomic_write_json(ws.trace_bundle_path(eval_id), traces)
        elog.append_event(
            'eval.finish',
            task_id=tid,
            payload={'eval_id': eval_id, 'cases': report.get('total_cases'), 'traces': len(traces)},
        )
        ctx.on_success(tid)
    except StopRequested as exc:
        if token.cancel_requested():
            elog.append_event('eval.cancel', task_id=tid, payload={'eval_id': eval_id, 'dataset_id': dataset_id})
        ctx.on_stop(tid, exc.at_step)
    except Exception as exc:
        if token.stop_requested():
            ctx.on_stop(tid, 'case')
            return
        if token.cancel_requested():
            elog.append_event('eval.cancel', task_id=tid, payload={'eval_id': eval_id, 'dataset_id': dataset_id})
        ctx.on_failure(tid, exc)
    finally:
        ctx.pop_thread(tid)


def _fetch_traces(tid: str, elog: EventLog, report: dict, token: CancelToken) -> dict[str, Any]:
    if token.requested():
        return {}
    return fetch_traces_for_report(report, max_workers=8)


def _trace_enabled() -> bool:
    return os.getenv('LAZYLLM_TRACE_ENABLED', '1').strip().lower() not in {'0', 'false', 'no', 'off'}


def _load_existing_report(ws: ThreadWorkspace, eval_id: str, base_dir) -> dict[str, Any]:
    path = ws.eval_path(eval_id)
    if path.is_file():
        import json

        return json.loads(path.read_text(encoding='utf-8'))
    return load_report(eval_id, base_dir)


def _eval_judge_llm_factory(ctx: ExecCtx, *, model_config=None, session_id: str = 'evo:eval'):
    cfg = replace(ctx.cfg.llm, producer_timeout_s=EVO_EVAL_JUDGE_TIMEOUT_S, max_retries=EVO_EVAL_JUDGE_MAX_RETRIES)
    gateway: ModelGateway[str] = ModelGateway(
        cfg, name='evo-eval-judge-llm', logger=logging.getLogger('evo.datagen.evaluate')
    )
    client = AutoModel(model=ctx.cfg.model_config.llm_role)

    return lambda: (
        lambda prompt: gateway.call(
            wrap_model_call(lambda: client(prompt), model_config, session_id=session_id),
            cache_key=prompt,
            agent='eval_judge',
        )
    )


def _eval_max_workers(payload: dict[str, Any]) -> int:
    raw = (payload.get('eval_options') or payload.get('options') or {}).get('max_workers')
    if raw is None:
        raw = EVO_EVAL_MAX_WORKERS
    return max(1, int(raw))


def _eval_phase_workers(payload: dict[str, Any], key: str, default: int) -> int:
    options = payload.get('eval_options') or payload.get('options') or {}
    return max(1, int(options.get(key) or options.get('max_workers') or default))
