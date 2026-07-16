from __future__ import annotations

from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from tempfile import gettempdir
from typing import Any
import lazyllm
from lazyllm import AutoModel, LOG
from lazyllm.tools.agent.skill_manager import SkillManager

from lazymind.chat.engine.tools.infra.skill_remote_store import SkillRemoteStore
from lazymind.chat.engine.tools.infra.skill_validation import (
    parse_skill_frontmatter,
    validate_skill_content,
)
from lazymind.chat.integrations.remote_fs import RemoteFS
from lazymind.config import config as _cfg
from lazymind.model_config import inject_model_config
from lazymind.review.skill_review.config import DEFAULT_REPORT_DIR_NAME
from lazymind.review.skill_review.cluster import cluster_drafts
from lazymind.review.skill_review.draft import build_skill_drafts
from lazymind.review.skill_review.db import (
    insert_skill_review_run_stats,
    read_session,
)
from lazymind.review.skill_review.miner import build_candidate_skills, build_skill_outlines
from lazymind.review.skill_review.resolution import resolve_skill_actions
from lazymind.review.skill_review.schemas import (
    SkillReviewBatchResult,
    SkillReviewResolution,
    SkillReviewRequest,
    SkillReviewRunStat,
    Trajectory,
    UserSkillReviewResult,
)
from lazymind.review.skill_review.reports import (
    finish_stage_report,
    stage_error,
    start_stage,
    write_json_file,
    write_report_file,
)
from lazymind.review.skill_review.trajectory import build_trajectories

GLOBAL_USER_ID = 'global'
REVIEW_STAGE_DRAFT = 'review_draft'
REVIEW_STAGE_CLUSTER = 'review_cluster'
REVIEW_STAGE_MINER = 'review_miner'
REVIEW_STAGE_SOLUTION = 'review_solution'
REVIEW_STAGE_APPLY = 'review_apply'


@dataclass
class _UserSkillReviewState:
    request: SkillReviewRequest
    taskid: str
    user_id: str
    source_user_id: str
    sessions: list[dict[str, Any]]
    run_started_at: datetime
    base_work_dir: Path
    stage_reports: list[dict[str, Any]] = field(default_factory=list)
    trajectories: list[Trajectory] = field(default_factory=list)
    drafts: list[Any] = field(default_factory=list)
    clusters: list[Any] = field(default_factory=list)
    outlines: list[Any] = field(default_factory=list)
    candidates: list[Any] = field(default_factory=list)
    resolutions: list[SkillReviewResolution] = field(default_factory=list)
    skill_manager: Any = None

    def counts(self) -> dict[str, int]:
        return {
            'draft': len(self.drafts),
            'cluster': len(self.clusters),
            'outline': len(self.outlines),
            'candidate': len(self.candidates),
            'resolution': len(self.resolutions),
        }


def build_skill_review_taskid(requestid: str, timestamp: datetime | None = None) -> str:
    normalized_requestid = str(requestid).strip() or 'skill_review'
    suffix = (timestamp or datetime.now()).strftime('%Y%m%d%H%M%S%f')
    return f'{normalized_requestid}_{suffix}'


def record_skill_review_pending(request: SkillReviewRequest, taskid: str | None = None) -> int:
    record_id = taskid or request.requestid
    now = datetime.now()
    review_user_id = request.user_id or GLOBAL_USER_ID
    return insert_skill_review_run_stats(SkillReviewRunStat(
        id=record_id,
        requestid=request.requestid,
        userid=review_user_id,
        status='pending',
        started_at=now.isoformat(),
        duration_ms=0,
        summary={
            'kind': 'skill_review',
            'requestid': request.requestid,
            'taskid': record_id,
            'userid': review_user_id,
            'status': 'pending',
            'artifact_dir': str(_resolve_artifact_dir(request.artifact_dir, requestid=request.requestid) / record_id),
            'started_at': now.isoformat(),
        },
    ))


def record_skill_review_failed(request: SkillReviewRequest, error: str, taskid: str | None = None) -> int:
    record_id = taskid or request.requestid
    now = datetime.now()
    review_user_id = request.user_id or GLOBAL_USER_ID
    return insert_skill_review_run_stats(SkillReviewRunStat(
        id=record_id,
        requestid=request.requestid,
        userid=review_user_id,
        status='failed',
        started_at=now.isoformat(),
        duration_ms=0,
        summary={
            'kind': 'skill_review',
            'requestid': request.requestid,
            'taskid': record_id,
            'userid': review_user_id,
            'status': 'failed',
            'error': error,
            'artifact_dir': str(_resolve_artifact_dir(request.artifact_dir, requestid=request.requestid) / record_id),
            'started_at': now.isoformat(),
        },
    ))


