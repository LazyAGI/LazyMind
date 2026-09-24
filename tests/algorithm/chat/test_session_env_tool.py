from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace
from pathlib import Path
from fastapi import FastAPI
from fastapi.testclient import TestClient
from lazyllm.tools import ToolManager
from lazyllm.tools.agent.toolError import ToolExecutionError

from lazymind.chat.engine.agent_runtime.conversation_env import ConversationEnvStore
from lazymind.chat.engine.agent_runtime.env_runtime import inject_runtime_env
from lazymind.chat.engine.agent_runtime.env_policy import validate_env_name
from lazymind.chat.engine.agent_runtime.env_redaction import redact_session_env_arguments
from lazymind.chat.engine.agent_runtime.env_input import session_env_inputs
from lazymind.chat.api import agent_control_routes

import lazyllm
import pytest

from lazymind.chat.engine.tools.session_env import (
    build_session_env_tool,
    build_user_env_tool,
    build_delete_session_env_tool,
    build_delete_user_env_tool,
)
from lazymind.chat.service.chat_service import clear_conversation_env
from lazymind.chat.service import chat_service
from lazymind.chat.service.chat_request import ChatRequest
from lazymind.chat.service.component.tool_registry import (
    SESSION_ENV_QUERY_APPENDIX,
    USER_ENV_TOOL_CONFIG,
    build_session_env_tool_config,
)
from lazymind.chat.service.component.tool_rendering import _tool_call_frame_text, _tool_result_preview
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator


@pytest.fixture(autouse=True)
def isolate_env_overrides():
    sid = lazyllm.globals._sid
    previous = lazyllm.globals.get('conversation_env_overrides')
    previous_defaults = lazyllm.globals.get('user_env_defaults')
    lazyllm.globals['user_env_defaults'] = {}
    lazyllm.globals['conversation_env_overrides'] = {}
    yield
    lazyllm.globals._init_sid(sid)
    if previous_defaults is None:
        lazyllm.globals.pop('user_env_defaults', None)
    else:
        lazyllm.globals['user_env_defaults'] = previous_defaults
    if previous is None:
        lazyllm.globals.pop('conversation_env_overrides', None)
    else:
        lazyllm.globals['conversation_env_overrides'] = previous


def _restore_dynamic_env(old_dynamic_env):
    if old_dynamic_env is None:
        lazyllm.globals.pop('dynamic_env_vars', None)
    else:
        lazyllm.globals['dynamic_env_vars'] = old_dynamic_env


@pytest.mark.parametrize('defaults', [{}, {'a_api_key': 'user-test-value'}])
def test_delete_session_env_removes_only_override_and_restores_default(defaults):
    store = ConversationEnvStore()
    store.set('one', 'a_api_key', 'session-test-value')
    store.set('two', 'a_api_key', 'other-test-value')
    scoped, lease = store.snapshot('one')
    previous = lazyllm.globals.get('dynamic_env_vars')
    try:
        inject_runtime_env(defaults, scoped)
        tool = build_delete_session_env_tool(store, 'one', lease)
        result = tool('a_api_key')
        assert result['deleted'] is True
        assert result['effective_source'] == ('user' if defaults else 'process_or_unset')
        assert store.get_many('one') == {}
        assert store.get_many('two') == {'a_api_key': 'other-test-value'}
        assert lazyllm.globals['dynamic_env_vars'] == defaults
        assert lazyllm.globals['conversation_env_overrides'] == {}
        assert 'test-value' not in str(result)
        assert tool('a_api_key')['deleted'] is False
        store.clear('one')
        with pytest.raises(ToolExecutionError, match='cleared'):
            tool('a_api_key')
    finally:
        _restore_dynamic_env(previous)


@pytest.mark.parametrize('language', ['zh', 'en'])
def test_delete_user_env_only_emits_confirmation_with_safe_metadata(monkeypatch, language):
    events = []
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.get_core_api', lambda *args: {'items': [{
        'id': 'env-one', 'name': 'a_api_key', 'updated_at': '2026-09-23T00:00:00Z',
        'masked_value': 'sec***ret', 'description': 'private note',
    }]})
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env._write_agent_data',
                        lambda tag, **data: events.append((tag, data)))
    result = build_delete_user_env_tool(language)('a_api_key')
    assert result['status'] == 'confirmation_required'
    tag, card = events[0]
    assert tag == 'ask_pending'
    assert card['ask_id'] == result['ask_id']
    assert card['questions'][0]['type'] == 'boolean'
    assert card['user_env_delete'] == {
        'id': 'env-one', 'name': 'a_api_key', 'expected_updated_at': '2026-09-23T00:00:00Z',
    }
    assert 'sec***ret' not in str(events)
    assert 'private note' not in str(events)
    assert build_delete_user_env_tool(language)('A_API_KEY')['reason'] == 'not_found'
    assert len(events) == 1


