"""Unit tests for plugins/step_agent.py (prompt rendering and tool resolution)"""
from __future__ import annotations

from lazymind.chat.plugins.step_agent import (
    _normalize_checkpoint_data,
    _render_step_prompt,
    _resolve_step_tools,
    save_step_artifact,
    save_step_checkpoint,
)

import lazyllm


def _init_globals(workspace: str = '/tmp/test_workspace') -> None:
    lazyllm.globals['agentic_config'] = {
        'step_workspace': workspace,
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'session-1',
        'step_checkpoint': {},
    }
    lazyllm.globals['plugin_event_queue'] = []


def test_render_step_prompt_replaces_artifact_placeholders():
    _init_globals()
    config = {'prompt': 'Generate image from: {{optimized_prompt}}', 'tools': []}
    artifacts = {'optimized_prompt': 'a cute orange cat in watercolor style'}
    result = _render_step_prompt(config, artifacts, None)
    assert 'a cute orange cat in watercolor style' in result
    assert '{{optimized_prompt}}' not in result


def test_render_step_prompt_removes_unmatched_placeholders():
    _init_globals()
    config = {'prompt': 'Use {{nonexistent_artifact}} here', 'tools': []}
    result = _render_step_prompt(config, {}, None)
    assert '{{' not in result


def test_render_step_prompt_appends_checkpoint_resume_block():
    _init_globals()
    config = {'prompt': 'Process items', 'tools': []}
    checkpoint = {'completed_count': 5, 'total_count': 10, 'phase_note': 'batch 1'}
    result = _render_step_prompt(config, {}, checkpoint)
    assert 'checkpoint' in result.lower()
    assert '5/10' in result or '5' in result


def test_render_step_prompt_includes_builtin_tool_instructions():
    _init_globals()
    config = {'prompt': 'Do something', 'tools': []}
    result = _render_step_prompt(config, {}, None)
    assert 'save_step_artifact' in result
    assert 'save_step_checkpoint' in result
    assert 'get_checkpoint_details' in result


def test_resolve_step_tools_empty_tools_inherits_all():
    _init_globals()

    def mock_tool_1():
        pass
    mock_tool_1.__name__ = 'search'

    def mock_tool_2():
        pass
    mock_tool_2.__name__ = 'fetch'
    default_tools = [mock_tool_1, mock_tool_2]

    builtin_tools = [save_step_artifact]
    config = {'tools': []}
    result = _resolve_step_tools(config, default_tools, builtin_tools)
    assert save_step_artifact in result
    assert mock_tool_1 in result
    assert mock_tool_2 in result


def test_resolve_step_tools_non_empty_list_filters_by_name():
    _init_globals()

    def mock_tool():
        pass
    mock_tool.__name__ = 'dalle_generate'
    default_tools = [mock_tool]
    builtin_tools = [save_step_artifact]
    config = {'tools': ['dalle_generate']}
    result = _resolve_step_tools(config, default_tools, builtin_tools)
    assert mock_tool in result
    assert save_step_artifact in result


def test_save_step_artifact_appends_to_event_queue():
    _init_globals()
    lazyllm.globals['plugin_event_queue'] = []
    save_step_artifact('my_artifact', 'some value')
    queue = lazyllm.globals.get('plugin_event_queue', [])
    assert any(e.get('type') == 'artifact' and e.get('artifact_id') == 'my_artifact' for e in queue)


def test_save_step_checkpoint_appends_to_event_queue():
    _init_globals()
    lazyllm.globals['plugin_event_queue'] = []
    save_step_checkpoint({'completed_count': 3, 'total_count': 10})
    queue = lazyllm.globals.get('plugin_event_queue', [])
    assert any(e.get('type') == 'checkpoint' for e in queue)


def test_normalize_checkpoint_data_preserves_counts():
    data = {
        'completed_count': 7,
        'total_count': 20,
        'phase_note': 'batch 2',
        'partial_results': ['a', 'b'],
    }
    result = _normalize_checkpoint_data(data, '')
    assert result['completed_count'] == 7
    assert result['total_count'] == 20
    assert result['phase_note'] == 'batch 2'
    assert result['partial_results'] == ['a', 'b']
