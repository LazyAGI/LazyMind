from __future__ import annotations

import logging
from dataclasses import dataclass, replace
from typing import Any

from channel_gateway.common.application.actions import ChannelActionExecutor
from channel_gateway.common.application.routing import (
    ChannelCommandRouter,
    RoutedCommand,
)
from channel_gateway.common.application.replies import ChannelReply
from channel_gateway.common.domain.channel import (
    NativeConversationTarget,
    NativeTargetError,
)
from channel_gateway.common.domain.commands import (
    ActionKind,
    ConversationNewCommand,
    ConversationSwitchCommand,
    command_kind,
)
from channel_gateway.common.domain.outbound import presentation_from_dict
from channel_gateway.common.ports.messaging import (
    NativeConversationSurfaceRegistry,
)
from channel_gateway.common.ports.repository import NavigationRepository


_logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class NativeTransition:
    external_address_hash: str
    surface: str
    target: NativeConversationTarget | None = None
    reply: ChannelReply | None = None


class NativeConversationCoordinator:
    """Projects a Core conversation transition onto a provider-native target."""

    def __init__(
        self,
        *,
        store: NavigationRepository,
        surfaces: NativeConversationSurfaceRegistry,
        executor: ChannelActionExecutor,
    ):
        self._store = store
        self._surfaces = surfaces
        self._executor = executor

    def open(
        self,
        *,
        routed: RoutedCommand,
        provider: str,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        text: str,
        request_id: str,
        surface: str,
        provider_context: dict[str, Any],
    ) -> NativeTransition:
        command = routed.command
        if not isinstance(
            command,
            (ConversationNewCommand, ConversationSwitchCommand),
        ):
            return NativeTransition(external_address_hash, surface)
        navigator = self._surfaces.surface(provider)
        if navigator is None:
            return NativeTransition(external_address_hash, surface)

        has_current_conversation = bool(
            self._store.get_route(account_id, external_address_hash)
        )
        self._store.reserve_native_operation(
            account_id=account_id,
            provider=provider,
            operation_id=request_id,
            operation_kind=command.command.value,
            container_id=str(provider_context.get('chat_id') or ''),
            source_external_address_hash=external_address_hash,
            prepared_command=command.model_dump(mode='json'),
            grounding_messages=list(routed.grounding_messages),
            prepared_catalog=routed.catalog,
        )
        rejected = self._executor.prepare_native_transition(
            command=command,
            account_id=account_id,
            external_address_hash=external_address_hash,
            owner_user_id=owner_user_id,
            request_id=request_id,
            grounding_messages=routed.grounding_messages,
            catalog=routed.catalog,
        )
        if rejected is not None:
            self._store.fail_native_operation(
                account_id=account_id,
                provider=provider,
                operation_id=request_id,
                error='native_transition_rejected',
            )
            return NativeTransition(
                external_address_hash,
                surface,
                reply=rejected,
            )
        try:
            target = navigator.open_conversation(
                account_id=account_id,
                owner_user_id=owner_user_id,
                current_external_address_hash=external_address_hash,
                current_provider_context=provider_context,
                has_current_conversation=has_current_conversation,
                operation_kind=command.command.value,
                request_id=request_id,
                request_text=text,
                prepared_command=command.model_dump(mode='json'),
                grounding_messages=list(routed.grounding_messages),
                prepared_catalog=routed.catalog,
            )
        except NativeTargetError:
            raise
        if target is None:
            return NativeTransition(external_address_hash, surface)

        target_surface = str(
            target.provider_context.get('surface') or surface
        )
        if target.cached_result is not None:
            return NativeTransition(
                target.external_address_hash,
                target_surface,
                target=target,
                reply=self._cached_reply(
                    command_kind(command),
                    target,
                ),
            )
        if target.reused and self._store.get_route(
            account_id,
            target.external_address_hash,
        ):
            try:
                recovered = self._executor.recover_native_transition(
                    intent_kind=command_kind(command),
                    account_id=account_id,
                    external_address_hash=target.external_address_hash,
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                )
            except Exception as exc:
                raise NativeTargetError(target) from exc
            return NativeTransition(
                target.external_address_hash,
                target_surface,
                target=target,
                reply=replace(recovered, target=target),
            )
        return NativeTransition(
            target.external_address_hash,
            target_surface,
            target=target,
        )

    @staticmethod
    def _cached_reply(
        intent_kind: ActionKind,
        target: NativeConversationTarget,
    ) -> ChannelReply:
        cached = target.cached_result or {}
        raw_presentations = cached.get('presentations')
        presentations = (
            raw_presentations
            if isinstance(raw_presentations, list)
            else []
        )
        return ChannelReply(
            intent_kind=intent_kind,
            text=str(cached.get('text') or ''),
            core_events=tuple(cached.get('core_events') or ()),
            sources=tuple(cached.get('sources') or ()),
            presentations=tuple(
                presentation
                for item in presentations
                if isinstance(item, dict)
                if (
                    presentation := presentation_from_dict(item)
                )
                is not None
            ),
            suppress_text_when_presented=bool(
                cached.get('suppress_text_when_presented', False)
            ),
            target=target,
        )


class ChannelMessageService:
    """Runs the linear route -> native target -> action pipeline."""

    def __init__(
        self,
        *,
        router: ChannelCommandRouter,
        native_conversations: NativeConversationCoordinator,
        executor: ChannelActionExecutor,
    ):
        self._router = router
        self._native_conversations = native_conversations
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
    ) -> ChannelReply:
        context = dict(provider_context or {})
        source_external_address_hash = external_address_hash
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

        transition = self._native_conversations.open(
            routed=routed,
            provider=provider,
            account_id=account_id,
            external_address_hash=external_address_hash,
            owner_user_id=owner_user_id,
            text=text,
            request_id=request_id,
            surface=surface,
            provider_context=context,
        )
        if transition.reply is not None:
            return transition.reply

        try:
            reply = self._executor.execute(
                command=routed.command,
                account_id=account_id,
                external_address_hash=(
                    transition.external_address_hash
                ),
                owner_user_id=owner_user_id,
                request_id=request_id,
                grounding_messages=routed.grounding_messages,
                catalog=routed.catalog,
                surface=transition.surface,
                provider=provider,
                provider_context=context,
                source_external_address_hash=(
                    source_external_address_hash
                ),
            )
        except Exception as exc:
            if transition.target is not None:
                raise NativeTargetError(
                    transition.target
                ) from exc
            raise
        return replace(reply, target=transition.target)
