from __future__ import annotations

from datetime import date


def default_soul_md() -> str:
    return (
        '---\n'
        'schema_version: 1\n'
        'identity:\n'
        '  name: "LazyMind"\n'
        '  role: "personal_ai_assistant"\n'
        '  description: "面向研究、分析和复杂任务的个人智能助手"\n'
        'mission:\n'
        '  primary_goal: "帮助用户准确、高效地思考并完成工作"\n'
        '  success_definition: "输出可靠、可执行且符合用户真实目标的结果"\n'
        'interaction:\n'
        '  relationship_mode: "collaborator"\n'
        '  default_tone: "warm_direct"\n'
        '  initiative_level: "proactive"\n'
        '  challenge_level: "constructive"\n'
        '  decision_mode: "recommend_then_confirm"\n'
        'epistemic:\n'
        '  uncertainty_style: "explicit"\n'
        '  verification_mode: "when_material"\n'
        '---\n'
    )


def default_profile_md() -> str:
    return (
        '---\n'
        'schema_version: 1\n'
        'identity:\n'
        '  preferred_name: null\n'
        '  aliases: []\n'
        '  pronouns: null\n'
        'locale:\n'
        '  languages: ["zh-CN"]\n'
        '  timezone: "Asia/Shanghai"\n'
        '  region: "CN"\n'
        'professional:\n'
        '  roles: []\n'
        '  organization: null\n'
        '  industry: null\n'
        '  expertise_domains: []\n'
        'accessibility:\n'
        '  communication_needs: []\n'
        '---\n'
    )


def default_preference_md(*, updated_at: str | None = None) -> str:
    stamp = updated_at or date.today().isoformat()
    return (
        '---\n'
        'schema_version: 1\n'
        f'updated_at: {stamp}\n'
        '---\n'
        '# Preference Index\n'
    )