def run_skill_review(request: SkillReviewRequest, taskid: str | None = None) -> SkillReviewBatchResult:
    with lazyllm.new_session(request.requestid):
        inject_model_config(request.model_configs)
        llm = AutoModel(model='llm')
        emb = AutoModel(model='embed_main')
        return _run_skill_review(request, llm, emb, taskid=taskid)


def _run_skill_review(
    request: SkillReviewRequest,
    llm: AutoModel,
    emb: AutoModel,
    *,
    taskid: str | None = None,
) -> SkillReviewBatchResult:
    run_taskid = taskid or build_skill_review_taskid(request.requestid)
    work_dir = _resolve_artifact_dir(request.artifact_dir, requestid=request.requestid)
    read_user_ids = [request.user_id] if request.user_id else None

    raw_sessions = read_session(request.start_time, request.end_time, read_user_ids)
    if request.user_id:
        user_sessions = _group_sessions_by_user(raw_sessions)
        user_sessions = {
            user_id: sessions
            for user_id, sessions in user_sessions.items()
            if user_id == request.user_id
        }
    else:
        user_sessions = {
            GLOBAL_USER_ID: [
                session
                for session in raw_sessions or []
                if isinstance(session, dict)
            ]
        }
    review_user_id = request.user_id or GLOBAL_USER_ID
    user_sessions.setdefault(review_user_id, [])
    LOG.info(
        f'[SkillReview] Found {len(user_sessions)} users for scope={request.user_id or GLOBAL_USER_ID}'
    )

    sessions = user_sessions.get(review_user_id, [])
    LOG.info(f'[SkillReview] Running skill review for user {review_user_id} with {len(sessions)} sessions')
    task_id = f"{review_user_id}_{datetime.now().strftime('%Y%m%d%H%M%S%f')}"

    user_result, user_stat = _run_user_skill_review(
        user_id=review_user_id,
        source_user_id=review_user_id,
        sessions=sessions,
        request=request,
        taskid=run_taskid,
        base_work_dir=work_dir / task_id,
        llm=llm,
        emb=emb,
    )
    records = _with_review_metadata(
        user_result.candidates,
        request=request,
        source_user_id=review_user_id,
    )
    applied_count = 0
    apply_error: str | None = None
    try:
        _record_skill_review_stage_safely(
            request,
            run_taskid,
            review_user_id,
            REVIEW_STAGE_APPLY,
            user_stat.started_at,
            {
                'resolution_count': len(records),
            },
        )
        with _skill_remote_context(user_id=review_user_id, taskid=request.requestid):
            applied_count, apply_report = _apply_skill_review_records(records, SkillRemoteStore())
        write_json_file(work_dir / task_id / 'skill_review_apply.json', apply_report)
        user_stat.summary['apply'] = apply_report
        if apply_report.get('error_count'):
            apply_error = f'failed to apply {apply_report["error_count"]} skill review records'
        LOG.info(f'[SkillReview] applied skill review records: {applied_count} records')
    except Exception as exc:
        LOG.exception(f'[SkillReview] failed to apply skill review records for user {review_user_id}: {exc}')
        apply_error = str(exc)
        user_stat.summary['apply'] = {
            'status': 'failed',
            'input_count': len(records),
            'output_count': applied_count,
            'error': apply_error,
        }

    if apply_error is not None:
        user_stat.status = 'failed'
        user_stat.summary['status'] = 'failed'
        user_stat.summary['error'] = apply_error

    try:
        insert_skill_review_run_stats([user_stat])
    except Exception as exc:
        LOG.exception(f'[SkillReview] failed to insert skill review run stats: {exc}')

    has_failure = user_result.status == 'failed' or apply_error is not None
    return SkillReviewBatchResult(
        success=not has_failure,
        inserted_count=applied_count,
        taskid=run_taskid,
        error=apply_error or (_batch_failure_message([user_result]) if has_failure else None),
    )


def _with_review_metadata(
    resolutions: list[SkillReviewResolution],
    *,
    request: SkillReviewRequest,
    source_user_id: str,
) -> list[SkillReviewResolution]:
    return [
        item.model_copy(update={
            'userid': source_user_id,
            'requestid': request.requestid,
        })
        for item in resolutions
    ]


