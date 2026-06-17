from __future__ import annotations

import hashlib
import shutil
import subprocess
from pathlib import Path
from typing import Any

from ...artifacts import ArtifactRef
from .schemas import validate_repair_artifact

IGNORED_ROOTS = ('.evo_repair_logs',)
IGNORED_FILES = {'opencode.json'}
KEEP_CURRENT_BRANCH_DECISIONS = {'accept_verified', 'promote_to_best', 'continue_current_branch', 'fix_current_patch'}
RESTORE_BEST_DECISIONS = {'fork_from_best', 'abandon_direction'}


def prepare_physical_attempt(seed_workspace: Path, attempt: int,
                             memory: dict[str, Any] | None = None) -> tuple[Path, dict[str, Any]]:
    root = _physical_root(seed_workspace, memory)
    updated = dict(memory or {})
    updated['physical_root'] = str(root)
    try:
        return _prepare_physical_attempt(seed_workspace, attempt, updated, root)
    except Exception as exc:
        updated['physical_prepare_result'] = {'status': 'failed', 'action': 'prepare_physical_attempt',
                                              'failure': str(exc)[:300]}
        return seed_workspace, updated


def _prepare_physical_attempt(seed_workspace: Path, attempt: int, memory: dict[str, Any],
                              root: Path) -> tuple[Path, dict[str, Any]]:
    original = root / 'baselines' / 'original'
    if not original.exists():
        _copy_workspace(seed_workspace, original, root)
    original_status = _workspace_status(original)
    if not isinstance(memory.get('best_baseline'), dict):
        memory['best_baseline'] = {
            'workspace_ref': str(original), 'baseline_commit': original_status['git_head'],
            'immutable_snapshot_ref': str(original), 'patch_ref': '', 'evaluation_ref': '', 'score': 0.0,
            'metric_snapshot': {}, 'reason': 'physical_original_baseline',
        }
    base_workspace, commit = _physical_attempt_source(memory, original)
    attempt_workspace = root / 'branches' / 'branch_active' / f'attempt_{attempt}'
    _copy_workspace(base_workspace, attempt_workspace, root)
    checkout = _checkout_clean(attempt_workspace, commit) if commit else {
        'status': 'passed', 'action': 'checkout_physical_workspace'
    }
    memory['physical_prepare_result'] = checkout
    return attempt_workspace, memory


def branch_state_before(attempt: int, plan_ref: ArtifactRef, workspace: Path,
                        memory: dict[str, Any] | None = None, *, mode: str = 'record') -> dict[str, Any]:
    status = _workspace_status(workspace)
    best = _memory_best_baseline(memory, workspace, status)
    invariant = prepare_attempt(workspace, status, best)
    physical_prepare = (memory or {}).get('physical_prepare_result') if mode == 'physical' else {}
    if isinstance(physical_prepare, dict) and physical_prepare.get('status') == 'failed':
        invariant = {'status': 'failed', 'action': 'prepare_attempt',
                     'failure': physical_prepare.get('failure', 'physical_prepare_failed'),
                     'physical_prepare_result': physical_prepare}
    record_mode = mode != 'physical'
    payload = {
        'id': f'repair_branch_state_before_attempt_{attempt}',
        'attempt': attempt,
        'repair_loop_plan_ref': str(plan_ref),
        'workspace_ref': str(workspace),
        'status': 'ready' if invariant['status'] == 'passed' else 'failed',
        'phase': 'before',
        'active_branch': {
            'branch_id': 'branch_active', 'workspace_ref': str(workspace), 'base_commit': status['git_head'],
            'workspace_snapshot_ref': f'snapshots/branch_active_before_attempt_{attempt}',
            'working_tree_status': status['working_tree_status'],
            'patch_lineage': list((memory or {}).get('active_patch_lineage') or []),
            'base_kind': 'best_intermediate' if best.get('patch_ref') else 'original',
        },
        'best_baseline': best,
        'workspace_status': status,
        'rejected_branches': list((memory or {}).get('rejected_branches') or []),
        'abandoned_hypotheses': list((memory or {}).get('invalidated_hypotheses') or []),
        'prepare_invariant': invariant,
        'record_mode': record_mode,
        'physical_root_ref': str((memory or {}).get('physical_root') or _physical_root(workspace, memory))
        if not record_mode else '',
    }
    validate_repair_artifact('RepairBranchState', payload)
    return payload


