from __future__ import annotations

from chat.components.skill_review.schemas import SkillReviewDecision, SkillReviewResult, Trajectory


def build_result(
    *,
    session_id: str,
    trajectory: Trajectory,
    decisions: list[SkillReviewDecision],
    work_dir: str,
    result_file: str,
) -> SkillReviewResult:
    status = 'completed' if trajectory.qualified else 'skipped'
    return SkillReviewResult(
        session_id=session_id,
        status=status,
        qualified=trajectory.qualified,
        trigger={
            'user_turns': trajectory.user_turns,
            'tool_turns': trajectory.tool_turns,
            'skip_reason': trajectory.skip_reason,
        },
        candidates=decisions if trajectory.qualified else [],
        artifacts={
            'work_dir': work_dir,
            'result_file': result_file,
        },
    )
