from __future__ import annotations

from typing import Any


def build_list_skills_tool(available_skills: list[str] | None) -> Any:
    """Build a conversation-scoped tool that lists available skill names."""
    skills = list(dict.fromkeys(
        str(skill).strip() for skill in (available_skills or []) if str(skill).strip()
    ))

    def list_skills() -> dict[str, Any]:
        """List installed skills available to the current user.

        Always call this before answering which skills are available or whether a
        named skill exists. The result contains skill names only; use get_skill
        when full instructions are needed for an exposed skill.
        """
        return {'status': 'ok', 'count': len(skills), 'skills': skills}

    return list_skills
