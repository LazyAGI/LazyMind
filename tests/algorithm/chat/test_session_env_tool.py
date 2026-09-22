from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace

import lazyllm
import pytest

from lazymind.chat.engine.tools.session_env import (
    ConversationEnvStore,
    build_session_env_tool,
    build_user_env_tool,
    inject_runtime_env,
    redact_session_env_arguments,
)
from lazymind.chat.service.chat_service import clear_conversation_env
from lazymind.chat.service import chat_service
from lazymind.chat.service.chat_request import ChatRequest
from lazymind.chat.service.component.tool_registry import (
    SESSION_ENV_QUERY_APPENDIX,
    SESSION_ENV_TOOL_POLICY_APPENDIX,
    USER_ENV_TOOL_CONFIG,
    build_session_env_tool_config,
)
from lazymind.chat.service.component.tool_rendering import _tool_call_frame_text, _tool_result_preview
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator


@pytest.fixture(autouse=True)
def isolate_env_overrides():
    sid = lazyllm.globals._sid
    previous = lazyllm.globals.get('conversation_env_overrides')
    lazyllm.globals['conversation_env_overrides'] = {}
    yield
    lazyllm.globals._init_sid(sid)
    if previous is None:
        lazyllm.globals.pop('conversation_env_overrides', None)
    else:
        lazyllm.globals['conversation_env_overrides'] = previous


def _restore_dynamic_env(old_dynamic_env):
    if old_dynamic_env is None:
        lazyllm.globals.pop('dynamic_env_vars', None)
    else:
        lazyllm.globals['dynamic_env_vars'] = old_dynamic_env


@pytest.mark.parametrize('scope', ['user', 'conversation'])
@pytest.mark.parametrize('value', [' leading-password', 'trailing-password ', ' both-password '])
def test_env_tools_preserve_credential_whitespace(monkeypatch, scope, value):
    calls = []

    def save(path, payload):
        calls.append(payload)
        return {'response': {'data': {'enabled': True}}}

    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.post_core_api', save)
    backing = {}
    tool = build_user_env_tool() if scope == 'user' else build_session_env_tool(backing, 'whitespace-test')
    old = lazyllm.globals.get('dynamic_env_vars')
    lazyllm.globals['dynamic_env_vars'] = {}
    try:
        result = tool('SERVICE_PASSWORD', value)
        assert result['status'] == 'ok'
        assert lazyllm.globals['dynamic_env_vars']['SERVICE_PASSWORD'] == value
        if scope == 'user':
            assert calls[0]['value'] == value
        else:
            assert backing['whitespace-test']['SERVICE_PASSWORD'] == value
    finally:
        _restore_dynamic_env(old)


@pytest.mark.parametrize('scope', ['user', 'conversation'])
@pytest.mark.parametrize('value', ['', '   ', '\t\n', 'bad\0value'])
def test_env_tools_reject_empty_or_invalid_credentials(monkeypatch, scope, value):
    def unexpected_save(*args, **kwargs):
        pytest.fail('invalid credential reached persistence')

    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.post_core_api', unexpected_save)
    backing = {}
    tool = build_user_env_tool() if scope == 'user' else build_session_env_tool(backing, 'invalid-value-test')
    assert tool('SERVICE_PASSWORD', value)['error_type'] == 'InvalidEnvValue'
    assert not backing


@pytest.mark.parametrize('has_user_env', [True, False])
def test_chat_env_availability_matches_runtime_without_exposing_values(monkeypatch, has_user_env):
    store = ConversationEnvStore()
    store.set('env-prompt-conversation', 'REDFOX_API_KEY', 'session-override-secret')
    monkeypatch.setattr(chat_service, '_conversation_env_store', store)
    monkeypatch.setattr(chat_service, 'AutoModel', lambda *_args, **_kwargs: object())
    observed_env = []

    def create_agent(self, llm, plan):
        observed_env.append(dict(lazyllm.globals.get('dynamic_env_vars', {})))
        return SimpleNamespace(describe_context=lambda *_args: {})

    monkeypatch.setattr(chat_service.AgentExecutor, 'create_agent', create_agent)
    user_env = {'REDFOX_API_KEY': 'user-secret', 'service_token': 'lowercase-secret'} if has_user_env else {}
    result = asyncio.run(chat_service.handle_chat(ChatRequest(
        message={'query': 'Is REDFOX_API_KEY configured?', 'history': []},
        conversation={
            'session_id': 'env-prompt-session',
            'conversation_id': 'env-prompt-conversation',
            'user_id': 'env-prompt-user',
        },
        runtime={'user_env_vars': user_env, 'context_prompt_export': True, 'llm_config': {}},
        personalization={'use_memory': False},
        agent={'available_skills': [], 'enable_subagent': False},
        workflow={'enable_workflow': False},
    )))

    prompt = result['prompt_markdown']
    assert 'Available Environment Variables' in prompt
    assert 'Conversation-level variables: ["REDFOX_API_KEY"]' in prompt
    assert 'session-override-secret' not in prompt
    assert 'user-secret' not in prompt
    assert 'lowercase-secret' not in prompt
    expected = {'REDFOX_API_KEY': 'session-override-secret'}
    if has_user_env:
        assert 'Enabled user-level variables: ["REDFOX_API_KEY", "service_token"]' in prompt
        expected['service_token'] = 'lowercase-secret'
    else:
        assert 'Enabled user-level variables: []' in prompt
        assert 'service_token' not in prompt
    assert observed_env == [expected]


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


