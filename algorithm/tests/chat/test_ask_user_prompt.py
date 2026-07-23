from lazymind.chat.engine.prompts.guidance import CLARIFICATION_GUIDANCE
from lazymind.chat.engine.prompts.system_prompt import build_system_prompt
from lazymind.chat.service.component.tool_registry import (
    ASK_USER_TOOL_CONFIG,
    collect_system_prompt_appendices,
)


def test_ask_user_contract_is_injected_when_tool_is_exposed():
    appendices = collect_system_prompt_appendices([ASK_USER_TOOL_CONFIG])

    prompt = build_system_prompt(True, tool_prompt_appendices=appendices)

    assert 'ask_user` is the ONLY valid channel' in prompt
    assert 'if you choose to ask, `ask_user` is the ONLY valid channel' in prompt
    assert 'does not depend on necessity' in prompt
    assert 'never permission to ask in plain text' in prompt
    assert '`ask_user` is more reliable than a plain-text question' in prompt
    assert 'For divergent or open-ended questions' in prompt
    assert 'user can edit each one' in prompt
    assert 'set `allow_other=false`' in prompt
    assert 'asking is part of the requested task' in prompt


def test_clarification_module_does_not_offer_text_fallback_when_tool_exists():
    assert 'MUST be sent by calling it' in CLARIFICATION_GUIDANCE
    assert 'Only when the tool is absent' in CLARIFICATION_GUIDANCE
