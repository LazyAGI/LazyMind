from dataclasses import asdict

import pytest

from lazymind.chat.engine.prompts.task_profile import resolve_task_profile


@pytest.mark.parametrize('query', [
    '我今天要复习，出题吧', '什么是向量数据库', '把这段话翻译成英文',
    '你好', '测试', '不要使用任何 Skill，直接回答', '你为什么不用单词skill的复习能力呢',
])
def test_task_profile_never_controls_skill_scope(query):
    profile = resolve_task_profile(query, enable_llm_fallback=False)
    assert 'skill_mode' not in asdict(profile)
    assert profile.excluded_resources.skill_names == ()


def test_classifier_cannot_override_host_skill_bindings():
    profile = resolve_task_profile(
        '帮我研究这个问题',
        classifier=lambda _prompt: '{"primary_outcome":"research","skill_mode":"suppress"}',
        explicit_resources={'skill_names': ['external/paper']},
        thinking_depth='high',
    )
    assert 'skill_mode' not in asdict(profile)
    assert profile.explicit_resources.skill_names == ('external/paper',)
    assert profile.excluded_resources.skill_names == ()


def test_router_failure_keeps_host_skill_bindings():
    def fail_router(_prompt):
        raise RuntimeError('router unavailable')

    profile = resolve_task_profile(
        '帮我看看这个问题应该怎么解决', classifier=fail_router,
        explicit_resources={'skill_names': ['external/paper']}, thinking_depth='high',
    )
    assert profile.source == 'fallback'
    assert 'skill_mode' not in asdict(profile)
    assert profile.explicit_resources.skill_names == ('external/paper',)
