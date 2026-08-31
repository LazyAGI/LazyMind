from __future__ import annotations

import lazyllm
from lazyllm.tools import inject_env_vars

from lazymind.chat.engine.agent_runtime import AgentRole, PromptBuilder
from lazymind.chat.engine.prompts.system_prompt import build_system_prompt
from lazymind.chat.engine.tools.session_env import (
    build_session_env_tool,
    redact_session_env_arguments,
)
from lazymind.chat.service.chat_service import clear_conversation_env
from lazymind.chat.service.component.tool_registry import (
    ASK_USER_TOOL_CONFIG,
    SESSION_ENV_QUERY_APPENDIX,
    SESSION_ENV_TOOL_POLICY_APPENDIX,
    build_session_env_tool_config,
    collect_query_appendices,
    collect_system_prompt_appendices,
)
from lazymind.chat.service.component.tool_rendering import _tool_call_frame_text


def _restore_dynamic_env(old_dynamic_env):
    if old_dynamic_env is None:
        lazyllm.globals.pop('dynamic_env_vars', None)
    else:
        lazyllm.globals['dynamic_env_vars'] = old_dynamic_env


def test_set_session_env_updates_store_and_runtime_without_echoing_secret():
    store: dict[str, dict[str, str]] = {}
    tool = build_session_env_tool(store, 'conversation-1')
    old_dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    lazyllm.globals['dynamic_env_vars'] = {}
    try:
        result = tool('REDFOX_API_KEY', 'secret-value')
        dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    finally:
        _restore_dynamic_env(old_dynamic_env)

    assert result['status'] == 'ok'
    assert result['name'] == 'REDFOX_API_KEY'
    assert result['value_set'] is True
    assert 'secret-value' not in str(result)
    assert store['conversation-1']['REDFOX_API_KEY'] == 'secret-value'
    assert dynamic_env['REDFOX_API_KEY'] == 'secret-value'


def test_set_session_env_rejects_reserved_names():
    store: dict[str, dict[str, str]] = {}
    tool = build_session_env_tool(store, 'conversation-1')

    result = tool('PATH', '/tmp/bin')
    proxy = tool('HTTP_PROXY', 'http://evil.example')
    bash_env = tool('BASH_ENV', '/tmp/hook.sh')

    assert result['status'] == 'error'
    assert result['error_type'] == 'InvalidEnvName'
    assert proxy['error_type'] == 'InvalidEnvName'
    assert bash_env['error_type'] == 'InvalidEnvName'
    assert store == {}


def test_set_session_env_rejects_invalid_name_and_empty_value():
    store: dict[str, dict[str, str]] = {}
    tool = build_session_env_tool(store, 'conversation-1')

    invalid_name = tool('RED FOX', 'secret-value')
    empty_value = tool('REDFOX_API_KEY', '  ')

    assert invalid_name['error_type'] == 'InvalidEnvName'
    assert empty_value['error_type'] == 'InvalidEnvValue'
    assert store == {}


def test_set_session_env_uses_globals_sid_when_conversation_id_missing():
    store: dict[str, dict[str, str]] = {}
    tool = build_session_env_tool(store, '')
    previous_sid = lazyllm.globals._sid
    old_dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    lazyllm.globals._init_sid('fallback-sid')
    lazyllm.globals['dynamic_env_vars'] = {}
    try:
        result = tool('REDFOX_API_KEY', 'secret-value')
    finally:
        _restore_dynamic_env(old_dynamic_env)
        lazyllm.globals._init_sid(previous_sid)

    assert result['status'] == 'ok'
    assert result['conversation_id'] == 'fallback-sid'
    assert store['fallback-sid']['REDFOX_API_KEY'] == 'secret-value'


def test_session_env_rehydrates_into_new_request_sid():
    store: dict[str, dict[str, str]] = {}
    previous_sid = lazyllm.globals._sid
    old_turn1 = None
    old_turn2 = None
    lazyllm.globals._init_sid('turn-1')
    old_turn1 = lazyllm.globals.get('dynamic_env_vars')
    lazyllm.globals['dynamic_env_vars'] = {}
    try:
        tool = build_session_env_tool(store, 'conversation-1')
        tool('REDFOX_API_KEY', 'secret-value')
        lazyllm.globals._init_sid('turn-2')
        old_turn2 = lazyllm.globals.get('dynamic_env_vars')
        inject_env_vars(store.get('conversation-1'))
        assert lazyllm.globals.get('dynamic_env_vars')['REDFOX_API_KEY'] == 'secret-value'
    finally:
        lazyllm.globals._init_sid('turn-2')
        _restore_dynamic_env(old_turn2)
        lazyllm.globals._init_sid('turn-1')
        _restore_dynamic_env(old_turn1)
        lazyllm.globals._init_sid(previous_sid)


def test_session_env_tool_config_name_matches_function():
    config = build_session_env_tool_config({}, 'conversation-1')

    assert config.name == 'set_session_env'
    assert config.tool.__name__ == 'set_session_env'
    assert config.appendix_system_prompt is SESSION_ENV_TOOL_POLICY_APPENDIX
    assert config.appendix_query is SESSION_ENV_QUERY_APPENDIX


