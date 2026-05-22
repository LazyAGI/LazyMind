from __future__ import annotations
import json
import logging
import os
import shlex
import shutil
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Literal
from evo.apply import opencode as oc
from evo.apply.errors import ApplyError
from evo.apply.git_workspace import GitWorkspace
from evo.apply.tests import TestOutcome, run_tests
from evo.harness.plan import StopRequested
from evo.runtime.config import EvoConfig
from algorithm.config import config

_NO_CHANGES_FEEDBACK = (
    '上一轮 opencode 未对任何 allowlist 中的文件做出修改。本轮必须实际改动至少一个允许文件，否则任务无法收尾。'
)
log = logging.getLogger('evo.apply')


@dataclass
class RoundResult:
    index: int
    files_changed: list[str] = field(default_factory=list)
    commit_sha: str | None = None
    test_passed: bool | None = None
    deploy_passed: bool | None = None
    error: dict | None = None
    started_at: float = 0.0
    finished_at: float = 0.0


@dataclass
class ApplyResult:
    apply_id: str
    base_commit: str
    branch_name: str
    status: Literal['SUCCEEDED', 'FAILED']
    rounds: list[RoundResult] = field(default_factory=list)
    final_commit: str | None = None
    deployment: dict | None = None
    error: dict | None = None
    diff_index_path: Path | None = None


@dataclass
class ApplyOptions:
    max_rounds: int = 3
    test_command: tuple[str, ...] = field(
        default_factory=lambda: tuple(shlex.split(config['evo_apply_test_command']))
    )
    instruction: str = '根据 report 完成代码修改'
    opencode_options: oc.OpencodeOptions = field(
        default_factory=lambda: oc.OpencodeOptions(
            model=config['evo_code_model'] or None,
            agent=config['evo_code_agent'] or None,
            variant=config['evo_code_variant'] or None,
            timeout_s=int(config['evo_code_timeout_s']),
        )
    )
    deploy_check: Callable[[Path, str | None], dict] | None = None
    extra_instructions: str = ''


def _check(token: Any | None, at: str | None = None) -> None:
    if token is not None and token.requested():
        raise StopRequested(at_step=at)


def _write_checkpoint(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding='utf-8')


def _load_checkpoint(path: Path) -> dict | None:
    if not path.is_file():
        return None
    return json.loads(path.read_text(encoding='utf-8'))


def _build_initial_checkpoint(apply_id: str, base_commit: str, branch_name: str, worktree: str) -> dict:
    return {
        'apply_id': apply_id,
        'status': 'running',
        'base_commit': base_commit,
        'branch_name': branch_name,
        'worktree': str(worktree),
        'next_round': 1,
        'prior_failure': '',
        'rounds': [],
        'final_commit': None,
        'preview_ready': False,
    }


def _filter_actions(report: dict) -> list[dict]:
    actions = report.get('actions', [])
    actions = [] if actions is None else actions
    if not isinstance(actions, list):
        raise ApplyError('REPORT_INVALID', 'report.actions must be a list', {'actual_type': type(actions).__name__})
    ready = [
        _mark_action_risk(a)
        for a in actions
        if isinstance(a, dict) and a.get('code_map_in_scope') and a.get('code_map_target')
    ]
    return sorted(ready, key=lambda a: (_action_score(a), str(a.get('priority') or '')), reverse=True)


def _mark_action_risk(action: dict) -> dict:
    out = dict(action)
    weak = _score(out.get('confidence')) < 0.5 or _score(out.get('validity_score')) < 0.5
    out['apply_risk'] = 'low_confidence' if weak else 'normal'
    out['evidence_status'] = 'weak' if weak else 'usable'
    return out


def _score(value: Any) -> float:
    try:
        return float(value or 0.0)
    except (TypeError, ValueError):
        return 0.0


def _action_score(action: dict) -> float:
    return _score(action.get('confidence')) + _score(action.get('validity_score'))


def _rel_under_chat(key: str, base: Path) -> str:
    p = Path(key.strip())
    if p.is_absolute():
        try:
            return p.resolve().relative_to(base).as_posix()
        except ValueError:
            marker = '/algorithm/chat/'
            raw = p.as_posix()
            if marker in raw:
                return raw.split(marker, 1)[1]
            return raw.lstrip('/')
    return p.as_posix().replace(os.sep, '/')


