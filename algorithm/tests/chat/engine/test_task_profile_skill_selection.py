from lazymind.chat.engine.prompts.task_profile import (
    _rule_profile,
    resolve_task_profile,
    select_skill_candidates,
)


def test_vocabulary_review_request_selects_learning_skill():
    profile, _ = _rule_profile('帮我看看我有哪些单词还没记住，需要复习的')

    assert profile.skill_mode == 'candidates'
    assert select_skill_candidates(
        ['research/deep-research', 'vocabulary/vocabulary-learning'],
        '帮我看看我有哪些单词还没记住，需要复习的',
        profile,
    ) == ['research/deep-research', 'vocabulary/vocabulary-learning']


def test_short_vocabulary_review_request_selects_learning_skill():
    query = '我今天要复习，出题吧'
    profile, _ = _rule_profile(query)

    assert profile.primary_outcome == 'learn'
    assert profile.skill_mode == 'candidates'
    assert select_skill_candidates(
        ['research/deep-research', 'vocabulary/vocabulary-learning'],
        query,
        profile,
    ) == ['research/deep-research', 'vocabulary/vocabulary-learning']


def test_simple_fact_and_transform_requests_keep_skills_available():
    for query in ('什么是向量数据库', '把这段话翻译成英文'):
        profile, _ = _rule_profile(query)

        assert profile.skill_mode == 'candidates'


def test_trivial_chat_is_the_narrow_rule_based_suppression_case():
    assert resolve_task_profile('你好').skill_mode == 'suppress'
    assert resolve_task_profile('测试').skill_mode == 'suppress'


def test_explicit_request_can_suppress_all_skills():
    assert resolve_task_profile('不要使用任何 Skill，直接回答').skill_mode == 'suppress'


def test_question_about_not_using_skill_is_not_suppression():
    assert resolve_task_profile('你为什么不用单词skill的复习能力呢').skill_mode != 'suppress'


def test_classifier_cannot_suppress_skills_without_explicit_user_request():
    profile = resolve_task_profile(
        '帮我研究这个问题',
        classifier=lambda _prompt: '{"primary_outcome":"research","skill_mode":"suppress"}',
        thinking_depth='high',
    )

    assert profile.skill_mode == 'candidates'


def test_router_failure_falls_back_to_candidates_instead_of_suppressing_skills():
    def fail_router(_prompt):
        raise RuntimeError('router unavailable')

    profile = resolve_task_profile(
        '帮我看看这个问题应该怎么解决',
        classifier=fail_router,
        thinking_depth='high',
    )

    assert profile.source == 'fallback'
    assert profile.skill_mode == 'candidates'
