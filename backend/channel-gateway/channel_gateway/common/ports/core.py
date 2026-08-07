from collections.abc import Callable
from typing import Any, Protocol

from channel_gateway.common.domain.chat import (
    ChatOptions,
    CoreStreamUpdate,
    CoreTurnResult,
)


class IntentClient(Protocol):
    def classify_intent(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        provider: str,
        message: str,
        state: dict[str, Any],
        command_registry: dict[str, Any],
    ) -> dict[str, Any]:
        ...

    def get_capability_catalog(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        kinds: set[str],
    ) -> dict[str, Any]:
        ...


class ConversationClient(Protocol):
    def chat(
        self,
        *,
        owner_user_id: str,
        text: str,
        conversation_id: str,
        request_id: str,
        options: ChatOptions | None = None,
        on_stream: Callable[[CoreStreamUpdate], None] | None = None,
    ) -> CoreTurnResult:
        ...

    def list_conversations(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        page_size: int = 100,
        page_token: str = '',
    ) -> dict[str, Any]:
        ...

    def get_conversation_detail(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> dict[str, Any]:
        ...

    def get_conversation_history(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        page_size: int = 3,
        page_token: str = '',
    ) -> dict[str, Any]:
        ...

    def dismiss_terminal_workflow_session(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> bool:
        ...

    def stop_conversation(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        history_id: str,
        request_id: str,
    ) -> bool:
        ...


class TaskClient(Protocol):
    def list_conversation_tasks(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> list[dict[str, Any]]:
        ...


class ExternalAgentClient(Protocol):
    def list_external_threads(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        provider: str,
        cursor: str = '',
        limit: int = 20,
    ) -> dict[str, Any]:
        ...

    def read_external_thread(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        provider: str,
        thread_id: str,
        offset: int | None = None,
        limit: int | None = None,
    ) -> dict[str, Any]:
        ...

    def bind_external_thread(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        provider: str,
        provider_thread_id: str = '',
        new_session: bool = False,
        cwd: str = '',
        conversation_id: str = '',
        display_name: str = '',
    ) -> dict[str, Any]:
        ...

    def snapshot_external_conversation(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        conversation_id: str,
    ) -> dict[str, Any]:
        ...

    def interrupt_external_conversation(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        conversation_id: str,
    ) -> None:
        ...

    def release_external_conversation(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        conversation_id: str,
    ) -> None:
        ...

    def respond_external_request(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        external_request_id: str,
        response: dict[str, Any],
    ) -> None:
        ...


class CapabilityClient(Protocol):
    def update_conversation_search_config(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        dataset_ids: list[str],
    ) -> dict[str, Any]:
        ...

    def update_conversation_agent_settings(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        settings: dict[str, Any],
    ) -> None:
        ...

    def set_default_dataset(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        dataset_id: str,
        name: str,
        enabled: bool,
    ) -> None:
        ...

    def set_tool_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        tool_name: str,
        enabled: bool,
    ) -> None:
        ...

    def set_skill_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        skill_id: str,
        enabled: bool,
    ) -> None:
        ...

    def set_workflow_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        workflow_ref: str,
        enabled: bool,
    ) -> None:
        ...

    def set_personalization_enabled(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        enabled: bool,
    ) -> None:
        ...

    def get_conversation_detail(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> dict[str, Any]:
        ...

    def mention(
        self,
        resource_type: str,
        item: dict[str, Any],
    ) -> dict[str, str]:
        ...


class StaticAssetClient(Protocol):
    def validate_static_asset(
        self,
        *,
        source: str,
        owner_user_id: str,
    ) -> None:
        ...

    def download_static_image(
        self,
        *,
        source: str,
        owner_user_id: str,
    ) -> bytes:
        ...

    def download_static_file(
        self,
        *,
        source: str,
        owner_user_id: str,
    ) -> bytes:
        ...


class LazyMindCore(
    IntentClient,
    ConversationClient,
    ExternalAgentClient,
    CapabilityClient,
    StaticAssetClient,
    Protocol,
):
    pass
