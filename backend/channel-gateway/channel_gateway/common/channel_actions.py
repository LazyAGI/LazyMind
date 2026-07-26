from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Sequence, assert_never

from channel_gateway.common.capability_actions import (
    ActionMessage,
    CapabilityActions,
)
from channel_gateway.common.commands import (
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
    command_kind,
)
from channel_gateway.common.conversation_actions import ConversationActions
from channel_gateway.common.database import GatewayStore
from channel_gateway.common.lazymind import LazyMindClient


@dataclass(frozen=True)
class ChannelReply:
    intent_kind: ActionKind
    text: str


class ChannelActionExecutor:
    """Deterministically dispatches validated commands to their action owner."""

    def __init__(self, *, store: GatewayStore, client: LazyMindClient):
        self._capabilities = CapabilityActions(store=store, client=client)
        self._conversations = ConversationActions(
            store=store,
            client=client,
            capabilities=self._capabilities,
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
    ) -> ChannelReply:
        context = {
            'account_id': account_id,
            'external_address_hash': external_address_hash,
            'owner_user_id': owner_user_id,
            'request_id': request_id,
        }
        try:
            match command:
                case ChatCommand(parameters=parameters):
                    text = self._conversations.chat(
                        message=parameters.message,
                        changes=parameters.resource_changes,
                        source_command=command,
                        source_messages=grounding_messages,
                        catalog=catalog,
                        **context,
                    )
                case ConversationNewCommand(parameters=parameters):
                    text = self._conversations.new(
                        message=parameters.message,
                        changes=parameters.resource_changes,
                        source_command=command,
                        source_messages=grounding_messages,
                        catalog=catalog,
                        **context,
                    )
                case ConversationListCommand():
                    text = self._conversations.list_conversations(**context)
                case ConversationSwitchCommand():
                    text = self._conversations.switch(
                        command=command,
                        source_messages=grounding_messages,
                        catalog=catalog,
                        **context,
                    )
                case ConversationCurrentCommand():
                    text = self._conversations.current(**context)
                case HistoryMoreCommand():
                    text = self._conversations.more_history(**context)
                case CapabilityListCommand(parameters=parameters):
                    text = self._capabilities.list_capabilities(
                        kinds=parameters.capabilities,
                        catalog=catalog,
                        account_id=account_id,
                        external_address_hash=external_address_hash,
                    )
                case CapabilityConfigureCommand(parameters=parameters):
                    text = self._capabilities.configure_capabilities(
                        changes=parameters.resource_changes,
                        source_command=command,
                        source_messages=grounding_messages,
                        catalog=catalog,
                        **context,
                    )
                case ClarifyCommand(parameters=parameters):
                    text = parameters.clarification_question
                case SelectionChooseCommand():
                    raise RuntimeError('selection.choose must be resolved before execution')
                case _:
                    assert_never(command)
        except ActionMessage as exc:
            text = str(exc)
        return ChannelReply(intent_kind=command_kind(command), text=text)
