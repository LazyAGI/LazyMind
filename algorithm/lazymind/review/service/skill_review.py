from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from tempfile import gettempdir
from typing import Any
import lazyllm
from lazyllm import AutoModel, LOG

from lazymind.model_config import inject_model_config
from lazymind.review.skill_review.config import DEFAULT_REPORT_DIR_NAME
from lazymind.review.skill_review.cluster import cluster_drafts
from lazymind.review.skill_review.draft import build_skill_drafts
from lazymind.review.skill_review.db import (
    insert_skill_review_records,
    insert_skill_review_run_stats,
    read_skill_review_records_by_ids,
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
    stable_hash,
    stage_error,
    start_stage,
    write_report_file,
)
from lazymind.review.skill_review.trajectory import build_trajectories

GLOBAL_USER_ID = 'global'


@dataclass
class _UserSkillReviewState:
    request: SkillReviewRequest
    user_id: str
    source_user_id: str
    sessions: list[dict[str, Any]]
    pending_records: list[dict[str, Any]]
    run_started_at: datetime
    base_work_dir: Path
    stage_reports: list[dict[str, Any]] = field(default_factory=list)
    trajectories: list[Trajectory] = field(default_factory=list)
    drafts: list[Any] = field(default_factory=list)
    clusters: list[Any] = field(default_factory=list)
    outlines: list[Any] = field(default_factory=list)
    candidates: list[Any] = field(default_factory=list)
    resolutions: list[SkillReviewResolution] = field(default_factory=list)

    def counts(self) -> dict[str, int]:
        return {
            'draft': len(self.drafts),
            'cluster': len(self.clusters),
            'outline': len(self.outlines),
            'candidate': len(self.candidates),
            'resolution': len(self.resolutions),
        }


def run_skill_review(request: SkillReviewRequest) -> SkillReviewBatchResult:
    with lazyllm.new_session(request.requestid):
        inject_model_config(request.model_configs)
        llm = AutoModel(model='llm')
        emb = AutoModel(model='embed_main')
        return _run_skill_review(request, llm, emb)