def branch_state_after(attempt: int, plan_ref: ArtifactRef, workspace: Path, before_state: dict[str, Any],
                       branch_decision: dict[str, Any], patch: dict[str, Any], evaluation: dict[str, Any],
                       apply_result: dict[str, Any] | None = None) -> dict[str, Any]:
    state_workspace = _state_workspace(workspace, apply_result)
    status = _workspace_status(state_workspace)
    decision = str(branch_decision.get('decision') or '')
    payload = {
        'id': f'repair_branch_state_after_attempt_{attempt}',
        'attempt': attempt,
        'repair_loop_plan_ref': str(plan_ref),
        'workspace_ref': str(state_workspace),
        'status': _state_status(decision, status),
        'phase': 'after',
        'active_branch': {
            'branch_id': 'branch_active', 'workspace_ref': str(state_workspace),
            'base_commit': _active_base_commit(before_state, decision, status),
            'workspace_snapshot_ref': f'snapshots/branch_active_after_attempt_{attempt}',
            'working_tree_status': status['working_tree_status'],
            'patch_lineage': _patch_lineage(before_state, branch_decision, patch),
            'base_kind': _active_base_kind(before_state, decision),
        },
        'best_baseline': _best_baseline(before_state, branch_decision, patch, evaluation, status, apply_result),
        'workspace_status': status,
        'rejected_branches': _rejected_branches(before_state, branch_decision, patch),
        'abandoned_hypotheses': _abandoned_hypotheses(before_state, branch_decision),
        'record_mode_action': apply_result or {'status': 'not_run'},
        'record_mode': bool(before_state.get('record_mode', True)),
        'physical_root_ref': str(before_state.get('physical_root_ref') or ''),
    }
    validate_repair_artifact('RepairBranchState', payload)
    return payload


def state_transition(attempt: int, before_state: dict[str, Any], branch_decision: dict[str, Any],
                     after_state: dict[str, Any]) -> dict[str, Any]:
    inputs = branch_decision.get('decision_inputs') if isinstance(branch_decision.get('decision_inputs'), dict) else {}
    payload = {
        'id': f'repair_state_transition_attempt_{attempt}',
        'attempt': attempt,
        'state_before_ref': f"{before_state['id']}@v1",
        'decision_ref': f"{branch_decision['id']}@v1",
        'decision_rule_hit': _decision_rule_hit(str(branch_decision.get('decision') or ''), inputs),
        'decision_inputs': inputs,
        'state_after_ref': f"{after_state['id']}@v1",
    }
    validate_repair_artifact('RepairStateTransition', payload)
    return payload


