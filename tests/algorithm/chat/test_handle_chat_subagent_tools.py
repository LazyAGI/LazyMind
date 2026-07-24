"""Tests for the _build_subagent_chat_tools integration in chat_service.

Verifies that:
- has_subagents=False: only create_subagent is in the tool list.
- has_subagents=True: all five subagent tools are present.
"""
from __future__ import annotations

import lazymind.chat.service.chat_service as cs
import lazymind.chat.engine.tools.subagent_chat_tools as sct


def test_build_subagent_chat_tools_without_existing_subagents():
    tools = cs._build_subagent_chat_tools(has_subagents=False)
    tool_names = {getattr(t, '__name__', str(t)) for t in tools}
    assert 'create_subagent' in tool_names
    # Query tools must NOT be present when no subagents exist yet.
    for name in ('list_subagents', 'get_subagent_status',
                 'list_subagent_artifacts', 'get_subagent_artifacts'):
        assert name not in tool_names, f'{name} should be absent when has_subagents=False'


def test_build_subagent_chat_tools_with_existing_subagents():
    tools = cs._build_subagent_chat_tools(has_subagents=True)
    tool_names = {getattr(t, '__name__', str(t)) for t in tools}
    expected = {
        'create_subagent',
        'list_subagents',
        'get_subagent_status',
        'list_subagent_artifacts',
        'get_subagent_artifacts',
    }
    assert expected == tool_names


def test_build_subagent_chat_tools_returns_callable_functions():
    tools = cs._build_subagent_chat_tools(has_subagents=True)
    for tool in tools:
        assert callable(tool), f'{tool!r} must be callable'


def test_build_subagent_chat_tools_create_subagent_is_the_correct_function():
    """Ensure the tool object in the list is the same function imported by chat_service."""
    tools = cs._build_subagent_chat_tools(has_subagents=False)
    assert tools[0] is sct.create_subagent


def test_build_subagent_chat_tools_query_tools_are_correct_functions():
    tools = cs._build_subagent_chat_tools(has_subagents=True)
    tool_set = set(tools)
    assert sct.list_subagents in tool_set
    assert sct.get_subagent_status in tool_set
    assert sct.list_subagent_artifacts in tool_set
    assert sct.get_subagent_artifacts in tool_set
