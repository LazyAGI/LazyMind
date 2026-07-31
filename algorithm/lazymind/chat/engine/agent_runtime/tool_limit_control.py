from __future__ import annotations

import json
import threading
import time
import uuid
from asyncio import CancelledError
from typing import Any, Optional

import lazyllm
from lazyllm import FileSystemQueue
from lazyllm.tools.agent.base import _write_agent_data

from lazymind.config import config


class ToolLimitDecisionCoordinator:
    def __init__(self) -> None:
        self._active_decisions: dict[str, str] = {}
        self._lock = threading.RLock()

    def _register(self, sid: str, decision_id: str) -> None:
        with self._lock:
            self._active_decisions[sid] = decision_id

    def _unregister(self, sid: str, decision_id: str) -> None:
        with self._lock:
            if self._active_decisions.get(sid) == decision_id:
                self._active_decisions.pop(sid, None)

    def submit(self, sid: str, decision_id: str, action: str) -> bool:
        normalized_action = str(action or '').strip().lower()
        if normalized_action not in {'continue', 'summarize'}:
            return False
        with self._lock:
            if self._active_decisions.get(sid) != decision_id:
                return False
            lazyllm.globals._init_sid(sid=sid)
            FileSystemQueue(klass='agent_control').enqueue(json.dumps({
                'decision_id': decision_id,
                'action': normalized_action,
            }))
            self._active_decisions.pop(sid, None)
        return True

    def _wait_for_action(self, decision_id: str, timeout: float) -> str:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for raw in FileSystemQueue(klass='agent_control').dequeue() or []:
                try:
                    payload = json.loads(raw)
                except (TypeError, ValueError):
                    continue
                if payload.get('decision_id') != decision_id:
                    continue
                action = str(payload.get('action') or '').strip().lower()
                if action in {'continue', 'summarize'}:
                    return action
            for raw in FileSystemQueue(klass='cancel').dequeue() or []:
                try:
                    if json.loads(raw).get('tag') == 'cancel':
                        raise CancelledError('stopped by user')
                except (TypeError, ValueError):
                    continue
            time.sleep(min(0.2, max(0.01, deadline - time.monotonic())))
        return 'summarize'

    def on_max_retries(self, output: Any, used_rounds: int, current_limit: int) -> Optional[int]:
        expanded_max_rounds = max(2, int(config['agentic_expanded_max_rounds']))
        if used_rounds >= expanded_max_rounds:
            return None
        workspace = lazyllm.locals.get('_lazyllm_agent', {}).get('workspace')
        runtime_round_limit = (
            workspace.get('_react_round_limit')
            if isinstance(workspace, dict) else None
        )
        if isinstance(runtime_round_limit, int) and runtime_round_limit > current_limit:
            lazyllm.LOG.info(
                f'ChatAgent reached its previous tool round boundary={current_limit}; '
                f'continuing with expanded limit={runtime_round_limit}.'
            )
            return runtime_round_limit
        timeout = max(0, float(config['agentic_tool_limit_wait_timeout']))
        sid = lazyllm.globals._sid
        decision_id = uuid.uuid4().hex
        self._register(sid, decision_id)
        try:
            lazyllm.LOG.warning(
                f'ChatAgent reached tool round limit={current_limit}; waiting up to '
                f'{timeout:g}s for a decision.'
            )
            _write_agent_data(
                'tool_limit_pending',
                decision_id=decision_id,
                used_rounds=used_rounds,
                round_limit=current_limit,
                expanded_max_rounds=expanded_max_rounds,
                timeout_seconds=timeout,
            )
            decision = self._wait_for_action(decision_id, timeout)
            return expanded_max_rounds if decision == 'continue' else None
        finally:
            self._unregister(sid, decision_id)


tool_limit_decision_coordinator = ToolLimitDecisionCoordinator()