def apply_branch_decision(workspace: Path, before_state: dict[str, Any],
                          branch_decision: dict[str, Any]) -> dict[str, Any]:
    physical = before_state.get('record_mode') is False
    decision = str(branch_decision.get('decision') or '')
    attempt = int(branch_decision.get('attempt') or 0)
    if decision in RESTORE_BEST_DECISIONS:
        checkpoint = checkpoint_current_branch(workspace, attempt, decision, required=False)
        if checkpoint.get('status') == 'failed':
            return checkpoint
        restore = (fork_physical_from_best(workspace, before_state, attempt) if physical
                   else restore_to_best_baseline(workspace, before_state))
        return {**restore, 'candidate_checkpoint': checkpoint}
    if decision == 'stop_failed':
        checkpoint = checkpoint_current_branch(workspace, attempt, decision, required=False)
        if checkpoint.get('status') == 'failed':
            return checkpoint
        status = _workspace_status(workspace)
        return {'status': 'passed', 'action': 'stop_failed', 'decision': decision,
                'checkpoint_status': checkpoint.get('checkpoint_status', 'not_run'),
                'candidate_checkpoint': checkpoint,
                **({'active_workspace_ref': str(workspace)} if physical else {}),
                'before_head': checkpoint.get('before_head') or status['git_head'],
                'after_head': checkpoint.get('after_head') or status['git_head']}
    if decision in KEEP_CURRENT_BRANCH_DECISIONS:
        checkpoint = checkpoint_current_branch(workspace, attempt, decision)
        if not physical or checkpoint.get('status') == 'failed':
            return checkpoint
        if decision in {'promote_to_best', 'accept_verified'}:
            snapshot = snapshot_physical_best(workspace, before_state, attempt)
            if snapshot.get('status') == 'failed':
                return snapshot
            return {**checkpoint, **snapshot, 'active_workspace_ref': str(workspace)}
        return {**checkpoint, 'active_workspace_ref': str(workspace)}
    return {'status': 'failed', 'action': 'physical_decision' if physical else 'record_decision',
            'decision': decision, 'failure': 'unknown_decision'}


def fork_physical_from_best(workspace: Path, before_state: dict[str, Any], attempt: int) -> dict[str, Any]:
    try:
        best = before_state.get('best_baseline') if isinstance(before_state.get('best_baseline'), dict) else {}
        best_workspace = Path(str(best.get('workspace_ref') or ''))
        commit = str(best.get('baseline_commit') or '').strip()
        if not best_workspace.exists() or not commit:
            return {'status': 'failed', 'action': 'physical_fork_from_best', 'target_commit': commit,
                    'failure': 'best_baseline_workspace_missing'}
        root = _physical_root(workspace, {'physical_root': before_state.get('physical_root_ref')})
        target = root / 'branches' / 'branch_active' / f'after_attempt_{attempt}'
        _copy_workspace(best_workspace, target, root)
        checkout = _checkout_clean(target, commit)
        if checkout.get('status') == 'failed':
            return checkout | {'action': 'physical_fork_from_best', 'active_workspace_ref': str(target)}
        status = _workspace_status(target)
        return {'status': 'passed', 'action': 'restore_best_baseline', 'target_commit': commit,
                'active_workspace_ref': str(target), 'before_head': _workspace_status(workspace)['git_head'],
                'after_head': status['git_head'], 'physical_action': 'fork_from_best'}
    except Exception as exc:
        return {'status': 'failed', 'action': 'physical_fork_from_best', 'target_commit': '',
                'failure': str(exc)[:300]}


def snapshot_physical_best(workspace: Path, before_state: dict[str, Any], attempt: int) -> dict[str, Any]:
    try:
        root = _physical_root(workspace, {'physical_root': before_state.get('physical_root_ref')})
        target = root / 'baselines' / f'best_attempt_{attempt}'
        _copy_workspace(workspace, target, root)
        status = _workspace_status(target)
        if status['working_tree_status'] != 'clean':
            return {'status': 'failed', 'action': 'snapshot_physical_best', 'failure': 'best_snapshot_dirty',
                    'best_snapshot_workspace_ref': str(target)}
        return {'status': 'passed', 'action': 'keep_current_branch',
                'best_snapshot_workspace_ref': str(target), 'immutable_snapshot_ref': str(target),
                'best_snapshot_commit': status['git_head'], 'physical_action': 'snapshot_best'}
    except Exception as exc:
        return {'status': 'failed', 'action': 'snapshot_physical_best', 'failure': str(exc)[:300]}


def prepare_attempt(workspace: Path, status: dict[str, Any], best: dict[str, Any]) -> dict[str, Any]:
    if status.get('working_tree_status') != 'clean':
        return {'status': 'failed', 'action': 'prepare_attempt', 'failure': 'workspace_dirty_before_attempt',
                'dirty_files': list(status.get('dirty_files') or []), 'git_head': status.get('git_head', ''),
                'best_baseline_commit': best.get('baseline_commit', '')}
    return {'status': 'passed', 'action': 'prepare_attempt', 'git_head': status.get('git_head', '')}


