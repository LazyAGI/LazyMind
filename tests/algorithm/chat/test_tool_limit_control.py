from __future__ import annotations

import json
import threading
import time

import lazyllm

from lazymind.chat.engine.agent_runtime.tool_limit_control import ToolLimitDecisionCoordinator
from lazymind.config import config


def _read_events() -> list[dict]:
    return [
        json.loads(raw)
        for raw in lazyllm.FileSystemQueue().dequeue() or []
        if raw
    ]


def test_tool_limit_coordinator_times_out_and_invalidates_decision() -> None:
    coordinator = ToolLimitDecisionCoordinator()
    sid = f'tool-limit-timeout-{time.time_ns()}'
    lazyllm.globals._init_sid(sid)

    with config.temp('agentic_tool_limit_wait_timeout', 0.0), \
            config.temp('agentic_expanded_max_rounds', 200):
        assert coordinator.on_max_retries(None, 21, 21) is None

    event = next(item for item in _read_events() if item['tag'] == 'tool_limit_pending')
    assert event['used_rounds'] == 21
    assert event['round_limit'] == 21
    assert event['expanded_max_rounds'] == 200
    assert event['timeout_seconds'] == 0
    assert coordinator.submit(sid, event['decision_id'], 'continue') is False


def test_tool_limit_coordinator_continues_same_invocation() -> None:
    coordinator = ToolLimitDecisionCoordinator()
    sid = f'tool-limit-continue-{time.time_ns()}'
    lazyllm.globals._init_sid(sid)
    result: dict[str, int | None] = {}

    def wait_for_decision() -> None:
        lazyllm.globals._init_sid(sid)
        result['limit'] = coordinator.on_max_retries(None, 21, 21)

    with config.temp('agentic_tool_limit_wait_timeout', 2.0), \
            config.temp('agentic_expanded_max_rounds', 200):
        thread = threading.Thread(target=wait_for_decision)
        thread.start()
        decision_id = ''
        deadline = time.time() + 1
        while time.time() < deadline and not decision_id:
            for event in _read_events():
                if event['tag'] == 'tool_limit_pending':
                    decision_id = event['decision_id']
                    break
            time.sleep(0.01)
        assert decision_id
        assert coordinator.submit(sid, decision_id, 'continue') is True
        thread.join(timeout=2)

    assert not thread.is_alive()
    assert result['limit'] == 200
    assert coordinator.submit(sid, decision_id, 'continue') is False


def test_tool_limit_coordinator_uses_runtime_expanded_limit() -> None:
    coordinator = ToolLimitDecisionCoordinator()
    lazyllm.globals._init_sid(f'tool-limit-auto-expand-{time.time_ns()}')
    _read_events()
    previous = lazyllm.locals.get('_lazyllm_agent')
    lazyllm.locals['_lazyllm_agent'] = {'workspace': {'_react_round_limit': 200}}

    try:
        with config.temp('agentic_expanded_max_rounds', 200):
            assert coordinator.on_max_retries(None, 21, 21) == 200
    finally:
        if previous is None:
            lazyllm.locals.pop('_lazyllm_agent', None)
        else:
            lazyllm.locals['_lazyllm_agent'] = previous

    assert not [
        item for item in _read_events()
        if item['tag'] == 'tool_limit_pending'
    ]
