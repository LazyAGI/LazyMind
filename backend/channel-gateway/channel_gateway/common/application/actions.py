from __future__ import annotations

from collections.abc import Callable
from typing import Any, Sequence

from channel_gateway.common.application.capabilities import (
    ActionMessage,
    CapabilityActions,
)
from channel_gateway.common.domain.commands import (
    CapabilityConfigureCommand,
    CapabilityListCommand,
    ChatCommand,
    ClarifyCommand,
    CommandEnvelope,
    ConversationCurrentCommand,
    ConversationListCommand,
    ConversationNewCommand,
    ConversationSettingsCommand,
    ConversationSettingsUpdateCommand,
    ConversationSwitchCommand,
    HistoryMoreCommand,
    SelectionChooseCommand,
    WorkflowInvokeCommand,
)
from channel_gateway.common.application.conversations import (
    ConversationActions,
)
from channel_gateway.common.application.replies import (
    ChannelReply,
    ChannelReplyBuilder,
)
from channel_gateway.common.ports.core import LazyMindCore
from channel_gateway.common.ports.repository import NavigationRepository
from channel_gateway.common.domain.chat import (
    BASIC_CHAT_FEATURES,
    ChannelFeatureProfile,
    CoreStreamUpdate,
)
from channel_gateway.common.domain.outbound import ReplyPresentation


