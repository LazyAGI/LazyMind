from __future__ import annotations

import logging
from typing import Any

from channel_gateway.common.channel_actions import (
    ChannelActionExecutor,
    ChannelReply,
)
from channel_gateway.common.commands import (
    SCHEMA_VERSION,
    CapabilityListCommand,
    ChatCommand,
    ChatParameters,
    CommandEnvelope,
    ConversationNewCommand,
    SelectionContinuation,
)
from channel_gateway.common.database import GatewayStore
from channel_gateway.common.intent_router import (
    ChannelIntentClassifier,
    ExactShortcutParser,
    canonicalize_command,
    resolve_pending_selection,
    validate_command,
)
from channel_gateway.common.lazymind import LazyMindError


_logger = logging.getLogger(__name__)


class ChannelMessageService:
    """Coordinates one inbound message; it contains no business action branches."""

    def __init__(
        self,
        *,
        store: GatewayStore,
        shortcuts: ExactShortcutParser,
        classifier: ChannelIntentClassifier,
        executor: ChannelActionExecutor,
    ):
        self._store = store
        self._shortcuts = shortcuts
        self._classifier = classifier
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
    ) -> ChannelReply:
        shortcut = self._shortcuts.parse(
            account_id=account_id,
            external_address_hash=external_address_hash,
            text=text,
        )
        routing_source = 'shortcut'
        if shortcut is None:
            try:
                command = self._classifier.classify(
                    provider=provider,
                    owner_user_id=owner_user_id,
                    message=text,
                    request_id=f'{request_id}_intent',
                    state=self._classifier_state(account_id, external_address_hash),
                )
                routing_source = 'llm'
            except LazyMindError as exc:
                _logger.warning(
                    'channel_intent_fallback_chat request_id=%s error_type=%s',
                    request_id,
                    type(exc).__name__,
                )
                command = ChatCommand(
                    schema_version=SCHEMA_VERSION,
                    command='chat',
                    parameters=ChatParameters(message=text),
                )
                routing_source = 'fallback'
            grounding_messages = (text,)
        else:
            command = shortcut.command
            grounding_messages = shortcut.grounding_messages
        command = canonicalize_command(command, text)
        resumed = (
            resolve_pending_selection(
                command,
                self._store.get_selection_context(
                    account_id,
                    external_address_hash,
                ),
                text,
            )
            if routing_source == 'llm' or command.command.value == 'selection.choose'
            else None
        )
        if resumed is not None:
            command = resumed.command
            grounding_messages = resumed.grounding_messages
        command = validate_command(command, grounding_messages)
        _logger.info(
            'channel_intent_routed source=%s action=%s request_id=%s',
            routing_source,
            command.command.value,
            request_id,
        )
        catalog = {}
        required_kinds = self._required_catalog_kinds(
            command,
            account_id,
            external_address_hash,
        )
        if required_kinds:
            catalog = self._classifier.catalog(
                owner_user_id=owner_user_id,
                request_id=request_id,
                kinds=required_kinds,
            )
        return self._executor.execute(
            command=command,
            account_id=account_id,
            external_address_hash=external_address_hash,
            owner_user_id=owner_user_id,
            request_id=request_id,
            grounding_messages=grounding_messages,
            catalog=catalog,
        )

    def _required_catalog_kinds(
        self,
        command: CommandEnvelope,
        account_id: str,
        external_address_hash: str,
    ) -> set[str]:
        parameters = command.parameters
        kinds = {
            change.resource_type
            for change in getattr(parameters, 'resource_changes', [])
        }
        if isinstance(command, CapabilityListCommand):
            kinds.update(parameters.capabilities)
        if isinstance(command, ConversationNewCommand) or (
            isinstance(command, ChatCommand)
            and not self._store.get_route(account_id, external_address_hash)
        ):
            kinds.add('knowledge_base')
        return kinds

    def _classifier_state(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> dict[str, Any]:
        navigation = (
            self._store.get_navigation_state(account_id, external_address_hash)
            or {}
        )
        state: dict[str, Any] = {
            'has_current_conversation': bool(
                self._store.get_route(account_id, external_address_hash)
            ),
            'new_conversation_pending': navigation.get('mode') == 'new_pending',
        }
        selection = self._store.get_selection_context(
            account_id,
            external_address_hash,
        )
        if isinstance(selection, dict):
            items = selection.get('items')
            latest_selection: dict[str, Any] = {
                'kind': str(selection.get('kind') or ''),
                'items': [
                    {
                        'index': index,
                        'name': str(
                            item.get('display_name')
                            or item.get('name')
                            or ''
                        )[:200],
                    }
                    for index, item in enumerate(
                        items if isinstance(items, list) else [],
                        start=1,
                    )
                    if isinstance(item, dict)
                ][:20],
            }
            continuation = selection.get('continuation')
            if isinstance(continuation, dict):
                try:
                    SelectionContinuation.model_validate(continuation)
                except ValueError:
                    pass
                else:
                    latest_selection['has_continuation'] = True
            state['latest_selection'] = latest_selection
        return state
