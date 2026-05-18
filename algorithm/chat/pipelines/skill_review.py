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
from chat.components.skill_review.result import build_result
from chat.components.skill_review.schemas import (
    CandidateSkill,
    SessionData,
    SkillDraft,
    SkillOutline,
    SkillReviewDecision,
    SkillReviewRequest,
    SkillReviewResult,
    TaskCluster,
    Trajectory,
)
from chat.components.skill_review.session_reader import read_session
from chat.components.skill_review.trajectory import build_trajectory
from chat.components.skill_review.workspace import SkillReviewWorkspace, stable_hash


def run_skill_review(request: SkillReviewRequest) -> SkillReviewResult:
    if request.llm_config:
        from chat.utils.load_config import inject_model_config

        inject_model_config(request.llm_config)
    work_dir = Path(request.work_dir or DEFAULT_WORK_DIR)
    input_hash = stable_hash({
        'session_id': request.session_id,
        'session_db_path': request.session_db_path,
        'min_user_turns': request.min_user_turns,
        'min_tool_turns': request.min_tool_turns,
    })
    model_hash = stable_hash(request.llm_config or {})
    workspace = SkillReviewWorkspace(
        base_dir=work_dir,
        session_id=request.session_id,
        input_hash=input_hash,
        model_config_hash=model_hash,
        force=request.force,
    )

    with workspace.lock():
        workspace.init_manifest()
        llm = SkillReviewLLM()
        try:
            session = _stage(
                workspace,
                STAGE_SESSION,
                SessionData,
                request.resume,
                lambda: read_session(request.session_db_path, request.session_id),
            )
            trajectory = _stage(
                workspace,
                STAGE_TRAJECTORY,
                Trajectory,
                request.resume,
                lambda: build_trajectory(
                    session,
                    min_user_turns=request.min_user_turns,
                    min_tool_turns=request.min_tool_turns,
                ),
            )
            if not trajectory.qualified:
                result = build_result(
                    session_id=request.session_id,
                    trajectory=trajectory,
                    decisions=[],
                    work_dir=str(workspace.path),
                    result_file=str(workspace.stage_path(STAGE_RESULT)),
                )
                workspace.write_json(STAGE_RESULT, result)
                workspace.mark_completed()
                return result

            craft = _stage(
                workspace,
                STAGE_CRAFT,
                SkillDraft,
                request.resume,
                lambda: build_skill_craft(trajectory, llm),
            )
            clusters = _stage_list(
                workspace,
                STAGE_CLUSTER,
                TaskCluster,
                request.resume,
                lambda: cluster_crafts([craft], llm),
            )
            outlines = _stage_list(
                workspace,
                STAGE_OUTLINE,
                SkillOutline,
                request.resume,
                lambda: [build_skill_outline(cluster, llm) for cluster in clusters],
            )
            candidates = _stage_list(
                workspace,
                STAGE_CANDIDATE,
                CandidateSkill,
                request.resume,
                lambda: [
                    build_candidate_skill(cluster, outline, llm)
                    for cluster, outline in zip(clusters, outlines)
                ],
            )
            decisions = _stage_list(
                workspace,
                STAGE_DECISION,
                SkillReviewDecision,
                request.resume,
                lambda: [
                    decide_skill_action(candidate, trajectory, llm)
                    for candidate in candidates
                ],
            )
            result = build_result(
                session_id=request.session_id,
                trajectory=trajectory,
                decisions=decisions,
                work_dir=str(workspace.path),
                result_file=str(workspace.stage_path(STAGE_RESULT)),
            )
            workspace.write_json(STAGE_RESULT, result)
            workspace.mark_completed()
            return result
        except Exception as exc:
            workspace.mark_failed(str(exc))
            raise


def _stage(
    workspace: SkillReviewWorkspace,
    stage: str,
    model: type,
    resume: bool,
    producer,
) -> Any:
    if resume and workspace.has_valid_stage(stage, model):
        return workspace.read_model(stage, model)
    workspace.mark_stage_started(stage)
    value = producer()
    workspace.write_json(stage, value)
    workspace.mark_stage_completed(stage)
    return value


def _stage_list(
    workspace: SkillReviewWorkspace,
    stage: str,
    model: type,
    resume: bool,
    producer,
) -> list[Any]:
    if resume and workspace.has_valid_stage(stage):
        data = workspace.read_json(stage)
        if isinstance(data, list):
            return [model.model_validate(item) for item in data]
    workspace.mark_stage_started(stage)
    values = producer()
    workspace.write_json(stage, values)
    workspace.mark_stage_completed(stage)
    return values
