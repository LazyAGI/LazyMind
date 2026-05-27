from __future__ import annotations
import json
import threading
import time
import urllib.request
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable
from evo.chat_runner import ChatInstance, ChatRegistry, ChatRunner
from evo.datagen import run_eval, load_report, fetch_traces_for_report
from evo.harness.plan import StopRequested
from evo.runtime.config import EVO_EVAL_JUDGE_MAX_WORKERS, EVO_EVAL_MAX_WORKERS, EVO_EVAL_RAG_MAX_WORKERS
from evo.runtime.fs import atomic_write as _atomic_write
from evo.runtime.fs import atomic_write_json
from evo.service.threads.workspace import EventLog, ThreadWorkspace
from .comparator import VerdictPolicy, compare_evals, judge_verdict

PHASES: tuple[str, ...] = ('launch_chat', 'run_eval', 'compare', 'persist')
REUSE_HEALTH_TIMEOUT_S = 10


@dataclass
class AbtestInputs:
    abtest_id: str
    thread_id: str
    apply_id: str
    baseline_eval_id: str
    dataset_id: str
    apply_worktree: Path
    candidate_chat_id: str | None = None
    target_chat_url: str | None = None
    eval_options: dict = field(default_factory=dict)
    policy: VerdictPolicy = field(default_factory=VerdictPolicy)
    judge_label: str = 'ab'
    candidate_env: dict[str, str] = field(default_factory=dict)
    model_config: dict[str, Any] | None = None


@dataclass
class AbtestResult:
    status: str
    verdict: str | None
    summary: dict | None
    candidate_chat_id: str | None
    new_eval_id: str | None
    error: str | None = None


def execute_abtest(
    *,
    inputs: AbtestInputs,
    workspace: ThreadWorkspace,
    log: EventLog,
    chat_runner: ChatRunner,
    chat_registry: ChatRegistry,
    cfg,
    llm_factory=None,
    cancel: Callable[[], bool] = lambda: False,
) -> AbtestResult:
    state_path = workspace.abtest_dir(inputs.abtest_id) / 'phase.json'
    state = _load_state(state_path)
    state.setdefault('completed', [])
    state.setdefault('candidate_chat_id', inputs.candidate_chat_id)
    state.setdefault('candidate_chat_url', inputs.target_chat_url)
    state.setdefault('new_eval_id', None)
    _clear_failed_summary(state)
    candidate: ChatInstance | None = None
    if state['candidate_chat_id']:
        candidate = chat_registry.get(state['candidate_chat_id'])
    ctx = _Ctx(inputs, workspace, log, chat_runner, chat_registry, cfg, llm_factory, state, candidate, cancel)
    log.append_event(
        'abtest.start',
        task_id=inputs.abtest_id,
        payload={
            'abtest_id': inputs.abtest_id,
            'apply_id': inputs.apply_id,
            'baseline_eval_id': inputs.baseline_eval_id,
            'dataset_id': inputs.dataset_id,
            'candidate_chat_id': inputs.candidate_chat_id,
            'target_chat_url': inputs.target_chat_url,
        },
    )
    try:
        for phase in PHASES:
            if cancel():
                return _cancelled_result(ctx, state_path)
            if _phase_done(ctx, phase):
                continue
            if phase in state['completed']:
                state['completed'].remove(phase)
            _PHASES_FN[phase](ctx)
            state['completed'].append(phase)
            _save_state(state_path, state)
    except StopRequested:
        return _cancelled_result(ctx, state_path)
    except Exception as exc:
        state['summary'] = _invalid_summary(exc)
        try:
            _phase_persist(ctx)
            _save_state(state_path, state)
        finally:
            if ctx.candidate is not None:
                _retire_candidate(ctx.candidate, chat_runner, chat_registry)
        log.append_event(
            'abtest.finish',
            task_id=inputs.abtest_id,
            payload={
                'abtest_id': inputs.abtest_id,
                'verdict': 'invalid',
                'candidate_chat_id': state.get('candidate_chat_id'),
                'candidate_chat_url': state.get('candidate_chat_url'),
                'baseline_eval_id': inputs.baseline_eval_id,
                'new_eval_id': state.get('new_eval_id'),
                'summary_path': str(workspace.abtest_dir(inputs.abtest_id) / 'summary.json'),
            },
        )
        return AbtestResult(
            'failed_transient',
            'invalid',
            state.get('summary'),
            state.get('candidate_chat_id'),
            state.get('new_eval_id'),
            str(exc),
        )
    summary = state.get('summary') or {}
    verdict = summary.get('verdict')
    if verdict == 'improved':
        pass
    elif verdict == 'regressed':
        if ctx.candidate is not None:
            _retire_candidate(ctx.candidate, chat_runner, chat_registry)
    elif verdict == 'invalid':
        if ctx.candidate is not None:
            _retire_candidate(ctx.candidate, chat_runner, chat_registry)
    elif verdict == 'inconclusive':
        pass
    log.append_event(
        'abtest.finish',
        task_id=inputs.abtest_id,
        payload={
            'abtest_id': inputs.abtest_id,
            'verdict': verdict,
            'candidate_chat_id': state.get('candidate_chat_id'),
            'candidate_chat_url': state.get('candidate_chat_url'),
            'baseline_eval_id': inputs.baseline_eval_id,
            'new_eval_id': state.get('new_eval_id'),
            'summary_path': str(workspace.abtest_dir(inputs.abtest_id) / 'summary.json'),
        },
    )
    return AbtestResult('succeeded', verdict, summary, state['candidate_chat_id'], state['new_eval_id'])


