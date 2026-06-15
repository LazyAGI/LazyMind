"""Tests for lazymind.chat.engine.subagent.runner.

Uses a FakeDB (in-memory) and a FakeAgent (injects known tag sequences) to
drive run_subagent_stream without any real database, LLM, or network calls.
"""
from __future__ import annotations

import asyncio
import json
from typing import Any, Dict, List, Optional
from unittest.mock import MagicMock

import pytest

import lazymind.chat.engine.subagent.runner as runner_mod
import lazymind.chat.engine.agent_core as core_mod


# ---------------------------------------------------------------------------
# In-memory FakeDB
# ---------------------------------------------------------------------------

class FakeDB:
    def __init__(self, task: Optional[Dict[str, Any]] = None):
        self._task = task
        self.steps: List[Dict[str, Any]] = []

    def load_task(self, task_id: str) -> Optional[Dict[str, Any]]:
        return self._task

    def append_step(self, task_id: str, seq: int, role: str, content: Dict[str, Any]) -> None:
        self.steps.append({'task_id': task_id, 'seq': seq, 'role': role, 'content': content})

    def load_steps(self, task_id: str) -> List[Dict[str, Any]]:
        return [s for s in self.steps if s['task_id'] == task_id]

    def max_step_seq(self, task_id: str) -> int:
        relevant = [s['seq'] for s in self.steps if s['task_id'] == task_id]
        return max(relevant) if relevant else -1

    def next_artifact_seq(self, task_id: str, key: str) -> int:
        return 1

    def save_artifact(self, task_id: str, key: str, content_type: str,
                      value: Dict[str, Any], seq: int) -> None:
        pass

    def load_artifacts(self, task_id: str, keys=None) -> List[Dict[str, Any]]:
        return []

    def saved_artifact_keys(self, task_id: str) -> List[str]:
        return []

    def dispose(self) -> None:
        pass


# ---------------------------------------------------------------------------
# Default task fixture
# ---------------------------------------------------------------------------

_DEFAULT_TASK_ID = 'task-001'
_DEFAULT_TASK = {
    'id': _DEFAULT_TASK_ID,
    'conversation_id': 'conv-1',
    'agent_type': 'test',
    'objective': 'do something',
    'params': {},
    'workspace_path': '/tmp/ws',
    'input_artifact_keys': [],
    'output_artifact_keys': ['result'],
    'mode': 'auto',
}


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _sse_to_events(raw: str) -> List[Dict[str, Any]]:
    """Parse SSE lines into a list of dicts (skips [DONE] and empty lines)."""
    events = []
    for line in raw.splitlines():
        line = line.strip()
        if line.startswith('data: ') and '[DONE]' not in line:
            events.append(json.loads(line[len('data: '):]))
    return events


async def _collect(gen) -> str:
    parts = []
    async for chunk in gen:
        parts.append(chunk)
    return ''.join(parts)


def _install_fake_db(monkeypatch, task=None):
    db = FakeDB(task or {**_DEFAULT_TASK})
    monkeypatch.setattr(runner_mod, 'SubAgentDB', lambda dsn: db)
    return db


def _install_fake_lazyllm(monkeypatch):
    """Patch lazyllm globals/locals init and AutoModel."""
    fake_llm_mod = MagicMock()
    fake_llm_mod.globals._init_sid = lambda sid: None
    fake_llm_mod.locals._init_sid = lambda sid: None
    monkeypatch.setattr(runner_mod, 'lazyllm', fake_llm_mod)
    monkeypatch.setattr(runner_mod, 'AutoModel', lambda model: 'fake_llm')
    monkeypatch.setattr(runner_mod, 'inject_model_config', lambda cfg: None)
    monkeypatch.setattr(runner_mod, 'set_context', lambda ctx: None)


def _install_fake_drive(monkeypatch, events, final_value='task done'):
    """Replace drive_agent in runner_mod with a fake that yields the given events."""
    async def fake_drive(agent, query, *, history=None):
        for ev in events:
            yield ('event', ev)
        yield ('final', final_value)

    monkeypatch.setattr(runner_mod, 'drive_agent', fake_drive)


