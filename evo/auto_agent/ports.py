from __future__ import annotations

from typing import Any, Protocol

from .intervention import AutoIntervention
from .models import ActiveApproval, PortCommandResult


class AutoAgentPorts(Protocol):
    def get_thread(self, thread_id: str) -> dict[str, Any]:
        ...

    def flow_status(self, thread_id: str) -> dict[str, Any]:
        ...

    def artifact(self, thread_id: str, artifact_id: str) -> dict[str, Any] | None:
        ...

    def active_approval(self, thread_id: str) -> ActiveApproval | None:
        ...

    def start_flow(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        ...

    def continue_flow(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        ...

    def pause_flow(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        ...

    def cancel_flow(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        ...

    def retry_failed(self, thread_id: str, *, command_id: str) -> PortCommandResult:
        ...

    def submit_intervention(
        self,
        thread_id: str,
        *,
        message_id: str,
        metadata: dict[str, Any],
        intervention: AutoIntervention,
    ) -> PortCommandResult:
        ...

    def resolve_approval(
        self,
        thread_id: str,
        *,
        action: str,
        approval_token: str,
        command_id: str,
    ) -> PortCommandResult:
        ...
