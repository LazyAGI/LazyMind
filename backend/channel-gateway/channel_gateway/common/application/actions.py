from __future__ import annotations

from collections.abc import Callable
from typing import Any, Sequence

from channel_gateway.common.application.capabilities import (
    ActionMessage,
    CapabilityActions,
)
from channel_gateway.common.domain.commands import (
    ActionKind,
    CapabilityConfigureCommand,
    CapabilityListCommand,
    ChatCommand,
    ClarifyCommand,
    CommandEnvelope,
    ConversationCurrentCommand,
    ConversationListCommand,
    ConversationNewCommand,
    ConversationSwitchCommand,
    HistoryMoreCommand,
    SelectionChooseCommand,
    WorkflowInvokeCommand,
    command_kind,
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
)


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

    def recover_native_transition(
        self,
        *,
        intent_kind: ActionKind,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> ChannelReply:
        return ChannelReply(
            intent_kind=intent_kind,
            text=self._conversations.recover_transition(
                account_id=account_id,
                external_address_hash=external_address_hash,
                owner_user_id=owner_user_id,
                request_id=request_id,
            ),
        )

    def prepare_native_transition(
        self,
        *,
        command: CommandEnvelope,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        grounding_messages: Sequence[str],
        catalog: dict[str, Any],
    ) -> ChannelReply | None:
        if not isinstance(command, ConversationSwitchCommand):
            return None
        try:
            self._conversations.preflight_switch(
                command=command,
                source_messages=grounding_messages,
                account_id=account_id,
                external_address_hash=external_address_hash,
                owner_user_id=owner_user_id,
                request_id=request_id,
                catalog=catalog,
            )
        except ActionMessage as exc:
            return ChannelReply(
                intent_kind=command_kind(command),
                text=str(exc),
            )
        return None

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
        surface: str = 'direct',
        source_external_address_hash: str | None = None,
        provider: str = '',
        provider_context: dict[str, Any] | None = None,
    ) -> ChannelReply:
        features = self._feature_resolver(provider)
        context = {
            'account_id': account_id,
            'external_address_hash': external_address_hash,
            'owner_user_id': owner_user_id,
            'request_id': request_id,
        }
        try:
            if (
                surface == 'native_thread'
                and isinstance(command, ConversationNewCommand)
                and self._store.get_route(
                    account_id,
                    external_address_hash,
                )
            ):
                raise ActionMessage(
                    '当前话题已经固定对应一个 LazyMind 会话，'
                    '不会被切换或覆盖。请新建话题开始新会话。'
                )
            if (
                surface == 'native_thread'
                and isinstance(command, ConversationSwitchCommand)
                and self._store.get_route(
                    account_id,
                    external_address_hash,
                )
            ):
                raise ActionMessage(
                    '每个话题固定对应一个 LazyMind 会话，'
                    '当前话题不会改绑。请在新的话题中选择目标会话。'
                )
            if isinstance(command, ChatCommand):
                parameters = command.parameters
                text = self._conversations.chat(
                    message=parameters.message,
                    changes=parameters.resource_changes,
                    source_command=command,
                    source_messages=grounding_messages,
                    catalog=catalog,
                    features=features,
                    ask_answers_structured=(
                        self._ask_answers(provider_context)
                    ),
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
                    **context,
                )
            elif isinstance(command, ConversationListCommand):
                text = self._conversations.list_conversations(**context)
                if surface == 'native_thread':
                    text = (
                        f'{text}\n\n'
                        '提示：原生话题与会话固定绑定，'
                        '选择编号时会在新话题中打开目标会话。'
                    )
            elif isinstance(command, ConversationSwitchCommand):
                text = self._conversations.switch(
                    command=command,
                    source_messages=grounding_messages,
                    selection_external_address_hash=(
                        source_external_address_hash
                        or external_address_hash
                    ),
                    catalog=catalog,
                    features=features,
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
                text = self._capabilities.list_capabilities(
                    kinds=command.parameters.capabilities,
                    catalog=catalog,
                    account_id=account_id,
                    external_address_hash=external_address_hash,
                    features=features,
                )
            elif isinstance(command, CapabilityConfigureCommand):
                text = self._capabilities.configure_capabilities(
                    changes=command.parameters.resource_changes,
                    source_command=command,
                    source_messages=grounding_messages,
                    catalog=catalog,
                    **context,
                )
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
                text = self._conversations.chat(
                    message=parameters.message,
                    changes=[],
                    source_command=command,
                    source_messages=grounding_messages,
                    catalog=catalog,
                    features=features,
                    mentions=(
                        self._client.mention('plugin', workflow),
                    ),
                    plugin_mode='auto',
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
