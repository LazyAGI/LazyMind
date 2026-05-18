from __future__ import annotations

from chat.components.skill_review.llm import SkillReviewLLM
from chat.components.skill_review.schemas import SkillDraft, TaskCluster
from chat.prompts.skill_review import cluster_prompt


def cluster_crafts(crafts: list[SkillDraft], llm: SkillReviewLLM) -> list[TaskCluster]:
    if not crafts:
        return []
    try:
        payload = llm.complete_json(cluster_prompt([craft.model_dump() for craft in crafts]))
        clusters = []
        for item in payload.get('clusters') or []:
            indexes = item.get('craft_indexes') or []
            selected = [crafts[int(idx)] for idx in indexes if isinstance(idx, int) and 0 <= idx < len(crafts)]
            if selected:
                clusters.append(TaskCluster(task_scope=str(item.get('task_scope') or 'General task'), crafts=selected))
        if clusters:
            return clusters
    except Exception:
        pass
    scope = crafts[0].contextual_description.applicable_scenario or crafts[0].contextual_description.task_goal
    return [TaskCluster(task_scope=scope or 'General task', crafts=crafts)]
