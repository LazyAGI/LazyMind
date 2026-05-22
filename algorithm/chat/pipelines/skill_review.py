from __future__ import annotations

import tempfile
from pathlib import Path
from typing import Any
from lazyllm import AutoModel

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
from chat.components.skill_review.draft import build_skill_draft
from chat.components.skill_review.db import insert_skill_review_records, read_session
from chat.components.skill_review.miner import build_candidate_skill, build_skill_outline
from chat.components.skill_review.resolution import resolve_skill_action
from chat.components.skill_review.schemas import (
    CandidateSkill,
    SkillReviewBatchResult,
    SkillReviewResolution,
    SkillReviewRequest,
    Trajectory,
    TrajectoryStep,
    UserSkillReviewResult,
)
from chat.components.skill_review.trajectory import build_trajectory, format_steps_text
from chat.components.skill_review.workspace import SkillReviewWorkspace, stable_hash
from chat.utils.load_config import get_config_path


def run_skill_review(request: SkillReviewRequest) -> SkillReviewBatchResult:
    llm = AutoModel(model='llm', config=get_config_path())
    emb = AutoModel(model='embed_main', config=get_config_path())
    work_dir = Path(tempfile.mkdtemp(prefix='lazyrag-skill-review-'))

    raw_sessions = read_session(request.start_time, request.end_time)
    user_sessions = _group_sessions_by_user(raw_sessions)
    user_results: list[UserSkillReviewResult] = []
    all_resolutions: list[SkillReviewResolution] = []
    
    for user_id, sessions in user_sessions.items():
        user_result = _run_user_skill_review(
            user_id=user_id,
            sessions=sessions,
            request=request,
            base_work_dir=work_dir,
            llm=llm,
            emb=emb,
        )
        user_results.append(user_result)
        all_resolutions.extend(user_result.candidates)

    try:
        inserted_count = insert_skill_review_records(all_resolutions)
    except Exception as exc:
        return SkillReviewBatchResult(success=False, inserted_count=0, error=str(exc))

    has_failure = any(item.status == 'failed' for item in user_results)
    return SkillReviewBatchResult(
        success=not has_failure,
        inserted_count=inserted_count,
        error='one or more user skill review runs failed' if has_failure else None,
    )


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
        force=False,
    )

    try:
        workspace.write_json(STAGE_SESSION, sessions)

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

        drafts = [
            build_skill_draft(trajectory, llm)
            for trajectory in qualified_trajectories
        ]
        workspace.write_json(STAGE_DRAFT, drafts)

        clusters = cluster_drafts(drafts, llm)
        workspace.write_json(STAGE_CLUSTER, clusters)

        outlines = [build_skill_outline(cluster, llm) for cluster in clusters]
        workspace.write_json(STAGE_OUTLINE, outlines)

        candidates = [
            build_candidate_skill(cluster, outline, llm)
            for cluster, outline in zip(clusters, outlines)
        ]
        workspace.write_json(STAGE_CANDIDATE, candidates)
        _write_candidate_skill_files(workspace, candidates)

        aggregate_trajectory = _aggregate_trajectory(
            user_id=user_id,
            trajectories=qualified_trajectories,
        )
        resolutions = [
            resolve_skill_action(candidate, aggregate_trajectory, llm)
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
    for index, raw in enumerate(raw_sessions or [], start=1):
        session = _normalize_session(raw, index)
        user_id = str((session.get('metadata') or {}).get('user_id') or 'unknown_user')
        sessions_by_user.setdefault(user_id, []).append(session)
    return sessions_by_user


def _normalize_session(raw: Any, index: int) -> dict[str, Any]:
    if not isinstance(raw, dict):
        raw = {'messages': [], 'raw': raw}
    session_id = str(
        raw.get('conversation_id')
        or raw.get('session_id')
        or raw.get('id')
        or f'session-{index}'
    )
    user_id = str(
        raw.get('create_user_id')
        or raw.get('user_id')
        or raw.get('uid')
        or 'unknown_user'
    )
    messages = [
        _normalize_message(message)
        for message in raw.get('messages') or []
        if isinstance(message, dict)
    ]
    return {
        'session_id': session_id,
        'source_db': 'read_session',
        'tables': [],
        'messages': messages,
        'metadata': {
            'user_id': user_id,
            'raw_session': {
                key: value
                for key, value in raw.items()
                if key != 'messages'
            },
        },
    }


def _normalize_message(raw: dict[str, Any]) -> dict[str, Any]:
    tool_name = raw.get('tool_name') or raw.get('name')
    skill_name = raw.get('skill_name') or raw.get('skill')
    return {
        'role': str(raw.get('role') or raw.get('type') or 'unknown'),
        'content': str(raw.get('content') or raw.get('result') or ''),
        'created_at': _optional_str(raw.get('created_at') or raw.get('timestamp')),
        'tool_name': _optional_str(tool_name),
        'skill_name': _optional_str(skill_name),
        'raw': raw,
    }


def _aggregate_trajectory(*, user_id: str, trajectories: list[Trajectory]) -> Trajectory:
    steps: list[TrajectoryStep] = []
    called_tools: list[str] = []
    called_skills: list[str] = []
    for trajectory in trajectories:
        called_tools.extend(trajectory.called_tools)
        called_skills.extend(trajectory.called_skills)
        steps.extend(trajectory.steps)
    return Trajectory(
        session_id=user_id,
        user_turns=sum(item.user_turns for item in trajectories),
        tool_turns=sum(item.tool_turns for item in trajectories),
        called_tools=_unique(called_tools),
        called_skills=_unique(called_skills),
        steps=steps,
        steps_text=format_steps_text(steps),
        qualified=bool(trajectories),
        skip_reason=None if trajectories else 'no qualified sessions',
    )


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
            'skip_reason': item.skip_reason,
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


def _optional_str(value: Any) -> str | None:
    if value is None:
        return None
    text = str(value).strip()
    return text or None


def _unique(values: list[str]) -> list[str]:
    seen: set[str] = set()
    result: list[str] = []
    for value in values:
        text = str(value or '').strip()
        if not text or text in seen:
            continue
        seen.add(text)
        result.append(text)
    return result


def _safe_filename(value: str) -> str:
    safe = ''.join(ch if ch.isalnum() or ch in ('-', '_', '.') else '_' for ch in value.strip())
    return safe or 'skill'
