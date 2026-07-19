from __future__ import annotations

import json

import pytest

from lazymind.chat.engine.prompts.system_prompt import add_standard_system_sections
from lazymind.chat.engine.prompts.task_profile import (
    TaskProfile,
    resolve_task_profile,
    select_skill_candidates,
    selected_prompt_modules,
)
from lazymind.chat.engine.agent_runtime import AgentRole, PromptBuilder


SCENARIOS = [
    # Learn (20)
    *[(query, 'learn') for query in (
        '如何制作AI视频', '教我从零制作播客', '零基础学习摄影', '我想学会数据分析',
        '给我一个Python入门教程', '怎么搭建个人网站', '从零到一学习产品经理',
        '教我阅读财务报表', 'How do I build a RAG system?', 'Teach me video editing',
        '如何学习日语', '怎么制作高质量短视频', '教我使用Docker', '零基础学大模型',
        '如何搭建家庭网络', '怎么做出第一条动画', '教我写一个小游戏', '从零开始学摄影',
        '如何使用AI绘图工具', '我想学会做播客',
    )],
    # Research (15)
    *[(query, 'research') for query in (
        '调研AI视频行业', '调查低空经济机会', '研究一下具身智能赛道', '深入研究开源模型生态',
        '给我一份独立开发者资料汇总', 'Research the agent landscape', 'Investigate EV battery trends',
        '调研中国SaaS市场', '研究一下未来教育趋势', '调查这个技术为什么流行',
        '2026年主流AI视频工具有哪些', 'AI Agent现在发展到哪了', '当前有哪些主流开源模型',
        '最新AI搜索产品有哪些', '今年主流创作平台有哪些',
    )],
    # Decide (12)
    *[(query, 'decide') for query in (
        'Runway和可灵怎么选', 'Notion和Obsidian哪个好', '这个项目值不值得做', '我要不要读研',
        '是否应该引入大模型', '技术栈如何选', '自研还是买SaaS', '该不该开源这个项目',
        'Should I buy or build?', 'Postgres versus MySQL怎么选', '现在要不要转行做AI',
        '相机和手机拍视频哪个好',
    )],
    # Plan (12)
    *[(query, 'plan') for query in (
        '制定三个月AI学习计划', '帮我规划职业发展', '给我一个产品实施步骤', '设计团队培训路线图',
        '制定30天副业计划', '帮我规划搬家', '给我一个项目行动方案', '规划一次日本旅行',
        'Create a launch roadmap', '制定年度目标计划', '规划数据平台建设', '给我迁移到微服务的路线图',
    )],
    # Diagnose (10)
    *[(query, 'diagnose') for query in (
        '网站为什么变慢了', 'RAG怎么排查答非所问', '定位模型回答变差的问题', '广告效果为什么下降',
        '怎么排查数据库超时', 'Diagnose this deployment failure', 'Troubleshoot slow API calls',
        '为什么视频生成效果很差', '怎么排查项目总是延期', '定位Prompt不稳定的问题',
    )],
    # Execute (10)
    *[(query, 'execute') for query in (
        '替我发送这封邮件', '帮我发布这篇文章', '帮我修改这个文件', '帮我运行测试',
        '直接部署这个服务', '替我删除这个日程', '帮我安装这个插件', 'Execute the migration',
        'Deploy it for me', '帮我发送会议邀请',
    )],
    # Create (10)
    *[(query, 'create') for query in (
        '创建一份产品说明', '生成三张海报', '写一份项目总结', '制作一份演示文稿',
        '产出一个营销文案', 'Create a landing page', 'Generate a logo', 'Draft a contract outline',
        '创建一个AI视频Skill', '写一份研究摘要',
    )],
    # Direct answers (11) -- total 100
    *[(query, 'answer') for query in (
        '什么是帧率', '解释一下向量数据库', '定义机会成本', '谁是图灵', '一年有多少天',
        'What is recursion', 'Define unit economics', '解释一下曝光三要素', '什么是现金流',
        '什么是微服务', '解释一下提示词工程',
    )],
]


@pytest.mark.parametrize(('query', 'expected'), SCENARIOS)
def test_one_hundred_divergent_scenarios_route_by_user_outcome(query: str, expected: str) -> None:
    profile = resolve_task_profile(query, enable_llm_fallback=False)
    assert profile.primary_outcome == expected