def _run_user_skill_review(
    *,
    user_id: str,
    source_user_id: str,
    sessions: list[dict[str, Any]],
    request: SkillReviewRequest,
    taskid: str,
    base_work_dir: Path,
    llm: AutoModel,
    emb: AutoModel,
) -> tuple[UserSkillReviewResult, SkillReviewRunStat]:
    with _skill_manager_context(request, user_id=user_id, taskid=request.requestid) as skill_manager:
        state = _UserSkillReviewState(
            request=request,
            taskid=taskid,
            user_id=user_id,
            source_user_id=source_user_id,
            sessions=sessions,
            run_started_at=datetime.now(),
            base_work_dir=base_work_dir,
            skill_manager=skill_manager,
        )

        try:
            return _run_user_skill_review_with_state(state, llm=llm, emb=emb)
        except Exception as exc:
            state.stage_reports.append(_pipeline_failure_report(user_id, exc))
            LOG.exception(f'user {user_id} skill review failed: {exc}')
            return _abort_user_skill_review(state, str(exc))


def _run_user_skill_review_with_state(
    state: _UserSkillReviewState,
    *,
    llm: AutoModel,
    emb: AutoModel,
) -> tuple[UserSkillReviewResult, SkillReviewRunStat]:
    request = state.request
    base_work_dir = state.base_work_dir
    user_id = state.user_id
    try:
        state.trajectories, trajectory_report = build_trajectories(
            state.sessions,
            min_user_turns=request.min_user_turns,
            min_tool_turns=request.min_tool_turns,
            artifact_dir=base_work_dir,
        )
        state.stage_reports.append(trajectory_report)

        qualified_trajectories = [item for item in state.trajectories if item.qualified]
        LOG.info(f'[SkillReview] user {user_id} found {len(qualified_trajectories)} qualified trajectories')
        if not qualified_trajectories:
            return _complete_user_skill_review(state)

        _record_user_skill_review_stage_safely(
            state,
            REVIEW_STAGE_DRAFT,
            {'qualified_trajectory_count': len(qualified_trajectories)},
        )
        state.drafts, draft_report = build_skill_drafts(
            qualified_trajectories,
            llm,
            artifact_dir=base_work_dir,
        )
        state.stage_reports.append(draft_report)
        if not state.drafts:
            return _complete_user_skill_review(state)

        _record_user_skill_review_stage_safely(
            state,
            REVIEW_STAGE_CLUSTER,
            {'draft_count': len(state.drafts)},
        )
        state.clusters, cluster_report = cluster_drafts(
            state.drafts,
            emb,
            llm=llm,
            artifact_dir=base_work_dir,
        )
        state.stage_reports.append(cluster_report)
        LOG.info(f'[SkillReview] user {user_id} found {len(state.clusters)} clusters')
        if not state.clusters:
            return _fail_user_skill_review(
                state,
                _stage_failure_message(cluster_report, 'cluster stage produced no clusters'),
            )

        _record_user_skill_review_stage_safely(
            state,
            REVIEW_STAGE_MINER,
            {'cluster_count': len(state.clusters)},
        )
        outline_pairs, outline_report = build_skill_outlines(
            state.clusters,
            llm,
            artifact_dir=base_work_dir,
        )
        state.outlines = [outline for _, outline in outline_pairs]
        state.stage_reports.append(outline_report)
        if not outline_pairs:
            return _fail_user_skill_review(state, 'all clusters failed during outline generation')

        state.candidates, candidate_report = build_candidate_skills(
            outline_pairs,
            llm,
            artifact_dir=base_work_dir,
        )
        LOG.info(
            f'[SkillReview] user {user_id} built {len(state.candidates)} candidates '
            f'from {len(qualified_trajectories)} qualified_trajectories',
        )
        state.stage_reports.append(candidate_report)
        if not state.candidates:
            return _fail_user_skill_review(state, 'all outlines failed during candidate generation')

        _record_user_skill_review_stage_safely(
            state,
            REVIEW_STAGE_SOLUTION,
            {'candidate_count': len(state.candidates)},
        )
        state.resolutions, resolution_report = resolve_skill_actions(
            state.candidates,
            llm,
            skill_manager=state.skill_manager,
            artifact_dir=base_work_dir,
        )
        state.stage_reports.append(resolution_report)
        if not state.resolutions:
            return _fail_user_skill_review(
                state,
                _stage_failure_message(resolution_report, 'all candidates failed during resolution'),
            )

        return _complete_user_skill_review(state)
    except Exception as exc:
        state.stage_reports.append(_pipeline_failure_report(user_id, exc))
        LOG.exception(f'user {user_id} skill review failed: {exc}')
        return _abort_user_skill_review(state, str(exc))