class ChannelActionExecutor:
    """Deterministically dispatches validated commands to their action owner."""

    def __init__(
        self,
        *,
        store: NavigationRepository,
        client: LazyMindCore,
        feature_resolver: (
            Callable[[str], ChannelFeatureProfile] | None
        ) = None,
    ):
        self._store = store
        self._client = client
        self._capabilities = CapabilityActions(store=store, client=client)
        self._conversations = ConversationActions(
            store=store,
            client=client,
            capabilities=self._capabilities,
        )
        self._replies = ChannelReplyBuilder(store)
        self._feature_resolver = (
            feature_resolver
            or (lambda _provider: BASIC_CHAT_FEATURES)
        )

    def execute(
        self,
        *,
        command: CommandEnvelope,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        grounding_messages: Sequence[str],
        catalog: dict[str, Any],
        provider: str = '',
        provider_context: dict[str, Any] | None = None,
        on_stream: Callable[[CoreStreamUpdate], None] | None = None,
    ) -> ChannelReply:
        features = self._feature_resolver(provider)
        context = {
            'account_id': account_id,
            'external_address_hash': external_address_hash,
            'owner_user_id': owner_user_id,
            'request_id': request_id,
        }
        presentations: tuple[ReplyPresentation, ...] = ()
        try:
            if isinstance(command, ChatCommand):
                parameters = command.parameters
                text = self._conversations.chat(
                    message=self._workspace_message(
                        parameters.message,
                        provider_context,
                    ),
                    changes=parameters.resource_changes,
                    source_command=command,
                    source_messages=grounding_messages,
                    catalog=catalog,
                    features=features,
                    ask_answers_structured=(
                        self._ask_answers(provider_context)
                    ),
                    ask_already_validated=(
                        self._workspace_ask_validated(provider_context)
                    ),
                    inputs=self._chat_inputs(provider_context),
                    mentions=self._workspace_mentions(provider_context),
                    thinking_depth=self._thinking_depth(provider_context),
                    on_stream=on_stream,
                    **context,
                )
            elif isinstance(command, ConversationNewCommand):
                parameters = command.parameters
                text = self._conversations.new(
                    message=parameters.message,
                    changes=parameters.resource_changes,
                    source_command=command,
                    source_messages=grounding_messages,
                    catalog=catalog,
                    features=features,
                    on_stream=on_stream,
                    **context,
                )
            elif isinstance(command, ConversationListCommand):
                text = self._conversations.list_conversations(**context)
            elif isinstance(command, ConversationSwitchCommand):
                text = self._conversations.switch(
                    command=command,
                    source_messages=grounding_messages,
                    selection_external_address_hash=external_address_hash,
                    catalog=catalog,
                    features=features,
                    on_stream=on_stream,
                    **context,
                )
            elif isinstance(command, ConversationCurrentCommand):
                text = self._conversations.current(
                    features=features,
                    **context,
                )
            elif isinstance(command, HistoryMoreCommand):
                text = self._conversations.more_history(**context)
            elif isinstance(command, CapabilityListCommand):
                text, capability_presentation = (
                    self._capabilities.list_capabilities(
                        kinds=command.parameters.capabilities,
                        catalog=catalog,
                        account_id=account_id,
                        external_address_hash=external_address_hash,
                        features=features,
                    )
                )
                presentations = (capability_presentation,)
                workspace_action = (
                    provider_context.get('workspace_action')
                    if isinstance(provider_context, dict)
                    else None
                )
                if (
                    isinstance(workspace_action, dict)
                    and workspace_action.get('kind') == 'navigate'
                    and workspace_action.get('view') == 'capabilities'
                ):
                    try:
                        _settings_text, settings_presentation = (
                            self._capabilities.conversation_settings(
                                section='overview',
                                catalog=catalog,
                                features=features,
                                account_id=account_id,
                                external_address_hash=external_address_hash,
                                owner_user_id=owner_user_id,
                                request_id=request_id,
                            )
                        )
                    except ActionMessage:
                        pass
                    else:
                        presentations = (
                            capability_presentation,
                            settings_presentation,
                        )
            elif isinstance(command, CapabilityConfigureCommand):
                text = self._capabilities.configure_capabilities(
                    changes=command.parameters.resource_changes,
                    source_command=command,
                    source_messages=grounding_messages,
                    catalog=catalog,
                    **context,
                )
            elif isinstance(command, ConversationSettingsCommand):
                text, settings_presentation = (
                    self._capabilities.conversation_settings(
                        section=command.parameters.section,
                        catalog=catalog,
                        features=features,
                        account_id=account_id,
                        external_address_hash=external_address_hash,
                        owner_user_id=owner_user_id,
                        request_id=request_id,
                    )
                )
                presentations = (settings_presentation,)
            elif isinstance(
                command,
                ConversationSettingsUpdateCommand,
            ):
                text, settings_presentation = (
                    self._capabilities.update_conversation_setting(
                        change=command.parameters.change,
                        catalog=catalog,
                        features=features,
                        account_id=account_id,
                        external_address_hash=external_address_hash,
                        owner_user_id=owner_user_id,
                        request_id=request_id,
                    )
                )
                presentations = (settings_presentation,)
            elif isinstance(command, WorkflowInvokeCommand):
                if not features.enable_plugin:
                    raise ActionMessage(
                        '当前渠道没有开放工作流功能，配置没有改变。'
                    )
                parameters = command.parameters
                workflow = self._workflow(
                    parameters.workflow_ref,
                    catalog,
                )
                conversation_id = self._store.get_route(
                    account_id,
                    external_address_hash,
                )
                if conversation_id:
                    self._client.dismiss_terminal_plugin_session(
                        owner_user_id=owner_user_id,
                        conversation_id=conversation_id,
                        request_id=request_id,
                    )
                text = self._conversations.chat(
                    message=self._workspace_message(
                        parameters.message,
                        provider_context,
                    ),
                    changes=[],
                    source_command=command,
                    source_messages=grounding_messages,
                    catalog=catalog,
                    features=features,
                    mentions=(
                        *(
                            mention
                            for mention in self._workspace_mentions(
                                provider_context
                            )
                            if mention.get('type') != 'plugin'
                        ),
                        self._client.mention('plugin', workflow),
                    ),
                    plugin_mode='auto',
                    thinking_depth=self._thinking_depth(provider_context),
                    on_stream=on_stream,
                    **context,
                )
            elif isinstance(command, ClarifyCommand):
                text = command.parameters.clarification_question
            elif isinstance(command, SelectionChooseCommand):
                raise RuntimeError(
                    'selection.choose must be resolved before execution'
                )
            else:
                raise TypeError(
                    f'Unsupported command type: {type(command).__name__}'
                )
        except ActionMessage as exc:
            text = str(exc)
        return self._replies.build(
            command=command,
            result=text,
            account_id=account_id,
            external_address_hash=external_address_hash,
            extra_presentations=presentations,
        )

    @staticmethod
    def _ask_answers(
        provider_context: dict[str, Any] | None,
    ) -> dict[str, Any] | None:
        if not isinstance(provider_context, dict):
            return None
        value = provider_context.get('ask_answers_structured')
        return dict(value) if isinstance(value, dict) else None

    @staticmethod
    def _workspace_mentions(
        provider_context: dict[str, Any] | None,
    ) -> tuple[dict[str, str], ...]:
        if not isinstance(provider_context, dict):
            return ()
        values = provider_context.get('workspace_mentions')
        return tuple(
            {
                'mention_id': str(item.get('mention_id') or ''),
                'type': str(item.get('type') or ''),
                'resource_id': str(item.get('resource_id') or ''),
                'display_name': str(item.get('display_name') or ''),
            }
            for item in (values if isinstance(values, list) else [])
            if isinstance(item, dict)
            and item.get('type')
            and item.get('resource_id')
            and item.get('display_name')
        )

    @staticmethod
    def _workspace_ask_validated(
        provider_context: dict[str, Any] | None,
    ) -> bool:
        return bool(
            isinstance(provider_context, dict)
            and provider_context.get('workspace_ask_validated') is True
        )

    @staticmethod
    def _chat_inputs(
        provider_context: dict[str, Any] | None,
    ) -> tuple[dict[str, str], ...]:
        if not isinstance(provider_context, dict):
            return ()
        values = provider_context.get('chat_inputs')
        return tuple(
            {
                str(key): str(value)
                for key, value in item.items()
                if key in {'input_type', 'input_base64', 'uri'}
            }
            for item in (values if isinstance(values, list) else [])
            if isinstance(item, dict)
            and item.get('input_type') in {'image', 'file'}
        )

    @staticmethod
    def _thinking_depth(
        provider_context: dict[str, Any] | None,
    ) -> str | None:
        if not isinstance(provider_context, dict):
            return None
        workspace = provider_context.get('workspace_state')
        if not isinstance(workspace, dict):
            return None
        value = str(workspace.get('thinking_depth') or '')
        return value if value in {'low', 'medium', 'high', 'max'} else None

    @staticmethod
    def _workspace_message(
        message: str,
        provider_context: dict[str, Any] | None,
    ) -> str:
        if not isinstance(provider_context, dict):
            return message
        workspace = provider_context.get('workspace_state')
        if not isinstance(workspace, dict):
            return message
        language = str(workspace.get('output_language') or 'zh')
        instructions = [{
            'zh': '请使用中文回答。',
            'en': 'Please answer in English.',
        }.get(language)]
        answer_depth = str(workspace.get('answer_depth') or 'medium')
        instructions.append({
            'low': {
                'zh': '请简洁作答，直接给出关键结论。',
                'en': (
                    'Answer concisely and state the key conclusion directly.'
                ),
            },
            'high': {
                'zh': '请深入作答，充分展开关键步骤、依据和权衡。',
                'en': (
                    'Answer in depth, fully explaining key steps, evidence, '
                    'and trade-offs.'
                ),
            },
        }.get(answer_depth, {}).get(language))
        instruction = ' '.join(item for item in instructions if item)
        return f'{message}\n\n{instruction}' if instruction else message

    @staticmethod
    def _workflow(
        workflow_ref: str,
        catalog: dict[str, Any],
    ) -> dict[str, Any]:
        workflows = catalog.get('workflow')
        if isinstance(workflows, list):
            for item in workflows:
                if (
                    isinstance(item, dict)
                    and bool(item.get('enabled', False))
                    and str(item.get('id') or '') == workflow_ref
                ):
                    return item
        raise ActionMessage('所选工作流当前不可用，请重新查看可用能力。')
