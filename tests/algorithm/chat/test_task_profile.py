from __future__ import annotations

import json
from itertools import permutations

import pytest

from lazymind.chat.engine.prompts.system_prompt import add_standard_system_sections
from lazymind.chat.engine.prompts.task_profile import (
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


OUTCOME_CLAUSES = {
    'answer': '解释一下帧率',
    'learn': '教我学会剪辑',
    'research': '调研AI工具市场',
    'analyze': '分析这份方案',
    'transform': '总结这份材料',
    'decide': '帮我选择合适工具',
    'plan': '制定实施计划',
    'create': '创建一份海报',
    'execute': '发布这份报告',
    'diagnose': '排查系统变慢原因',
}
OUTCOME_NAMES = tuple(OUTCOME_CLAUSES)


def _chain(*outcomes: str, variant: int = 0) -> str:
    clauses = [OUTCOME_CLAUSES[outcomes[0]]]
    clauses.extend(f'然后{OUTCOME_CLAUSES[item]}' for item in outcomes[1:])
    return '，'.join(clauses) + f'，表达变体{variant}'


SINGLE_SCENARIOS = [
    {
        'id': f'single-{outcome}-{variant}',
        'query': _chain(outcome, variant=variant),
        'primary': outcome,
        'secondary': (),
    }
    for outcome in OUTCOME_NAMES
    for variant in range(20)
]

PAIR_SCENARIOS = [
    {
        'id': f'pair-{first}-{second}-{variant}',
        'query': _chain(first, second, variant=variant),
        'primary': first,
        'secondary': (second,),
    }
    for first, second in permutations(OUTCOME_NAMES, 2)
    for variant in range(2)
]

TRIPLE_SCENARIOS = [
    {
        'id': f'triple-{first}-{second}-{third}-{variant}',
        'query': _chain(first, second, third, variant=variant),
        'primary': first,
        'secondary': (second, third),
    }
    for first, second, third in list(permutations(OUTCOME_NAMES, 3))[:50]
    for variant in range(2)
]

BOUNDARY_PAIRS = (
    ('research', 'analyze'),
    ('analyze', 'diagnose'),
    ('create', 'transform'),
    ('plan', 'execute'),
    ('answer', 'learn'),
    ('analyze', 'decide'),
    ('answer', 'research'),
)
BOUNDARY_SCENARIOS = [
    {
        'id': f'boundary-{first}-{second}-{variant}',
        'query': _chain(first, second, variant=variant),
        'primary': first,
        'secondary': (second,),
    }
    for first, second in BOUNDARY_PAIRS
    for variant in range(20)
]

ORTHOGONAL_TEMPLATES = (
    ('分析附件里的代码', 'analyze', 'code', 'attachment', 'provided_content_only', True),
    ('总结附件里的财报', 'transform', 'document', 'attachment', 'provided_content_only', True),
    ('翻译以下文字：hello', 'transform', 'topic', 'inline_content', 'provided_content_only', False),
    ('调研最新AI工具', 'research', 'topic', 'query_only', 'web', False),
    ('比较这两个数据库', 'analyze', 'data', 'query_only', 'model_knowledge', False),
    ('把这段代码改写成函数', 'transform', 'code', 'inline_content', 'provided_content_only', False),
    ('分析这个网址 https://example.com', 'analyze', 'topic', 'url', 'provided_content_only', False),
    ('创建一份演示文稿', 'create', 'topic', 'query_only', 'model_knowledge', False),
    ('发布这份报告', 'execute', 'document', 'query_only', 'model_knowledge', False),
    ('网站变慢，找出原因', 'diagnose', 'system', 'query_only', 'model_knowledge', False),
)
ORTHOGONAL_SCENARIOS = [
    {
        'id': f'orthogonal-{index}-{variant}',
        'query': query + f'，版本{variant}',
        'primary': primary,
        'subject_kind': subject,
        'input_mode': input_mode,
        'source_strategy': source,
        'has_attachments': attachments,
    }
    for index, (query, primary, subject, input_mode, source, attachments) in enumerate(
        ORTHOGONAL_TEMPLATES
    )
    for variant in range(10)
]

REQUEST_ASSESSMENT_TEMPLATES = (
    ('用500个场景覆盖10个目标的所有组合', 'contradictory', 'blocking'),
    ('不要联网，给我最新AI工具', 'contradictory', 'blocking'),
    ('总结这份文档', 'underspecified', 'blocking'),
    ('保证这个方案100%成功', 'ambiguous', 'optional'),
)
REQUEST_ASSESSMENT_SCENARIOS = [
    {
        'id': f'assessment-{index}-{variant}',
        'query': query + f'，场景{variant}',
        'status': status,
        'interaction_need': interaction_need,
    }
    for index, (query, status, interaction_need) in enumerate(REQUEST_ASSESSMENT_TEMPLATES)
    for variant in range(25)
]

ENGLISH_CLAUSES = {
    'answer': 'Explain what frame rate means',
    'learn': 'Teach me video editing',
    'research': 'Research the AI tools market',
    'analyze': 'Analyze this proposal',
    'transform': 'Summarize this document',
    'decide': 'Should I choose product A',
    'plan': 'Build an implementation roadmap',
    'create': 'Generate a product brief',
    'execute': 'Execute the deployment',
    'diagnose': 'Diagnose this deployment failure',
}
MULTILINGUAL_SCENARIOS = [
    {
        'id': f'english-{outcome}-{variant}',
        'query': f'{query}; variant {variant}',
        'primary': outcome,
    }
    for outcome, query in ENGLISH_CLAUSES.items()
    for variant in range(4)
]

ROUTING_SCENARIOS = (
    SINGLE_SCENARIOS + PAIR_SCENARIOS + TRIPLE_SCENARIOS
    + BOUNDARY_SCENARIOS + ORTHOGONAL_SCENARIOS + MULTILINGUAL_SCENARIOS
)
FALLBACK_SCENARIOS = [f'帮我看看一些材料，模糊表达{index}' for index in range(40)]

SKILL_MENTION_SCENARIOS = [
    {
        'id': f'mention-skill-{outcome}-{variant}',
        'query': _chain(outcome, variant=variant),
        'bindings': {'skill_names': [f'{outcome}/selected-skill']},
        'expected_skill_mode': 'explicit',
        'expected_selected_skills': [f'{outcome}/selected-skill'],
    }
    for outcome in OUTCOME_NAMES
    for variant in range(2)
]
KNOWLEDGE_BASE_MENTION_SCENARIOS = [
    {
        'id': f'mention-kb-{outcome}-{variant}',
        'query': _chain(outcome, variant=variant),
        'bindings': {'knowledge_base_ids': [f'kb-{outcome}']},
        'expected_source_strategy': 'knowledge_base',
    }
    for outcome in OUTCOME_NAMES
    for variant in range(2)
]
PLUGIN_MENTION_SCENARIOS = [
    {
        'id': f'mention-plugin-{outcome}-{variant}',
        'query': _chain(outcome, variant=variant),
        'bindings': {'plugin_refs': [f'plugin/{outcome}']},
        'expected_plugin_refs': (f'plugin/{outcome}',),
        'expected_primary': outcome,
    }
    for outcome in OUTCOME_NAMES
    for variant in range(2)
]
RESOURCE_PAIR_KINDS = (
    {'skill_names': ['review/selected'], 'knowledge_base_ids': ['kb-selected']},
    {'skill_names': ['review/selected'], 'plugin_refs': ['plugin/selected']},
    {'knowledge_base_ids': ['kb-selected'], 'plugin_refs': ['plugin/selected']},
)
RESOURCE_PAIR_SCENARIOS = [
    {
        'id': f'mention-pair-{kind_index}-{variant}',
        'query': _chain(OUTCOME_NAMES[variant % len(OUTCOME_NAMES)], variant=variant),
        'bindings': bindings,
    }
    for kind_index, bindings in enumerate(RESOURCE_PAIR_KINDS)
    for variant in range(8)
]
RESOURCE_TRIPLE_SCENARIOS = [
    {
        'id': f'mention-triple-{variant}',
        'query': _chain(OUTCOME_NAMES[variant], variant=variant),
        'bindings': {
            'skill_names': ['review/selected'],
            'knowledge_base_ids': ['kb-selected'],
            'plugin_refs': ['plugin/selected'],
        },
    }
    for variant in range(8)
]
RESOURCE_CONFLICT_SCENARIOS = [
    *[
        {
            'id': f'mention-conflict-skill-{variant}',
            'query': f'解释一下这个主题，但不要使用任何Skill，变体{variant}',
            'bindings': {
                'skill_names': ['review/selected'],
                'mentions': [{
                    'resource_type': 'skill', 'resource_ref': 'review/selected',
                    'display_name': 'Skill',
                }],
            },
            'expected_excluded': ('review/selected',),
        }
        for variant in range(6)
    ],
    *[
        {
            'id': f'mention-conflict-kb-{variant}',
            'query': f'解释一下这个主题，但不要使用这个知识库，变体{variant}',
            'bindings': {
                'knowledge_base_ids': ['kb-selected'],
                'mentions': [{
                    'resource_type': 'knowledge_base', 'resource_ref': 'kb-selected',
                    'display_name': '这个知识库',
                }],
            },
            'expected_excluded': ('kb-selected',),
        }
        for variant in range(5)
    ],
    *[
        {
            'id': f'mention-conflict-plugin-{variant}',
            'query': f'解释一下这个主题，但不要启动这个Plugin，变体{variant}',
            'bindings': {
                'plugin_refs': ['plugin/selected'],
                'mentions': [{
                    'resource_type': 'plugin', 'resource_ref': 'plugin/selected',
                    'display_name': '这个Plugin',
                }],
            },
            'expected_excluded': ('plugin/selected',),
        }
        for variant in range(5)
    ],
]
RESOURCE_CURRENT_VS_DEFAULT_SCENARIOS = [
    *[
        {
            'id': f'mention-default-only-{variant}',
            'query': f'分析产品方案，默认资源变体{variant}',
            'bindings': {},
            'available_skills': ['default/enabled-skill'],
            'expected_not_explicit': True,
        }
        for variant in range(6)
    ],
    *[
        {
            'id': f'mention-current-over-default-{variant}',
            'query': f'分析产品方案，本轮资源变体{variant}',
            'bindings': {'skill_names': ['current/mentioned-skill']},
            'available_skills': ['default/enabled-skill', 'current/mentioned-skill'],
            'expected_selected_skills': ['current/mentioned-skill'],
        }
        for variant in range(6)
    ],
]
RESOURCE_BINDING_SCENARIOS = (
    SKILL_MENTION_SCENARIOS
    + KNOWLEDGE_BASE_MENTION_SCENARIOS
    + PLUGIN_MENTION_SCENARIOS
    + RESOURCE_PAIR_SCENARIOS
    + RESOURCE_TRIPLE_SCENARIOS
    + RESOURCE_CONFLICT_SCENARIOS
    + RESOURCE_CURRENT_VS_DEFAULT_SCENARIOS
)


def test_evaluation_suite_contains_nine_hundred_scenarios() -> None:
    total = len(ROUTING_SCENARIOS) + len(REQUEST_ASSESSMENT_SCENARIOS) + len(FALLBACK_SCENARIOS)
    assert total == 900


def test_resource_binding_suite_adds_one_hundred_twenty_scenarios() -> None:
    assert len(RESOURCE_BINDING_SCENARIOS) == 120
    assert 900 + len(RESOURCE_BINDING_SCENARIOS) == 1020


def test_all_ordered_two_outcome_combinations_are_covered() -> None:
    covered = {(item['primary'], item['secondary'][0]) for item in PAIR_SCENARIOS}
    assert covered == set(permutations(OUTCOME_NAMES, 2))
    assert len(covered) == 90


def test_triple_suite_covers_fifty_distinct_ordered_chains() -> None:
    covered = {(item['primary'], *item['secondary']) for item in TRIPLE_SCENARIOS}
    assert len(covered) == 50


@pytest.mark.parametrize('scenario', ROUTING_SCENARIOS, ids=lambda item: item['id'])
def test_expanded_routing_scenarios(scenario: dict) -> None:
    profile = resolve_task_profile(
        scenario['query'],
        enable_llm_fallback=False,
        has_attachments=scenario.get('has_attachments', False),
    )
    assert profile.primary_outcome == scenario['primary']
    if 'secondary' in scenario:
        assert profile.secondary_outcomes == scenario['secondary']
    for field in ('subject_kind', 'input_mode', 'source_strategy'):
        if field in scenario:
            assert getattr(profile, field) == scenario[field]


@pytest.mark.parametrize('scenario', REQUEST_ASSESSMENT_SCENARIOS, ids=lambda item: item['id'])
def test_request_assessment_scenarios(scenario: dict) -> None:
    assessment = resolve_task_profile(
        scenario['query'], enable_llm_fallback=False,
    ).request_assessment
    assert assessment.status == scenario['status']
    assert assessment.interaction_need == scenario['interaction_need']


@pytest.mark.parametrize('query', FALLBACK_SCENARIOS)
def test_classifier_failure_scenarios_are_safe(query: str) -> None:
    profile = resolve_task_profile(query, classifier=lambda _: 'invalid')
    assert profile.source == 'fallback'
    assert profile.primary_outcome == 'answer'
    assert profile.skill_mode == 'suppress'


@pytest.mark.parametrize('scenario', RESOURCE_BINDING_SCENARIOS, ids=lambda item: item['id'])
def test_explicit_resource_binding_scenarios(scenario: dict) -> None:
    profile = resolve_task_profile(
        scenario['query'],
        enable_llm_fallback=False,
        explicit_resources=scenario['bindings'],
    )
    bindings = scenario['bindings']
    if bindings.get('skill_names') and not scenario.get('expected_excluded'):
        assert profile.skill_mode == 'explicit'
        available = ['default/enabled-skill', *bindings['skill_names']]
        assert select_skill_candidates(available, scenario['query'], profile) == bindings['skill_names']
    if bindings.get('knowledge_base_ids') and not scenario.get('expected_excluded'):
        assert profile.source_strategy == 'knowledge_base'
        assert profile.explicit_resources.knowledge_base_ids == tuple(bindings['knowledge_base_ids'])
    if bindings.get('plugin_refs') and not scenario.get('expected_excluded'):
        assert profile.explicit_resources.plugin_refs == tuple(bindings['plugin_refs'])
    if 'expected_primary' in scenario:
        assert profile.primary_outcome == scenario['expected_primary']
    if 'expected_skill_mode' in scenario:
        assert profile.skill_mode == scenario['expected_skill_mode']
    if 'expected_source_strategy' in scenario:
        assert profile.source_strategy == scenario['expected_source_strategy']
    if 'expected_plugin_refs' in scenario:
        assert profile.explicit_resources.plugin_refs == scenario['expected_plugin_refs']
    if scenario.get('expected_excluded'):
        excluded = profile.excluded_resources
        actual = excluded.skill_names + excluded.knowledge_base_ids + excluded.plugin_refs
        assert actual == scenario['expected_excluded']
        assert profile.request_assessment.status == 'ready'
    if scenario.get('expected_not_explicit'):
        assert profile.skill_mode != 'explicit'
    if 'expected_selected_skills' in scenario:
        available = scenario.get('available_skills') or [
            'default/enabled-skill', *bindings.get('skill_names', []),
        ]
        assert select_skill_candidates(available, scenario['query'], profile) == scenario[
            'expected_selected_skills'
        ]


@pytest.mark.parametrize(('query', 'expected'), SCENARIOS)
def test_one_hundred_divergent_scenarios_route_by_user_outcome(query: str, expected: str) -> None:
    profile = resolve_task_profile(query, enable_llm_fallback=False)
    assert profile.primary_outcome == expected


def test_ai_video_learning_uses_research_tutorial_and_suppresses_skills() -> None:
    profile = resolve_task_profile('如何制作AI视频', enable_llm_fallback=False)

    assert profile.primary_outcome == 'learn'
    assert profile.outcome_subtype == 'tutorial'
    assert profile.complexity == 'open_ended'
    assert profile.freshness == 'current'
    assert profile.research_required is True
    assert profile.deliverable_kind == 'tutorial'
    assert profile.skill_mode == 'suppress'
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
    assert profile.primary_outcome == 'research'
    assert profile.secondary_outcomes == ('learn',)
    assert [item for item in modules if item in {'tutorial', 'research_report'}] == [
        'research_report', 'tutorial',
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


def test_blocking_request_issue_is_disclosed_as_authoritative_runtime_state() -> None:
    profile = resolve_task_profile(
        '用500个场景覆盖10个目标的所有组合', enable_llm_fallback=False,
    )
    bundle = add_standard_system_sections(
        PromptBuilder.for_role(AgentRole.CHAT),
        True,
        task_profile=profile,
        dynamic_prompt_modules=True,
    ).input('用500个场景覆盖10个目标的所有组合', source='user').build()

    assert '# Request quality check' in bundle.system_prompt
    assert '# Clarification required' in bundle.system_prompt
    assert '#### Request Assessment [AUTHORITATIVE]' in bundle.current_input
    assert 'mathematical_inconsistency' in bundle.current_input
    assert 'Interaction need: blocking' in bundle.current_input


def test_task_profile_is_ephemeral_and_does_not_mutate_intent() -> None:
    intent = {'goal': 'learn photography', 'constraints': ['one hour per day']}
    before = json.dumps(intent, sort_keys=True)
    resolve_task_profile('现在教我制作AI视频', intent=intent, enable_llm_fallback=False)
    assert json.dumps(intent, sort_keys=True) == before


def test_analyze_and_transform_have_distinct_source_contracts() -> None:
    analysis = resolve_task_profile(
        '分析附件里的财报', enable_llm_fallback=False, has_attachments=True,
    )
    transformation = resolve_task_profile(
        '总结附件里的财报', enable_llm_fallback=False, has_attachments=True,
    )
    assert analysis.primary_outcome == 'analyze'
    assert analysis.deliverable_kind == 'analysis_report'
    assert transformation.primary_outcome == 'transform'
    assert transformation.deliverable_kind == 'transformed_content'
    assert transformation.source_strategy == 'provided_content_only'


def test_comparison_is_analysis_but_contextual_selection_is_decision() -> None:
    comparison = resolve_task_profile('比较Runway和可灵', enable_llm_fallback=False)
    selection = resolve_task_profile('做广告视频时Runway和可灵怎么选', enable_llm_fallback=False)
    assert comparison.primary_outcome == 'analyze'
    assert comparison.outcome_subtype == 'compare'
    assert selection.primary_outcome == 'decide'
    assert selection.outcome_subtype == 'select'


def test_request_assessment_detects_impossible_combination_budget() -> None:
    profile = resolve_task_profile(
        '用500个场景覆盖10个目标的所有组合', enable_llm_fallback=False,
    )
    assessment = profile.request_assessment
    assert assessment.status == 'contradictory'
    assert assessment.interaction_need == 'blocking'
    assert assessment.issues[0].issue_type == 'mathematical_inconsistency'
    assert '1023' in assessment.issues[0].description
    assert 'request_analysis' in selected_prompt_modules(profile)
    assert 'clarification' in selected_prompt_modules(profile)


def test_missing_referenced_attachment_requires_minimal_clarification() -> None:
    assessment = resolve_task_profile(
        '总结这份文档', enable_llm_fallback=False, has_attachments=False,
    ).request_assessment
    assert assessment.status == 'underspecified'
    assert assessment.interaction_need == 'blocking'
    assert len(assessment.clarification_questions) == 1


def test_available_attachment_avoids_unnecessary_question() -> None:
    assessment = resolve_task_profile(
        '总结这份文档', enable_llm_fallback=False, has_attachments=True,
    ).request_assessment
    assert assessment.status == 'ready'
    assert assessment.interaction_need == 'none'


def test_optional_issue_uses_assumption_without_forcing_clarification() -> None:
    profile = resolve_task_profile(
        '保证这个方案100%成功', enable_llm_fallback=False,
    )
    assert profile.request_assessment.interaction_need == 'optional'
    assert 'request_analysis' in selected_prompt_modules(profile)
    assert 'clarification' not in selected_prompt_modules(profile)


def test_skill_candidates_are_relevance_ranked_and_capped_at_five() -> None:
    profile = resolve_task_profile('调研AI视频行业', enable_llm_fallback=False)
    available = [f'research/video-{index}' for index in range(8)] + ['writing/poetry']
    visible = select_skill_candidates(available, '调研AI视频行业', profile)
    assert visible is not None
    assert len(visible) == 5
    assert all(item.startswith('research/video-') for item in visible)


def test_explicit_skill_selection_overrides_learning_suppression() -> None:
    profile = resolve_task_profile(
        '教我制作AI视频',
        enable_llm_fallback=False,
        explicit_resources={'skill_names': ['video/ai-production']},
    )
    assert profile.primary_outcome == 'learn'
    assert profile.skill_mode == 'explicit'
    assert select_skill_candidates(
        ['research/deep-research', 'video/ai-production'], '教我制作AI视频', profile,
    ) == ['video/ai-production']


def test_explicit_knowledge_base_overrides_inferred_web_source() -> None:
    profile = resolve_task_profile(
        '分析当前AI视频市场',
        enable_llm_fallback=False,
        explicit_resources={'knowledge_base_ids': ['kb-market']},
    )
    assert profile.primary_outcome == 'analyze'
    assert profile.source_strategy == 'knowledge_base'
    assert profile.research_required is True


def test_explicit_knowledge_base_and_explicit_web_request_use_mixed_sources() -> None:
    profile = resolve_task_profile(
        '基于资料库并联网搜索最新信息',
        enable_llm_fallback=False,
        explicit_resources={'knowledge_base_ids': ['kb-internal']},
    )
    assert profile.source_strategy == 'mixed'


def test_explicit_plugin_binding_preserves_user_outcome() -> None:
    profile = resolve_task_profile(
        '帮我制定发布计划',
        enable_llm_fallback=False,
        explicit_resources={'plugin_refs': ['marketing/campaign']},
    )
    assert profile.primary_outcome == 'plan'
    assert profile.explicit_resources.plugin_refs == ('marketing/campaign',)
    assert 'explicit plugin selection' in profile.reasons


def test_mixed_resource_allow_and_deny_keeps_only_allowed_mentions() -> None:
    profile = resolve_task_profile(
        '帮我分析方案，注意，你不要用内部资料，可以使用公开资料',
        enable_llm_fallback=False,
        explicit_resources={
            'knowledge_base_ids': ['kb-internal', 'kb-public'],
            'mentions': [
                {'resource_type': 'knowledge_base', 'resource_ref': 'kb-internal',
                 'display_name': '内部资料'},
                {'resource_type': 'knowledge_base', 'resource_ref': 'kb-public',
                 'display_name': '公开资料'},
            ],
        },
    )
    assert profile.primary_outcome == 'analyze'
    assert profile.explicit_resources.knowledge_base_ids == ('kb-public',)
    assert profile.excluded_resources.knowledge_base_ids == ('kb-internal',)
    assert profile.source_strategy == 'knowledge_base'
    assert profile.request_assessment.status == 'ready'


def test_colloquial_resource_denials_are_executed_without_clarification() -> None:
    for wording in ('别用内部库', '我不想用内部库', '不用内部库', '忽略内部库'):
        profile = resolve_task_profile(
            f'帮我解释这个问题，{wording}',
            enable_llm_fallback=False,
            explicit_resources={
                'knowledge_base_ids': ['kb-internal'],
                'mentions': [{'resource_type': 'knowledge_base',
                              'resource_ref': 'kb-internal', 'display_name': '内部库'}],
            },
        )
        assert not profile.explicit_resources.knowledge_base_ids
        assert profile.excluded_resources.knowledge_base_ids == ('kb-internal',)
        assert profile.source_strategy == 'model_knowledge'
        assert profile.request_assessment.status == 'ready'


def test_ambiguous_resource_preference_uses_llm_intent_result() -> None:
    result = {
        'primary_outcome': 'analyze', 'secondary_outcomes': [],
        'complexity': 'open_ended', 'freshness': 'stable',
        'research_required': False, 'deliverable_kind': 'analysis_report',
        'secondary_deliverables': [], 'skill_mode': 'suppress',
        'source_strategy': 'knowledge_base', 'confidence': 0.88,
        'reasons': ['resource preference'], 'excluded_resource_refs': ['kb-internal'],
    }
    profile = resolve_task_profile(
        '分析方案，尽量别依赖内部库，外部库你看着办',
        classifier=lambda _: json.dumps(result),
        explicit_resources={
            'knowledge_base_ids': ['kb-internal', 'kb-external'],
            'mentions': [
                {'resource_type': 'knowledge_base', 'resource_ref': 'kb-internal',
                 'display_name': '内部库'},
                {'resource_type': 'knowledge_base', 'resource_ref': 'kb-external',
                 'display_name': '外部库'},
            ],
        },
    )
    assert profile.source == 'llm'
    assert profile.explicit_resources.knowledge_base_ids == ('kb-external',)
    assert profile.excluded_resources.knowledge_base_ids == ('kb-internal',)
    assert profile.request_assessment.status == 'ready'


def test_rule_only_preview_marks_when_llm_review_would_be_needed() -> None:
    calls = []
    profile = resolve_task_profile(
        '分析方案，尽量别依赖内部库，外部库你看着办',
        classifier=lambda prompt: calls.append(prompt),
        enable_llm_fallback=False,
        explicit_resources={
            'knowledge_base_ids': ['kb-internal', 'kb-external'],
            'mentions': [
                {'resource_type': 'knowledge_base', 'resource_ref': 'kb-internal',
                 'display_name': '内部库'},
                {'resource_type': 'knowledge_base', 'resource_ref': 'kb-external',
                 'display_name': '外部库'},
            ],
        },
    )
    assert calls == []
    assert profile.routing_review_required is True
    assert '资源' in profile.routing_review_reason


def test_llm_classification_cannot_override_explicit_resources() -> None:
    result = {
        'primary_outcome': 'learn',
        'secondary_outcomes': [],
        'complexity': 'open_ended',
        'freshness': 'current',
        'research_required': True,
        'deliverable_kind': 'tutorial',
        'secondary_deliverables': [],
        'skill_mode': 'suppress',
        'source_strategy': 'web',
        'confidence': 0.9,
        'reasons': ['learning request'],
    }
    profile = resolve_task_profile(
        '帮我看看AI视频方向',
        classifier=lambda _: json.dumps(result),
        explicit_resources={
            'skill_names': ['video/ai-production'],
            'knowledge_base_ids': ['kb-video'],
            'plugin_refs': ['video/workflow'],
        },
    )
    assert profile.skill_mode == 'explicit'
    assert profile.source_strategy == 'knowledge_base'
    assert profile.explicit_resources.skill_names == ('video/ai-production',)
    assert profile.explicit_resources.plugin_refs == ('video/workflow',)
