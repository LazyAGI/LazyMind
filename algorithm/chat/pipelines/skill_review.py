from __future__ import annotations

from pathlib import Path
from typing import Any

from chat.components.skill_review.cluster import cluster_crafts
from chat.components.skill_review.config import (
    DEFAULT_WORK_DIR,
    STAGE_CANDIDATE,
    STAGE_CLUSTER,
    STAGE_CRAFT,
    STAGE_DECISION,
    STAGE_OUTLINE,
    STAGE_RESULT,
    STAGE_SESSION,
    STAGE_TRAJECTORY,
)
from chat.components.skill_review.craft import build_skill_craft
from chat.components.skill_review.decision import decide_skill_action
from chat.components.skill_review.llm import SkillReviewLLM
from chat.components.skill_review.miner import build_candidate_skill, build_skill_outline
from chat.components.skill_review.schemas import (
    CandidateSkill,
    SessionData,
    SessionMessage,
    SkillReviewBatchResult,
    SkillReviewDecision,
    SkillReviewRequest,
    Trajectory,
    TrajectoryStep,
    UserSkillReviewResult,
)
from chat.components.skill_review.session_reader import read_session
from chat.components.skill_review.trajectory import build_trajectory
from chat.components.skill_review.workspace import SkillReviewWorkspace, stable_hash


def run_skill_review(request: SkillReviewRequest) -> SkillReviewBatchResult:
    if request.llm_config:
        from chat.utils.load_config import inject_model_config

        inject_model_config(request.llm_config)
    work_dir = Path(request.work_dir or DEFAULT_WORK_DIR)
    model_hash = stable_hash(request.llm_config or {})

    raw_sessions = read_session()
    user_sessions = _group_sessions_by_user(raw_sessions)
    llm = SkillReviewLLM()
    user_results: list[UserSkillReviewResult] = []
    all_decisions: list[SkillReviewDecision] = []

    for user_id, sessions in user_sessions.items():
        user_result = _run_user_skill_review(
            user_id=user_id,
            sessions=sessions,
            request=request,
            base_work_dir=work_dir,
            model_hash=model_hash,
            llm=llm,
        )
        user_results.append(user_result)
        all_decisions.extend(user_result.candidates)

    has_failure = any(item.status == 'failed' for item in user_results)
    has_completed = any(item.status == 'completed' for item in user_results)
    status = 'failed' if has_failure else 'completed' if has_completed else 'skipped'
    qualified_user_count = sum(1 for item in user_results if item.qualified)
    return SkillReviewBatchResult(
        review_id=request.session_id or 'all',
        status=status,
        qualified=qualified_user_count > 0,
        user_count=len(user_results),
        qualified_user_count=qualified_user_count,
        candidates=all_decisions,
        users=user_results,
        artifacts={
            'work_dir': str(work_dir),
            'user_dirs': {
                item.user_id: item.artifacts.get('work_dir', '')
                for item in user_results
            },
        },
        error='one or more user skill review runs failed' if has_failure else None,
    )


def _run_user_skill_review(
    *,
    user_id: str,
    sessions: list[SessionData],
    request: SkillReviewRequest,
    base_work_dir: Path,
    model_hash: str,
    llm: SkillReviewLLM,
) -> UserSkillReviewResult:
    input_hash = stable_hash({
        'user_id': user_id,
        'sessions': [session.model_dump() for session in sessions],
        'min_user_turns': request.min_user_turns,
        'min_tool_turns': request.min_tool_turns,
    })
    workspace = SkillReviewWorkspace(
        base_dir=base_work_dir,
        session_id=user_id,
        input_hash=input_hash,
        model_config_hash=model_hash,
        force=request.force,
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
                decisions=[],
                workspace=workspace,
            )
            workspace.write_json(STAGE_RESULT, result)
            return result

        crafts = [
            build_skill_craft(trajectory, llm)
            for trajectory in qualified_trajectories
        ]
        workspace.write_json(STAGE_CRAFT, crafts)

        clusters = cluster_crafts(crafts, llm)
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
        decisions = [
            decide_skill_action(candidate, aggregate_trajectory, llm)
            for candidate in candidates
        ]
        workspace.write_json(STAGE_DECISION, decisions)

        result = _build_user_result(
            user_id=user_id,
            sessions=sessions,
            trajectories=trajectories,
            decisions=decisions,
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


def _group_sessions_by_user(raw_sessions: Any) -> dict[str, list[SessionData]]:
    sessions_by_user: dict[str, list[SessionData]] = {}
    for index, raw in enumerate(raw_sessions or [], start=1):
        session = _normalize_session(raw, index)
        user_id = str(session.metadata.get('user_id') or 'unknown_user')
        sessions_by_user.setdefault(user_id, []).append(session)
    return sessions_by_user


def _normalize_session(raw: Any, index: int) -> SessionData:
    if isinstance(raw, SessionData):
        return raw
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
    return SessionData(
        session_id=session_id,
        source_db='read_session',
        tables=[],
        messages=messages,
        metadata={
            'user_id': user_id,
            'raw_session': {
                key: value
                for key, value in raw.items()
                if key != 'messages'
            },
        },
    )


def _normalize_message(raw: dict[str, Any]) -> SessionMessage:
    tool_name = raw.get('tool_name') or raw.get('name')
    skill_name = raw.get('skill_name') or raw.get('skill')
    return SessionMessage(
        role=str(raw.get('role') or raw.get('type') or 'unknown'),
        content=str(raw.get('content') or raw.get('result') or ''),
        created_at=_optional_str(raw.get('created_at') or raw.get('timestamp')),
        tool_name=_optional_str(tool_name),
        skill_name=_optional_str(skill_name),
        raw=raw,
    )


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
        qualified=bool(trajectories),
        skip_reason=None if trajectories else 'no qualified sessions',
    )


def _build_user_result(
    *,
    user_id: str,
    sessions: list[SessionData],
    trajectories: list[Trajectory],
    decisions: list[SkillReviewDecision],
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
        candidates=decisions if qualified else [],
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