@contextmanager
def _skill_manager_context(request: SkillReviewRequest, *, user_id: str, taskid: str):
    skill_fs_url = str(_cfg['skill_fs_url'] or '').strip()
    if not skill_fs_url:
        yield None
        return

    with _skill_remote_context(user_id=user_id, taskid=taskid):
        core_api_url = str(_cfg['core_api_url'] or '').strip()
        yield SkillManager(dir=skill_fs_url, fs=RemoteFS(base_url=core_api_url))


@contextmanager
def _skill_remote_context(*, user_id: str, taskid: str):
    previous_agentic_config = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {
        **(previous_agentic_config if isinstance(previous_agentic_config, dict) else {}),
        'user_id': user_id,
        'task_id': taskid,
        'session_id': taskid,
    }
    try:
        yield
    finally:
        if previous_agentic_config is None:
            lazyllm.globals.pop('agentic_config', None)
        else:
            lazyllm.globals['agentic_config'] = previous_agentic_config


def _record_user_skill_review_stage_safely(
    state: _UserSkillReviewState,
    stage: str,
    extra: dict[str, Any] | None = None,
) -> None:
    _record_skill_review_stage_safely(
        state.request,
        state.taskid,
        state.source_user_id,
        stage,
        state.run_started_at.isoformat(),
        extra,
    )


def _record_skill_review_stage_safely(
    request: SkillReviewRequest,
    taskid: str,
    user_id: str,
    stage: str,
    started_at: str,
    extra: dict[str, Any] | None = None,
) -> None:
    try:
        started = _parse_iso_datetime(started_at) or datetime.now()
        now = datetime.now()
        summary = {
            'kind': 'skill_review',
            'requestid': request.requestid,
            'taskid': taskid,
            'userid': user_id,
            'status': stage,
            'stage': stage,
            'artifact_dir': str(_resolve_artifact_dir(request.artifact_dir, requestid=request.requestid) / taskid),
            'started_at': started_at,
            'updated_at': now.isoformat(),
        }
        if extra:
            summary.update(extra)
        insert_skill_review_run_stats(SkillReviewRunStat(
            id=taskid,
            requestid=request.requestid,
            userid=user_id,
            status=stage,
            started_at=started_at,
            duration_ms=_duration_ms(started, now),
            summary=summary,
        ))
    except Exception as exc:
        LOG.exception(f'[SkillReview] failed to update stage={stage} task={taskid}: {exc}')


def _apply_skill_review_records(
    records: list[SkillReviewResolution],
    store: SkillRemoteStore,
) -> tuple[int, dict[str, Any]]:
    if not records:
        return 0, {
            'status': 'completed',
            'input_count': 0,
            'output_count': 0,
            'error_count': 0,
            'applied': [],
            'errors': [],
        }

    skill_fs_url = str(_cfg['skill_fs_url'] or '').strip()
    if not skill_fs_url:
        raise RuntimeError('skill_fs_url is not configured; cannot apply skill review records')

    applied: list[dict[str, Any]] = []
    errors: list[dict[str, Any]] = []

    for record in records:
        try:
            applied.append(_apply_skill_review_record(record, store))
        except Exception as exc:
            LOG.exception(f'[SkillReview] failed to apply resolution {record.id}: {exc}')
            errors.append(stage_error('apply', record.id, exc))

    status = 'failed' if records and not applied else ('partial' if errors else 'completed')
    report = {
        'status': status,
        'input_count': len(records),
        'output_count': len(applied),
        'error_count': len(errors),
        'applied': applied,
        'errors': errors,
    }
    return len(applied), report