def checkpoint_current_branch(workspace: Path, attempt: int, decision: str, *, required: bool = True) -> dict[str, Any]:
    before = _workspace_status(workspace)
    changed = list(before.get('dirty_files') or [])

    def fail(failure: str, **extra: Any) -> dict[str, Any]:
        return {'status': 'failed', 'action': 'keep_current_branch', 'decision': decision,
                'checkpoint_status': 'failed', 'before_head': before['git_head'], **extra, 'failure': failure}

    if not changed:
        if required:
            return fail('checkpoint_requires_dirty_patch')
        return {'status': 'passed', 'action': 'keep_current_branch', 'decision': decision,
                'checkpoint_status': 'not_needed', 'before_head': before['git_head'],
                'after_head': before['git_head']}
    add = _git_result(workspace, ['add', '--', *changed])
    if add.returncode:
        return fail((add.stderr or add.stdout).strip()[:300])
    commit = _git_result(workspace, ['-c', 'user.email=evo@example.local', '-c', 'user.name=evo',
                                     'commit', '-m', f'repair attempt {attempt}'])
    if commit.returncode:
        return fail((commit.stderr or commit.stdout).strip()[:300])
    after = _workspace_status(workspace)
    if after['working_tree_status'] != 'clean':
        return fail('workspace_dirty_after_checkpoint', after_head=after['git_head'])
    if after['git_head'] == before['git_head']:
        return fail('checkpoint_commit_missing', after_head=after['git_head'])
    ref = f'refs/evo/repair/candidate/attempt_{attempt}'
    update_ref = _git_result(workspace, ['update-ref', ref, after['git_head']])
    if update_ref.returncode:
        return fail((update_ref.stderr or update_ref.stdout).strip()[:300], after_head=after['git_head'])
    return {'status': 'passed', 'action': 'keep_current_branch', 'decision': decision,
            'checkpoint_status': 'committed', 'before_head': before['git_head'],
            'after_head': after['git_head'], 'checkpoint_ref': ref, 'files_checkpointed': changed}


def restore_to_best_baseline(workspace: Path, before_state: dict[str, Any]) -> dict[str, Any]:
    best = before_state.get('best_baseline') if isinstance(before_state.get('best_baseline'), dict) else {}
    commit = str(best.get('baseline_commit') or '').strip()
    if not commit:
        return {'status': 'failed', 'action': 'restore_best_baseline', 'target_commit': '',
                'failure': 'best_baseline_commit_missing'}
    before_head = _git(workspace, ['rev-parse', '--verify', 'HEAD']).strip()
    _restore_worktree(workspace)
    branch = _git(workspace, ['branch', '--show-current']).strip() or 'evo-repair-active'
    checkout = _git_result(workspace, ['checkout', '-B', branch, commit])
    if checkout.returncode:
        return {'status': 'failed', 'action': 'restore_best_baseline', 'target_commit': commit,
                'before_head': before_head, 'failure': (checkout.stderr or checkout.stdout).strip()[:300]}
    _restore_worktree(workspace)
    status = _workspace_status(workspace)
    if status['working_tree_status'] != 'clean' or status['git_head'] != commit:
        return {'status': 'failed', 'action': 'restore_best_baseline', 'target_commit': commit,
                'before_head': before_head, 'after_head': status['git_head'],
                'failure': 'workspace_not_clean_at_best_commit'}
    return {'status': 'passed', 'action': 'restore_best_baseline', 'target_commit': commit,
            'before_head': before_head, 'after_head': status['git_head']}