@dataclass
class _Ctx:
    inputs: AbtestInputs
    ws: ThreadWorkspace
    log: EventLog
    runner: ChatRunner
    registry: ChatRegistry
    cfg: Any
    llm_factory: Any
    state: dict
    candidate: ChatInstance | None
    cancel: Callable[[], bool]


def _cancelled_result(c: _Ctx, state_path: Path) -> AbtestResult:
    if c.candidate is not None:
        _retire_candidate(c.candidate, c.runner, c.registry)
    c.log.append_event(
        'abtest.finish',
        task_id=c.inputs.abtest_id,
        payload={
            'abtest_id': c.inputs.abtest_id,
            'status': 'cancelled',
            'candidate_chat_id': c.state.get('candidate_chat_id'),
            'new_eval_id': c.state.get('new_eval_id'),
        },
    )
    _save_state(state_path, c.state)
    return AbtestResult('cancelled', None, None, c.state.get('candidate_chat_id'), c.state.get('new_eval_id'))


def _phase_launch_chat(c: _Ctx) -> None:
    if c.candidate is None or c.candidate.status != 'healthy':
        c.candidate = c.runner.launch(
            source_dir=c.inputs.apply_worktree,
            label=c.inputs.judge_label,
            env=c.inputs.candidate_env,
            owner_thread_id=c.inputs.thread_id,
        )
        c.registry.register(c.candidate)
    c.state['candidate_chat_id'] = c.candidate.chat_id
    c.state['candidate_chat_url'] = f'{c.candidate.base_url}/api/chat/stream'
    _wait_health(c.candidate, timeout_s=60)


def _probe_health(candidate: ChatInstance, timeout_s: float) -> bool:
    try:
        _wait_health(candidate, timeout_s=timeout_s)
        return True
    except Exception:
        candidate.status = 'unhealthy'
        return False


def _phase_done(c: _Ctx, phase: str) -> bool:
    if phase not in c.state['completed']:
        return False
    if phase != 'launch_chat':
        return True
    if c.candidate is None or c.candidate.status != 'healthy':
        return False
    return _probe_health(c.candidate, timeout_s=REUSE_HEALTH_TIMEOUT_S)


def _reset_candidate_checkpoint(state: dict) -> None:
    completed = state.get('completed') or []
    state['completed'] = [p for p in completed if p not in {'launch_chat', 'run_eval'}]
    state['candidate_chat_id'] = None
    state['candidate_chat_url'] = None
    state['new_eval_id'] = None


def _invalid_summary(exc: Exception) -> dict:
    return {
        'verdict': 'invalid',
        'aligned_cases': 0,
        'metrics': {},
        'missing_metrics': [],
        'top_diff_cases': [],
        'reasons': [f'candidate evaluation failed: {exc}'],
        'error': str(exc),
    }