def _apply_skill_review_record(record: SkillReviewResolution, store: SkillRemoteStore) -> dict[str, Any]:
    content_error = validate_skill_content(record.skill_content)
    if content_error:
        raise ValueError(content_error)

    frontmatter, _ = parse_skill_frontmatter(record.skill_content)
    content_name = str(frontmatter.get('name') or '').strip()
    content_category = str(frontmatter.get('category') or '').strip()

    if record.type == 'new':
        category = content_category
        name = content_name or record.skill_name
        if not category:
            raise ValueError(f'category is required to create skill {name!r}')
        result = store.create(category, name, record.skill_content)
        return {
            'id': record.id,
            'type': record.type,
            'name': name,
            'category': category,
            'store_result': result,
        }

    if record.type == 'patch':
        existing_identity = store.resolve_existing_identity(record.skill_name)
        if existing_identity.get('error') and content_category:
            existing_identity = store.resolve_existing_identity(record.skill_name, content_category)
        if existing_identity.get('error'):
            raise ValueError(str(existing_identity['error']))
        old_category = str(existing_identity.get('category') or '').strip()
        old_name = str(existing_identity.get('name') or record.skill_name).strip()
        new_category = content_category or old_category
        new_name = content_name or old_name
        if not new_category:
            raise ValueError(f'category is required to patch skill {record.skill_name!r}')
        if (
            (new_category, new_name) != (old_category, old_name)
            and _skill_package_exists(store, new_category, new_name)
        ):
            raise ValueError(f'cannot rename skill {old_name!r} to existing skill {new_category}/{new_name}')
        if (new_category, new_name) == (old_category, old_name):
            before = store.list_files(old_category, old_name)
            after = dict(before)
            after['SKILL.md'] = record.skill_content
            replace_result = store.replace_files(old_category, old_name, before, after)
            store_result = {'replace': replace_result}
        else:
            create_result = store.create(new_category, new_name, record.skill_content)
            remove_result = store.remove(old_category, old_name)
            store_result = {
                'create': create_result,
                'remove': remove_result,
            }
        return {
            'id': record.id,
            'type': record.type,
            'old_name': old_name,
            'old_category': old_category,
            'name': new_name,
            'category': new_category,
            'store_result': store_result,
        }

    raise ValueError(f'unsupported skill review resolution type {record.type!r}')


def _skill_package_exists(store: SkillRemoteStore, category: str, name: str) -> bool:
    try:
        return bool(store.fs.exists(store.package_dir(category, name)))
    except AttributeError:
        return False


def _complete_user_skill_review(
    state: _UserSkillReviewState,
) -> tuple[UserSkillReviewResult, SkillReviewRunStat]:
    return _finish_user_skill_review(
        state,
        _build_user_result(
            user_id=state.user_id,
            sessions=state.sessions,
            trajectories=state.trajectories,
            resolutions=state.resolutions,
        ),
    )


def _finish_user_skill_review(
    state: _UserSkillReviewState,
    result: UserSkillReviewResult,
) -> tuple[UserSkillReviewResult, SkillReviewRunStat]:
    stage_reports = state.stage_reports
    errors = _stage_errors(stage_reports)
    _write_error_report(state.base_work_dir, errors)
    if result.status == 'failed':
        LOG.error(
            f'user {result.user_id} skill review failed: {result.error or "unknown error"}; '
            f'error_summary={_error_summary(errors)}'
        )
    return result, _build_run_stat(
        request=state.request,
        taskid=state.taskid,
        source_user_id=state.source_user_id,
        result=result,
        run_started_at=state.run_started_at,
        summary=_run_summary(
            result=result,
            stage_reports=stage_reports,
            errors=errors,
            counts=state.counts(),
        ),
    )


def _fail_user_skill_review(
    state: _UserSkillReviewState,
    message: str,
) -> tuple[UserSkillReviewResult, SkillReviewRunStat]:
    return _finish_user_skill_review(
        state,
        UserSkillReviewResult(
            user_id=state.user_id,
            status='failed',
            qualified=any(item.qualified for item in state.trajectories),
            session_count=len(state.sessions),
            qualified_session_count=sum(1 for item in state.trajectories if item.qualified),
            error=message,
        ),
    )


def _abort_user_skill_review(
    state: _UserSkillReviewState,
    message: str,
) -> tuple[UserSkillReviewResult, SkillReviewRunStat]:
    return _finish_user_skill_review(
        state,
        UserSkillReviewResult(
            user_id=state.user_id,
            status='failed',
            qualified=False,
            session_count=len(state.sessions),
            qualified_session_count=0,
            error=message,
        ),
    )


def _build_run_stat(
    *,
    request: SkillReviewRequest,
    taskid: str,
    source_user_id: str,
    result: UserSkillReviewResult,
    run_started_at: datetime,
    summary: dict[str, Any],
) -> SkillReviewRunStat:
    ended_at = datetime.now()
    requestid = request.requestid
    return SkillReviewRunStat(
        id=taskid,
        requestid=requestid,
        userid=source_user_id,
        status=result.status,
        started_at=run_started_at.isoformat(),
        duration_ms=_duration_ms(run_started_at, ended_at),
        summary=summary,
    )