def _allow_spec(config: EvoConfig) -> tuple[frozenset[str], tuple[str, ...]]:
    base = config.chat_source.resolve()
    files: set[str] = set()
    for k in config.code_access.code_map:
        sk = str(k).strip()
        if not sk or sk.rstrip().endswith('/'):
            continue
        files.add(_rel_under_chat(sk, base))
    roots: list[str] = []
    for r0 in config.code_access.new_file_roots:
        s = str(r0).strip().rstrip('/')
        if s:
            roots.append(_rel_under_chat(s, base))
    ru = list(dict.fromkeys(roots))
    ru.sort(key=lambda x: (-len(x), x))
    return (frozenset(files), tuple(ru))


def _prompt_allow_lines(allow_files: frozenset[str], new_roots: tuple[str, ...]) -> list[str]:
    lines = [f'file: {x}' for x in sorted(allow_files)]
    lines += [f'dir (new files allowed): {x}/' for x in new_roots]
    return lines


def _plan_allow_spec(plan: list[dict], config: EvoConfig) -> tuple[frozenset[str], tuple[str, ...]]:
    allow_files, _ = _allow_spec(config)
    plan_files = sorted({
        str(f).strip()
        for item in plan
        for f in (item.get('files') or [])
        if str(f).strip()
    })
    invalid = sorted(set(plan_files) - allow_files)
    selected = frozenset(f for f in plan_files if f in allow_files)
    if invalid or not selected:
        raise ApplyError(
            'REPORT_ACTIONS_NOT_READY',
            'modification plan targets must be explicit code_map files',
            {'invalid_files': invalid, 'plan_files': plan_files},
        )
    return selected, ()


def _allowlist_violation_context(paths: list[str]) -> str:
    body = '\n'.join((f'- {p}' for p in paths)) if paths else ''
    return ('以下路径不在 allowlist，已回滚；请只改允许范围内的文件：\n' + body).strip()


def _sanitize_path_text(text: str, chat_source: Path) -> str:
    base = str(chat_source.resolve()).rstrip('/')
    return text.replace(base + '/', '')


def _build_modification_plan(actions: list[dict], chat_source: Path) -> list[dict]:
    return [
        {
            'id': str(a.get('id', '')),
            'title': str(a.get('title', '')),
            'rationale': str(a.get('rationale', '')),
            'suggested_changes': _sanitize_path_text(str(a.get('suggested_changes', '')), chat_source),
            'priority': str(a.get('priority', '')),
            'confidence': a.get('confidence'),
            'validity_score': a.get('validity_score'),
            'risk_level': a.get('apply_risk', 'normal'),
            'evidence_status': a.get('evidence_status', 'usable'),
            'verifier_notes': a.get('verifier_notes') or [],
            'contradicting_evidence': a.get('contradicting_evidence') or [],
            'files': [_rel_under_chat(str(a.get('code_map_target', '')), chat_source)],
        }
        for a in actions
    ]


def _build_prompt(
    instruction: str,
    plan: list[dict],
    allow_lines: list[str],
    prior_failure: str,
    extra_instructions: str = '',
) -> str:
    parts: list[str] = [instruction.strip(), '']
    if extra_instructions.strip():
        parts.append('用户补充要求：')
        parts.append(extra_instructions.strip())
        parts.append('')
    parts.append('允许修改的范围（严格遵守，禁止改动其它路径）：')
    parts.extend((f'- {f}' for f in allow_lines))
    parts.append('')
    parts.append(
        '所有文件读写都必须使用上述相对路径；不要访问或修改 worktree 外的绝对路径，也不要修改未列出的依赖实现文件。'
    )
    parts.append('只能修改每个 action.files 明确列出的文件；不要为了通过测试顺手改调用链上的其它模块。')
    parts.append('risk_level=low_confidence 的计划仍需执行，但必须采用最小、保守、可测试的修改；如果证据不足，只修复明确落在 files 中的问题。')
    parts.append('')
    parts.append('修改计划（JSON）：')
    parts.append(json.dumps(plan, ensure_ascii=False, indent=2))
    if prior_failure.strip():
        parts.append('')
        parts.append('上一轮失败上下文（请据此调整本轮修改）：')
        parts.append(prior_failure.strip())
    return '\n'.join(parts) + '\n'


