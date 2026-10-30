from __future__ import annotations

import json

import lazyllm
import pytest

from lazymind.chat.engine.tools import multimodal
from lazymind.chat.service.component import tool_registry


def test_explicit_image_request_keeps_unconfigured_tool_visible(monkeypatch):
    image_config = next(
        item for item in tool_registry.DEFAULT_TOOLS
        if item.name == 'image_generator'
    )
    monkeypatch.setattr(tool_registry, 'tool_is_active', lambda _config: False)

    assert tool_registry.filter_tools(
        [image_config],
        user_query='生成一张小狗的照片',
    ) == [image_config]
    assert tool_registry.filter_tools(
        [image_config],
        user_query='介绍一下小狗',
    ) == []
    assert tool_registry.filter_tools(
        [image_config],
        user_query='不要生成图片，只描述一下',
    ) == []


def test_natural_video_request_keeps_unconfigured_tool_visible(monkeypatch):
    video_config = next(
        item for item in tool_registry.DEFAULT_TOOLS
        if item.name == 'video_generator'
    )
    monkeypatch.setattr(tool_registry, 'tool_is_active', lambda _config: False)

    assert tool_registry.filter_tools(
        [video_config],
        user_query='生成一个 小狗跳舞的视频',
    ) == [video_config]
    assert tool_registry.filter_tools(
        [video_config],
        user_query='介绍一下小狗跳舞视频的拍摄方法',
    ) == []
    assert tool_registry.filter_tools(
        [video_config],
        user_query='不要生成一个小狗跳舞的视频，只写脚本',
    ) == []


def test_media_prompt_requires_requested_tool_and_forbids_fallback():
    media_configs = [
        item for item in tool_registry.DEFAULT_TOOLS
        if item.name in {'image_generator', 'video_generator'}
    ]

    appendices = tool_registry.collect_system_prompt_appendices(media_configs)
    policy = '\n'.join(appendices['tool_policy'])

    assert 'MUST call the corresponding' in policy
    assert '`MEDIA_CAPABILITY_DEPENDENCY_MISSING`' in policy
    assert 'do not call `ask_user`' in policy
    assert 'still images, keyframes, a GIF, a script' in policy


def test_unconfigured_image_tool_returns_structured_dependency(monkeypatch):
    monkeypatch.setitem(lazyllm.globals['config'], 'dynamic_model_configs', {})
    monkeypatch.setattr(multimodal, 'is_model_role_available', lambda _role: False)

    result = multimodal.image_generator('一只在草地上的小狗')

    message = result['_agent_control']['final_text']
    marker = 'MEDIA_CAPABILITY_DEPENDENCY_MISSING '
    assert marker in message
    payload = json.loads(message.split(marker, 1)[1])
    assert result['_agent_control']['stop'] is True
    assert result['status'] == 'blocked'
    assert payload['workflow'] == 'DIRECT_CHAT'
    assert payload['missing'][0]['id'] == 'image_generator'
    assert payload['missing'][0]['settings_url'].endswith(
        'target=image_generator'
    )


@pytest.mark.parametrize('agent_type,role', [
    ('image_generation', 'image_generator'),
    ('image_editing', 'image_editor'),
    ('video_generation', 'video_generator'),
])
@pytest.mark.parametrize('available', [False, True])
def test_media_subagent_checks_configuration_before_creating_task(monkeypatch, agent_type, role, available):
    from lazymind.chat.engine.tools import subagent_chat_tools as subagent

    monkeypatch.setitem(lazyllm.globals['config'], 'dynamic_model_configs', {})
    monkeypatch.setattr(multimodal, 'is_model_role_available', lambda requested: available and requested == role)
    monkeypatch.setattr(subagent, '_agentic_config', lambda: {'mode': 'manual', 'conversation_id': 'media-test'})
    events = []
    monkeypatch.setattr(subagent, '_write_agent_data', lambda tag, **kw: events.append(tag))

    result = subagent.create_subagent(agent_type, '生成小狗图片', '生成一个小狗', output_slots=['image'])

    if available:
        assert result['status'] == 'ok'
        assert events == ['task_created']
    else:
        assert events == []
        assert result['status'] == 'blocked'
        assert result['_agent_control']['stop'] is True
        assert result['missing'][0]['id'] == role
        assert 'MEDIA_CAPABILITY_DEPENDENCY_MISSING ' in result['_agent_control']['final_text']


@pytest.mark.parametrize('query', ['生成一个小狗', '画只猫', '一张小狗的图片'])
def test_chat_media_configuration_surface_handles_short_and_clarified_requests(monkeypatch, query):
    from lazyllm.tools.agent import ToolManager
    from lazymind.chat.service.component import AgentEventFrameTranslator
    from lazymind.chat.engine.prompts.system_prompt import build_system_prompt

    monkeypatch.setattr(tool_registry, 'tool_is_active', lambda _config: False)
    monkeypatch.setitem(lazyllm.globals['config'], 'dynamic_model_configs', {})
    monkeypatch.setattr(multimodal, 'is_model_role_available', lambda _role: False)
    image_config = next(item for item in tool_registry.DEFAULT_TOOLS if item.name == 'image_generator')
    configs = tool_registry.filter_tools([image_config], user_query=query, include_unready=True)
    manager = ToolManager([cfg.tool for cfg in configs])
    assert [item['function']['name'] for item in manager.tools_description] == ['image_generator']
    prompt = build_system_prompt(
        True, current_query=query,
        tool_prompt_appendices=tool_registry.collect_system_prompt_appendices(configs),
    )
    assert 'call `image_generator` directly' in prompt
    assert 'without `create_subagent`, `search_skill`, or `get_skill`' in prompt

    result = configs[0].tool(query)
    translator = AgentEventFrameTranslator(query=query)
    frames = translator.feed({'tag': 'tool_results', 'tool_results': [{
        'id': 'call-image', 'name': 'image_generator', 'result': {'ok': True, 'value': result},
    }]})
    detail = next(frame['capability_dependency'] for frame in frames if frame.get('capability_dependency'))
    assert detail['status'] == 'blocked'
    assert detail['missing'][0]['settings_url'] == '/settings?section=model_defaults&target=image_generator'
    assert result['_agent_control']['stop'] is True


def test_chat_media_configuration_surface_preserves_user_disabling(monkeypatch):
    monkeypatch.setattr(tool_registry, 'tool_is_active', lambda _config: False)
    image_config = next(item for item in tool_registry.DEFAULT_TOOLS if item.name == 'image_generator')
    assert tool_registry.filter_tools(
        [image_config], user_query='不要生成图片，只描述一下', include_unready=True,
    ) == []
    assert tool_registry.filter_tools(
        [image_config], available_tools=[], user_query='生成一个小狗', include_unready=True,
    ) == []
