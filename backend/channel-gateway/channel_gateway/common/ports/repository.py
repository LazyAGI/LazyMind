import datetime as dt
from typing import Any, Protocol


class IntentRepository(Protocol):
    def get_selection_context(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any] | None:
        ...


class NavigationRepository(
    IntentRepository,
    Protocol,
):
    def get_route(self, account_id: str, external_address_hash: str) -> str:
        ...

    def get_navigation_state(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any] | None:
        ...

    def begin_new_conversation(
        self,
        account_id: str,
        external_address_hash: str,
        draft: dict[str, Any] | None = None,
    ) -> None:
        ...

    def activate_conversation(
        self,
        account_id: str,
        external_address_hash: str,
        conversation_id: str,
        history_next_page_token: str | None = None,
        *,
        consume_pending_turn: bool = False,
        preserve_selection: bool = False,
    ) -> None:
        ...

    def save_selection_snapshot(
        self,
        account_id: str,
        external_address_hash: str,
        kind: str,
        items: list[dict[str, Any]],
        expires_at: dt.datetime,
        continuation: dict[str, Any] | None = None,
    ) -> None:
        ...

    def get_selection_snapshot(
        self,
        account_id: str,
        external_address_hash: str,
        expected_kind: str | None = None,
    ) -> list[dict[str, Any]] | None:
        ...

    def clear_selection_snapshot(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> None:
        ...

    def save_pending_turn(
        self,
        account_id: str,
        external_address_hash: str,
        options: dict[str, Any],
    ) -> None:
        ...

    def get_pending_turn(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any]:
        ...

    def get_new_conversation_draft(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any]:
        ...

    def set_history_cursor(
        self,
        account_id: str,
        external_address_hash: str,
        conversation_id: str,
        next_page_token: str,
    ) -> None:
        ...