@pytest.mark.parametrize('has_user_env', [True, False])
def test_chat_env_availability_matches_runtime_without_exposing_values(monkeypatch, has_user_env):
    store = ConversationEnvStore()
    store.set('env-prompt-conversation', 'REDFOX_API_KEY', 'session-override-secret')
    monkeypatch.setattr(chat_service, '_conversation_env_store', store)
    monkeypatch.setattr(chat_service, 'AutoModel', lambda *_args, **_kwargs: object())
    observed_env = []

    def create_agent(self, llm, plan):
        observed_env.append(dict(lazyllm.globals.get('dynamic_env_vars', {})))
        assert {'delete_user_env', 'set_user_env', 'set_session_env'} <= set(plan.stop_tools)
        assert 'delete_session_env' not in plan.stop_tools
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
    assert 'delete_session_env' in prompt
    assert 'delete_user_env' in prompt
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


@pytest.mark.parametrize('tool_name', ['set_session_env', 'set_user_env'])
@pytest.mark.parametrize('value', ['secret-value', '<redacted>'])
def test_normalize_history_omits_env_values_instead_of_teaching_placeholders(tool_name, value):
    import json
    from lazymind.chat.service.component.history import normalize_history_for_agent

    call_payload = json.dumps({
        'id': 'call-1',
        'name': tool_name,
        'arguments': {'name': 'REDFOX_API_KEY', 'value': value, 'description': value},
    }, ensure_ascii=False, separators=(',', ':'))
    result_payload = json.dumps({
        'id': 'call-1',
        'name': tool_name,
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
    assert arguments == {'name': 'REDFOX_API_KEY'}
    assert 'secret-value' not in json.dumps(normalized)
    assert '<redacted>' not in json.dumps(normalized)
    assert json.loads(normalized[1]['content'])['value_set'] is True


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


def _run_tool(tool, name='SERVICE_BASE_URL'):
    return ToolManager([tool]).execute_with_records({
        'id': 'env-call', 'function': {'name': tool.__name__, 'arguments': {'name': name}},
    }).results[0]


@pytest.mark.parametrize('case', json.loads(
    (Path(__file__).resolve().parents[3] / 'tests/contracts/env_names.json').read_text(),
))
def test_env_name_shared_contract(case):
    if case['valid']:
        assert validate_env_name(case['name']) == case['name']
    else:
        with pytest.raises(ValueError):
            validate_env_name(case['name'])


@pytest.mark.parametrize('name', ['PATH', '11', 'LD_AUDIT', 'NODE_OPTIONS'])
@pytest.mark.parametrize('kind', ['session', 'user', 'delete_session', 'delete_user'])
def test_invalid_env_names_fail_at_tool_manager_boundary(name, kind):
    store = ConversationEnvStore()
    _, lease = store.snapshot('invalid')
    tool = {
        'session': build_session_env_tool(store, 'invalid', lease),
        'user': build_user_env_tool(),
        'delete_session': build_delete_session_env_tool(store, 'invalid', lease),
        'delete_user': build_delete_user_env_tool(),
    }[kind]
    result = _run_tool(tool, name)
    assert result['ok'] is False
    assert 'could not' in _tool_result_preview(tool.__name__, result, language='en').lower()


@pytest.mark.parametrize('builder', [build_user_env_tool, build_delete_user_env_tool])
def test_core_failure_is_sanitized_at_tool_manager_boundary(monkeypatch, builder):
    def fail(*args):
        raise RuntimeError('internal request contained synthetic-secret')
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.get_core_api', fail)
    result = _run_tool(builder())
    assert result['ok'] is False
    assert 'synthetic-secret' not in str(result)


def test_stale_session_failure_reaches_tool_manager():
    store = ConversationEnvStore()
    tool = build_session_env_tool(store, 'stale')
    store.clear('stale')
    result = _run_tool(tool)
    assert result['ok'] is False
    assert 'cleared' in result['value']


def test_user_deletion_confirmation_remains_a_success(monkeypatch):
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.get_core_api', lambda *_: {'items': [{
        'id': 'one', 'name': 'SERVICE_BASE_URL', 'updated_at': '2026-09-24T00:00:00Z',
    }]})
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env._write_agent_data', lambda *_, **__: None)
    result = _run_tool(build_delete_user_env_tool())
    assert result['ok'] is True
    assert result['value']['status'] == 'confirmation_required'


@pytest.mark.parametrize('existing', [False, True])
def test_user_setup_only_requests_secure_input(monkeypatch, existing):
    events = []
    item = {'id': 'one', 'name': 'AWS_REGION', 'updated_at': '2026-09-24T00:00:00Z'}
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env.get_core_api',
                        lambda *_: {'items': [item] if existing else []})
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env._write_agent_data',
                        lambda tag, **data: events.append(data))
    result = _run_tool(build_user_env_tool(), 'AWS_REGION')
    assert result['ok'] is True
    assert result['value']['status'] == 'input_required'
    metadata = events[0]['env_input']
    assert metadata['scope'] == 'user'
    assert 'value' not in metadata
    assert ('id' in metadata) == existing
    if existing:
        assert metadata['expected_updated_at'] == item['updated_at']


