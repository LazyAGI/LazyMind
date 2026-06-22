from __future__ import annotations

from collections.abc import Mapping
from typing import Any, Protocol

from .intervention import AutoIntervention
from .models import ActiveApproval


class AutoAgentPorts(Protocol):
    def get_thread(self, thread_id: str) -> dict[str, Any]:
        ...

    def flow_status(self, thread_id: str) -> dict[str, Any]:
        ...

    def artifact(self, thread_id: str, artifact_id: str) -> dict[str, Any] | None:
        ...

    def active_approval(self, thread_id: str) -> ActiveApproval | None:
        ...

    def start_flow(self, thread_id: str, *, command_id: str) -> dict[str, Any]:
        ...

    def continue_flow(self, thread_id: str, *, command_id: str) -> dict[str, Any]:
        ...

    def pause_flow(self, thread_id: str, *, command_id: str) -> dict[str, Any]:
        ...

    def cancel_flow(self, thread_id: str, *, command_id: str) -> dict[str, Any]:
        ...

    def retry_failed(self, thread_id: str, *, command_id: str) -> dict[str, Any]:
        ...

    def send_message(
        self,
        thread_id: str,
        *,
        content: str,
        message_id: str,
        metadata: Mapping[str, Any],
        intervention: AutoIntervention | None = None,
    ) -> dict[str, Any]:
        ...

    def resolve_approval(
        self,
        thread_id: str,
        *,
        action: str,
        approval_token: str,
        command_id: str,
    ) -> dict[str, Any]:
        ...
