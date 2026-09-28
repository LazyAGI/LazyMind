from __future__ import annotations

import json
from pathlib import Path

import lazyllm
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.skill_manager import SkillManager
from lazyllm.tools.tool_config_inject import get_dynamic_env_vars

from lazymind.chat.api import agent_control_routes
from lazymind.chat.engine.agent_runtime.conversation_env import ConversationEnvStore
from lazymind.chat.engine.agent_runtime.env_runtime import inject_runtime_env
from lazymind.chat.engine.tools.session_env import build_session_env_tool
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

_KEY = 'DYNAMIC_TEST_API_KEY'
_SECRET = 'synthetic-card-value'


@pytest.fixture
def runtime(monkeypatch, tmp_path):
    previous_sid = lazyllm.globals._sid
    previous = dict(get_dynamic_env_vars())
    monkeypatch.setattr(agent_control_routes, 'config', {'enable_router': False, 'core_internal_token': 'test-internal'})
    app = FastAPI()
    app.include_router(agent_control_routes.router)
    store = ConversationEnvStore()
    events = []
    monkeypatch.setattr('lazymind.chat.engine.tools.session_env._write_agent_data',
                        lambda tag, **data: events.append({'tag': tag, **data}))
    try:
        yield store, TestClient(app, headers={'X-LazyMind-Internal-Token': 'test-internal'}), events
    finally:
        lazyllm.globals._init_sid(previous_sid)
        inject_runtime_env(previous)


def make_skill(tmp_path: Path, keys: list[str], declared=False):
    folder = tmp_path / 'probe'
    scripts = folder / 'scripts'
    scripts.mkdir(parents=True)
    required = ('required_env:\n' + ''.join(f'  - {name}\n' for name in keys)) if declared else ''
    (folder / 'SKILL.md').write_text(
        '---\nname: probe\ndescription: environment probe\n' + required + '---\n# probe\n',
    )
    (scripts / 'probe.py').write_text(
        'import os, sys\n'
        f'keys = {keys!r}\n'
        'missing = [name for name in keys if not os.getenv(name)]\n'
        'if missing:\n'
        '    sys.stderr.write("missing " + " ".join(missing))\n'
        '    sys.exit(1)\n'
        'print("configured")\n',
    )
    return SkillManager(dir=str(tmp_path))


def begin_turn(store, sid, conversation='one', defaults=None):
    lazyllm.globals._init_sid(sid)
    inject_runtime_env(defaults, store.get_many(conversation))


def submit_card(store, client, events, name=_KEY, value=_SECRET, conversation='one'):
    receipt = build_session_env_tool(store, conversation)(name)
    card = events[-1]
    assert card['env_input'] == {'scope': 'conversation', 'name': name}
    assert value not in json.dumps(card)
    translator = AgentEventFrameTranslator(query='Configure environment')
    frames = translator.feed(card)
    assert frames[0]['ask_pending']['env_input']['name'] == name
    assert translator.finish(receipt) == []
    response = client.post('/api/chat/session-env:input', json={
        'conversation_id': conversation, 'ask_id': receipt['ask_id'], 'value': value,
    })
    assert response.status_code == 200 and response.json()['ok'] is True
    assert value not in response.text
    return receipt


@pytest.mark.parametrize('declared', [False, True])
def test_missing_env_secure_card_then_new_turn_retries_skill(runtime, tmp_path, monkeypatch, declared):
    store, client, events = runtime
    monkeypatch.delenv(_KEY, raising=False)
    manager = make_skill(tmp_path, [_KEY], declared)
    begin_turn(store, 'first')
    with pytest.raises(ToolExecutionError) as failure:
        manager.run_script('probe', 'scripts/probe.py')
    assert _KEY in failure.value.missing_env
    submit_card(store, client, events)
    # The API request does not mutate a stale execution context.
    assert _KEY not in get_dynamic_env_vars()
    begin_turn(store, 'resumed')
    assert get_dynamic_env_vars()[_KEY] == _SECRET
    result = manager.run_script('probe', 'scripts/probe.py')
    assert result['status'] == 'ok'
    assert result['stdout'].strip() == 'configured'
    assert _SECRET not in str(events) + str(result)


def test_proactive_input_new_conversation_isolation_and_cleanup(runtime, tmp_path, monkeypatch):
    store, client, events = runtime
    monkeypatch.delenv(_KEY, raising=False)
    manager = make_skill(tmp_path, [_KEY])
    begin_turn(store, 'first')
    submit_card(store, client, events)
    begin_turn(store, 'later')
    assert manager.run_script('probe', 'scripts/probe.py')['status'] == 'ok'
    begin_turn(store, 'other', conversation='two')
    with pytest.raises(ToolExecutionError):
        manager.run_script('probe', 'scripts/probe.py')
    store.clear('one')
    begin_turn(store, 'after-clear')
    with pytest.raises(ToolExecutionError):
        manager.run_script('probe', 'scripts/probe.py')


@pytest.mark.parametrize('defaults', [{}, {_KEY: 'user-default'}])
def test_secure_input_preserves_session_precedence_and_user_default(runtime, defaults):
    store, client, events = runtime
    begin_turn(store, 'first', defaults=defaults)
    submit_card(store, client, events)
    begin_turn(store, 'resumed', defaults=defaults)
    assert get_dynamic_env_vars()[_KEY] == _SECRET
    begin_turn(store, 'other', conversation='two', defaults=defaults)
    assert get_dynamic_env_vars() == defaults
    assert defaults.get(_KEY) in (None, 'user-default')


def test_two_variables_are_collected_in_separate_secure_cards(runtime, tmp_path):
    store, client, events = runtime
    manager = make_skill(tmp_path, ['APP_ID', 'AWS_REGION'])
    begin_turn(store, 'first')
    for name in ['APP_ID', 'AWS_REGION']:
        submit_card(store, client, events, name=name)
        begin_turn(store, f'resume-{name}')
    assert manager.run_script('probe', 'scripts/probe.py')['stdout'].strip() == 'configured'
    assert store.get_many('one') == {'APP_ID': _SECRET, 'AWS_REGION': _SECRET}
