from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass


_TERMINAL_EVENT = 'run_finished'
_RUNTIME_EVENTS = {'model_retry_scheduled', 'model_call_finished', _TERMINAL_EVENT}
_INCOMPLETE_CODES = {
    'length',
    'content_filter',
    'insufficient_system_resource',
    'unknown',
}
_MODEL_FAILURE_CODES = {
    'invalid_request', 'authentication_failed', 'permission_denied', 'not_found',
    'rate_limited', 'usage_limit_exceeded', 'concurrency_limited', 'quota_exhausted',
    'balance_exhausted', 'organization_spend_limit_exceeded', 'project_spend_limit_exceeded',
    'input_filtered', 'output_filtered', 'token_limit', 'request_timeout',
    'provider_overloaded', 'service_unavailable', 'provider_internal_error',
    'provider_rejected', 'conflict', 'unprocessable_entity', 'protocol_error',
    'transport_error',
}


class ChatProtocolError(ValueError):
    pass


@dataclass(frozen=True, slots=True)
class RunTerminal:
    status: str
    reason: str
    code: str
    partial_output: bool

    @property
    def completed(self) -> bool:
        return self.status == 'completed'

    @property
    def error_type(self) -> str:
        if self.status == 'cancelled':
            return 'chat_cancelled'
        if self.reason == 'model_incomplete':
            return 'chat_model_incomplete'
        if self.reason == 'model_failure':
            return 'chat_model_failure'
        return 'chat_runtime_error'

    @property
    def error_message(self) -> str:
        return ':'.join(item for item in (self.status, self.reason, self.code) if item)


def decode_run_terminal(data: Mapping[str, object]) -> RunTerminal | None:
    raw_event = data.get('runtime_event')
    if raw_event is None:
        return None
    if not isinstance(raw_event, Mapping):
        raise ChatProtocolError('runtime_event must be an object')
    schema_version = raw_event.get('schema_version')
    if not isinstance(schema_version, int) or isinstance(schema_version, bool) or schema_version != 1:
        raise ChatProtocolError('unsupported runtime_event schema_version')
    if not _string(raw_event.get('event_id')) or not _string(raw_event.get('run_id')):
        raise ChatProtocolError('runtime_event envelope fields are required')
    event_type = _string(raw_event.get('type'))
    if event_type not in _RUNTIME_EVENTS:
        raise ChatProtocolError('unsupported runtime_event type')
    if event_type != _TERMINAL_EVENT:
        return None
    raw_terminal = raw_event.get('data')
    if not isinstance(raw_terminal, Mapping):
        raise ChatProtocolError('run_finished data must be an object')
    if not isinstance(raw_terminal.get('partial_output'), bool):
        raise ChatProtocolError('run_finished partial_output must be boolean')
    code_present = 'code' in raw_terminal
    if code_present and not isinstance(raw_terminal.get('code'), str):
        raise ChatProtocolError('run_finished code must be a string')
    terminal = RunTerminal(
        status=_string(raw_terminal.get('status')),
        reason=_string(raw_terminal.get('reason')),
        code=_string(raw_terminal.get('code')),
        partial_output=raw_terminal['partial_output'],
    )
    _validate_terminal(terminal, code_present=code_present)
    return terminal


def terminal_has_business_payload(data: Mapping[str, object]) -> bool:
    return bool(
        _text(data.get('text'))
        or _text(data.get('think'))
        or data.get('sources')
        or data.get('task_created')
        or data.get('ask_pending')
    )


def _validate_terminal(terminal: RunTerminal, *, code_present: bool) -> None:
    if terminal.status == 'completed':
        valid = terminal.reason in {'normal', 'awaiting_user_input'} and not code_present
    elif terminal.status == 'interrupted':
        valid = (
            terminal.reason == 'model_incomplete'
            and terminal.code in _INCOMPLETE_CODES
        ) or (
            terminal.reason == 'model_failure'
            and terminal.code in _MODEL_FAILURE_CODES
        )
    elif terminal.status == 'failed':
        valid = (
            terminal.reason == 'model_failure'
            and terminal.code in _MODEL_FAILURE_CODES
        ) or (
            terminal.reason == 'runtime_failure'
            and bool(terminal.code)
        )
    elif terminal.status == 'cancelled':
        valid = terminal.reason == 'user_cancelled' and not code_present
    else:
        valid = False
    if not valid:
        raise ChatProtocolError('invalid run_finished status/reason/code combination')


def _text(value: object) -> str:
    return str(value or '').strip()


def _string(value: object) -> str:
    return value.strip() if isinstance(value, str) else ''


__all__ = [
    'ChatProtocolError',
    'RunTerminal',
    'decode_run_terminal',
    'terminal_has_business_payload',
]
