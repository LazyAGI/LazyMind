from __future__ import annotations

from typing import Any

from lazymind.chat.engine.skills import SkillRetriever


def build_search_skills_tool(skill_manager: Any, retriever: SkillRetriever) -> Any:
    def search_skills(query: str, limit: int = 8) -> dict[str, Any]:
        '''Search enabled skills by meaning and keywords, then expose matching skills.

        Use this when the initially shown skills do not cover the task, the task changes
        during the conversation, or a compound request may need another skill. Call this
        tool first, inspect the returned name and description, then call get_skill in a
        later action only for a relevant result.

        Args:
            query (str): A concise description of the capability or workflow needed.
            limit (int, optional): Maximum number of matching skills. Defaults to 8.
        '''
        result = retriever.retrieve(
            query,
            skill_manager.list_skill_metadata('allowed'),
            limit=max(1, min(int(limit), 20)),
        )
        exposed = skill_manager.expose_skills(result.skill_ids)
        exposed_by_id = {
            str(item.get('id')): item for item in exposed.get('skills', [])
        }
        hits = []
        for hit in result.hits:
            item = exposed_by_id.get(hit.descriptor.skill_id)
            if item:
                hits.append({
                    **item,
                    'score': round(hit.score, 8),
                    'channels': list(hit.channels),
                })
        return {
            'status': exposed.get('status', 'ok'),
            'query': query,
            'count': len(hits),
            'strategy': result.strategy,
            'latency_ms': result.latency_ms,
            'embedding_error': result.embedding_error,
            'skills': hits,
            'errors': exposed.get('errors', []),
        }

    return search_skills