def _install_fake_build(monkeypatch):
    monkeypatch.setattr(runner_mod, 'build_react_agent',
                        lambda llm, tools, **kw: 'fake_agent')


def _install_fake_translator(monkeypatch):
    """AgentEventFrameTranslator that turns text events into {text:...} frames."""
    class FakeTranslator:
        def __init__(self, query=''):
            self.citation_state: Dict[str, Any] = {}

        def feed(self, item):
            tag = item.get('tag', '')
            if tag == 'text':
                return [{'text': item.get('delta', ''), 'think': None}]
            if tag == 'think':
                return [{'text': None, 'think': item.get('delta', '')}]
            return []

        def finish(self, result):
            return []

    monkeypatch.setattr(runner_mod, 'AgentEventFrameTranslator', FakeTranslator)


# ---------------------------------------------------------------------------
# Test: task not found
# ---------------------------------------------------------------------------

def test_run_subagent_stream_task_not_found(monkeypatch):
    db = FakeDB(task=None)
    monkeypatch.setattr(runner_mod, 'SubAgentDB', lambda dsn: db)

    async def run():
        return await _collect(runner_mod.run_subagent_stream('bad-id', 'dsn://'))

    raw = asyncio.run(run())
    events = _sse_to_events(raw)
    assert events[0]['type'] == 'error'
    assert 'not found' in events[0]['message']
    assert raw.endswith('data: [DONE]\n\n')


# ---------------------------------------------------------------------------
# Test: happy path SSE sequence
# ---------------------------------------------------------------------------

def test_run_subagent_stream_happy_path(monkeypatch):
    db = _install_fake_db(monkeypatch)
    _install_fake_lazyllm(monkeypatch)
    _install_fake_build(monkeypatch)
    _install_fake_translator(monkeypatch)

    # Simulate: text event → tool_calls → tool_results (triggers artifact emit) → text
    tool_calls_event = {'tag': 'tool_calls', 'tool_calls': [{'id': 'c1', 'name': 'save_artifact', 'args': {}}]}
    tool_results_event = {'tag': 'tool_results', 'tool_results': [{'id': 'c1', 'name': 'save_artifact', 'result': 'ok'}]}
    events = [
        {'tag': 'text', 'delta': 'Starting...'},
        tool_calls_event,
        tool_results_event,
        {'tag': 'text', 'delta': 'Done.'},
    ]
    _install_fake_drive(monkeypatch, events)

    # Patch ctx.saved_keys to return declared key so completeness check passes.
    real_ensure = runner_mod.SubAgentContext if hasattr(runner_mod, 'SubAgentContext') else None
    original_set_ctx = runner_mod.set_context

    ctx_holder: list = []

    def capturing_set_context(ctx):
        ctx_holder.append(ctx)
        # Pre-populate saved keys to pass completeness check.
        ctx._artifact_counts['result'] = 1

    monkeypatch.setattr(runner_mod, 'set_context', capturing_set_context)

    async def run():
        return await _collect(runner_mod.run_subagent_stream(_DEFAULT_TASK_ID, 'dsn://'))

    raw = asyncio.run(run())
    events_out = _sse_to_events(raw)
    types_out = [e['type'] for e in events_out]

    assert types_out[0] == 'task_start'
    assert types_out[1] == 'progress'  # initial progress
    assert 'text' in types_out
    assert 'progress' in types_out   # tool_results progress bump
    assert 'done' in types_out
    assert raw.endswith('data: [DONE]\n\n')


# ---------------------------------------------------------------------------
# Test: missing artifact → error frame
# ---------------------------------------------------------------------------

def test_run_subagent_stream_missing_artifact_emits_error(monkeypatch):
    db = _install_fake_db(monkeypatch)
    _install_fake_lazyllm(monkeypatch)
    _install_fake_build(monkeypatch)
    _install_fake_translator(monkeypatch)
    _install_fake_drive(monkeypatch, [])
    # set_context does NOT pre-populate saved keys → completeness check fails

    async def run():
        return await _collect(runner_mod.run_subagent_stream(_DEFAULT_TASK_ID, 'dsn://'))

    raw = asyncio.run(run())
    events_out = _sse_to_events(raw)
    error_events = [e for e in events_out if e.get('type') == 'error']
    assert error_events, 'Expected an error event for missing artifact'
    assert 'result' in error_events[0]['message']
    assert raw.endswith('data: [DONE]\n\n')