def test_conversation_env_store_supports_get_many_and_clear():
    backing: dict[str, dict[str, str]] = {}
    store = ConversationEnvStore(backing)

    store.set('conversation-1', 'REDFOX_API_KEY', 'secret-value')
    store.set('conversation-1', 'REDFOX_TOKEN', 'token-value')

    assert store.get_many('conversation-1', ['REDFOX_API_KEY']) == {
        'REDFOX_API_KEY': 'secret-value',
    }
    assert backing['conversation-1']['REDFOX_TOKEN'] == 'token-value'
    assert store.clear('conversation-1') is True
    assert store.clear('conversation-1') is False
    assert backing == {}


@pytest.mark.parametrize('scope', ['conversation', 'user'])
def test_redaction_placeholder_cannot_overwrite_configuration(monkeypatch, scope):
    from lazymind.chat.engine.tools import session_env

    def unexpected_request(*args, **kwargs):
        pytest.fail('redacted values must be rejected before contacting Core')

    monkeypatch.setattr(session_env, 'post_core_api', unexpected_request)
    backing = {'test-conversation': {'CODEX_E2E_TOKEN': 'existing-value'}}
    tool = build_user_env_tool() if scope == 'user' else build_session_env_tool(backing, 'test-conversation')
    result = tool('CODEX_E2E_TOKEN', '<redacted>')
    assert result['status'] == 'error'
    assert result['error_type'] == 'RedactedEnvValue'
    assert backing['test-conversation']['CODEX_E2E_TOKEN'] == 'existing-value'


@pytest.mark.parametrize('tool_name', ['set_session_env', 'set_user_env'])
def test_env_business_error_is_not_rendered_as_success(tool_name):
    result = {'ok': True, 'value': {'status': 'error', 'name': 'CODEX_E2E_TOKEN', 'error_type': 'RedactedEnvValue'}}
    preview = _tool_result_preview(tool_name, result, language='zh')
    assert '已可用于' not in preview
    assert '已调用完成' not in preview
    assert '未能' in preview


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


def test_set_session_env_rejects_non_credential_and_runtime_control_names():
    store: dict[str, dict[str, str]] = {}
    tool = build_session_env_tool(store, 'conversation-1')

    plain_config = tool('OPENAI_ORG_ID', 'org-1')
    runtime_config = tool('MODEL_CONFIG_TOKEN', 'secret-value')
    cert_path = tool('CUSTOM_CERT_SECRET', 'secret-value')

    assert plain_config['error_type'] == 'InvalidEnvName'
    assert 'credential name' in plain_config['error']
    assert runtime_config['error_type'] == 'InvalidEnvName'
    assert 'runtime behavior' in runtime_config['error']
    assert cert_path['error_type'] == 'InvalidEnvName'
    assert 'runtime behavior' in cert_path['error']
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


def test_session_env_tool_config_name_matches_function():
    config = build_session_env_tool_config({}, 'conversation-1')

    assert config.name == 'set_session_env'
    assert config.tool.__name__ == 'set_session_env'
    assert config.appendix_system_prompt is SESSION_ENV_TOOL_POLICY_APPENDIX
    assert config.appendix_query is SESSION_ENV_QUERY_APPENDIX


def test_session_env_instructions_defer_explicit_persistence_to_user_tool():
    tool = build_session_env_tool({}, 'conversation-1')
    for instructions in (tool.__doc__, SESSION_ENV_TOOL_POLICY_APPENDIX['tool_policy']):
        instructions = ' '.join(instructions.split())
        assert 'use `set_user_env` instead' in instructions
        assert 'do not also create a conversation override' in instructions
        assert 'only to temporary setup' in instructions


