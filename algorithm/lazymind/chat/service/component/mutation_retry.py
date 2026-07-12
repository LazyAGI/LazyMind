from __future__ import annotations

import re


_PERSIST_VERB = re.compile(
    r'(记住|保存|记录|写入|更新|删除|移除|忘记|remember|save|record|persist|update|delete|remove|forget)',
    re.IGNORECASE,
)
_VOCAB_TARGET = re.compile(
    r'(词表|词汇|同义词|术语|映射|等同于|就是|vocab(?:ulary)?|glossary|synonym|terminology|mapping|means|same as|equivalent)',
    re.IGNORECASE,
)
_MEMORY_TARGET = re.compile(
    r'(用户画像|个人画像|用户偏好|个人偏好|工作邮箱|邮箱|手机号|电话号码|称呼|回复风格|'
    r'user profile|preference|work email|email address|phone number|address me|call me)',
    re.IGNORECASE,
)


def detect_durable_mutation_tool(query: str) -> str | None:
    """Return the required durable-write tool for an explicit supported intent."""
    text = str(query or '').strip()
    if not text or not _PERSIST_VERB.search(text):
        return None
    if _VOCAB_TARGET.search(text):
        return 'vocab_learn'
    if _MEMORY_TARGET.search(text):
        return 'memory_editor'
    return None


def build_mutation_retry_query(query: str, tool_name: str) -> str:
    return (
        '## Durable mutation retry [AUTHORITATIVE]\n'
        f'The user explicitly requested a durable mutation that requires `{tool_name}`, '
        'but the previous attempt did not produce a successful structured result from that tool.\n'
        f'Call `{tool_name}` now. Do not merely describe what you would do. '
        'If an argument error is returned, correct it once. Report success only after '
        '`success: true`; otherwise state that the mutation was not saved.\n\n'
        f'## Original User Request\n{query}'
    )


def mutation_failure_message(query: str, tool_name: str) -> str:
    if re.search(r'[\u3400-\u9fff]', str(query or '')):
        target = '用户画像' if tool_name == 'memory_editor' else '词表'
        return f'未能将这次修改保存到{target}；对应持久化工具没有返回成功结果。请稍后重试。'
    target = 'user profile' if tool_name == 'memory_editor' else 'vocabulary'
    return (
        f'This change was not saved to your {target}: '
        'the durable-write tool did not return a successful result. Please try again later.'
    )