def _run_summary(
    *,
    result: UserSkillReviewResult,
    stage_reports: list[dict[str, Any]],
    errors: list[dict],
    counts: dict[str, int],
) -> dict[str, Any]:
    return {
        'status': result.status,
        'error': result.error,
        'counts': {
            'session': result.session_count,
            'qualified_session': result.qualified_session_count,
            **counts,
        },
        'error_count': len(errors),
        'error_summary': _error_summary(errors),
        'stages': stage_reports,
    }


def _stage_errors(reports: list[dict[str, Any]]) -> list[dict]:
    errors: list[dict] = []
    for report in reports:
        errors.extend(report.get('errors') or [])
    return errors


def _stage_failure_message(report: dict[str, Any], fallback: str) -> str:
    errors = report.get('errors') or []
    if not errors:
        return fallback
    first = errors[0]
    stage = first.get('stage') or report.get('stage') or 'unknown'
    item_id = first.get('item_id') or 'unknown'
    message = first.get('message') or fallback
    return f'{stage} failed for {item_id}: {message}'


def _batch_failure_message(results: list[UserSkillReviewResult]) -> str:
    failed = [item for item in results if item.status == 'failed']
    if not failed:
        return ''
    details = [
        f'{item.user_id}: {item.error or "unknown error"}'
        for item in failed
    ]
    return 'failed user skill review runs: ' + '; '.join(details)


def _duration_ms(started_at: datetime, ended_at: datetime) -> int:
    return max(0, int((ended_at - started_at).total_seconds() * 1000))


def _parse_iso_datetime(value: str) -> datetime | None:
    try:
        return datetime.fromisoformat(value)
    except Exception:
        return None


def _error_summary(errors: list[dict]) -> dict[str, Any]:
    by_stage: dict[str, int] = {}
    by_type: dict[str, int] = {}
    for error in errors:
        stage = str(error.get('stage') or 'unknown')
        error_type = str(error.get('error_type') or 'unknown')
        by_stage[stage] = by_stage.get(stage, 0) + 1
        by_type[error_type] = by_type.get(error_type, 0) + 1
    return {
        'total_errors': len(errors),
        'errors_by_stage': by_stage,
        'errors_by_type': by_type,
    }


def _pipeline_failure_report(user_id: str, exc: Exception) -> dict[str, Any]:
    return finish_stage_report(
        'pipeline',
        start_stage(),
        input_count=1,
        output_count=0,
        errors=[stage_error('pipeline', user_id, exc)],
        status='failed',
    )


def _write_error_report(base_work_dir: Path, errors: list[dict]) -> None:
    if not errors:
        return
    write_report_file(base_work_dir, {
        'generated_at': datetime.now().isoformat(),
        **_error_summary(errors),
        'errors': errors,
    })


def _group_sessions_by_user(raw_sessions: Any) -> dict[str, list[dict[str, Any]]]:
    sessions_by_user: dict[str, list[dict[str, Any]]] = {}
    for raw in raw_sessions or []:
        if not isinstance(raw, dict):
            continue
        user_id = str(raw.get('create_user_id') or 'unknown_user')
        session = raw
        sessions_by_user.setdefault(user_id, []).append(session)
    return sessions_by_user


def _build_user_result(
    *,
    user_id: str,
    sessions: list[dict[str, Any]],
    trajectories: list[Trajectory],
    resolutions: list[SkillReviewResolution],
) -> UserSkillReviewResult:
    qualified_trajectories = [item for item in trajectories if item.qualified]
    skipped = [
        {
            'session_id': item.session_id,
            'user_turns': item.user_turns,
            'tool_turns': item.tool_turns,
        }
        for item in trajectories
        if not item.qualified
    ]
    qualified = bool(qualified_trajectories) or bool(resolutions)
    return UserSkillReviewResult(
        user_id=user_id,
        status='completed' if qualified else 'skipped',
        qualified=qualified,
        session_count=len(sessions),
        qualified_session_count=len(qualified_trajectories),
        trigger={
            'total_user_turns': sum(item.user_turns for item in trajectories),
            'total_tool_turns': sum(item.tool_turns for item in trajectories),
            'skipped_sessions': skipped,
        },
        candidates=resolutions if qualified else [],
    )


def _resolve_artifact_dir(artifact_dir: str | Path | None, *, requestid: str) -> Path:
    if artifact_dir is None or (isinstance(artifact_dir, str) and not artifact_dir.strip()):
        return Path(gettempdir()) / DEFAULT_REPORT_DIR_NAME / requestid
    return Path(artifact_dir)