@pytest.mark.parametrize('value', [' padded-value ', '', '   ', 'bad\x00value', '<redacted>'])
def test_session_input_uses_dedicated_api_and_never_echoes_value(monkeypatch, value):
    events = []
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env._write_agent_data',
                        lambda tag, **data: events.append(data))
    monkeypatch.setattr(agent_control_routes, 'config', {'enable_router': False, 'core_internal_token': 'test-internal'})
    store = ConversationEnvStore()
    tool = build_session_env_tool(store, 'input')
    result = _run_tool(tool)
    assert result['ok'] is True and store.get_many('input') == {}
    app = FastAPI()
    app.include_router(agent_control_routes.router)
    response = TestClient(app).post('/api/chat/session-env:input', headers={
        'X-LazyMind-Internal-Token': 'test-internal',
    }, json={
        'conversation_id': 'input', 'ask_id': events[0]['ask_id'], 'value': value,
    })
    valid = value == ' padded-value '
    assert response.status_code == (200 if valid else 400)
    assert store.get_many('input') == ({'SERVICE_BASE_URL': value} if valid else {})
    if value:
        assert value not in response.text
        assert value not in str(events)


def test_pending_input_is_scoped_idempotent_and_invalidated_by_cleanup(monkeypatch):
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env._write_agent_data', lambda *_, **__: None)
    store = ConversationEnvStore()
    tool = build_session_env_tool(store, 'one')
    receipt = tool('APP_ID')
    ask_id = receipt['ask_id']
    assert not session_env_inputs.submit(ask_id, 'other', 'wrong')
    assert session_env_inputs.submit(ask_id, 'one', 'first')
    assert session_env_inputs.submit(ask_id, 'one', 'replay')
    assert store.get_many('one') == {'APP_ID': 'first'}
    store.clear('one')
    with pytest.raises(ValueError, match='cleared'):
        session_env_inputs.submit(ask_id, 'one', 'late')
    with pytest.raises(ToolExecutionError):
        tool('APP_ID')


def test_tools_no_longer_accept_value_arguments_and_prompt_never_requests_them():
    import inspect
    session = build_session_env_tool(ConversationEnvStore(), 'schema')
    for tool in (session, build_user_env_tool()):
        assert 'value' not in inspect.signature(tool).parameters
    assert 'secure input card' in SESSION_ENV_QUERY_APPENDIX
    assert build_session_env_tool_config(ConversationEnvStore(), 'config').name == 'set_session_env'
    assert USER_ENV_TOOL_CONFIG.tool.__name__ == 'set_user_env'


@pytest.mark.parametrize('token', ['', 'wrong'])
def test_session_input_requires_service_authentication(monkeypatch, token):
    monkeypatch.setattr(agent_control_routes, 'config', {'core_internal_token': 'expected'})
    app = FastAPI()
    app.include_router(agent_control_routes.router)
    response = TestClient(app).post('/api/chat/session-env:input',
                                    headers={'X-LazyMind-Internal-Token': token},
                                    json={'conversation_id': 'input', 'ask_id': 'ask', 'value': 'private-value'})
    assert response.status_code == 401
    assert 'private-value' not in response.text


def test_session_input_expiration_rejects_late_value(monkeypatch):
    from lazymind.chat.engine.agent_runtime.env_input import SessionEnvInputRegistry
    registry = SessionEnvInputRegistry()
    store = ConversationEnvStore()
    _, lease = store.snapshot('one')
    monkeypatch.setattr('lazymind.chat.engine.agent_runtime.env_input.time.monotonic', lambda: 0)
    registry.register('ask', store, 'one', lease, 'APP_ID')
    monkeypatch.setattr('lazymind.chat.engine.agent_runtime.env_input.time.monotonic', lambda: 1801)
    assert not registry.submit('ask', 'one', 'late-value')
    assert not registry.owns('ask', 'one')
    assert store.get_many('one') == {}


@pytest.mark.parametrize('already_applied', [False, True])
def test_session_cancel_reports_actual_outcome_after_a_lost_response(monkeypatch, already_applied):
    from lazymind.chat.engine.agent_runtime.env_input import SessionEnvInputRegistry
    registry = SessionEnvInputRegistry()
    monkeypatch.setattr('lazymind.chat.engine.agent_runtime.env_input.session_env_inputs', registry)
    monkeypatch.setattr(agent_control_routes, 'config', {'enable_router': False, 'core_internal_token': 'test-internal'})
    store = ConversationEnvStore()
    _, lease = store.snapshot('one')
    registry.register('ask', store, 'one', lease, 'APP_ID')
    if already_applied:
        assert registry.submit('ask', 'one', 'first')
    app = FastAPI()
    app.include_router(agent_control_routes.router)
    response = TestClient(app).post('/api/chat/session-env:input',
                                    headers={'X-LazyMind-Internal-Token': 'test-internal'},
                                    json={'conversation_id': 'one', 'ask_id': 'ask', 'cancel': True})
    assert response.status_code == 200
    assert response.json() == {'ok': True, 'status': 'configured' if already_applied else 'canceled'}
    assert registry.submit('ask', 'one', 'late-value') == already_applied
    assert store.get_many('one') == ({'APP_ID': 'first'} if already_applied else {})
