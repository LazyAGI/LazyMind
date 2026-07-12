from lazymind.chat.service.component.mutation_retry import (
    build_mutation_retry_query,
    detect_durable_mutation_tool,
    mutation_failure_message,
)


def test_detects_explicit_profile_mutation() -> None:
    assert detect_durable_mutation_tool(
        '请把我的工作邮箱 qa@example.invalid 保存到用户画像。'
    ) == 'memory_editor'


def test_detects_explicit_vocab_mutation() -> None:
    assert detect_durable_mutation_tool(
        '请记住词表映射：星河协议就是 Galaxy Protocol。'
    ) == 'vocab_learn'


def test_ignores_non_durable_or_unrelated_requests() -> None:
    assert detect_durable_mutation_tool('我的邮箱是什么？') is None
    assert detect_durable_mutation_tool('请保存这份 PDF 文件。') is None
    assert detect_durable_mutation_tool('解释一下什么是同义词。') is None


def test_retry_query_requires_structured_success() -> None:
    retry = build_mutation_retry_query('记住这个词表映射', 'vocab_learn')

    assert 'Call `vocab_learn` now' in retry
    assert '`success: true`' in retry
    assert 'Original User Request' in retry


def test_failure_message_never_claims_success() -> None:
    message = mutation_failure_message('请保存到用户画像', 'memory_editor')

    assert '未能' in message
    assert '没有返回成功结果' in message