def _clear_failed_summary(state: dict) -> None:
    if (state.get('summary') or {}).get('error'):
        state['completed'] = [p for p in state.get('completed', []) if p not in {'compare', 'persist'}]
        state.pop('summary', None)


def _wait_health(candidate: ChatInstance, timeout_s: float = 60) -> None:
    import time

    if not candidate.health_url:
        time.sleep(2)
        return
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        try:
            req = urllib.request.Request(candidate.health_url, method='GET')
            with urllib.request.urlopen(req, timeout=5) as resp:
                if resp.status == 200:
                    candidate.status = 'healthy'
                    return
        except Exception:
            pass
        time.sleep(1)
    raise RuntimeError(f'candidate chat {candidate.chat_id} health check failed after {timeout_s}s')


def _phase_run_eval(c: _Ctx) -> None:
    if c.candidate is None:
        raise RuntimeError('candidate chat is not available')
    report = run_eval(
        dataset_id=c.inputs.dataset_id,
        target_chat_url=f'{c.candidate.base_url}/api/chat/stream',
        cfg=c.cfg,
        llm_factory=c.llm_factory,
        max_workers=_eval_max_workers(c.inputs.eval_options),
        rag_max_workers=_eval_phase_workers(c.inputs.eval_options, 'rag_max_workers', min(2, EVO_EVAL_RAG_MAX_WORKERS)),
        judge_max_workers=_eval_phase_workers(c.inputs.eval_options, 'judge_max_workers', EVO_EVAL_JUDGE_MAX_WORKERS),
        dataset_name=c.inputs.eval_options.get('dataset_name', ''),
        filters=c.inputs.eval_options.get('filters') or {},
        require_trace=True,
        model_config=c.inputs.model_config,
        persist_report=False,
        attempt_id=c.inputs.abtest_id,
        resume=_has_eval_partial(c),
        cancel=c.cancel,
        on_progress=lambda current, total: c.log.append_event(
            'abtest.progress',
            task_id=c.inputs.abtest_id,
            payload={'current': current, 'total': total, 'dataset_id': c.inputs.dataset_id},
        ),
        on_judge_progress=lambda current, total: c.log.append_event(
            'abtest.judge_progress',
            task_id=c.inputs.abtest_id,
            payload={'current': current, 'total': total, 'dataset_id': c.inputs.dataset_id},
        ),
    )
    eval_id = report.get('report_id') or f'cand-{c.inputs.abtest_id}'
    report['report_id'] = eval_id
    c.state['new_eval_id'] = eval_id
    _atomic_write(c.ws.eval_path(eval_id), json.dumps(report, ensure_ascii=False, indent=2))
    atomic_write_json(c.ws.trace_bundle_path(eval_id), fetch_traces_for_report(report, max_workers=8))


def _eval_max_workers(eval_options: dict[str, Any]) -> int:
    raw = eval_options.get('max_workers')
    if raw is None:
        raw = EVO_EVAL_MAX_WORKERS
    return max(1, int(raw))


def _has_eval_partial(c: _Ctx) -> bool:
    partial_path = (
        c.cfg.storage.base_dir
        / 'datasets'
        / c.inputs.dataset_id
        / 'eval_attempts'
        / c.inputs.abtest_id
        / 'partial.json'
    )
    return partial_path.is_file()


def _eval_phase_workers(eval_options: dict[str, Any], key: str, default: int) -> int:
    return max(1, int(eval_options.get(key) or eval_options.get('max_workers') or default))


def _phase_compare(c: _Ctx) -> None:
    base = _load_baseline_report(c)
    new = json.loads(c.ws.eval_path(c.state['new_eval_id']).read_text(encoding='utf-8'))
    diff = compare_evals(base, new, primary_metric=c.inputs.policy.primary_metric)
    diff.update(judge_verdict(diff, c.inputs.policy))
    c.state['summary'] = diff