def _failure_context(files_changed: list[str], test_outcome: TestOutcome) -> str:
    parts: list[str] = []
    if files_changed:
        parts.append('## 当前相对 baseline 已修改的文件')
        parts.extend((f'- {f}' for f in files_changed))
        parts.append('')
    tb = test_outcome.traceback_md_path
    if tb and tb.is_file():
        parts.append(tb.read_text(encoding='utf-8').strip())
    return '\n'.join(parts).strip()


def _commit_subj(apply_id: str, thread_id: str | None, round_idx: int) -> str:
    t = thread_id if thread_id else 'unknown'
    return f'evo apply_id={apply_id} thread={t} round={round_idx}'


def _exhausted_error(rounds: list[RoundResult], max_rounds: int) -> dict:
    last = rounds[-1] if rounds else None
    code = (last.error or {}).get('code') if last else None
    if code == 'OPENCODE_NO_CHANGES':
        return {
            'code': 'OPENCODE_NO_CHANGES',
            'kind': 'permanent',
            'message': f'opencode 在 {max_rounds} 轮内均未产生文件变更',
            'details': {'rounds': max_rounds},
        }
    if code == 'APPLY_PATH_OUT_OF_ALLOWLIST':
        det = (last.error or {}).get('details') or {} if last else {}
        return {
            'code': 'APPLY_PATH_OUT_OF_ALLOWLIST',
            'kind': 'transient',
            'message': f'allowlist 越界在 {max_rounds} 轮内未解决',
            'details': dict(det),
        }
    if last and last.error:
        err = dict(last.error)
        err.setdefault('kind', 'transient')
        err.setdefault('details', {})
        return err
    return {
        'code': 'MAX_ROUNDS_EXCEEDED',
        'kind': 'transient',
        'message': f'tests still failing after {max_rounds} round(s)',
        'details': {},
    }


def _opencode_api_error(last_error: dict) -> dict:
    err = last_error.get('error') if isinstance(last_error, dict) else None
    details = err if isinstance(err, dict) else {'raw': last_error}
    data = details.get('data') if isinstance(details, dict) else None
    status = data.get('statusCode') if isinstance(data, dict) else None
    message = data.get('message') if isinstance(data, dict) else None
    return {
        'code': 'OPENCODE_API_ERROR',
        'kind': 'permanent' if status in {401, 403} else 'transient',
        'message': str(message or details.get('name') or 'opencode API error'),
        'details': details,
    }


def _mark_round(
    cp: dict,
    rounds: list[RoundResult],
    rr: RoundResult,
    *,
    error: dict | None = None,
    prior_failure: str = '',
    on_round: Callable[[RoundResult], None] | None = None,
) -> str:
    rr.error = error
    rr.finished_at = time.time()
    rounds.append(rr)
    _append_checkpoint_round(cp, rr)
    if prior_failure:
        cp['prior_failure'] = prior_failure
    if on_round:
        on_round(rr)
    return cp.get('prior_failure', '')


def _retry_error(code: str, message: str, details: dict | None = None) -> dict:
    return {'code': code, 'kind': 'transient', 'message': message, 'details': details or {}}


