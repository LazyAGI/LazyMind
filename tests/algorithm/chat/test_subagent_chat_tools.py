"""Tests for lazymind.chat.engine.tools.subagent_chat_tools.

Covers: create_subagent auto/manual modes, _resolve_task, and query tools
(list_subagents, get_subagent_status, list_subagent_artifacts,
get_subagent_artifacts).  All external HTTP calls are monkeypatched.
"""
from __future__ import annotations

from typing import Any, Dict, List, Optional
from unittest.mock import patch

import lazymind.chat.engine.tools.subagent_chat_tools as sct


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_TASKS = [
    {'task_id': 'tid-1', 'seq_in_conversation': 1, 'title': '任务A', 'agent_type': 'type_a',
     'status': 'succeeded', 'progress_pct': 100, 'current_phase': '完成',
     'artifacts': [{'artifact_key': 'result', 'content_type': 'text'}]},
    {'task_id': 'tid-2', 'seq_in_conversation': 2, 'title': '素材收集', 'agent_type': 'research',
     'status': 'running', 'progress_pct': 60, 'current_phase': '分析中', 'estimated_sec': 20,
     'artifacts': [
         {'artifact_key': 'refs', 'content_type': 'file_list'},
         {'artifact_key': 'keywords', 'content_type': 'json'},
     ]},
]


def _patch_config(monkeypatch, conv_id='conv-1'):
    """Patch lazyllm.globals so agentic_config returns a known dict."""
    cfg: Dict[str, Any] = {'mode': 'auto', 'conversation_id': conv_id}
    monkeypatch.setattr(sct, '_agentic_config', lambda: cfg)
    return cfg


def _patch_list_tasks(monkeypatch, tasks=None):
    monkeypatch.setattr(sct, '_list_conversation_tasks', lambda: tasks if tasks is not None else _TASKS)


def _r(result):
    """Unwrap tool_success envelope: {success, tool, result: {...}} → inner dict."""
    return result.get('result', result)

def test_resolve_task_by_exact_title():
    task = sct._resolve_task('素材收集', _TASKS)
    assert task['task_id'] == 'tid-2'


def test_resolve_task_by_seq_chinese():
    task = sct._resolve_task('第2个', _TASKS)
    assert task['task_id'] == 'tid-2'


def test_resolve_task_by_seq_step_suffix():
    task = sct._resolve_task('第1步', _TASKS)
    assert task['task_id'] == 'tid-1'


def test_resolve_task_by_agent_type():
    task = sct._resolve_task('research', _TASKS)
    assert task['task_id'] == 'tid-2'


def test_resolve_task_by_substring():
    task = sct._resolve_task('素材', _TASKS)
    assert task['task_id'] == 'tid-2'


def test_resolve_task_not_found():
    task = sct._resolve_task('no_such_task', _TASKS)
    assert task is None


def test_resolve_task_empty_ref():
    assert sct._resolve_task('', _TASKS) is None


# ---------------------------------------------------------------------------
# create_subagent — manual mode (no polling)
# ---------------------------------------------------------------------------

def test_create_subagent_manual_returns_immediately(monkeypatch):
    cfg = _patch_config(monkeypatch)
    cfg['mode'] = 'manual'
    write_calls = []
    monkeypatch.setattr(sct, '_write_agent_data', lambda tag, **kw: write_calls.append((tag, kw)))

    result = sct.create_subagent(
        agent_type='research',
        title='素材收集',
        objective='gather refs',
        output_artifact_keys=['refs'],
    )
    assert _r(result)['status'] == 'ok'
    assert '后台执行' in _r(result)['message']
    # Must have emitted exactly one task_created event.
    assert write_calls[0][0] == 'task_created'
    assert write_calls[0][1]['title'] == '素材收集'
    assert write_calls[0][1]['mode'] == 'manual'


# ---------------------------------------------------------------------------
# create_subagent — auto mode (polling until succeeded)
# ---------------------------------------------------------------------------

def test_create_subagent_auto_polls_and_returns_summary(monkeypatch):
    _patch_config(monkeypatch)
    write_calls = []
    monkeypatch.setattr(sct, '_write_agent_data', lambda tag, **kw: write_calls.append(tag))

    poll_count = [0]

    def fake_get_core_api(path):
        poll_count[0] += 1
        if poll_count[0] < 3:
            return {'status': 'running'}
        return {'status': 'succeeded', 'summary': '完成了'}

    monkeypatch.setattr(sct, 'get_core_api', fake_get_core_api)
    monkeypatch.setattr(sct.time, 'sleep', lambda s: None)

    result = sct.create_subagent(
        agent_type='test',
        title='测试任务',
        objective='do it',
        output_artifact_keys=['out'],
    )
    assert _r(result)['status'] == 'ok'
    assert '已完成' in _r(result)['message']
    assert poll_count[0] >= 3