def _load_baseline_report(c: _Ctx) -> dict:
    thread_eval = c.ws.eval_path(c.inputs.baseline_eval_id)
    if thread_eval.exists():
        return json.loads(thread_eval.read_text(encoding='utf-8'))
    return load_report(c.inputs.baseline_eval_id, c.cfg.storage.base_dir)


def _phase_persist(c: _Ctx) -> None:
    out_dir = c.ws.abtest_dir(c.inputs.abtest_id)
    summary = c.state.get('summary') or {}
    _atomic_write(out_dir / 'summary.json', json.dumps(summary, ensure_ascii=False, indent=2))
    _atomic_write(out_dir / 'summary.md', _summary_markdown(summary, c.inputs))
    decision = {
        'verdict': summary.get('verdict'),
        'candidate_chat_id': c.state.get('candidate_chat_id'),
        'candidate_chat_url': c.state.get('candidate_chat_url'),
        'baseline_eval_id': c.inputs.baseline_eval_id,
        'new_eval_id': c.state.get('new_eval_id'),
        'dataset_id': c.inputs.dataset_id,
        'apply_id': c.inputs.apply_id,
    }
    _atomic_write(out_dir / 'decision.json', json.dumps(decision, ensure_ascii=False, indent=2))


def _retire_candidate(candidate: ChatInstance, chat_runner: ChatRunner, chat_registry: ChatRegistry) -> None:
    done = threading.Event()

    def _stop() -> None:
        try:
            chat_runner.stop(candidate.chat_id)
        except Exception:
            pass
        finally:
            done.set()

    thread = threading.Thread(target=_stop, daemon=True, name=f'evo-retire-chat-{candidate.chat_id}')
    thread.start()
    thread.join(timeout=15)
    if not done.is_set() and candidate.pid:
        try:
            import os
            import signal

            os.kill(candidate.pid, signal.SIGKILL)
        except OSError:
            pass
    try:
        chat_registry.purge(candidate.chat_id)
    except Exception:
        pass


def _url_port(url: str) -> int:
    from urllib.parse import urlparse

    parsed = urlparse(url)
    if parsed.port:
        return parsed.port
    return 443 if parsed.scheme == 'https' else 80


_PHASES_FN: dict[str, Callable[[_Ctx], None]] = {
    'launch_chat': _phase_launch_chat,
    'run_eval': _phase_run_eval,
    'compare': _phase_compare,
    'persist': _phase_persist,
}


def _load_state(path: Path) -> dict:
    return json.loads(path.read_text(encoding='utf-8')) if path.exists() else {}


def _save_state(path: Path, state: dict) -> None:
    state['_updated_at'] = time.time()
    _atomic_write(path, json.dumps(state, ensure_ascii=False, indent=2, default=str))


def _summary_markdown(summary: dict, inputs: AbtestInputs) -> str:
    if not summary:
        return f'# abtest {inputs.abtest_id}\n\n(no summary)\n'
    lines = [
        f'# abtest {inputs.abtest_id}',
        '',
        f'- baseline: `{inputs.baseline_eval_id}`',
        f'- dataset: `{inputs.dataset_id}`',
        f'- apply: `{inputs.apply_id}`',
        f"- verdict: **{summary.get('verdict')}**",
        f"- aligned cases: {summary.get('aligned_cases')}",
        '',
        '## metrics',
        '',
        '| metric | mean A | mean B | Δmean | win_rate B | sign p |',
        '| --- | --- | --- | --- | --- | --- |',
    ]
    for m, info in (summary.get('metrics') or {}).items():
        lines.append(
            f"| {m} | {info.get('mean_a')} | {info.get('mean_b')} | "
            f"{info.get('delta_mean')} | {info.get('win_rate_b')} | {info.get('sign_p')} |"
        )
    top = summary.get('top_diff_cases') or []
    if top:
        lines += ['', '## top diffs', '', '| case | a | b | Δ |', '| --- | --- | --- | --- |']
        for row in top:
            lines.append(f"| {row['case_key']} | {row['a']} | {row['b']} | {row['delta']} |")
    lines += ['', '## reasons', '']
    for r in summary.get('reasons', []):
        lines.append(f'- {r}')
    return '\n'.join(lines) + '\n'
