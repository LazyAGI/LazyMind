from dataclasses import dataclass
from typing import Any

from channel_gateway.common.domain.channel import ChannelAddress


class FeishuRuntimeError(RuntimeError):
    pass


@dataclass(frozen=True, slots=True)
class FeishuInboundMessage:
    message_id: str
    chat_id: str
    chat_type: str
    message_type: str
    sender_id: str
    sender_is_bot: bool
    root_id: str
    parent_id: str
    thread_id: str
    text: str


@dataclass(frozen=True, slots=True)
class FeishuInboundAction:
    message_id: str
    chat_id: str
    sender_id: str
    action: str
    text: str
    selection: str
    selection_id: str
    root_message_id: str
    intended_chat_id: str
    ask_answers_structured: dict[str, Any] | None


@dataclass(frozen=True, slots=True)
class FeishuAppRegistration:
    app_id: str
    app_secret: str
    owner_open_id: str
    owner_name: str
    tenant_key: str


@dataclass(frozen=True, slots=True)
class FeishuAppCredentials:
    app_id: str
    app_secret: str
    provider_account_id: str
    provider_tenant_key: str
    display_name: str


@dataclass(frozen=True, slots=True)
class FeishuWorkspace:
    account_id: str
    chat_id: str
    owner_open_id: str
    status: str
    last_error: str


class FeishuAddressFactory:
    @staticmethod
    def direct(
        account_id: str,
        chat_id: str,
        sender_id: str,
    ) -> ChannelAddress:
        canonical = (
            f'feishu:{account_id}:p2p:{chat_id}:{sender_id}'
        )
        return ChannelAddress(
            canonical_key=canonical,
            actor_key=canonical,
        )

    @staticmethod
    def workspace_thread(
        account_id: str,
        chat_id: str,
        root_message_id: str,
        sender_id: str,
    ) -> ChannelAddress:
        return ChannelAddress(
            canonical_key=(
                f'feishu:{account_id}:workspace:{chat_id}:'
                f'topic:{root_message_id}'
            ),
            actor_key=(
                f'feishu:{account_id}:actor:{sender_id}'
            ),
        )