def test_runtime_env_replaces_stale_credentials_and_preserves_explicit_override():
    old = lazyllm.globals.get('dynamic_env_vars')
    user_vars = {'Mixed_API_KEY': 'user-secret', 'USER_TOKEN': 'other-secret'}
    conversation_vars = {'Mixed_API_KEY': 'conversation-secret'}
    try:
        lazyllm.globals['dynamic_env_vars'] = {'STALE_TOKEN': 'stale-secret'}
        inject_runtime_env(user_vars, conversation_vars)
        assert lazyllm.globals['dynamic_env_vars'] == {
            'Mixed_API_KEY': 'conversation-secret', 'USER_TOKEN': 'other-secret',
        }
        assert lazyllm.globals['conversation_env_overrides'] == conversation_vars
        assert user_vars['Mixed_API_KEY'] == 'user-secret'
        inject_runtime_env(None)
        assert lazyllm.globals['dynamic_env_vars'] == {}
        assert lazyllm.globals['conversation_env_overrides'] == {}
    finally:
        _restore_dynamic_env(old)


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


def test_user_env_arguments_are_redacted_in_tool_call_frames():
    redacted = redact_session_env_arguments(
        'set_user_env',
        {'name': 'TAVILY_API_KEY', 'value': 'secret-value', 'description': 'search'},
    )
    call_text, preview = _tool_call_frame_text({
        'id': 'call-user-env-1',
        'function': {
            'name': 'set_user_env',
            'arguments': {'name': 'TAVILY_API_KEY', 'value': 'secret-value', 'description': 'search'},
        },
    }, 'en')

    assert redacted['name'] == 'TAVILY_API_KEY'
    assert redacted['value'] == '<redacted>'
    assert redacted['description'] == '<redacted>'
    assert 'secret-value' not in call_text
    assert 'TAVILY_API_KEY' in call_text
    assert preview == 'TAVILY_API_KEY'


@pytest.mark.parametrize('tool_name', ['set_session_env', 'set_user_env'])
@pytest.mark.parametrize('language', ['en', 'zh'])
@pytest.mark.parametrize('arguments', [
    '{"name":"TEST_TOKEN","value":"synthetic-malformed-secret',
    '"synthetic-malformed-secret"',
    '["synthetic-malformed-secret"]',
    ['synthetic-malformed-secret'],
    {'name': 'TEST_TOKEN', 'value': 'synthetic-malformed-secret'},
])
def test_env_argument_redaction_covers_non_object_arguments(tool_name, language, arguments):
    call = {'id': 'malformed-env', 'function': {'name': tool_name, 'arguments': arguments}}
    original = json.dumps(call)
    call_text, preview = _tool_call_frame_text(call, language)
    assert 'synthetic-malformed-secret' not in call_text
    assert 'synthetic-malformed-secret' not in preview

    translator = AgentEventFrameTranslator(query='设置变量' if language == 'zh' else 'Set variable')
    frames = translator.feed({'tag': 'tool_calls', 'tool_calls': [call]})
    frames.extend(translator.feed({'tag': 'tool_results', 'tool_results': [{
        'id': 'malformed-env', 'name': tool_name,
        'result': {'ok': False, 'value': 'Invalid arguments'},
    }]}))
    assert 'synthetic-malformed-secret' not in json.dumps(frames)
    assert json.dumps(call) == original


def test_user_env_tool_config_is_persistent_tool():
    from lazyllm.tools.agent.toolsManager import ToolManager

    assert USER_ENV_TOOL_CONFIG.name == 'set_user_env'
    assert USER_ENV_TOOL_CONFIG.tool.__name__ == 'set_user_env'
    assert 'persist' in USER_ENV_TOOL_CONFIG.description_en.lower()
    parameters = ToolManager([USER_ENV_TOOL_CONFIG.tool]).tools_description[0]['function']['parameters']
    assert set(parameters['required']) == {'name', 'value'}
    assert {'type': 'null'} in parameters['properties']['enabled']['anyOf']
    assert {'type': 'null'} in parameters['properties']['description']['anyOf']


