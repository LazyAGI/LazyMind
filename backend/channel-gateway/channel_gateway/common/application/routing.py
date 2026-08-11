from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from channel_gateway.common.application.intents import (
    ChannelIntentClassifier,
    ExactShortcutParser,
    canonicalize_command,
    resolve_pending_selection,
    validate_command,
    validate_workflow_catalog,
)
from channel_gateway.common.domain.chat import (
    BASIC_CHAT_FEATURES,
    ChannelExecutionContext,
    ChannelFeatureProfile,
)
from channel_gateway.common.domain.commands import (
    COMMAND_ADAPTER,
    SCHEMA_VERSION,
    CapabilityListCommand,
    ChatCommand,
    ChatParameters,
    CommandEnvelope,
    ConversationNewCommand,
    ConversationSettingsCommand,
    ConversationSettingsUpdateCommand,
    SelectionContinuation,
)
from channel_gateway.common.ports.repository import NavigationRepository


@dataclass(frozen=True)
class RoutedCommand:
    command: CommandEnvelope
    grounding_messages: tuple[str, ...]
    catalog: dict[str, Any]
    source: str


class ChannelCommandRouter:
    """Turns one channel input into one validated, executable command."""

    def __init__(
        self,
        *,
        store: NavigationRepository,
        shortcuts: ExactShortcutParser,
        classifier: ChannelIntentClassifier,
        feature_resolver: (
            Callable[[str], ChannelFeatureProfile] | None
        ) = None,
    ):
        self._store = store
        self._shortcuts = shortcuts
        self._classifier = classifier
        self._feature_resolver = (
            feature_resolver
            or (lambda _provider: BASIC_CHAT_FEATURES)
        )

    def route(
        self,
        *,
        provider: str,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        text: str,
        request_id: str,
        surface: str,
        provider_context: dict[str, Any],
    ) -> RoutedCommand | str:
        continuation_catalog: dict[str, Any] = {}
        execution = ChannelExecutionContext.from_provider_context(
            provider_context
        )
        command_action = provider_context.get('command_action')
        selection_action = provider_context.get('selection_action')
        structured_ask = execution.ask_answers_structured
        if isinstance(command_action, dict):
            command = COMMAND_ADAPTER.validate_python(command_action)
            grounding_messages = (text,)
            routing_source = 'provider_action'
        elif isinstance(selection_action, dict):
            shortcut = self._provider_selection(
                account_id=account_id,
                external_address_hash=external_address_hash,
                selection_action=selection_action,
            )
            if not isinstance(shortcut, RoutedCommand):
                return shortcut
            command = shortcut.command
            grounding_messages = shortcut.grounding_messages
            continuation_catalog.update(shortcut.catalog)
            routing_source = shortcut.source
        elif isinstance(structured_ask, dict):
            command = ChatCommand(
                schema_version=SCHEMA_VERSION,
                command='chat',
                parameters=ChatParameters(message=text),
            )
            grounding_messages = (text,)
            routing_source = 'provider_action'
        else:
            command, grounding_messages, routing_source = self._route_text(
                provider=provider,
                account_id=account_id,
                external_address_hash=external_address_hash,
                owner_user_id=owner_user_id,
                text=text,
                request_id=request_id,
                surface=surface,
                continuation_catalog=continuation_catalog,
            )

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
            if (
                routing_source == 'llm'
                or command.command.value == 'selection.choose'
            )
            else None
        )
        if resumed is not None:
            command = resumed.command
            grounding_messages = resumed.grounding_messages
            continuation_catalog = dict(resumed.prepared_catalog)
        command = validate_command(command, grounding_messages)
        command = validate_workflow_catalog(
            command,
            continuation_catalog,
        )
        required_kinds = self._required_catalog_kinds(
            command,
            account_id,
            external_address_hash,
            execution,
        )
        if required_kinds:
            continuation_catalog.update(
                self._classifier.catalog(
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                    kinds=required_kinds,
                )
            )
        return RoutedCommand(
            command=command,
            grounding_messages=grounding_messages,
            catalog=continuation_catalog,
            source=routing_source,
        )

    def _provider_selection(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        selection_action: dict[str, Any],
    ) -> RoutedCommand | str:
        selection = self._store.get_selection_context(
            account_id,
            external_address_hash,
        )
        expected_id = str(selection_action.get('selection_id') or '')
        current_id = (
            str(selection.get('id') or '')
            if isinstance(selection, dict)
            else ''
        )
        if not expected_id or expected_id != current_id:
            return '这张选择卡已过期，请重新查看最新列表后再选择。'
        shortcut = self._shortcuts.parse(
            account_id=account_id,
            external_address_hash=external_address_hash,
            text=str(selection_action.get('index') or ''),
        )
        if shortcut is None:
            return '这个选项已失效，请重新查看最新列表后再选择。'
        return RoutedCommand(
            command=shortcut.command,
            grounding_messages=shortcut.grounding_messages,
            catalog=dict(shortcut.prepared_catalog),
            source='provider_action',
        )

    def _route_text(
        self,
        *,
        provider: str,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        text: str,
        request_id: str,
        surface: str,
        continuation_catalog: dict[str, Any],
    ) -> tuple[CommandEnvelope, tuple[str, ...], str]:
        shortcut = (
            self._shortcuts.parse(
                account_id=account_id,
                external_address_hash=external_address_hash,
                text=text,
            )
            if text.strip().isdigit()
            else None
        )
        if shortcut is not None:
            continuation_catalog.update(shortcut.prepared_catalog)
            return (
                shortcut.command,
                shortcut.grounding_messages,
                'selection',
            )

        classifier_state = self._classifier_state(
            account_id,
            external_address_hash,
            surface,
        )
        if self._feature_resolver(provider).enable_workflow:
            workflow_catalog = self._classifier.catalog(
                owner_user_id=owner_user_id,
                request_id=request_id,
                kinds={'workflow'},
            )
            continuation_catalog.update(workflow_catalog)
            classifier_state['available_workflows'] = [
                {
                    'ref': str(item.get('id') or ''),
                    'name': str(item.get('name') or ''),
                    'description': str(
                        item.get('description') or ''
                    )[:2000],
                }
                for item in workflow_catalog.get('workflow', [])
                if isinstance(item, dict)
                and bool(item.get('enabled', False))
            ][:20]
        return (
            self._classifier.classify(
                provider=provider,
                owner_user_id=owner_user_id,
                message=text,
                request_id=f'{request_id}_intent',
                state=classifier_state,
            ),
            (text,),
            'llm',
        )

    def _required_catalog_kinds(
        self,
        command: CommandEnvelope,
        account_id: str,
        external_address_hash: str,
        execution: ChannelExecutionContext,
    ) -> set[str]:
        parameters = command.parameters
        kinds = {
            change.resource_type
            for change in getattr(parameters, 'resource_changes', [])
        }
        if isinstance(command, CapabilityListCommand):
            kinds.update(parameters.capabilities)
        if isinstance(command, ConversationSettingsCommand):
            settings_kinds = {
                'knowledge_base',
                'skill',
                'tool',
                'personalization',
                'workflow',
            }
            if parameters.section == 'overview':
                kinds.update(settings_kinds)
            elif parameters.section in settings_kinds:
                kinds.add(parameters.section)
        if isinstance(command, ConversationSettingsUpdateCommand):
            setting = parameters.change.setting
            if setting in {
                'knowledge_base',
                'skill',
                'tool',
                'personalization',
                'workflow',
            }:
                kinds.add(setting)
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
        surface: str,
    ) -> dict[str, Any]:
        navigation = (
            self._store.get_navigation_state(
                account_id,
                external_address_hash,
            )
            or {}
        )
        state: dict[str, Any] = {
            'surface': surface,
            'has_current_conversation': bool(
                self._store.get_route(
                    account_id,
                    external_address_hash,
                )
            ),
            'new_conversation_pending': (
                navigation.get('mode') == 'new_pending'
            ),
        }
        selection = self._store.get_selection_context(
            account_id,
            external_address_hash,
        )
        if not isinstance(selection, dict):
            return state
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
