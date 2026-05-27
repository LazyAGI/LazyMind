from __future__ import annotations
import json
import os
import shutil
from pathlib import Path
from evo.apply import GitWorkspace, opencode as oc
from evo.apply.errors import classify
from evo.apply.runner import ApplyOptions, RoundResult, execute_apply
from evo.harness.plan import StopRequested
from evo.runtime.fs import load_json
from evo.runtime.model_config import require_thread_model_config
from evo.service.core import store as _store
from evo.service.threads.workspace import EventLog, ThreadWorkspace
from .context import CancelToken, ExecCtx


def execute(ctx: ExecCtx, tid: str) -> None:
    row = _store.get(ctx.store, tid)
    if not row:
        return
    if row['status'] == 'queued':
        ctx.report_start(tid)
    try:
        _run(ctx, tid, row, resume=row['status'] != 'queued')
    except StopRequested as exc:
        ctx.on_stop(tid, exc.at_step)
    except Exception as exc:
        ctx.on_failure(tid, exc)
    finally:
        ctx.pop_thread(tid)
        ctx.pop_procs(tid)


def _run(ctx: ExecCtx, tid: str, row: dict, *, resume: bool) -> None:
    report_id = row['report_id']
    thread_id = row.get('thread_id')
    ws = ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id) if thread_id else None
    elog = EventLog(ws.events_path) if ws else None
    if elog:
        elog.append_event('apply.resume' if resume else 'apply.start', task_id=tid,
                          payload={'apply_id': tid, 'report_id': report_id})
    result = execute_apply(
        apply_id=tid,
        report=load_json(_report_path(ctx, report_id, thread_id)),
        config=ctx.cfg,
        workspace=GitWorkspace(ctx.cfg.storage.git_dir, ctx.cfg.chat_source),
        thread_id=thread_id,
        options=_apply_options(ctx, row),
        cancel_token=CancelToken(ctx, tid),
        on_round_start=lambda rr: _record_round(ctx, tid, rr, phase='running', elog=elog),
        on_round=lambda rr: _record_round(ctx, tid, rr, phase='completed', elog=elog),
        on_proc=lambda proc: ctx.register_proc(tid, proc),
        resume=resume,
    )
    preview_dir = ctx.cfg.storage.applies_dir / tid / 'preview'
    diff_index = preview_dir / tid / 'index.json'
    _store.patch(ctx.store, tid, base_commit=result.base_commit,
                 branch_name=result.branch_name, final_commit=result.final_commit)
    ctx.update_payload(
        tid,
        {
            'result': {
                'base_commit': result.base_commit,
                'branch_name': result.branch_name,
                'final_commit': result.final_commit,
                'preview_dir': str(preview_dir),
                'diff_index': str(diff_index) if diff_index.is_file() else None,
                'round_count': len(result.rounds),
                'status': result.status,
            }
        },
    )
    if result.status == 'SUCCEEDED':
        if elog:
            elog.append_event('apply.finish', task_id=tid, payload={'apply_id': tid, **(result.deployment or {})})
        ctx.report_success(tid)
        if ws and result.deployment and result.deployment.get('candidate_chat_id'):
            ws.attach_artifact('chat_ids', result.deployment['candidate_chat_id'])
        return
    err = result.error or {}
    code = err.get('code', 'UNKNOWN')
    kind = err.get('kind') or classify(code)
    ctx.on_failure(tid, _store.StateError(code, err.get('message') or 'apply failed', kind=kind))


def _record_round(ctx: ExecCtx, tid: str, rr: RoundResult, *, phase: str, elog: EventLog | None = None) -> None:
    if phase == 'running':
        _store.append_round(ctx.store, tid, rr.index, phase='running')
        _store.patch(ctx.store, tid, current_round=rr.index, current_step=f'round_{rr.index:03d}.opencode')
        if elog:
            elog.append_event('apply.round.start', task_id=tid, payload={'apply_id': tid, 'round': rr.index})
        return
    _store.update_round(
        ctx.store,
        tid,
        rr.index,
        phase='completed',
        commit_sha=rr.commit_sha,
        files_changed=rr.files_changed,
        test_passed=int(rr.test_passed) if rr.test_passed is not None else None,
        error_json=json.dumps(rr.error, ensure_ascii=False) if rr.error else None,
        finished_at=rr.finished_at,
    )
    if elog:
        elog.append_event('apply.round.diff', task_id=tid, payload={
            'apply_id': tid,
            'round': rr.index,
            'files_changed': rr.files_changed,
            'commit_sha': rr.commit_sha,
            'test_passed': rr.test_passed,
            'error': rr.error,
            'diff_summary': _round_summary(rr),
        })


