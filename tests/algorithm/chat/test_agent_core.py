"""Tests for lazymind.chat.engine.agent_core.

build_react_agent and drive_agent are tested with monkeypatched ReactAgent and
StreamCallHelper so no LLM or network calls are made.
"""
from __future__ import annotations

import asyncio
from typing import Any
from unittest.mock import MagicMock, patch

import pytest

import lazymind.chat.engine.agent_core as core_mod


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

class _FakeReactAgent:
    def __init__(self, llm, tools, **kwargs):
        self.llm = llm
        self.tools = tools
        self.kwargs = kwargs


def _patched_build(monkeypatch, **cfg_overrides):
    """Return a build_react_agent call that uses _FakeReactAgent."""
    monkeypatch.setattr(core_mod._agent_mod, 'ReactAgent', _FakeReactAgent)
    fake_cfg = {'max_retries': 5, **cfg_overrides}
    monkeypatch.setattr(core_mod, '_cfg', fake_cfg)
    return core_mod.build_react_agent


# ---------------------------------------------------------------------------
# build_react_agent tests
# ---------------------------------------------------------------------------

def test_build_react_agent_shared_defaults(monkeypatch):
    build = _patched_build(monkeypatch)
    agent = build(llm='llm', tools=['tool_a'], force_summarize_context='q')

    assert isinstance(agent, _FakeReactAgent)
    assert agent.kwargs['stream'] is True
    assert agent.kwargs['max_retries'] == 5
    assert agent.kwargs['enable_builtin_tools'] is False
    assert agent.kwargs['force_summarize'] is True
    assert agent.kwargs['force_summarize_context'] == 'q'


def test_build_react_agent_none_kwargs_not_forwarded(monkeypatch):
    build = _patched_build(monkeypatch)
    agent = build(llm='llm', tools=[])
    # ChatAgent-only optional kwargs must be absent when None
    for key in ('prompt', 'skills', 'workspace', 'keep_full_turns', 'fs', 'skills_dir'):
        assert key not in agent.kwargs, f'{key} should not be forwarded when None'


def test_build_react_agent_optional_kwargs_forwarded(monkeypatch):
    build = _patched_build(monkeypatch)
    agent = build(
        llm='llm',
        tools=[],
        prompt='sys prompt',
        skills=['s1'],
        workspace='/tmp/ws',
        keep_full_turns=3,
        fs='fake_fs',
        skills_dir='/skills',
    )
    assert agent.kwargs['prompt'] == 'sys prompt'
    assert agent.kwargs['skills'] == ['s1']
    assert agent.kwargs['workspace'] == '/tmp/ws'
    assert agent.kwargs['keep_full_turns'] == 3
    assert agent.kwargs['fs'] == 'fake_fs'
    assert agent.kwargs['skills_dir'] == '/skills'


def test_build_react_agent_uses_max_retries_from_config(monkeypatch):
    build = _patched_build(monkeypatch, max_retries=10)
    agent = build(llm='llm', tools=[])
    assert agent.kwargs['max_retries'] == 10


# ---------------------------------------------------------------------------
# drive_agent tests
# ---------------------------------------------------------------------------

class _FakeHelper:
    """Mimics StreamCallHelper.astream + future.result."""

    def __init__(self, events, final_value, raise_on_final=None):
        self._events = events
        self._final = final_value
        self._raise = raise_on_final
        self.future = MagicMock()
        if raise_on_final:
            self.future.result.side_effect = raise_on_final
        else:
            self.future.result.return_value = final_value

    async def astream(self, query, **kwargs):
        for ev in self._events:
            yield ev


def _patch_helper(monkeypatch, events, final_value, raise_on_final=None):
    helper = _FakeHelper(events, final_value, raise_on_final)
    monkeypatch.setattr(
        core_mod._sh,
        'StreamCallHelper',
        lambda agent, init_sid: helper,
    )
    return helper


def test_drive_agent_yields_events_then_final(monkeypatch):
    events = [{'tag': 'text', 'delta': 'hello'}, {'tag': 'text', 'delta': ' world'}]
    _patch_helper(monkeypatch, events, final_value='done!')

    async def run():
        results = []
        async for kind, payload in core_mod.drive_agent('agent', 'query'):
            results.append((kind, payload))
        return results

    results = asyncio.run(run())
    assert results[0] == ('event', {'tag': 'text', 'delta': 'hello'})
    assert results[1] == ('event', {'tag': 'text', 'delta': ' world'})
    assert results[2] == ('final', 'done!')
    assert len(results) == 3


def test_drive_agent_passes_history(monkeypatch):
    received_kwargs: dict = {}

    class _CapturingHelper:
        def __init__(self, agent, init_sid):
            self.future = MagicMock()
            self.future.result.return_value = 'ok'

        async def astream(self, query, **kwargs):
            received_kwargs.update(kwargs)
            return
            yield  # make it an async generator

    monkeypatch.setattr(core_mod._sh, 'StreamCallHelper', _CapturingHelper)

    async def run():
        async for _ in core_mod.drive_agent('agent', 'q', history=[{'role': 'user', 'content': 'hi'}]):
            pass

    asyncio.run(run())
    assert received_kwargs.get('llm_chat_history') == [{'role': 'user', 'content': 'hi'}]


def test_drive_agent_propagates_future_exception(monkeypatch):
    _patch_helper(monkeypatch, [], final_value=None, raise_on_final=ValueError('boom'))

    async def run():
        results = []
        async for kind, payload in core_mod.drive_agent('agent', 'q'):
            results.append((kind, payload))
        return results

    with pytest.raises(ValueError, match='boom'):
        asyncio.run(run())


def test_drive_agent_no_history_kwarg_omitted(monkeypatch):
    received_kwargs: dict = {}

    class _CapturingHelper:
        def __init__(self, agent, init_sid):
            self.future = MagicMock()
            self.future.result.return_value = 'ok'

        async def astream(self, query, **kwargs):
            received_kwargs.update(kwargs)
            return
            yield

    monkeypatch.setattr(core_mod._sh, 'StreamCallHelper', _CapturingHelper)

    async def run():
        async for _ in core_mod.drive_agent('agent', 'q'):
            pass

    asyncio.run(run())
    assert 'llm_chat_history' not in received_kwargs