def test_ai_video_learning_uses_research_tutorial_and_suppresses_skills() -> None:
    profile = resolve_task_profile('如何制作AI视频', enable_llm_fallback=False)

    assert profile == TaskProfile(
        primary_outcome='learn',
        complexity='open_ended',
        freshness='current',
        research_required=True,
        deliverable_kind='tutorial',
        skill_mode='suppress',
        confidence=0.92,
        reasons=('explicit outcome wording', 'current-information signal'),
    )
    assert selected_prompt_modules(profile) == [
        'learning', 'fresh_research', 'tutorial', 'skill_restraint',
    ]


def test_simple_fact_uses_no_deliverable_module() -> None:
    profile = resolve_task_profile('什么是帧率', enable_llm_fallback=False)
    assert profile.primary_outcome == 'answer'
    assert profile.complexity == 'simple'
    assert selected_prompt_modules(profile) == ['skill_restraint']


def test_compound_request_discloses_at_most_two_deliverables() -> None:
    profile = resolve_task_profile('研究一下AI视频，然后教我做第一条作品', enable_llm_fallback=False)
    modules = selected_prompt_modules(profile)
    assert profile.primary_outcome == 'learn'
    assert profile.secondary_outcomes == ('research',)
    assert [item for item in modules if item in {'tutorial', 'research_report'}] == [
        'tutorial', 'research_report',
    ]


def test_explicit_skill_request_does_not_inject_restraint() -> None:
    profile = resolve_task_profile('创建一个AI视频Skill', enable_llm_fallback=False)
    assert profile.skill_mode == 'explicit'
    assert 'skill_restraint' not in selected_prompt_modules(profile)


def test_invalid_classifier_response_falls_back_without_raising() -> None:
    profile = resolve_task_profile(
        '我想搞点AI视频相关的东西',
        classifier=lambda _: 'not json',
    )
    assert profile.source == 'fallback'
    assert profile.primary_outcome == 'answer'
    assert profile.skill_mode == 'suppress'
    assert profile.router_error


def test_valid_classifier_preserves_explicit_current_signal() -> None:
    result = {
        'primary_outcome': 'decide',
        'secondary_outcomes': [],
        'complexity': 'open_ended',
        'freshness': 'stable',
        'research_required': False,
        'deliverable_kind': 'decision_brief',
        'secondary_deliverables': [],
        'skill_mode': 'candidates',
        'confidence': 0.85,
        'reasons': ['implicit choice'],
    }
    profile = resolve_task_profile(
        '我现在想搞点AI视频相关的东西',
        classifier=lambda _: json.dumps(result),
    )
    assert profile.source == 'llm'
    assert profile.freshness == 'current'
    assert profile.research_required is True


def test_dynamic_prompt_builder_injects_only_selected_contracts() -> None:
    profile = resolve_task_profile('如何制作AI视频', enable_llm_fallback=False)
    builder = PromptBuilder.for_role(AgentRole.CHAT)
    bundle = add_standard_system_sections(
        builder,
        True,
        task_profile=profile,
        dynamic_prompt_modules=True,
    ).input('如何制作AI视频', source='user').build()

    assert '# Learning requests' in bundle.system_prompt
    assert '# Current research' in bundle.system_prompt
    assert 'Deliver a tutorial' in bundle.system_prompt
    assert '# Decision and planning requests' not in bundle.system_prompt
    assert 'Deliver a decision brief' not in bundle.system_prompt


def test_task_profile_is_ephemeral_and_does_not_mutate_intent() -> None:
    intent = {'goal': 'learn photography', 'constraints': ['one hour per day']}
    before = json.dumps(intent, sort_keys=True)
    resolve_task_profile('现在教我制作AI视频', intent=intent, enable_llm_fallback=False)
    assert json.dumps(intent, sort_keys=True) == before


def test_skill_candidates_are_relevance_ranked_and_capped_at_five() -> None:
    profile = resolve_task_profile('调研AI视频行业', enable_llm_fallback=False)
    available = [f'research/video-{index}' for index in range(8)] + ['writing/poetry']
    visible = select_skill_candidates(available, '调研AI视频行业', profile)
    assert visible is not None
    assert len(visible) == 5
    assert all(item.startswith('research/video-') for item in visible)