def execute_apply(
    *,
    apply_id: str,
    report: dict,
    config: EvoConfig,
    workspace: GitWorkspace,
    thread_id: str | None = None,
    options: ApplyOptions | None = None,
    cancel_token: Any | None = None,
    on_round: Callable[[RoundResult], None] | None = None,
    on_round_start: Callable[[RoundResult], None] | None = None,
    on_proc: Callable[[Any], None] | None = None,
    resume: bool = False,
) -> ApplyResult:
    options = options or ApplyOptions()
    apply_dir = config.storage.applies_dir / apply_id
    apply_dir.mkdir(parents=True, exist_ok=True)
    cp_path = apply_dir / 'checkpoint.json'
    if resume:
        cp = _load_checkpoint(cp_path)
        if cp is not None and cp.get('preview_ready'):
            preview_dir = cp.get('preview_dir', '')
            round_data = [RoundResult(**r) for r in cp.get('rounds', [])]
            return ApplyResult(
                apply_id=apply_id,
                base_commit=cp.get('base_commit', ''),
                branch_name=cp.get('branch_name', ''),
                status='SUCCEEDED',
                rounds=round_data,
                final_commit=cp.get('final_commit'),
                diff_index_path=Path(preview_dir) / 'index.json' if preview_dir else None,
            )
        start_round = cp.get('next_round', 1) if cp else 1
        prior_failure = cp.get('prior_failure', '') if cp else ''
        existing_rounds = [RoundResult(**r) for r in cp.get('rounds') or []] if cp else []
        base_commit = cp.get('base_commit') if cp else None
    else:
        start_round = 1
        prior_failure = ''
        existing_rounds = []
        base_commit = None
    actions = _filter_actions(report)
    if not actions:
        workspace.ensure_bare()
        worktree, wt_head = workspace.get_or_create_worktree(apply_id)
        branch = GitWorkspace.branch_name(apply_id)
        cp = _build_initial_checkpoint(apply_id, wt_head, branch, str(worktree))
        cp.update({'status': 'succeeded', 'preview_ready': False, 'no_actions': True})
        _write_checkpoint(cp_path, cp)
        return ApplyResult(
            apply_id=apply_id,
            base_commit=wt_head,
            branch_name=branch,
            status='SUCCEEDED',
        )
    plan = _build_modification_plan(actions, config.chat_source)
    allow_files, new_roots = _plan_allow_spec(plan, config)
    if not allow_files and (not new_roots):
        raise ApplyError('CODE_MAP_EMPTY', 'code_map is empty; nothing modifiable')
    allow_lines = _prompt_allow_lines(allow_files, new_roots)
    binary = oc.preflight(
        options.opencode_options.binary, auth_dir=config.storage.opencode_dir, options=options.opencode_options
    )
    workspace.ensure_bare()
    worktree, wt_head = workspace.get_or_create_worktree(apply_id)
    if base_commit is None:
        base_commit = wt_head
    branch = GitWorkspace.branch_name(apply_id)
    (apply_dir / 'input').mkdir(parents=True, exist_ok=True)
    plan_path = apply_dir / 'input' / 'modification_plan.json'
    if not plan_path.exists():
        plan_path.write_text(json.dumps(plan, ensure_ascii=False, indent=2), encoding='utf-8')
    cp = _build_initial_checkpoint(apply_id, base_commit, branch, str(worktree))
    cp['next_round'] = start_round
    cp['prior_failure'] = prior_failure
    cp['rounds'] = [r.__dict__ for r in existing_rounds]
    _write_checkpoint(cp_path, cp)
    rounds: list[RoundResult] = list(existing_rounds)
    final_status: Literal['SUCCEEDED', 'FAILED'] = 'FAILED'
    final_error: dict | None = None
    final_commit: str | None = None
    deployment: dict | None = None
    session = oc.OpencodeSession(cwd=worktree, binary=binary, options=options.opencode_options, on_proc=on_proc)
    try:
        for i in range(start_round, options.max_rounds + 1):
            _check(cancel_token, at=f'round_{i:03d}.start')
            cp['next_round'] = i
            _write_checkpoint(cp_path, cp)
            round_dir = apply_dir / 'rounds' / f'round_{i:03d}'
            if round_dir.exists():
                shutil.rmtree(round_dir)
            (round_dir / 'input').mkdir(parents=True, exist_ok=True)
            rr = RoundResult(index=i, started_at=time.time())
            prompt = _build_prompt(options.instruction, plan, allow_lines, prior_failure, options.extra_instructions)
            (round_dir / 'input' / 'prompt.txt').write_text(prompt, encoding='utf-8')
            if on_round_start:
                on_round_start(rr)
            try:
                outcome = session.run(prompt, round_dir / 'opencode')
            except ApplyError as exc:
                prior_failure = _mark_round(cp, rounds, rr, error=exc.to_payload(), on_round=on_round)
                _write_checkpoint(cp_path, cp)
                if exc.kind == 'permanent':
                    break
                continue
            if outcome.returncode != 0 or outcome.last_error:
                err = _opencode_api_error(outcome.last_error) if outcome.last_error else _retry_error(
                    'OPENCODE_RUN_FAILED', f'opencode exit={outcome.returncode}', {'last_error': outcome.last_error}
                )
                prior_failure = _mark_round(cp, rounds, rr, error=err, prior_failure=err['message'], on_round=on_round)
                _write_checkpoint(cp_path, cp)
                if err.get('kind') == 'permanent':
                    break
                continue
            _check(cancel_token, at=f'round_{i:03d}.opencode_done')
            sha, oob = workspace.commit_allowlisted(worktree, _commit_subj(
                apply_id, thread_id, i), allow_files, new_roots)
            rr.commit_sha = sha
            if oob is not None:
                err = _retry_error('APPLY_PATH_OUT_OF_ALLOWLIST',
                                   'changes outside allowlist were reverted', {'paths': oob})
                prior_failure = _mark_round(cp, rounds, rr, error=err, prior_failure=_allowlist_violation_context(oob),
                                            on_round=on_round)
                _write_checkpoint(cp_path, cp)
                continue
            if sha is None:
                err = {'code': 'OPENCODE_NO_CHANGES', 'kind': 'permanent',
                       'message': 'opencode 本轮未修改任何允许文件', 'details': {}}
                prior_failure = _mark_round(cp, rounds, rr, error=err, prior_failure=_NO_CHANGES_FEEDBACK,
                                            on_round=on_round)
                _write_checkpoint(cp_path, cp)
                continue
            rr.files_changed = [d.path for d in workspace.diff(worktree, base_commit)]
            _check(cancel_token, at=f'round_{i:03d}.diff_done')
            test_outcome = run_tests(worktree, round_dir / 'tests', command=options.test_command, on_proc=on_proc)
            rr.test_passed = test_outcome.passed
            if not test_outcome.passed:
                prior_failure = _mark_round(
                    cp, rounds, rr, prior_failure=_failure_context(
                        rr.files_changed, test_outcome), on_round=on_round)
                _write_checkpoint(cp_path, cp)
                continue
            try:
                deployment = options.deploy_check(worktree, thread_id) if options.deploy_check else None
                rr.deploy_passed = True
            except Exception as exc:
                rr.deploy_passed = False
                err = _retry_error('APPLY_DEPLOY_FAILED', f'candidate deploy/smoke failed: {exc}')
                prior_failure = _mark_round(cp, rounds, rr, error=err, prior_failure=err['message'], on_round=on_round)
                _write_checkpoint(cp_path, cp)
                continue
            _mark_round(cp, rounds, rr, on_round=on_round)
            final_status = 'SUCCEEDED'
            final_commit = sha
            break
    finally:
        session.close()
    if final_status != 'SUCCEEDED':
        final_error = _exhausted_error(rounds, options.max_rounds)
    diff_index_path: Path | None = None
    preview_dir = apply_dir / 'preview'
    if final_status == 'SUCCEEDED':
        preview_dir.mkdir(parents=True, exist_ok=True)
        from evo.service.diff_map import write_diff_map

        diff_index_path = write_diff_map(
            workspace=workspace, apply_id=apply_id, worktree=worktree, base_commit=base_commit, out_dir=preview_dir
        )
    cp['final_commit'] = final_commit
    cp['preview_ready'] = final_status == 'SUCCEEDED'
    cp['preview_dir'] = str(preview_dir) if final_status == 'SUCCEEDED' else None
    cp['status'] = final_status.lower()
    cp['rounds'] = [r.__dict__ for r in rounds]
    _write_checkpoint(cp_path, cp)
    return ApplyResult(
        apply_id=apply_id,
        base_commit=base_commit,
        branch_name=branch,
        status=final_status,
        rounds=rounds,
        final_commit=final_commit,
        deployment=deployment,
        error=final_error,
        diff_index_path=diff_index_path,
    )


def _append_checkpoint_round(cp: dict, rr: RoundResult) -> None:
    rounds = list(cp.get('rounds') or [])
    rounds.append(rr.__dict__)
    cp['rounds'] = rounds
