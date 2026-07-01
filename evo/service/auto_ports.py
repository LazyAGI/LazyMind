from __future__ import annotations

from typing import Any

from fastapi import HTTPException

from evo.auto_agent import ActiveApproval, AutoIntervention, CommandStatus, PortCommandResult

_OK_STATUSES = {'accepted', 'accepted_existing'}
_RUNNING_STATUSES = {'conflict', 'in_progress', 'running'}
_ERROR_STATUSES = {'error', 'failed'}


class HubAutoAgentPorts:
    def __init__(self, hub: Any) -> None:
        self.hub = hub

    def get_thread(self, thread_id: str) -> dict[str, Any]:
        return self.hub.get_thread(thread_id)

    def flow_status(self, thread_id: str) -> dict[str, Any]:
        return self.hub.flow_status(thread_id)

    def artifact(self, thread_id: str, artifact_id: str) -> dict[str, Any] | None:
        try:
            return self.hub.artifact(thread_id, artifact_id)
        except HTTPException as exc:
            if exc.status_code == 404:
                return None
            raise

    def active_approval(self, thread_id: str) -> ActiveApproval | None:
        return self.hub.active_approval(thread_id)

    def start_flow(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        return _result(lambda: self.hub.start(thread_id, {'command_id': command_id}))

    def continue_flow(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        return _result(lambda: self.hub.continue_thread(thread_id, {'command_id': command_id}))

    def pause_flow(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        return _result(lambda: self.hub.pause(thread_id, command_id=command_id))

    def cancel_flow(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        return _result(lambda: self.hub.cancel(thread_id, command_id=command_id))

    def retry_failed(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        return _result(lambda: self.hub.retry(thread_id, {'command_id': command_id}))

    def submit_intervention(
        self,
        thread_id: str,
        *,
        message_id: str,
        metadata: dict[str, Any],
        intervention: AutoIntervention,
    ) -> PortCommandResult:
        del metadata
        return _result(
            lambda: self.hub.execute_auto_intervention(
                thread_id,
                intervention.model_dump(mode='json'),
                command_id=message_id,
            )
        )

    def resolve_approval(
        self,
        thread_id: str,
        *,
        action: str,
        approval_token: str,
        command_id: str,
    ) -> PortCommandResult:
        approval = self.active_approval(thread_id)
        if action == 'approve' and approval is not None and approval.status == 'resolving':
            return _result(lambda: self.hub.probe_resolving_approval(thread_id, approval_token=approval_token))
        return _result(
            lambda: self.hub.resolve_approval(
                thread_id,
                action=action,
                approval_token=approval_token,
                command_id=command_id,
            )
        )


def _result(call: Any) -> PortCommandResult:
    try:
        return _command_result(call())
    except HTTPException as exc:
        detail = exc.detail if isinstance(exc.detail, dict) else {'detail': str(exc.detail)}
        if isinstance(exc.detail, dict):
            status = str(detail.get('status') or '').lower()
            if status in _OK_STATUSES or status in _RUNNING_STATUSES or status in _ERROR_STATUSES:
                return _command_result(detail)
        return PortCommandResult(status=CommandStatus.ERROR, raw=detail, error=str(exc.detail))


def _command_result(raw: dict[str, Any]) -> PortCommandResult:
    status = str(raw.get('status') or '').lower() if isinstance(raw, dict) else ''
    if status in _ERROR_STATUSES:
        return PortCommandResult(status=CommandStatus.ERROR, raw=raw, error=str(raw.get('reason') or status))
    if status in _RUNNING_STATUSES:
        return PortCommandResult(status=CommandStatus.RUNNING, raw=raw)
    if status in _OK_STATUSES:
        return PortCommandResult(status=CommandStatus.OK, raw=raw)
    return PortCommandResult(status=CommandStatus.OK, raw=raw)
