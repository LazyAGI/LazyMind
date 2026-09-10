from __future__ import annotations

import os
import tempfile
from unittest.mock import patch
import lazyllm
import pytest
from lazyllm.tools import inject_env_vars
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.skill_manager import SkillManager
from lazyllm.tools.tool_config_inject import get_dynamic_env_vars
from lazymind.chat.engine.tools.ask_user import ask_user
from lazymind.chat.engine.tools.session_env import build_session_env_tool
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator


_MISSING_KEY = 'DYNAMIC_TEST_API_KEY'


_SECRET = 'secret-from-ask-card'


def _make_env_skill(base_dir: str, skill_name: str = 'env-skill') -> str:
    skill_dir = os.path.join(base_dir, skill_name)
    scripts_dir = os.path.join(skill_dir, 'scripts')
    os.makedirs(scripts_dir, exist_ok=True)
    with open(os.path.join(skill_dir, 'SKILL.md'), 'w', encoding='utf-8') as f:
        f.write(
            '---\n'
            f'name: {skill_name}\n'
            f'description: {skill_name} needs an API key\n'
            '---\n'
            f'# {skill_name}\n'
            f'Requires `{_MISSING_KEY}`.\n'
        )
    with open(os.path.join(scripts_dir, 'needs_key.py'), 'w', encoding='utf-8') as f:
        f.write(
            'import os\nimport sys\n'
            f'value = os.getenv({_MISSING_KEY!r}, "")\n'
            'if not value:\n'
            f'    sys.stderr.write("missing {_MISSING_KEY}")\n'
            '    sys.exit(1)\n'
            'print(value)\n'
        )
    return skill_name


def _begin_turn(session_id: str, conversation_id: str, store: dict[str, dict[str, str]]) -> None:
    lazyllm.globals._init_sid(session_id)
    inject_env_vars(store.get(conversation_id))


def _run_needs_key(manager: SkillManager, skill_name: str):
    return manager.run_script(skill_name, 'scripts/needs_key.py', allow_unsafe=True)


def _ask_for_missing_key(env_name: str = _MISSING_KEY) -> tuple[str, dict]:
    question = (
        f'Please paste {env_name}. It applies only to this conversation.'
    )
    captured: dict = {}

    def _capture(tag: str, **kwargs):
        captured['tag'] = tag
        captured.update(kwargs)

    with patch('lazymind.chat.engine.tools.ask_user._write_agent_data', _capture):
        receipt = ask_user(
            [{'text': question, 'type': 'text'}],
            title='Missing skill credential',
            description='This value is stored for the current conversation only.',
        )
    translator = AgentEventFrameTranslator(query='run the skill')
    frames = translator.feed({'tag': captured['tag'], **{
        key: value for key, value in captured.items() if key != 'tag'
    }})
    finish_frames = translator.finish(receipt)
    return receipt, {
        'receipt': receipt,
        'frames': frames,
        'finish_frames': finish_frames,
        'question': question,
        'payload': captured,
    }


def test_missing_env_ask_user_card_then_set_and_retry_skill():
    store: dict[str, dict[str, str]] = {}
    conversation_id = 'conversation-chain'
    previous_sid = lazyllm.globals._sid
    old_dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    with tempfile.TemporaryDirectory() as tmp:
        skill_name = _make_env_skill(tmp)
        manager = SkillManager(dir=tmp)
        set_env = build_session_env_tool(store, conversation_id)
        try:
            _begin_turn('turn-1', conversation_id, store)
            with pytest.raises(ToolExecutionError) as missing:
                _run_needs_key(manager, skill_name)
            assert _MISSING_KEY in str(missing.value)
            assert missing.value.missing_env == [_MISSING_KEY]
            assert f'missing_env: ["{_MISSING_KEY}"]' in str(missing.value)

            receipt, ask = _ask_for_missing_key()
            pending = ask['frames'][0]['ask_pending']
            assert ask['payload']['tag'] == 'ask_pending'
            assert 'Waiting for answer on next turn' in receipt
            assert pending['questions'][0]['type'] == 'text'
            assert _MISSING_KEY in pending['questions'][0]['text']
            assert 'current conversation only' in pending['description']
            assert ask['finish_frames'] == []

            user_answer = f'{ask["question"]}: {_SECRET}'
            _begin_turn('turn-2', conversation_id, store)
            assert get_dynamic_env_vars().get(_MISSING_KEY) in (None, '')
            assert _SECRET in user_answer

            result = set_env(_MISSING_KEY, _SECRET)
            assert result['status'] == 'ok'
            assert result['conversation_id'] == conversation_id
            assert _SECRET not in str(result)
            retried = _run_needs_key(manager, skill_name)
        finally:
            lazyllm.globals._init_sid(previous_sid)
            if old_dynamic_env is None:
                lazyllm.globals.pop('dynamic_env_vars', None)
            else:
                lazyllm.globals['dynamic_env_vars'] = old_dynamic_env

    assert retried['status'] == 'ok'
    assert retried['stdout'].strip() == _SECRET
    assert store[conversation_id][_MISSING_KEY] == _SECRET


def test_later_turn_new_sid_rehydrates_conversation_env_for_skill():
    store: dict[str, dict[str, str]] = {}
    conversation_id = 'conversation-rehydrate'
    previous_sid = lazyllm.globals._sid
    old_dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    with tempfile.TemporaryDirectory() as tmp:
        skill_name = _make_env_skill(tmp)
        manager = SkillManager(dir=tmp)
        set_env = build_session_env_tool(store, conversation_id)
        try:
            _begin_turn('turn-set', conversation_id, store)
            set_env(_MISSING_KEY, _SECRET)
            _begin_turn('turn-later', conversation_id, store)
            assert get_dynamic_env_vars()[_MISSING_KEY] == _SECRET
            result = _run_needs_key(manager, skill_name)
        finally:
            lazyllm.globals._init_sid(previous_sid)
            if old_dynamic_env is None:
                lazyllm.globals.pop('dynamic_env_vars', None)
            else:
                lazyllm.globals['dynamic_env_vars'] = old_dynamic_env

    assert result['status'] == 'ok'
    assert result['stdout'].strip() == _SECRET


def test_new_conversation_does_not_see_other_conversation_env():
    store: dict[str, dict[str, str]] = {}
    previous_sid = lazyllm.globals._sid
    old_dynamic_env = lazyllm.globals.get('dynamic_env_vars')
    with tempfile.TemporaryDirectory() as tmp:
        skill_name = _make_env_skill(tmp)
        manager = SkillManager(dir=tmp)
        set_env_a = build_session_env_tool(store, 'conversation-a')
        try:
            _begin_turn('sid-a', 'conversation-a', store)
            set_env_a(_MISSING_KEY, _SECRET)
            _begin_turn('sid-b', 'conversation-b', store)
            assert _MISSING_KEY not in get_dynamic_env_vars()
            with pytest.raises(ToolExecutionError) as missing:
                _run_needs_key(manager, skill_name)
            assert store['conversation-a'][_MISSING_KEY] == _SECRET
            assert 'conversation-b' not in store
        finally:
            lazyllm.globals._init_sid(previous_sid)
            if old_dynamic_env is None:
                lazyllm.globals.pop('dynamic_env_vars', None)
            else:
                lazyllm.globals['dynamic_env_vars'] = old_dynamic_env

    assert _MISSING_KEY in str(missing.value)
