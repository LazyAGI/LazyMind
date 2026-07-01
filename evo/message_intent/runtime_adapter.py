from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any, Protocol

RUN_ID = 'run_1'

__all__ = ['MessageCommandContext', 'MessageRuntimePort', 'RUN_ID']


@dataclass(frozen=True)
class MessageCommandContext:
    turn_id: str
    intent_index: int = 0
    run_id: str = RUN_ID


class MessageRuntimePort(Protocol):
    def continue_flow(self, thread_id: str, context: MessageCommandContext) -> dict[str, Any]:
        ...

    def pause_flow(self, thread_id: str, context: MessageCommandContext) -> dict[str, Any]:
        ...

    def cancel_flow(self, thread_id: str, context: MessageCommandContext) -> dict[str, Any]:
        ...

    def retry_failed(self, thread_id: str, context: MessageCommandContext) -> dict[str, Any]:
        ...

    def rerun_case(self, thread_id: str, case_id: str, context: MessageCommandContext) -> dict[str, Any]:
        ...

    def read_report_section(
        self,
        thread_id: str,
        *,
        artifact_ref: str,
        selector: str,
        cursor: str,
        max_chars: int,
        context: MessageCommandContext,
    ) -> dict[str, Any]:
        ...

    def read_case_result(
        self,
        thread_id: str,
        *,
        case_id: str,
        selector: str,
        cursor: str,
        max_chars: int,
        context: MessageCommandContext,
    ) -> dict[str, Any]:
        ...

    def prepare_patch(
        self,
        thread_id: str,
        *,
        artifact_ref: str,
        json_pointer: str,
        patch_value: Any,
        provenance: Mapping[str, Any],
        context: MessageCommandContext,
    ) -> dict[str, Any]:
        ...

    def stale_expected_refs(self, thread_id: str, expected_refs: tuple[str, ...]) -> list[str]:
        ...

    def execute_approval(
        self,
        thread_id: str,
        *,
        command_id: str,
        prepared_payload: Mapping[str, Any],
        expected_fingerprint: str,
    ) -> dict[str, Any]:
        ...
