from __future__ import annotations

from dataclasses import dataclass, replace
from typing import Any

from channel_gateway.common.application.ask_text import parse_text_ask_answer
from channel_gateway.common.application.intents import (
    ExactShortcutParser,
    canonicalize_command,
    resolve_pending_selection,
    validate_command,
)
from channel_gateway.common.domain.chat import (
    ChannelExecutionContext,
)
from channel_gateway.common.domain.commands import (
    COMMAND_ADAPTER,
    SCHEMA_VERSION,
    CapabilityListCommand,
    CapabilityListParameters,
    ChatCommand,
    ChatParameters,
    CommandEnvelope,
    ConversationCurrentCommand,
    ConversationCurrentParameters,
    ConversationListCommand,
    ConversationListParameters,
    ConversationNewCommand,
    ConversationNewParameters,
    ConversationSettingsCommand,
    ConversationSettingsParameters,
    ConversationSettingsUpdateCommand,
    ConversationStopCommand,
    ConversationStopParameters,
    HistoryMoreCommand,
    HistoryMoreParameters,
)
from channel_gateway.common.ports.core import CapabilityCatalogClient
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
        catalog: CapabilityCatalogClient,
    ):
        self._store = store
        self._shortcuts = shortcuts
        self._catalog = catalog

    def route(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        text: str,
        request_id: str,
        provider_context: dict[str, Any],
    ) -> RoutedCommand | str:
        continuation_catalog: dict[str, Any] = {}
        execution = ChannelExecutionContext.from_provider_context(
            provider_context
        )
        plain_text_interactions = execution.interaction_mode == 'plain_text'
        text_selection = (
            self._store.get_selection_context(
                account_id,
                external_address_hash,
            )
            if plain_text_interactions
            else None
        )
        text_ask = (
            parse_text_ask_answer(text, text_selection)
            if plain_text_interactions
            else None
        )
        if (
            isinstance(text_selection, dict)
            and text_selection.get('kind') == 'ask'
            and text_ask is None
        ):
            return '回答格式不正确。请按问题提示回复选项编号或“题号: 答案”。'
        command_action = provider_context.get('command_action')
        selection_action = provider_context.get('selection_action')
        structured_ask = execution.ask_answers_structured
        if text_ask is not None:
            execution = replace(
                execution,
                ask_answers_structured=text_ask,
            )
            provider_context['channel_execution'] = execution.to_dict()
            command = ChatCommand(
                schema_version=SCHEMA_VERSION,
                command='chat',
                parameters=ChatParameters(message=text),
            )
            grounding_messages = (text,)
            routing_source = 'text_ask'
        elif isinstance(command_action, dict):
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
                account_id=account_id,
                external_address_hash=external_address_hash,
                text=text,
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
            if command.command.value == 'selection.choose'
            else None
        )
        if resumed is not None:
            command = resumed.command
            grounding_messages = resumed.grounding_messages
            continuation_catalog = dict(resumed.prepared_catalog)
        command = validate_command(command, grounding_messages)
        required_kinds = self._required_catalog_kinds(
            command,
            account_id,
            external_address_hash,
            execution,
        )
        if required_kinds:
            continuation_catalog.update(
                self._catalog.get_capability_catalog(
                    owner_user_id=owner_user_id,
                    request_id=f'{request_id}_catalog',
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
        account_id: str,
        external_address_hash: str,
        text: str,
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

        command = _control_command(text)
        return (
            command or ChatCommand(
                schema_version=SCHEMA_VERSION,
                command='chat',
                parameters=ChatParameters(message=text),
            ),
            (text,),
            'text_control' if command is not None else 'chat',
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
            capability_settings = {
                'knowledge_base',
                'skill',
                'tool',
                'workflow',
            }
            if execution.include_capability_settings:
                kinds.update(capability_settings)
            elif setting in capability_settings | {'personalization'}:
                kinds.add(setting)
        if isinstance(command, ConversationNewCommand) or (
            isinstance(command, ChatCommand)
            and not self._store.get_route(account_id, external_address_hash)
        ):
            kinds.add('knowledge_base')
        return kinds


def _control_command(text: str) -> CommandEnvelope | None:
    normalized = ''.join(text.lower().split())
    if normalized in {'新建会话', '创建会话', '新会话', 'newchat'}:
        return ConversationNewCommand(
            schema_version=SCHEMA_VERSION,
            command='conversation.new',
            parameters=ConversationNewParameters(evidence=[text]),
        )
    assistants = {
        '查看会话': 'lazymind', '历史会话': 'lazymind',
        '切换会话': 'lazymind', '查看lazymind会话': 'lazymind',
        '查看codex会话': 'codex', '查看cursor会话': 'cursor',
        '查看workbuddy会话': 'workbuddy', '查看codebuddy会话': 'workbuddy',
    }
    if normalized in assistants:
        return ConversationListCommand(
            schema_version=SCHEMA_VERSION,
            command='conversation.list',
            parameters=ConversationListParameters(
                assistant=assistants[normalized], evidence=[text],
            ),
        )
    if normalized in {'当前会话', '查看当前会话'}:
        return ConversationCurrentCommand(
            schema_version=SCHEMA_VERSION,
            command='conversation.current',
            parameters=ConversationCurrentParameters(evidence=[text]),
        )
    if normalized in {'更多历史', '查看更多历史', '更早历史'}:
        return HistoryMoreCommand(
            schema_version=SCHEMA_VERSION,
            command='history.more',
            parameters=HistoryMoreParameters(evidence=[text]),
        )
    if normalized in {'停止', '停止生成', '停止任务'}:
        return ConversationStopCommand(
            schema_version=SCHEMA_VERSION,
            command='conversation.stop',
            parameters=ConversationStopParameters(evidence=[text]),
        )
    if normalized in {'能力', '查看能力'}:
        return CapabilityListCommand(
            schema_version=SCHEMA_VERSION,
            command='capability.list',
            parameters=CapabilityListParameters(
                capabilities=[
                    'knowledge_base', 'skill', 'workflow', 'tool',
                    'personalization',
                ],
                evidence=[text],
            ),
        )
    if normalized in {'设置', '查看设置', '会话设置'}:
        return ConversationSettingsCommand(
            schema_version=SCHEMA_VERSION,
            command='conversation.settings',
            parameters=ConversationSettingsParameters(evidence=[text]),
        )
    return None
