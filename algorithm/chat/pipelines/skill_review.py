from __future__ import annotations

import tempfile
from datetime import datetime
from pathlib import Path
from typing import Any
from lazyllm import AutoModel, LOG

from chat.components.skill_review.cluster import cluster_drafts
from chat.components.skill_review.config import (
    STAGE_CANDIDATE,
    STAGE_CLUSTER,
    STAGE_DRAFT,
    STAGE_OUTLINE,
    STAGE_RESOLUTION,
    STAGE_RESULT,
    STAGE_SESSION,
    STAGE_TRAJECTORY,
)
from chat.components.skill_review.draft import build_skill_drafts
from chat.components.skill_review.db import insert_skill_review_records, read_session
from chat.components.skill_review.miner import build_candidate_skill, build_skill_outline
from chat.components.skill_review.resolution import resolve_skill_action
from chat.components.skill_review.schemas import (
    CandidateSkill,
    SkillReviewBatchResult,
    SkillReviewResolution,
    SkillReviewRequest,
    Trajectory,
    UserSkillReviewResult,
)
from chat.components.skill_review.trajectory import build_trajectory
from chat.components.skill_review.workspace import SkillReviewWorkspace, stable_hash
from chat.utils.load_config import get_config_path


def run_skill_review(
    request: SkillReviewRequest,
    llm: AutoModel | None = None,
    emb: AutoModel | None = None,
) -> SkillReviewBatchResult:
    llm = llm or AutoModel(model='llm', config=get_config_path())
    emb = emb or AutoModel(model='embed_main', config=get_config_path())
    work_dir = Path(tempfile.mkdtemp(prefix='lazyrag-skill-review-'))
    

    raw_sessions = read_session(request.start_time, request.end_time, request.user_ids)
    user_sessions = _group_sessions_by_user(raw_sessions)
    user_results: list[UserSkillReviewResult] = []
    inserted_count = 0
    LOG.info(f'Found {len(user_sessions)} users')
    
    
    for user_id, sessions in user_sessions.items():
        LOG.info(f'Running skill review for user {user_id} with {len(sessions)} sessions')
        task_id = f"{user_id}_{datetime.now().strftime('%Y%m%d%H%M%S')}"
        user_result = _run_user_skill_review(
            user_id=task_id,
            sessions=sessions,
            request=request,
            base_work_dir=work_dir,
            llm=llm,
            emb=emb,
        )
        user_results.append(user_result)
        state = 'failed' if user_result.status == 'failed' else 'success'
        records = _with_review_metadata(
            user_result.candidates,
            request=request,
            source_user_id=user_id,
            state=state,
        )
        try:
            inserted_count += insert_skill_review_records(records)
        except Exception as exc:
            import traceback
            traceback.print_exc()
            raise exc

    has_failure = any(item.status == 'failed' for item in user_results)
    return SkillReviewBatchResult(
        success=not has_failure,
        inserted_count=inserted_count,
        error='one or more user skill review runs failed' if has_failure else None,
    )


def _with_review_metadata(
    resolutions: list[SkillReviewResolution],
    *,
    request: SkillReviewRequest,
    source_user_id: str,
    state: str,
) -> list[SkillReviewResolution]:
    return [
        item.model_copy(update={
            'state': state,
            'userid': source_user_id,
            'requestid': request.requestid,
        })
        for item in resolutions
    ]


