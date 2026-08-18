from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Dict, Optional
from uuid import uuid4


RUNTIME_EVENT_TYPES = frozenset({'model_retry_scheduled', 'model_call_finished', 'run_finished'})


def runtime_event(event_type: str, run_id: str, data: Dict[str, Any], *, event_id: Optional[str] = None) -> dict:
    if event_type not in RUNTIME_EVENT_TYPES:
        raise ValueError(f'unsupported runtime event type: {event_type}')
    return {
        'schema_version': 1,
        'event_id': event_id or uuid4().hex,
        'run_id': run_id,
        'type': event_type,
        'data': data,
    }


@dataclass
class RunAccumulator:
    run_id: str
    semantic_output: bool = False
    ask_pending: bool = False
    terminal_emitted: bool = False
    last_model_terminal: Optional[Dict[str, Any]] = None

    def observe_model_event(self, event: Dict[str, Any]) -> None:
        if event.get('type') != 'model_call_finished': return
        data = event.get('data')
        if isinstance(data, dict):
            self.last_model_terminal = data
            self.semantic_output = self.semantic_output or bool(data.get('has_semantic_output'))

    def finish(self, *, succeeded: bool) -> dict:
        if self.terminal_emitted:
            raise RuntimeError(f'run {self.run_id} already has a terminal')
        self.terminal_emitted = True
        status, reason, code = self._terminal_fields(succeeded)
        data: Dict[str, Any] = {
            'status': status,
            'reason': reason,
            'partial_output': self.semantic_output,
        }
        if code: data['code'] = code
        terminal = self.last_model_terminal or {}
        if terminal.get('model_call_id'):
            data['model_call_id'] = terminal['model_call_id']
        failure = terminal.get('failure')
        if isinstance(failure, dict):
            if failure.get('diagnostic_id'):
                data['diagnostic_id'] = failure['diagnostic_id']
            if isinstance(failure.get('provider_http_status'), int):
                data['provider_http_status'] = failure['provider_http_status']
            if isinstance(failure.get('retry_after_ms'), int):
                data['retry_after_ms'] = failure['retry_after_ms']
        return runtime_event('run_finished', self.run_id, data)

    def _terminal_fields(self, succeeded: bool) -> tuple[str, str, str]:
        if succeeded:
            return 'completed', 'awaiting_user_input' if self.ask_pending else 'normal', ''
        terminal = self.last_model_terminal or {}
        if terminal.get('kind') == 'finish':
            finish = str(terminal.get('finish') or 'unknown')
            return 'interrupted', 'model_incomplete', finish
        if terminal.get('kind') == 'failure':
            failure = terminal.get('failure') or {}
            code = str(
                failure.get('code')
                or failure.get('origin')
                or 'provider_rejected'
            )
            status = 'interrupted' if terminal.get('has_semantic_output') else 'failed'
            return status, 'model_failure', code
        return 'failed', 'runtime_failure', 'runtime_failure'
