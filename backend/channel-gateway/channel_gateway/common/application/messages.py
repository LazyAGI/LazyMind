from __future__ import annotations

import logging
from collections.abc import Callable
from typing import Any

from channel_gateway.common.application.actions import ChannelActionExecutor
from channel_gateway.common.application.routing import (
    ChannelCommandRouter,
)
from channel_gateway.common.application.replies import ChannelReply
from channel_gateway.common.domain.chat import CoreStreamUpdate
from channel_gateway.common.domain.commands import (
    ActionKind,
    ChatCommand,
    ConversationNewCommand,
    ConversationSwitchCommand,
)


_logger = logging.getLogger(__name__)


class ChannelMessageService:
    """Runs the linear route -> action pipeline."""

    def __init__(
        self,
        *,
        router: ChannelCommandRouter,
        executor: ChannelActionExecutor,
    ):
        self._router = router
        self._executor = executor

    def process(
        self,
        *,
        provider: str,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        text: str,
        request_id: str,
        surface: str = 'direct',
        provider_context: dict[str, Any] | None = None,
        on_stream: Callable[[CoreStreamUpdate], None] | None = None,
    ) -> ChannelReply:
        context = dict(provider_context or {})
        routed = self._router.route(
            provider=provider,
            account_id=account_id,
            external_address_hash=external_address_hash,
            owner_user_id=owner_user_id,
            text=text,
            request_id=request_id,
            surface=surface,
            provider_context=context,
        )
        if isinstance(routed, str):
            return ChannelReply(
                intent_kind=ActionKind.SELECTION_CHOOSE,
                text=routed,
            )
        _logger.info(
            'channel_intent_routed source=%s action=%s request_id=%s',
            routed.source,
            routed.command.command.value,
            request_id,
        )
        if (
            on_stream is not None
            and _starts_core_stream(routed.command)
        ):
            on_stream(CoreStreamUpdate())

        return self._executor.execute(
            command=routed.command,
            account_id=account_id,
            external_address_hash=external_address_hash,
            owner_user_id=owner_user_id,
            request_id=request_id,
            grounding_messages=routed.grounding_messages,
            catalog=routed.catalog,
            provider=provider,
            provider_context=context,
            on_stream=on_stream,
        )


def _starts_core_stream(command) -> bool:
    if isinstance(command, ChatCommand):
        return True
    if isinstance(
        command,
        (ConversationNewCommand, ConversationSwitchCommand),
    ):
        return bool(command.parameters.message)
    return False
