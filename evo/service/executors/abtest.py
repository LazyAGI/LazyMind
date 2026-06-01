from __future__ import annotations
from pathlib import Path
from lazyllm import AutoModel
from evo.abtest import AbtestInputs, VerdictPolicy, execute_abtest
from evo.runtime.model_gateway import ModelGateway
from evo.runtime.model_config import require_thread_model_config, wrap_model_call
from evo.service.core import state as thread_state, store as _store
from evo.service.threads.workspace import EventLog, ThreadWorkspace
from .context import CancelToken, ExecCtx
from algorithm.chat.utils.load_config import get_config_path


def execute(ctx: ExecCtx, tid: str) -> None:
    cur = _store.get(ctx.store, tid)
    if cur is None:
        return
    if cur['status'] == 'queued':
        ctx.report_start(tid)
    thread_id = cur.get('thread_id')
    if not thread_id:
        ctx.on_failure(tid, _store.StateError('ABTEST_NO_THREAD', 'abtest flow requires a thread_id'))
        return
    payload = cur.get('payload') or {}
    ws = ThreadWorkspace(ctx.cfg.storage.base_dir, thread_id)
    elog = EventLog(ws.events_path)
    runner = ctx.chat_runner_factory()
    token = CancelToken(ctx, tid)
    model_config = require_thread_model_config(ctx.cfg.storage.base_dir, thread_id, ctx.cfg.model_config.llm_role)
    policy_data = payload.get('policy') or {}
    if isinstance(policy_data.get('guard_metrics'), list):
        policy_data['guard_metrics'] = tuple(policy_data['guard_metrics'])
    client = AutoModel(model=ctx.cfg.model_config.llm_role, config=get_config_path())
    gateway: ModelGateway[str] = ModelGateway(ctx.cfg.llm, name='evo-abtest-llm')

    inputs = AbtestInputs(
        abtest_id=tid,
        thread_id=thread_id,
        apply_id=payload['apply_id'],
        baseline_eval_id=payload['baseline_eval_id'],
        dataset_id=payload['dataset_id'],
        apply_worktree=Path(payload['apply_worktree']),
        candidate_chat_id=payload.get('candidate_chat_id'),
        target_chat_url=ctx.cfg.eval_run.target_chat_url,
        eval_options=_eval_options(payload, ws),
        policy=ctx.abtest_policy.get(tid) or VerdictPolicy(**policy_data),
        candidate_env=_candidate_env(ctx, payload['apply_id'], Path(payload['apply_worktree'])),
        model_config=model_config,
    )
    try:
        result = execute_abtest(
            inputs=inputs,
            workspace=ws,
            log=elog,
            chat_runner=runner,
            chat_registry=ctx.chat_registry,
            cfg=ctx.cfg,
            llm_factory=lambda: (
                lambda prompt: gateway.call(
                    wrap_model_call(lambda: client(prompt), model_config, session_id=f'evo:{tid}'),
                    cache_key=prompt,
                    agent='abtest',
                )
            ),
            cancel=token.requested,
        )
        ctx.update_payload(
            tid,
            {
                'verdict': result.verdict,
                'candidate_chat_id': result.candidate_chat_id,
                'new_eval_id': result.new_eval_id,
            },
        )
        from evo.runtime.fs import atomic_write_json

        abtest_checkpoint = {
            'abtest_id': tid,
            'status': result.status,
            'verdict': result.verdict,
            'candidate_chat_id': result.candidate_chat_id,
            'new_eval_id': result.new_eval_id,
        }
        atomic_write_json(ws.abtest_dir(tid) / 'checkpoint.json', abtest_checkpoint)
        if result.status == 'succeeded':
            ctx.on_success(tid)
        elif result.status == 'cancelled':
            ctx.on_stop(tid, 'abtest')
        else:
            ctx.on_failure(tid, RuntimeError(result.error or 'abtest failed'))
    except Exception as exc:
        ctx.on_failure(tid, exc)
    finally:
        ctx.pop_thread(tid)
        ctx.abtest_policy.pop(tid, None)


def _candidate_env(ctx: ExecCtx, apply_id: str, worktree: Path) -> dict[str, str]:
    from . import apply as apply_exec

    env = apply_exec.candidate_launch_env(worktree, apply_exec._ensure_chat_package_alias(ctx, apply_id, worktree))
    env['LAZYMIND_MODEL_CONFIG_PATH'] = _candidate_model_config_path(
        ctx, env.get('LAZYMIND_MODEL_CONFIG_PATH', 'dynamic')
    )
    return env


def _candidate_model_config_path(ctx: ExecCtx, raw: str) -> str:
    aliases = {
        'dynamic': 'runtime_models.yaml',
        'online': 'runtime_models.online.yaml',
        'inner': 'runtime_models.inner.yaml',
    }
    name = aliases.get(str(raw or 'dynamic').strip().lower())
    if not name:
        return raw
    from importlib import import_module

    path = Path(import_module('algorithm.config').__file__).resolve().parent / 'common' / name
    if not path.is_file():
        raise RuntimeError(f'candidate model config not found: {path}')
    return str(path)


def _eval_options(payload: dict, ws: ThreadWorkspace) -> dict:
    options = dict(payload.get('eval_options') or {})
    inputs = (thread_state.read_json(ws.thread_meta_path) or {}).get('inputs') or {}
    if inputs.get('dataset_name'):
        options.setdefault('dataset_name', inputs['dataset_name'])
    return options