def _run_skill_review(request: SkillReviewRequest, llm: AutoModel, emb: AutoModel) -> SkillReviewBatchResult:
    work_dir = _resolve_artifact_dir(request.artifact_dir, requestid=request.requestid)
    read_user_ids = [request.user_id] if request.user_id else None

    raw_sessions = read_session(request.start_time, request.end_time, read_user_ids)
    pending_records = read_skill_review_records_by_ids(request.pending_skill_ids)
    if request.user_id:
        user_sessions = _group_sessions_by_user(raw_sessions)
        user_sessions = {
            user_id: sessions
            for user_id, sessions in user_sessions.items()
            if user_id == request.user_id
        }
        pending_records_by_user = _group_pending_records_by_user(pending_records)
        pending_records_by_user = {
            user_id: records
            for user_id, records in pending_records_by_user.items()
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
        pending_records_by_user = {
            GLOBAL_USER_ID: [
                record
                for record in pending_records or []
                if isinstance(record, dict)
            ]
        }
    review_user_id = request.user_id or GLOBAL_USER_ID
    user_sessions.setdefault(review_user_id, [])
    pending_records_by_user.setdefault(review_user_id, [])
    scoped_pending_count = sum(len(records) for records in pending_records_by_user.values())
    LOG.info(
        f'[SkillReview] Found {len(user_sessions)} users and {scoped_pending_count} pending skill review records '
        f'for scope={request.user_id or GLOBAL_USER_ID}'
    )

    sessions = user_sessions.get(review_user_id, [])
    user_pending_records = pending_records_by_user.get(review_user_id, [])
    LOG.info(f'[SkillReview] Running skill review for user {review_user_id} with {len(sessions)} sessions')
    task_id = f"{review_user_id}_{datetime.now().strftime('%Y%m%d%H%M%S%f')}"

    user_result, user_stat = _run_user_skill_review(
        user_id=review_user_id,
        source_user_id=review_user_id,
        sessions=sessions,
        pending_records=user_pending_records,
        request=request,
        base_work_dir=work_dir / task_id,
        llm=llm,
        emb=emb,
    )
    records = _with_review_metadata(
        user_result.candidates,
        request=request,
        source_user_id=review_user_id,
    )
    inserted_count = 0
    insert_error: str | None = None
    try:
        inserted_count = insert_skill_review_records(records)
        LOG.info(f'[SkillReview] inserted skill review records: {inserted_count} records')
    except Exception as exc:
        LOG.exception(f'[SkillReview] failed to insert skill review records for user {review_user_id}: {exc}')
        insert_error = str(exc)

    try:
        insert_skill_review_run_stats([user_stat])
    except Exception as exc:
        LOG.exception(f'[SkillReview] failed to insert skill review run stats: {exc}')

    has_failure = user_result.status == 'failed' or insert_error is not None
    return SkillReviewBatchResult(
        success=not has_failure,
        inserted_count=inserted_count,
        error=insert_error or (_batch_failure_message([user_result]) if has_failure else None),
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
    pending_records: list[dict[str, Any]],
    request: SkillReviewRequest,
    base_work_dir: Path,
    llm: AutoModel,
    emb: AutoModel,
) -> tuple[UserSkillReviewResult, SkillReviewRunStat]:
    state = _UserSkillReviewState(
        request=request,
        user_id=user_id,
        source_user_id=source_user_id,
        sessions=sessions,
        pending_records=pending_records,
        run_started_at=datetime.now(),
        base_work_dir=base_work_dir,
    )

    try:
        state.trajectories, trajectory_report = build_trajectories(
            sessions,
            min_user_turns=request.min_user_turns,
            min_tool_turns=request.min_tool_turns,
            artifact_dir=base_work_dir,
        )
        state.stage_reports.append(trajectory_report)

        qualified_trajectories = [item for item in state.trajectories if item.qualified]
        LOG.info(f'[SkillReview] user {user_id} found {len(qualified_trajectories)} qualified trajectories')
        if not qualified_trajectories and not pending_records:
            return _complete_user_skill_review(state)

        state.drafts, draft_report = build_skill_drafts(
            qualified_trajectories,
            llm,
            pending_records=pending_records,
            artifact_dir=base_work_dir,
        )
        state.stage_reports.append(draft_report)
        if not state.drafts:
            return _complete_user_skill_review(state)

        state.clusters, cluster_report = cluster_drafts(
            state.drafts,
            emb,
            artifact_dir=base_work_dir,
        )
        state.stage_reports.append(cluster_report)
        LOG.info(f'[SkillReview] user {user_id} found {len(state.clusters)} clusters')
        if not state.clusters:
            return _fail_user_skill_review(
                state,
                _stage_failure_message(cluster_report, 'cluster stage produced no clusters'),
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

        state.resolutions, resolution_report = resolve_skill_actions(
            state.candidates,
            llm,
            artifact_dir=base_work_dir,
        )
        state.stage_reports.append(resolution_report)

        return _complete_user_skill_review(state)
    except Exception as exc:
        state.stage_reports.append(_pipeline_failure_report(user_id, exc))
        LOG.exception(f'user {user_id} skill review failed: {exc}')
        return _abort_user_skill_review(state, str(exc))


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
            qualified=any(item.qualified for item in state.trajectories) or bool(state.pending_records),
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
    source_user_id: str,
    result: UserSkillReviewResult,
    run_started_at: datetime,
    summary: dict[str, Any],
) -> SkillReviewRunStat:
    ended_at = datetime.now()
    requestid = request.requestid
    stat_id = stable_hash({
        'requestid': requestid,
        'userid': source_user_id,
    })
    return SkillReviewRunStat(
        id=stat_id,
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


def _group_pending_records_by_user(records: list[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    records_by_user: dict[str, list[dict[str, Any]]] = {}
    for record in records or []:
        if not isinstance(record, dict):
            continue
        user_id = str(record.get('userid') or 'unknown_user')
        records_by_user.setdefault(user_id, []).append(record)
    return records_by_user


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
