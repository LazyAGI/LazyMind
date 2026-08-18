from __future__ import annotations

import lazyllm
from lazyllm.tools import inject_env_vars

from lazymind.chat.engine.tools.session_env import build_session_env_tool
from lazymind.chat.service.component.tool_registry import build_session_env_tool_config


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

    assert result['status'] == 'error'
    assert result['error_type'] == 'InvalidEnvName'
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
    assert config.appendix_system_prompt is None
