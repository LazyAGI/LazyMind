from __future__ import annotations
import json
import shutil
from pathlib import Path
from evo.harness.plan import StopRequested
from evo.runtime.fs import load_json
from evo.runtime.model_config import require_thread_model_config
from evo.service.core import store as _store
from evo.service.threads.workspace import EventLog, ThreadWorkspace
from .context import CancelToken, ExecCtx


class PipelineFailed(Exception):
    code = 'PIPELINE_FAILED'
    kind = 'permanent'


def execute(ctx: ExecCtx, tid: str) -> None:
    row = _store.get(ctx.store, tid)
    if not row:
        return
    if row['status'] == 'queued':
        ctx.report_start(tid)
    try:
        _run(ctx, tid, row, resume=row['status'] != 'queued')
        ctx.report_success(tid)
    except StopRequested as exc:
        ctx.on_stop(tid, exc.at_step)
    except Exception as exc:
        ctx.on_failure(tid, exc)
    finally:
        ctx.pop_thread(tid)


def _run(ctx: ExecCtx, tid: str, row: dict, *, resume: bool) -> None:
    from evo.harness.pipeline import PipelineOptions, build_standard_plan
    from evo.orchestrator.llm import default_embed_provider, default_llm_provider
    from evo.runtime.session import create_session, session_scope
    payload = row.get('payload') or {}
    thread_id = row.get('thread_id')
    require_thread_model_config(ctx.cfg.storage.base_dir, thread_id, ctx.cfg.model_config.llm_role)
    eval_id = payload.get('eval_id')
    judge_path = _eval_path(ctx, thread_id, eval_id)
    if judge_path is None:
        raise _store.StateError('RUN_EVAL_REPORT_NOT_FOUND', f'eval report not found for eval_id={eval_id!r}')
    _write_feedback(ctx, tid, payload.get('extra_instructions'))
    elog = EventLog(ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id).events_path) if thread_id else None
    if elog:
        elog.append_event('run.resume' if resume else 'run.start', task_id=tid,
                          payload={'run_id': tid, 'eval_id': eval_id})
    if resume:
        _drop_step_checkpoints(ctx, tid)
    session = create_session(
        config=ctx.cfg,
        run_id=tid,
        thread_id=thread_id,
        llm_provider=default_llm_provider(ctx.cfg),
        embed_provider=default_embed_provider(ctx.cfg),
    )
    opts = PipelineOptions(**{k: payload[k] for k in ('badcase_limit', 'score_field') if k in payload})
    plan = build_standard_plan(
        opts,
        logger=session.logger('plan'),
        judge_path=judge_path,
        trace_path=_trace_path(ctx, thread_id, eval_id),
        before_step=_run_progress(ctx, tid, eval_id, elog),
    )
    with session_scope(session):
        result = plan.run(session, cancel_token=CancelToken(ctx, tid))
    if not result.success:
        raise PipelineFailed(f'pipeline failed: {[(o.name, o.error) for o in result.failed]}')
    _finish(ctx, tid, thread_id, eval_id, result, elog)


def _finish(ctx: ExecCtx, tid: str, thread_id: str | None, eval_id: str | None, result, elog: EventLog | None) -> None:
    report_path = (result.get('persist') or {}).get('report')
    if not report_path:
        return
    rid = load_json(report_path).get('report_id') or Path(report_path).stem
    ctx.update_payload(tid, {'report_id': rid})
    if thread_id:
        ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id).attach_artifact('run_ids', tid)
    if elog:
        elog.append_event('run.finish', task_id=tid, payload={
                          'run_id': tid, 'eval_id': eval_id, 'report_id': rid, 'report_path': str(report_path)})


def _run_progress(ctx: ExecCtx, tid: str, eval_id: str | None, elog: EventLog | None):
    def emit(step: str, _step_ctx) -> None:
        _store.patch(ctx.store, tid, current_step=step)
        if elog:
            elog.append_event('run.progress', task_id=tid, payload={
                'run_id': tid, 'eval_id': eval_id, 'step': step, 'status': 'running'})
    return emit


def _drop_step_checkpoints(ctx: ExecCtx, tid: str) -> None:
    # Step checkpoints store return values, not the AnalysisSession side effects.
    # Replaying them on resume can produce empty reports with no loaded cases.
    shutil.rmtree(ctx.cfg.storage.runs_dir / tid / 'steps', ignore_errors=True)


def _write_feedback(ctx: ExecCtx, tid: str, feedback: str | None) -> None:
    if feedback:
        path = ctx.cfg.storage.runs_dir / tid / 'revise_feedback.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps({'feedback': feedback}, ensure_ascii=False, indent=2), encoding='utf-8')


def _eval_path(ctx: ExecCtx, thread_id: str | None, eval_id: str | None):
    if not thread_id or not eval_id:
        return None
    p = ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id).eval_path(eval_id)
    return p if p.exists() else None


def _trace_path(ctx: ExecCtx, thread_id: str | None, eval_id: str | None):
    if not thread_id or not eval_id:
        return None
    p = ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id).trace_bundle_path(eval_id)
    return p if p.exists() else None