# ---------------------------------------------------------------------------
# Test: drive_agent raises → outer except → error frame
# ---------------------------------------------------------------------------

def test_run_subagent_stream_agent_exception_emits_error(monkeypatch):
    _install_fake_db(monkeypatch)
    _install_fake_lazyllm(monkeypatch)
    _install_fake_build(monkeypatch)
    _install_fake_translator(monkeypatch)

    async def exploding_drive(agent, query, *, history=None):
        raise RuntimeError('llm exploded')
        yield  # pragma: no cover

    monkeypatch.setattr(runner_mod, 'drive_agent', exploding_drive)

    async def run():
        return await _collect(runner_mod.run_subagent_stream(_DEFAULT_TASK_ID, 'dsn://'))

    raw = asyncio.run(run())
    events_out = _sse_to_events(raw)
    error_events = [e for e in events_out if e.get('type') == 'error']
    assert error_events
    assert 'llm exploded' in error_events[0]['message']


# ---------------------------------------------------------------------------
# Test: text/think frames from AgentEventFrameTranslator appear in SSE
# ---------------------------------------------------------------------------

def test_run_subagent_stream_text_think_events(monkeypatch):
    db = _install_fake_db(monkeypatch)
    _install_fake_lazyllm(monkeypatch)
    _install_fake_build(monkeypatch)
    _install_fake_translator(monkeypatch)

    events = [
        {'tag': 'think', 'delta': 'reasoning...'},
        {'tag': 'text', 'delta': 'answer'},
    ]
    _install_fake_drive(monkeypatch, events)

    # Pre-populate saved key.
    def pre_save_ctx(ctx):
        ctx._artifact_counts['result'] = 1
    monkeypatch.setattr(runner_mod, 'set_context', pre_save_ctx)

    async def run():
        return await _collect(runner_mod.run_subagent_stream(_DEFAULT_TASK_ID, 'dsn://'))

    raw = asyncio.run(run())
    events_out = _sse_to_events(raw)
    types_out = [e['type'] for e in events_out]
    assert 'think' in types_out
    assert 'text' in types_out


# ---------------------------------------------------------------------------
# Test: _rebuild_history_from_steps pairing validation
# ---------------------------------------------------------------------------

def test_rebuild_history_valid_pairs():
    db = FakeDB()
    db.steps = [
        {'task_id': 't1', 'seq': 0, 'role': 'assistant',
         'content': {'text': '', 'tool_calls': [{'id': 'c1', 'name': 'tool_a', 'args': {}}]}},
        {'task_id': 't1', 'seq': 1, 'role': 'tool',
         'content': {'tool_results': [{'tool_call_id': 'c1', 'name': 'tool_a', 'result': 'res'}]}},
    ]
    history = runner_mod._rebuild_history_from_steps(db, 't1')
    assert len(history) == 2
    assert history[0]['role'] == 'assistant'
    assert history[1]['role'] == 'tool'
    assert history[1]['tool_call_id'] == 'c1'


def test_rebuild_history_orphan_tool_result_dropped():
    db = FakeDB()
    db.steps = [
        {'task_id': 't1', 'seq': 0, 'role': 'assistant',
         'content': {'text': '', 'tool_calls': [{'id': 'c1', 'name': 'tool_a', 'args': {}}]}},
        {'task_id': 't1', 'seq': 1, 'role': 'tool',
         'content': {'tool_results': [{'tool_call_id': 'WRONG_ID', 'name': 'tool_a', 'result': 'r'}]}},
    ]
    history = runner_mod._rebuild_history_from_steps(db, 't1')
    # Orphan tool result: assistant step should also be dropped (we stop at last complete boundary).
    assert all(h.get('role') != 'tool' for h in history)


def test_rebuild_history_no_steps_returns_empty():
    db = FakeDB()
    history = runner_mod._rebuild_history_from_steps(db, 't1')
    assert history == []