def _round_summary(rr: RoundResult) -> str:
    if rr.error:
        return f"round failed: {rr.error.get('code') or 'UNKNOWN'}"
    if rr.test_passed is False:
        return 'tests failed'
    if rr.test_passed is True:
        return 'tests passed'
    return 'tests not run'


def candidate_launch_env(worktree, alias_root=None) -> dict[str, str]:
    env = {k: v for k, v in os.environ.items() if k.startswith(('LAZYMIND_', 'EVO_', 'MAAS_'))}
    if alias_root:
        env['LAZYMIND_EVO_CANDIDATE_CWD'] = str(alias_root)
    env['PYTHONPATH'] = _candidate_pythonpath(worktree, alias_root)
    return env


def _ensure_chat_package_alias(ctx: ExecCtx, apply_id: str, worktree):
    root = ctx.cfg.storage.applies_dir / apply_id / 'chat_alias'
    if root.is_symlink() or root.is_file():
        root.unlink()
    root.mkdir(parents=True, exist_ok=True)
    alias = root / 'chat'
    target = Path(worktree)
    if alias.is_symlink():
        if Path(os.readlink(alias)) == target:
            return root
        alias.unlink()
    elif alias.exists():
        shutil.rmtree(alias, ignore_errors=True)
    elif os.path.lexists(alias):
        alias.unlink()
    alias.symlink_to(target, target_is_directory=True)
    return root


def _candidate_pythonpath(worktree, alias_root=None) -> str:
    paths = []
    if alias_root:
        paths.append(str(alias_root))
    paths.extend(p for p in os.environ.get('PYTHONPATH', '').split(os.pathsep) if p)
    paths.append(str(worktree))
    return os.pathsep.join(dict.fromkeys(paths))


def cleanup(ctx: ExecCtx, tid: str, *, drop_logs: bool, drop_diffs: bool) -> None:
    if drop_logs:
        shutil.rmtree(ctx.cfg.storage.applies_dir / tid, ignore_errors=True)
    if drop_diffs:
        shutil.rmtree(ctx.cfg.storage.diffs_dir / tid, ignore_errors=True)


def resolve_report(ctx: ExecCtx, report_id: str | None, *, thread_id: str | None = None) -> tuple[str, str, dict]:
    if report_id:
        return _read_report(_report_path(ctx, report_id, thread_id))
    run = _latest_run(ctx, thread_id)
    if not run:
        raise _store.StateError('REPORT_NOT_FOUND', 'no succeeded run with report_id')
    rid = (run.get('payload') or {}).get('report_id')
    return _read_report(_report_path(ctx, rid, thread_id))


def resolve_worktree(ctx: ExecCtx, apply_id: str):
    result = ((_store.must_get(ctx.store, apply_id).get('payload') or {}).get('result') or {})
    path = result.get('worktree') or result.get('apply_worktree')
    if path:
        return Path(path)
    return ctx.cfg.storage.git_dir / 'worktrees' / f'apply_{apply_id}'


def _read_report(path: Path) -> tuple[str, str, dict]:
    data = load_json(path)
    return str(data.get('report_id') or path.stem), str(data.get('run_id') or ''), data


def _report_path(ctx: ExecCtx, report_id: str, thread_id: str | None = None) -> Path:
    if thread_id:
        p = ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id).outputs_dir / 'reports' / f'{report_id}.json'
        if p.exists():
            return p
    return ctx.cfg.storage.reports_dir / f'{report_id}.json'


def _latest_run(ctx: ExecCtx, thread_id: str | None) -> dict | None:
    rows = _store.list_flow_tasks_by_thread(
        ctx.store, 'run', thread_id) if thread_id else _store.list_recent(ctx.store, 'run', 50)
    for row in reversed(rows):
        if row.get('status') == 'succeeded' and (row.get('payload') or {}).get('report_id'):
            return row
    return None


def _apply_options(ctx: ExecCtx, task: dict | None = None) -> ApplyOptions:
    opts = ctx.apply_opts or ApplyOptions()
    extra = ((task or {}).get('payload') or {}).get('extra_instructions')
    if extra:
        opts.extra_instructions = extra
    model_config = require_thread_model_config(
        ctx.cfg.storage.base_dir, (task or {}).get('thread_id'), ctx.cfg.model_config.llm_role
    )
    provider_config = oc.provider_config_from_evo_llm(model_config)
    opts.opencode_options.provider_config = provider_config
    opts.opencode_options.model = f'{provider_config.provider}/{provider_config.model}'
    return opts