def test_set_user_env_calls_core_and_updates_runtime(monkeypatch):
    calls = []

    def fake_post_core_api(path, payload, *, user_id=None):
        calls.append((path, payload, user_id))
        return {'response': {'data': {'name': payload['name'], 'masked_value': 'tvly****3456'}}}

    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.post_core_api', fake_post_core_api)
    old_dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    lazyllm.globals['dynamic_env_vars'] = {}
    try:
        result = build_user_env_tool()('tavily_api_key', 'tvly-secret-3456', 'search', True)
        dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    finally:
        _restore_dynamic_env(old_dynamic_env)

    assert result['status'] == 'ok'
    assert result['scope'] == 'user'
    assert result['name'] == 'tavily_api_key'
    assert calls == [('/user/env-vars', {
        'name': 'tavily_api_key',
        'value': 'tvly-secret-3456',
        'description': 'search',
        'enabled': True,
    }, None)]
    assert dynamic_env['tavily_api_key'] == 'tvly-secret-3456'
    assert 'tvly-secret-3456' not in str(result)


def test_set_user_env_updates_existing_var_on_conflict(monkeypatch):
    from lazymind.chat.engine.tools.infra.core_api_client import CoreAPIError

    calls = []

    def fake_post_core_api(path, payload, *, user_id=None):
        calls.append(('post', path, payload, user_id))
        raise CoreAPIError('POST', 'http://core/user/env-vars', 409, {'message': 'env name already exists'})

    def fake_get_core_api(path, params=None, *, user_id=None):
        calls.append(('get', path, params, user_id))
        return {'items': [{'id': 'env_existing', 'name': 'TAVILY_API_KEY'}]}

    def fake_patch_core_api(path, payload, *, user_id=None):
        calls.append(('patch', path, payload, user_id))
        return {'response': {'data': {'name': payload['name'], 'masked_value': 'tvly****3456'}}}

    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.post_core_api', fake_post_core_api)
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.get_core_api', fake_get_core_api)
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.patch_core_api', fake_patch_core_api)

    result = build_user_env_tool()('TAVILY_API_KEY', 'tvly-secret-3456')

    assert result['status'] == 'ok'
    assert result['action'] == 'updated'
    assert calls[0][0] == 'post'
    assert calls[1] == ('get', '/user/env-vars', None, None)
    assert calls[2][0] == 'patch'
    assert calls[2][1] == '/user/env-vars/env_existing'


@pytest.mark.parametrize('enabled', [False, True])
@pytest.mark.parametrize('changes', [{}, {'description': '', 'enabled': True}, {'enabled': False}])
def test_user_env_key_update_preserves_unspecified_metadata(monkeypatch, enabled, changes):
    from unittest.mock import Mock
    from lazymind.chat.engine.tools.infra.core_api_client import CoreAPIError

    root = 'lazymind.chat.engine.tools.session_env.'
    post = Mock(side_effect=CoreAPIError('POST', 'http://test', 409, 'duplicate'))
    monkeypatch.setattr(root + 'post_core_api', post)
    monkeypatch.setattr(root + 'get_core_api', lambda *_: {'items': [{
        'id': 'existing-env', 'name': 'TEST_TOKEN', 'enabled': enabled, 'description': 'keep note',
    }]})
    expected_enabled = changes.get('enabled', enabled)
    update = Mock(return_value={'response': {'data': {'enabled': expected_enabled}}})
    monkeypatch.setattr(root + 'patch_core_api', update)
    old = lazyllm.globals.get('dynamic_env_vars')
    lazyllm.globals['dynamic_env_vars'] = {'TEST_TOKEN': 'old-synthetic-value'}
    try:
        result = build_user_env_tool()('TEST_TOKEN', 'new-synthetic-value', **changes)
        assert result['status'] == 'ok'
        assert result['enabled'] is expected_enabled
        assert update.call_args.args[1] == {'name': 'TEST_TOKEN', 'value': 'new-synthetic-value', **changes}
        assert lazyllm.globals['dynamic_env_vars'].get('TEST_TOKEN') == (
            'new-synthetic-value' if expected_enabled else None
        )
    finally:
        _restore_dynamic_env(old)


