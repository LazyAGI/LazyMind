from __future__ import annotations

from chat.components.skill_review.schemas import SkillDraft, TaskCluster
from chat.prompts.skill_review import cluster_prompt


def cluster_drafts(drafts: list[SkillDraft], llm) -> list[TaskCluster]:
    # TODO: Implement for Cluster Drafts
    if not drafts:
        return []
    try:
        payload = llm.complete_json(cluster_prompt([draft.model_dump() for draft in drafts]))
        clusters = []
        for item in payload.get('clusters') or []:
            indexes = item.get('draft_indexes') or []
            selected = [drafts[int(idx)] for idx in indexes if isinstance(idx, int) and 0 <= idx < len(drafts)]
            if selected:
                clusters.append(TaskCluster(task_scope=str(item.get('task_scope') or 'General task'), drafts=selected))
        if clusters:
            return clusters
    except Exception:
        pass
    scope = drafts[0].contextual_description.applicable_scenario or drafts[0].contextual_description.task_goal
    return [TaskCluster(task_scope=scope or 'General task', drafts=drafts)]
