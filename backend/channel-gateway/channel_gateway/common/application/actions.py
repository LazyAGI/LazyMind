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
    ChannelExecutionContext,
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
        execution = ChannelExecutionContext.from_provider_context(
            provider_context
        )
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
                assistant_conversation_id = self._external_agent_conversation(
                    execution
                )
                text = self._conversations.chat(
                    message=parameters.message,
                    changes=parameters.resource_changes,
                    source_command=command,
                    source_messages=grounding_messages,
                    catalog=catalog,
                    features=features,
                    ask_answers_structured=(
                        execution.ask_answers_structured
                    ),
                    inputs=tuple(
                        item.to_dict() for item in execution.attachments
                    ),
                    thinking_depth=execution.thinking_depth,
                    conversation_id_override=assistant_conversation_id,
                    external_agent=assistant_conversation_id is not None,
                    activate_route=assistant_conversation_id is None,
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
                        save_selection=(
                            str(
                                (provider_context or {}).get(
                                    'workspace_surface'
                                )
                                or ''
                            )
                            != 'management'
                        ),
                    )
                )
                presentations = (capability_presentation,)
                if execution.include_capability_settings:
                    try:
                        _settings_text, settings_presentation = (
                            self._capabilities.conversation_settings(
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
                        expected_conversation_id=(
                            command.parameters.expected_conversation_id
                        ),
                        catalog=catalog,
                        account_id=account_id,
                        external_address_hash=external_address_hash,
                        owner_user_id=owner_user_id,
                        request_id=request_id,
                    )
                )
                _capability_text, capability_presentation = (
                    self._capabilities.list_capabilities(
                        kinds=[
                            'knowledge_base',
                            'skill',
                            'workflow',
                            'tool',
                        ],
                        catalog=catalog,
                        account_id=account_id,
                        external_address_hash=external_address_hash,
                        features=features,
                    )
                )
                presentations = (
                    capability_presentation,
                    settings_presentation,
                )
            elif isinstance(command, WorkflowInvokeCommand):
                if not features.enable_workflow:
                    raise ActionMessage(
                        '当前渠道没有开放工作流功能，配置没有改变。'
                    )
                parameters = command.parameters
                workflow = self._workflow(
                    parameters.workflow_ref,
                    catalog,
                )
                text = self._conversations.chat(
                    message=parameters.message,
                    changes=[],
                    source_command=command,
                    source_messages=grounding_messages,
                    catalog=catalog,
                    features=features,
                    mentions=(self._client.mention('workflow', workflow),),
                    thinking_depth=execution.thinking_depth,
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
    def _external_agent_conversation(
        execution: ChannelExecutionContext,
    ) -> str | None:
        return execution.external_agent_conversation_id or None

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