@pytest.mark.parametrize('has_values', [False, True])
def test_cleared_conversation_rejects_old_tools_but_allows_new_turn(has_values):
    store = ConversationEnvStore()
    _, lease = store.snapshot('cleared-conversation')
    old_tool = build_session_env_tool(store, 'cleared-conversation', lease)
    other_tool = build_session_env_tool(store, 'other-conversation')
    if has_values:
        store.set('cleared-conversation', 'TEST_TOKEN', 'previous-value')
    store.clear('cleared-conversation')
    # A turn may be cleared between taking its snapshot and constructing its tools.
    late_tool = build_session_env_tool(store, 'cleared-conversation', lease)
    new_tool = build_session_env_tool(store, 'cleared-conversation')
    old_dynamic = lazyllm.globals.get('dynamic_env_vars')
    lazyllm.globals['dynamic_env_vars'] = {}
    try:
        for tool in (old_tool, late_tool):
            assert tool('TEST_TOKEN', 'late-value')['error_type'] == 'StaleConversation'
        assert store.get_many('cleared-conversation') == {}
        assert lazyllm.globals['dynamic_env_vars'] == {}
        assert other_tool('TEST_TOKEN', 'other-value')['status'] == 'ok'
        assert new_tool('TEST_TOKEN', 'new-value')['status'] == 'ok'
        assert old_tool('TEST_TOKEN', 'late-value')['error_type'] == 'StaleConversation'
        assert store.get_many('cleared-conversation') == {'TEST_TOKEN': 'new-value'}
        assert store.get_many('other-conversation') == {'TEST_TOKEN': 'other-value'}
    finally:
        _restore_dynamic_env(old_dynamic)


def test_unused_conversation_leases_are_not_retained():
    import gc
    import weakref

    store = ConversationEnvStore()
    _, lease = store.snapshot('no-credentials')
    reference = weakref.ref(lease)
    del lease
    gc.collect()
    assert reference() is None
    assert len(store._leases) == 0


@pytest.mark.parametrize('has_override', [True, False])
def test_user_env_changes_keep_session_precedence_and_clear_disabled_values(monkeypatch, has_override):
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.post_core_api', lambda *_: {})
    old = lazyllm.globals.get('dynamic_env_vars')
    lazyllm.globals['dynamic_env_vars'] = {}
    try:
        if has_override:
            build_session_env_tool(ConversationEnvStore(), 'priority-test')('TEST_API_KEY', 'session-secret')
        tool = build_user_env_tool()
        tool('TEST_API_KEY', 'user-secret')
        assert lazyllm.globals['dynamic_env_vars']['TEST_API_KEY'] == ('session-secret' if has_override else 'user-secret')
        result = tool('TEST_API_KEY', 'replacement-secret', enabled=False)
        assert result['enabled'] is False
        assert result['available_to'] == []
        assert lazyllm.globals['dynamic_env_vars'].get('TEST_API_KEY') == ('session-secret' if has_override else None)
    finally:
        _restore_dynamic_env(old)


def test_user_env_result_and_errors_never_echo_management_payload(monkeypatch):
    secret = 'private-test-value'
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.post_core_api', lambda *_: {
        'response': {'data': {'masked_value': secret, 'description': secret, 'value': secret}},
    })
    old = lazyllm.globals.get('dynamic_env_vars')
    try:
        result = build_user_env_tool()('TEST_API_KEY', secret, description=secret)
        assert secret not in str(result)
        assert 'response' not in result

        def fail(*_):
            raise RuntimeError(secret)

        monkeypatch.setattr('lazymind.chat.engine.tools.session_env.post_core_api', fail)
        assert secret not in str(build_user_env_tool()('TEST_API_KEY', secret))
    finally:
        _restore_dynamic_env(old)


def test_env_tools_reject_nul_before_storing(monkeypatch):
    from unittest.mock import Mock
    post = Mock()
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.post_core_api', post)
    store = ConversationEnvStore()
    assert build_session_env_tool(store, 'nul-test')('TEST_API_KEY', 'bad\0value')['status'] == 'error'
    assert store.get_many('nul-test') == {}
    assert build_user_env_tool()('TEST_API_KEY', 'bad\0value')['status'] == 'error'
    post.assert_not_called()


def test_session_env_json_string_arguments_are_redacted():
    redacted = redact_session_env_arguments(
        'set_session_env',
        '{"name":"REDFOX_API_KEY","value":"secret-value"}',
    )

    assert redacted == {'name': 'REDFOX_API_KEY', 'value': '<redacted>'}


def test_ask_words_cloze_answers_are_redacted_in_tool_call_frames():
    arguments = {
        'session_id': 'session-1',
        'mode': 'create',
        'type': 'cloze',
        'questions': [{
            'text': 'A ____ team.',
            'correct_answer': 'diverse',
            'grading_criteria': 'must equal diverse',
        }],
    }
    redacted = redact_session_env_arguments('ask_words', arguments)
    call_text, _ = _tool_call_frame_text({
        'id': 'call-cloze-1',
        'function': {'name': 'ask_words', 'arguments': arguments},
    }, 'en')

    assert redacted['questions'] == [{
        'text': 'A ____ team.',
        'correct_answer': '<redacted>',
    }]
    assert 'diverse' not in call_text
    assert 'grading_criteria' not in call_text


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
