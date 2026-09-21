import base64
import copy

import lazyllm
import pytest
from fastapi import HTTPException
from lazyllm.components import ChatPrompter
from lazyllm.module.llms.onlinemodule.supplier.openai import OpenAIChat
from lazyllm.tools.agent import ReactAgent

from lazymind.chat.service import multimodal_input as mi
from lazymind.chat.service.utils import file_validation


@pytest.fixture
def images(tmp_path, monkeypatch):
    monkeypatch.setattr(file_validation, 'MOUNT_BASE_DIR', str(tmp_path))
    current = tmp_path / 'current.png'
    current.write_bytes(base64.b64decode(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='
    ))
    old = tmp_path / 'old.jpg'
    old.write_bytes(b'old image')
    return current, old


def test_only_current_images_are_attached_without_mutating_history(images):
    current, old = images
    original = [{'role': 'user', 'content': 'previous question'}]
    before = copy.deepcopy(original)
    history, paths = mi.prepare_direct_image_history(
        original, {'1': [str(old)], '2': [str(current), str(current), 'report.pdf']}, 2, enabled=True,
    )
    assert original == before
    assert paths == [str(current)]
    blocks = history[-1]['content']
    assert blocks[1]['image_url']['url'] == 'data:image/png;base64,' + base64.b64encode(current.read_bytes()).decode()
    assert len(blocks) == 2


@pytest.mark.parametrize('enabled,turn', [(False, 2), (True, 3), (True, None)])
def test_text_models_and_turns_without_images_do_not_receive_images(images, enabled, turn):
    current, _ = images
    original = [{'role': 'user', 'content': 'prior'}]
    history, paths = mi.prepare_direct_image_history(original, {'2': [str(current)]}, turn, enabled=enabled)
    assert history is original and paths == []


def test_images_outside_attachment_mount_are_rejected(images, tmp_path):
    outside = tmp_path.parent / 'outside.png'
    with pytest.raises(HTTPException) as error:
        mi.prepare_direct_image_history([], {'2': [str(outside)]}, 2, enabled=True)
    assert error.value.detail == 'Path outside mount directory'


def test_main_model_receives_image_and_question_in_one_request(images, monkeypatch):
    current, _ = images
    history, _ = mi.prepare_direct_image_history([], {'2': [str(current)]}, 2, enabled=True)
    calls = []

    def respond(self, data, **kwargs):
        calls.append(copy.deepcopy(data))
        return [{'choices': [{'message': {'role': 'assistant', 'content': 'A pixel'}}]}]

    monkeypatch.setattr(OpenAIChat, '_forward_with_retry', respond)
    llm = OpenAIChat(api_key='test', model='visual-main', type='vlm', stream=False).prompt(ChatPrompter('Answer.'))
    llm('What is in this picture?', llm_chat_history=history)
    assert len(calls) == 1
    assert calls[0]['model'] == 'visual-main'
    assert calls[0]['messages'][-1]['content'] == 'What is in this picture?'
    assert calls[0]['messages'][-2]['content'][1]['type'] == 'image_url'


def test_images_survive_a_real_agent_tool_round(images, monkeypatch):
    current, _ = images
    history, _ = mi.prepare_direct_image_history([], {'2': [str(current)]}, 2, enabled=True)
    calls = []

    def respond(self, data, **kwargs):
        calls.append(copy.deepcopy(data))
        if len(calls) == 1:
            message = {'role': 'assistant', 'content': None, 'tool_calls': [{
                'id': 'call_1', 'type': 'function',
                'function': {'name': 'get_number', 'arguments': '{}'},
            }]}
        else:
            message = {'role': 'assistant', 'content': 'Done'}
        return [{'choices': [{'message': message}]}]

    def get_number() -> int:
        """Return a number needed for the user's calculation."""
        return 2

    monkeypatch.setattr(OpenAIChat, '_forward_with_retry', respond)
    with lazyllm.new_session():
        llm = OpenAIChat(api_key='test', model='visual-main', type='vlm', stream=False)
        agent = ReactAgent(llm, tools=[get_number], prompt='Use get_number then answer.',
                           stream=False, enable_builtin_tools=False, max_retries=3)
        agent('Compare the picture with the number.', llm_chat_history=history)
    assert len(calls) == 2
    for request in calls:
        assert any(isinstance(m.get('content'), list) and any(
            block.get('type') == 'image_url' for block in m['content']
        ) for m in request['messages'])
    assert any(m['role'] == 'tool' for m in calls[1]['messages'])


def test_image_token_estimate_does_not_treat_base64_as_text():
    from lazymind.chat.engine.agent_runtime.message_fields import estimate_message_tokens
    from lazymind.chat.engine.agent_runtime.projection_state import message_tokens
    from lazymind.chat.engine.agent_runtime.pruner import estimate_history_tokens

    def image_message(size):
        return {'role': 'user', 'content': [{
            'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,' + 'A' * size},
        }]}

    small, large = image_message(100), image_message(100000)
    original = copy.deepcopy(large)
    assert estimate_message_tokens(small) == estimate_message_tokens(large)
    assert message_tokens(large) == estimate_history_tokens([large])
    assert large == original
