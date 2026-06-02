from __future__ import annotations
import logging
from lazyllm import AutoModel
from evo.datagen import run_generate_pipeline
from evo.datagen.kb_client import KBClient
from evo.harness.plan import StopRequested
from evo.runtime.model_config import require_thread_model_config, wrap_model_call
from evo.runtime.model_gateway import ModelGateway
from evo.service.core import store as _store
from evo.service.threads.workspace import EventLog, ThreadWorkspace
from .context import CancelToken, ExecCtx

log = logging.getLogger('evo.service.executors.dataset_gen')


def _resolve_llm_factory(cfg, *, model_config=None, session_id: str = 'evo:dataset_gen'):
    client = AutoModel(model=cfg.model_config.llm_role)
    gateway: ModelGateway[str] = ModelGateway(cfg.llm, name='evo-dataset-gen-llm', logger=log)

    return lambda: (
        lambda prompt: gateway.call(
            wrap_model_call(lambda: client(prompt), model_config, session_id=session_id),
            cache_key=prompt,
            agent='dataset_gen',
        )
    )


def execute(ctx: ExecCtx, tid: str) -> None:
    cur = _store.get(ctx.store, tid)
    if cur is None:
        return
    if cur['status'] == 'queued':
        ctx.report_start(tid)
    thread_id = cur.get('thread_id')
    payload = cur.get('payload') or {}
    kb_id = payload.get('kb_id')
    algo_id = payload.get('algo_id', 'general_algo')
    eval_name = payload.get('eval_name', tid)
    num_cases = payload.get('num_cases')
    resume = bool(payload.get('resume', True))
    if not kb_id:
        ctx.on_failure(tid, _store.StateError('DATASET_NO_KB', '生成评测集失败，因为知识库是空的', kind='permanent'))
        return
    token = CancelToken(ctx, tid)
    elog = EventLog(ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id).events_path) if thread_id else None
    try:
        if elog:
            elog.append_event(
                'dataset_gen.start',
                task_id=tid,
                payload={'dataset_id': eval_name, 'kb_id': kb_id, 'algo_id': algo_id, 'num_cases': num_cases},
            )
        ds = KBClient.from_config(ctx.cfg)
        model_config = require_thread_model_config(ctx.cfg.storage.base_dir, thread_id, ctx.cfg.model_config.llm_role)
        llm_factory = _resolve_llm_factory(
            ctx.cfg,
            model_config=model_config,
            session_id=f'evo:{tid}',
        )
        path, data = run_generate_pipeline(
            kb_id=kb_id,
            algo_id=algo_id,
            eval_name=eval_name,
            dataset_source=ds,
            config=ctx.cfg,
            thread_id=thread_id,
            llm_factory=llm_factory,
            cancel=token.requested,
            num_cases=num_cases,
            attempt_id=tid,
            resume=resume,
            on_progress=lambda current, total: (
                elog.append_event(
                    'dataset_gen.progress',
                    task_id=tid,
                    payload={'current': current, 'total': total, 'dataset_id': eval_name},
                )
                if elog
                else None
            ),
        )
        ctx.update_payload(tid, {'dataset_path': path})
        if thread_id:
            ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id).attach_artifact('dataset_ids', eval_name)
            elog.append_event(
                'dataset_gen.finish',
                task_id=tid,
                payload={'dataset_id': eval_name, 'path': path, 'cases': data.get('total_nums')},
            )
        ctx.on_success(tid)
    except StopRequested as exc:
        if elog and token.cancel_requested():
            elog.append_event('dataset_gen.cancel', task_id=tid, payload={'dataset_id': eval_name})
        ctx.on_stop(tid, exc.at_step)
    except Exception as exc:
        if token.stop_requested():
            ctx.on_stop(tid, 'case')
            return
        if elog and token.cancel_requested():
            elog.append_event('dataset_gen.cancel', task_id=tid, payload={'dataset_id': eval_name})
        log.exception('dataset_gen %s failed: %s', tid, exc)
        ctx.on_failure(tid, exc)
    finally:
        ctx.pop_thread(tid)