def _run_user_skill_review(
    *,
    user_id: str,
    sessions: list[dict[str, Any]],
    request: SkillReviewRequest,
    base_work_dir: Path,
    llm: AutoModel,
    emb: AutoModel,
) -> UserSkillReviewResult:
    input_hash = stable_hash({
        'user_id': user_id,
        'start_time': request.start_time.isoformat(),
        'end_time': request.end_time.isoformat(),
        'sessions': sessions,
        'min_user_turns': request.min_user_turns,
        'min_tool_turns': request.min_tool_turns,
        'embedding_role': emb.__class__.__name__,
    })
    workspace = SkillReviewWorkspace(
        base_dir=base_work_dir,
        session_id=user_id,
        input_hash=input_hash,
    )

    try:
        workspace.write_json(STAGE_SESSION, sessions)
        LOG.info(f'user {user_id} wrote {len(sessions)} sessions to workspace')

        trajectories = [
            build_trajectory(
                session,
                min_user_turns=request.min_user_turns,
                min_tool_turns=request.min_tool_turns,
            )
            for session in sessions
        ]
        workspace.write_json(STAGE_TRAJECTORY, trajectories)

        qualified_trajectories = [item for item in trajectories if item.qualified]
        LOG.info(f'user {user_id} found {len(qualified_trajectories)} qualified trajectories')
        if not qualified_trajectories:
            result = _build_user_result(
                user_id=user_id,
                sessions=sessions,
                trajectories=trajectories,
                resolutions=[],
                workspace=workspace,
            )
            workspace.write_json(STAGE_RESULT, result)
            return result

        drafts = build_skill_drafts(qualified_trajectories, llm)
        workspace.write_json(STAGE_DRAFT, drafts)

        clusters = cluster_drafts(drafts, emb)
        workspace.write_json(STAGE_CLUSTER, clusters)
        LOG.info(f'user {user_id} found {len(clusters)} clusters')

        outline_pairs = []
        for cluster in clusters:
            outline = build_skill_outline(cluster, llm)
            if outline is not None:
                outline_pairs.append((cluster, outline))
        outlines = [outline for _, outline in outline_pairs]
        workspace.write_json(STAGE_OUTLINE, outlines)

        candidates = [
            build_candidate_skill(cluster, outline, llm)
            for cluster, outline in outline_pairs
        ]
        LOG.info(f'user {user_id} built {len(candidates)} candidates from {len(qualified_trajectories)} qualified_trajectories')
        workspace.write_json(STAGE_CANDIDATE, candidates)
        _write_candidate_skill_files(workspace, candidates)

        resolutions = [
            resolve_skill_action(candidate, llm)
            for candidate in candidates
        ]
        workspace.write_json(STAGE_RESOLUTION, resolutions)

        result = _build_user_result(
            user_id=user_id,
            sessions=sessions,
            trajectories=trajectories,
            resolutions=resolutions,
            workspace=workspace,
        )
        workspace.write_json(STAGE_RESULT, result)
        return result
    except Exception as exc:
        raise exc
        return UserSkillReviewResult(
            user_id=user_id,
            status='failed',
            qualified=False,
            session_count=len(sessions),
            qualified_session_count=0,
            artifacts={'work_dir': str(workspace.path)},
            error=str(exc),
        )


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
    workspace: SkillReviewWorkspace,
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
    qualified = bool(qualified_trajectories)
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
        artifacts={
            'work_dir': str(workspace.path),
            'result_file': str(workspace.stage_path(STAGE_RESULT)),
            'candidate_files': _candidate_skill_paths(workspace),
        },
    )


def _write_candidate_skill_files(
    workspace: SkillReviewWorkspace,
    candidates: list[CandidateSkill],
) -> None:
    skill_dir = workspace.path / 'skills'
    skill_dir.mkdir(parents=True, exist_ok=True)
    for index, candidate in enumerate(candidates, start=1):
        filename = f'{index:02d}_{_safe_filename(candidate.skill_name)}.md'
        path = skill_dir / filename
        tmp = path.with_suffix(path.suffix + '.tmp')
        tmp.write_text(candidate.content, encoding='utf-8')
        tmp.replace(path)


def _candidate_skill_paths(workspace: SkillReviewWorkspace) -> list[str]:
    skill_dir = workspace.path / 'skills'
    if not skill_dir.exists():
        return []
    return [str(path) for path in sorted(skill_dir.glob('*.md'))]


def _safe_filename(value: str) -> str:
    safe = ''.join(ch if ch.isalnum() or ch in ('-', '_', '.') else '_' for ch in value.strip())
    return safe or 'skill'