def _workspace_status(workspace: Path) -> dict[str, Any]:
    dirty, created, deleted = _git_status(workspace)
    head = _git(workspace, ['rev-parse', '--verify', 'HEAD']).strip()
    return {'git_head': head, 'workspace_hash': _workspace_hash(workspace),
            'working_tree_status': 'clean' if not dirty else 'dirty',
            'dirty_files': dirty, 'untracked_files': created, 'deleted_files': deleted,
            'ignored_dirty_files': [], 'apply_status': 'applied' if dirty else 'not_applied',
            'rollback_status': 'not_needed'}


def _workspace_hash(workspace: Path) -> str:
    digest = hashlib.sha256()
    for path in sorted(workspace.rglob('*')):
        rel = path.relative_to(workspace).as_posix()
        if not path.is_file() or _ignored(rel):
            continue
        digest.update(rel.encode())
        digest.update(path.read_bytes())
    return f'sha256:{digest.hexdigest()}'


def _memory_best_baseline(memory: dict[str, Any] | None, workspace: Path, status: dict[str, Any]) -> dict[str, Any]:
    best = (memory or {}).get('best_baseline') if isinstance((memory or {}).get('best_baseline'), dict) else {}
    if best:
        return {'workspace_ref': str(best.get('workspace_ref') or workspace),
                'baseline_commit': str(best.get('baseline_commit') or status['git_head']),
                'immutable_snapshot_ref': str(best.get('immutable_snapshot_ref') or ''),
                'patch_ref': str(best.get('patch_ref') or ''),
                'evaluation_ref': str(best.get('evaluation_ref') or ''),
                'score': float(best.get('score') or 0.0),
                'metric_snapshot': best.get('metric_snapshot') if isinstance(best.get('metric_snapshot'), dict) else {},
                'reason': str(best.get('reason') or 'memory_best_baseline')}
    return {'workspace_ref': str(workspace), 'baseline_commit': status['git_head'],
            'immutable_snapshot_ref': f'snapshots/best_before_attempt_{status["git_head"][:8] or "unknown"}',
            'patch_ref': '', 'evaluation_ref': '', 'score': 0.0, 'metric_snapshot': {},
            'reason': 'record_mode_current_workspace_baseline'}


def _git_status(workspace: Path) -> tuple[list[str], list[str], list[str]]:
    dirty, created, deleted = [], [], []
    for line in _git(workspace, ['status', '--porcelain', '--untracked-files=all']).splitlines():
        code = line[:2]
        path = line[3:].split(' -> ')[-1] if len(line) >= 4 else ''
        if _ignored(path):
            continue
        dirty.append(path)
        if code == '??' or 'A' in code:
            created.append(path)
        if 'D' in code:
            deleted.append(path)
    return sorted(set(dirty)), sorted(set(created)), sorted(set(deleted))


def _git(workspace: Path, args: list[str]) -> str:
    result = _git_result(workspace, args)
    return result.stdout if result.returncode == 0 else ''


def _git_result(workspace: Path, args: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), *args],
                          capture_output=True, text=True, check=False)


def _ignored(path: str) -> bool:
    parts = set(Path(path).parts)
    return (not path or path in IGNORED_FILES or path.endswith('.pyc') or '__pycache__' in parts
            or any(path == root or path.startswith(f'{root}/') for root in IGNORED_ROOTS) or '.git' in parts)


def _restore_worktree(workspace: Path) -> None:
    dirty, created, _ = _git_status(workspace)
    created_set = set(created)
    tracked = sorted(path for path in dirty if path not in created_set)
    if tracked:
        _git(workspace, ['restore', '--staged', '--worktree', '--', *tracked])
    if created:
        _git(workspace, ['restore', '--staged', '--', *created])
        subprocess.run(['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), 'clean', '-fd', '--',
                        *created], capture_output=True, text=True, check=False)


def _state_status(decision: str, status: dict[str, Any]) -> str:
    if decision == 'stop_failed':
        return 'failed'
    if decision in {'fork_from_best', 'abandon_direction'} and status.get('working_tree_status') == 'clean':
        return 'reset_to_best'
    if status.get('working_tree_status') == 'dirty':
        return 'dirty_explained'
    return 'ready'