def test_session_env_policy_is_injected_when_tool_is_exposed():
    config = build_session_env_tool_config({}, 'conversation-1')
    appendices = collect_system_prompt_appendices([config, ASK_USER_TOOL_CONFIG])
    prompt = build_system_prompt(True, tool_prompt_appendices=appendices)

    assert 'this conversation only' in prompt
    assert 'attempt the skill first' in prompt
    assert 'missing_env' in prompt
    assert 'call it once with `type=text`' in prompt
    assert 'call `set_session_env` then immediately retry' in prompt
    assert 'Never echo secret values' in prompt


def test_session_env_query_appendix_follows_user_input():
    config = build_session_env_tool_config({}, 'conversation-1')
    appendix = '\n'.join(collect_query_appendices([config]))
    bundle = (
        PromptBuilder.for_role(AgentRole.CHAT)
        .runtime(
            'tools', 'Active Tool Instructions', appendix, 'tool.registry',
            authoritative=True, placement='after_input',
        )
        .input('REDFOX_API_KEY=secret-value', source='user')
        .build()
    )

    assert 'call `set_session_env` first' in bundle.current_input
    assert 'retry the interrupted skill' in bundle.current_input
    assert 'apply only to this conversation' in bundle.current_input
    assert bundle.current_input.index('set_session_env') > bundle.current_input.index(
        'REDFOX_API_KEY=secret-value'
    )
    assert 'set_session_env' not in bundle.system_prompt
    assert collect_query_appendices([config], 'before') == []


def test_set_session_env_is_scoped_to_conversation():
    store: dict[str, dict[str, str]] = {}
    tool_a = build_session_env_tool(store, 'conversation-a')
    tool_b = build_session_env_tool(store, 'conversation-b')
    old_dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    lazyllm.globals['dynamic_env_vars'] = {}
    try:
        result_a = tool_a('REDFOX_API_KEY', 'secret-a')
        result_b = tool_b('REDFOX_API_KEY', 'secret-b')
        dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    finally:
        _restore_dynamic_env(old_dynamic_env)

    assert result_a['status'] == 'ok'
    assert result_b['status'] == 'ok'
    assert store['conversation-a']['REDFOX_API_KEY'] == 'secret-a'
    assert store['conversation-b']['REDFOX_API_KEY'] == 'secret-b'
    assert dynamic_env['REDFOX_API_KEY'] == 'secret-b'


def test_session_env_arguments_are_redacted_in_tool_call_frames():
    redacted = redact_session_env_arguments(
        'set_session_env',
        {'name': 'REDFOX_API_KEY', 'value': 'secret-value'},
    )
    call_text, preview = _tool_call_frame_text({
        'id': 'call-env-1',
        'function': {
            'name': 'set_session_env',
            'arguments': {'name': 'REDFOX_API_KEY', 'value': 'secret-value'},
        },
    }, 'en')

    assert redacted['value'] == '<redacted>'
    assert redacted['name'] == 'REDFOX_API_KEY'
    assert 'secret-value' not in call_text
    assert 'REDFOX_API_KEY' in call_text
    assert preview == 'REDFOX_API_KEY'


def test_normalize_history_redacts_session_env_tool_arguments():
    import json
    from lazymind.chat.service.component.history import normalize_history_for_agent

    call_payload = json.dumps({
        'id': 'call-1',
        'name': 'set_session_env',
        'arguments': {'name': 'REDFOX_API_KEY', 'value': 'secret-value'},
    }, ensure_ascii=False, separators=(',', ':'))
    result_payload = json.dumps({
        'id': 'call-1',
        'name': 'set_session_env',
        'result': {'status': 'ok', 'name': 'REDFOX_API_KEY', 'value_set': True},
    }, ensure_ascii=False, separators=(',', ':'))
    normalized = normalize_history_for_agent([
        {
            'role': 'assistant',
            'content': (
                f'<tool_call>{call_payload}</tool_call>'
                f'<tool_result>{result_payload}</tool_result>'
            ),
        },
    ])

    arguments = json.loads(normalized[0]['tool_calls'][0]['function']['arguments'])
    assert arguments['name'] == 'REDFOX_API_KEY'
    assert arguments['value'] == '<redacted>'
    assert 'secret-value' not in json.dumps(normalized)


def test_clear_conversation_env_drops_only_that_conversation():
    from lazymind.chat.service import chat_service

    previous = dict(chat_service._conversation_env_vars)
    chat_service._conversation_env_vars.clear()
    chat_service._conversation_env_vars['conversation-1'] = {'REDFOX_API_KEY': 'secret'}
    chat_service._conversation_env_vars['conversation-2'] = {'OTHER_KEY': 'keep'}
    try:
        assert clear_conversation_env('conversation-1') is True
        assert clear_conversation_env('conversation-1') is False
        assert 'conversation-1' not in chat_service._conversation_env_vars
        assert chat_service._conversation_env_vars['conversation-2'] == {'OTHER_KEY': 'keep'}
    finally:
        chat_service._conversation_env_vars.clear()
        chat_service._conversation_env_vars.update(previous)