def test_create_subagent_auto_failed_task(monkeypatch):
    _patch_config(monkeypatch)
    monkeypatch.setattr(sct, '_write_agent_data', lambda tag, **kw: None)
    monkeypatch.setattr(sct, 'get_core_api',
                        lambda path: {'status': 'failed', 'current_phase': '出错了'})
    monkeypatch.setattr(sct.time, 'sleep', lambda s: None)

    result = sct.create_subagent(
        agent_type='test', title='失败任务', objective='fail', output_artifact_keys=['x'],
    )
    assert _r(result)['status'] == 'ok'
    assert '失败' in _r(result)['message']


def test_create_subagent_auto_emits_heartbeat(monkeypatch):
    """Heartbeat must be written when poll interval >= HEARTBEAT_INTERVAL."""
    _patch_config(monkeypatch)
    write_calls = []
    monkeypatch.setattr(sct, '_write_agent_data', lambda tag, **kw: write_calls.append(tag))

    poll_count = [0]

    def fake_get(path):
        poll_count[0] += 1
        if poll_count[0] < 2:
            return {'status': 'running'}
        return {'status': 'succeeded'}

    monkeypatch.setattr(sct, 'get_core_api', fake_get)

    # Force heartbeat: patch time.time so that elapsed time jumps past _HEARTBEAT_INTERVAL
    # between the initial timestamp and the first poll check.
    import time as time_mod
    real_time = time_mod.time
    call_count = [0]

    def fake_time():
        call_count[0] += 1
        # First call records last_heartbeat; subsequent calls report +20s elapsed.
        return real_time() + (call_count[0] - 1) * 20

    monkeypatch.setattr(sct.time, 'time', fake_time)
    monkeypatch.setattr(sct.time, 'sleep', lambda s: None)

    sct.create_subagent(
        agent_type='test', title='hb', objective='hb', output_artifact_keys=['x'],
    )
    assert 'heartbeat' in write_calls


# ---------------------------------------------------------------------------
# list_subagents
# ---------------------------------------------------------------------------

def test_list_subagents_all(monkeypatch):
    _patch_list_tasks(monkeypatch)
    result = sct.list_subagents()
    assert _r(result)['status'] == 'ok'
    assert '任务A' in _r(result)['message']
    assert '素材收集' in _r(result)['message']


def test_list_subagents_filtered_by_status(monkeypatch):
    _patch_list_tasks(monkeypatch)
    result = sct.list_subagents(status='running')
    assert '素材收集' in _r(result)['message']
    assert '任务A' not in _r(result)['message']


def test_list_subagents_empty(monkeypatch):
    _patch_list_tasks(monkeypatch, tasks=[])
    result = sct.list_subagents()
    assert '暂无' in _r(result)['message']


# ---------------------------------------------------------------------------
# get_subagent_status
# ---------------------------------------------------------------------------

def test_get_subagent_status_found(monkeypatch):
    _patch_list_tasks(monkeypatch)
    result = sct.get_subagent_status('素材收集')
    assert _r(result)['status'] == 'ok'
    assert '60%' in _r(result)['message']
    assert '分析中' in _r(result)['message']


def test_get_subagent_status_not_found(monkeypatch):
    _patch_list_tasks(monkeypatch)
    result = sct.get_subagent_status('不存在的任务')
    assert _r(result)['status'] == 'empty'


# ---------------------------------------------------------------------------
# list_subagent_artifacts
# ---------------------------------------------------------------------------

def test_list_subagent_artifacts_found(monkeypatch):
    _patch_list_tasks(monkeypatch)
    result = sct.list_subagent_artifacts('素材收集')
    assert _r(result)['status'] == 'ok'
    assert 'refs' in _r(result)['keys']
    assert 'keywords' in _r(result)['keys']


def test_list_subagent_artifacts_not_found(monkeypatch):
    _patch_list_tasks(monkeypatch)
    result = sct.list_subagent_artifacts('不存在')
    assert _r(result)['status'] == 'empty'


# ---------------------------------------------------------------------------
# get_subagent_artifacts
# ---------------------------------------------------------------------------

def test_get_subagent_artifacts_all_keys(monkeypatch):
    _patch_list_tasks(monkeypatch)
    result = sct.get_subagent_artifacts('素材收集')
    assert _r(result)['status'] == 'ok'
    assert len(_r(result)['artifacts']) == 2


def test_get_subagent_artifacts_filtered_keys(monkeypatch):
    _patch_list_tasks(monkeypatch)
    result = sct.get_subagent_artifacts('素材收集', keys=['refs'])
    assert _r(result)['status'] == 'ok'
    assert all(a['artifact_key'] == 'refs' for a in _r(result)['artifacts'])