def _active_base_commit(before_state: dict[str, Any], decision: str, status: dict[str, Any]) -> str:
    if decision in {'fork_from_best', 'abandon_direction'}:
        return str(((before_state.get('best_baseline') or {}).get('baseline_commit') or status['git_head']))
    if decision in {'promote_to_best', 'accept_verified'}:
        return status['git_head']
    return str(((before_state.get('active_branch') or {}).get('base_commit') or status['git_head']))


def _active_base_kind(before_state: dict[str, Any], decision: str) -> str:
    best = before_state.get('best_baseline') if isinstance(before_state.get('best_baseline'), dict) else {}
    if decision in {'promote_to_best', 'accept_verified'}:
        return 'best_intermediate'
    if decision in {'fork_from_best', 'abandon_direction'} and best.get('patch_ref'):
        return 'best_intermediate'
    return 'original'


def _best_baseline(before_state: dict[str, Any], branch_decision: dict[str, Any], patch: dict[str, Any],
                   evaluation: dict[str, Any], status: dict[str, Any],
                   apply_result: dict[str, Any] | None = None) -> dict[str, Any]:
    previous = before_state.get('best_baseline') if isinstance(before_state.get('best_baseline'), dict) else {}
    if branch_decision.get('decision') not in {'promote_to_best', 'accept_verified'}:
        return previous
    apply_result = apply_result or {}
    overall = (evaluation.get('overall_eval') or {}).get('summary') or {}
    bad = (evaluation.get('badcase_eval') or {}).get('summary') or {}
    return {
        'workspace_ref': str(apply_result.get('best_snapshot_workspace_ref') or patch.get('workspace_ref')
                             or before_state.get('workspace_ref') or ''),
        'baseline_commit': status['git_head'],
        'immutable_snapshot_ref': str(apply_result.get('immutable_snapshot_ref')
                                      or f"snapshots/best_{patch.get('id') or 'attempt'}"),
        'patch_ref': f"{patch.get('id')}@v1" if patch.get('id') else '',
        'evaluation_ref': f"{evaluation.get('id')}@v1" if evaluation.get('id') else '',
        'score': float(overall.get('delta_mean') or bad.get('delta_mean') or 0.0),
        'metric_snapshot': {key: value for key, value in {**bad, **overall}.items() if isinstance(key, str)},
        'reason': str(branch_decision.get('reason') or ''),
    }


def _patch_lineage(before_state: dict[str, Any], branch_decision: dict[str, Any], patch: dict[str, Any]) -> list[str]:
    if branch_decision.get('decision') in {'fork_from_best', 'abandon_direction', 'stop_failed'}:
        return []
    existing = [str(item) for item in ((before_state.get('active_branch') or {}).get('patch_lineage') or [])
                if str(item)]
    patch_ref = f"{patch.get('id')}@v1" if patch.get('id') else ''
    return list(dict.fromkeys([*existing, *([patch_ref] if patch_ref else [])]))


def _rejected_branches(before_state: dict[str, Any], branch_decision: dict[str, Any],
                       patch: dict[str, Any]) -> list[dict[str, Any]]:
    existing = list(before_state.get('rejected_branches') or [])
    if branch_decision.get('decision') in {'fork_from_best', 'abandon_direction', 'stop_failed'}:
        existing.append({'patch_ref': f"{patch.get('id')}@v1" if patch.get('id') else '',
                         'decision_ref': f"{branch_decision.get('id')}@v1",
                         'reason': branch_decision.get('reason', '')})
    return existing


def _abandoned_hypotheses(before_state: dict[str, Any], branch_decision: dict[str, Any]) -> list[str]:
    existing = [str(item) for item in before_state.get('abandoned_hypotheses') or []]
    if branch_decision.get('decision') == 'abandon_direction':
        seed = branch_decision.get('next_instruction_seed')
        seed = seed if isinstance(seed, dict) else {}
        existing += [str(item) for item in seed.get('abandoned_hypothesis_ids') or []]
    return list(dict.fromkeys(item for item in existing if item))


def _state_workspace(workspace: Path, apply_result: dict[str, Any] | None) -> Path:
    if isinstance(apply_result, dict) and apply_result.get('active_workspace_ref'):
        return Path(str(apply_result.get('active_workspace_ref')))
    return workspace


def _physical_attempt_source(memory: dict[str, Any], original: Path) -> tuple[Path, str]:
    active = memory.get('active_branch') if isinstance(memory.get('active_branch'), dict) else {}
    best = memory.get('best_baseline') if isinstance(memory.get('best_baseline'), dict) else {}
    lineage = [str(item) for item in active.get('patch_lineage') or [] if item]
    active_workspace = Path(str(active.get('workspace_ref') or ''))
    best_patch_ref = str(best.get('patch_ref') or '')
    needs_active = bool(lineage and (not best_patch_ref or lineage[-1] != best_patch_ref))
    if needs_active and not active_workspace.exists():
        raise ValueError('active_branch_workspace_missing')
    if needs_active:
        return active_workspace, _workspace_status(active_workspace)['git_head']
    best_workspace = Path(str(best.get('workspace_ref') or original))
    if not best_workspace.exists():
        best_workspace = original
    return best_workspace, str(best.get('baseline_commit') or _workspace_status(best_workspace)['git_head'])


def _physical_root(seed_workspace: Path, memory: dict[str, Any] | None = None) -> Path:
    configured = str((memory or {}).get('physical_root') or '').strip()
    return Path(configured) if configured else seed_workspace.parent / f'{seed_workspace.name}_branches'


def _copy_workspace(source: Path, target: Path, root: Path) -> None:
    source, target, root = source.resolve(), target.resolve(), root.resolve()
    if root == target or root not in target.parents:
        raise ValueError(f'physical copy target outside root: {target}')
    allowed = {root / 'baselines', root / 'branches'}
    if not any(parent == target or parent in target.parents for parent in allowed):
        raise ValueError(f'physical copy target outside baselines/branches: {target}')
    if source == target or source in target.parents or target in source.parents:
        raise ValueError(f'physical copy source/target overlap: {source} -> {target}')
    if target.exists():
        shutil.rmtree(target)
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(source, target, ignore=_copy_ignore)


def _copy_ignore(_root: str, names: list[str]) -> set[str]:
    return {name for name in names if name in {'.evo_repair_logs', '__pycache__'} or name.endswith('.pyc')}


def _checkout_clean(workspace: Path, commit: str) -> dict[str, Any]:
    checkout = _git_result(workspace, ['checkout', '-B', 'evo-repair-active', commit])
    if checkout.returncode:
        return {'status': 'failed', 'action': 'checkout_physical_workspace', 'target_commit': commit,
                'failure': (checkout.stderr or checkout.stdout).strip()[:300]}
    _restore_worktree(workspace)
    status = _workspace_status(workspace)
    if status['working_tree_status'] != 'clean' or status['git_head'] != commit:
        return {'status': 'failed', 'action': 'checkout_physical_workspace', 'target_commit': commit,
                'after_head': status['git_head'], 'failure': 'physical_workspace_not_clean_at_commit'}
    return {'status': 'passed', 'action': 'checkout_physical_workspace', 'target_commit': commit,
            'after_head': status['git_head']}


def _decision_rule_hit(decision: str, inputs: dict[str, Any]) -> str:
    return {'accept_verified': 'all_accept_gates_passed', 'promote_to_best': 'partial_progress_promote',
            'fix_current_patch': 'execution_failure_touched_patch',
            'continue_current_branch': 'same_location_needs_more_patch',
            'stop_failed': 'budget_or_unrecoverable_failure',
            'abandon_direction': 'hypothesis_rejected'}.get(decision, 'fork_from_best')
