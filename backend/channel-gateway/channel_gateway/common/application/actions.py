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
                assistant_conversation_id, executor = (
                    self._external_agent_executor(
                        provider_context,
                        request_id,
                    )
                )
                disabled_tools, enable_plugin, plugin_refs = (
                    self._workspace_capability_policy(
                        provider,
                        provider_context,
                        catalog,
                    )
                )
                if executor is None:
                    self._prepare_workspace_plugins(
                        plugin_refs=plugin_refs,
                        catalog=catalog,
                        account_id=account_id,
                        external_address_hash=external_address_hash,
                        owner_user_id=owner_user_id,
                        request_id=request_id,
                    )
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
                    ask_already_validated=(
                        self._workspace_ask_validated(provider_context)
                    ),
                    inputs=self._chat_inputs(provider_context),
                    mentions=self._workspace_mentions(provider_context),
                    workspace_dataset_ids=(
                        self._workspace_dataset_ids(
                            provider,
                            provider_context,
                        )
                        if executor is None
                        else None
                    ),
                    disabled_tools=disabled_tools,
                    enable_plugin=enable_plugin,
                    plugin_mode=self._chat_plugin_mode(
                        provider,
                        executor,
                    ),
                    thinking_depth=self._thinking_depth(provider_context),
                    conversation_id_override=assistant_conversation_id,
                    executor=executor,
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
                    allow_disabled=provider == 'feishu',
                )
                _disabled, _enabled, plugin_refs = (
                    self._workspace_capability_policy(
                        provider,
                        provider_context,
                        catalog,
                    )
                )
                workflow_ref = str(workflow.get('id') or '')
                if provider == 'feishu' and workflow_ref not in plugin_refs:
                    raise ActionMessage(
                        '这个插件尚未加入当前会话，请先在“能力”中选择后再试。'
                    )
                self._prepare_workspace_plugins(
                    plugin_refs=(workflow_ref,),
                    catalog=catalog,
                    account_id=account_id,
                    external_address_hash=external_address_hash,
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                )
                text = self._conversations.chat(
                    message=parameters.message,
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
    def _workspace_dataset_ids(
        provider: str,
        provider_context: dict[str, Any] | None,
    ) -> tuple[str, ...] | None:
        if provider != 'feishu' or not isinstance(provider_context, dict):
            return None
        values = provider_context.get('workspace_resources')
        return tuple(dict.fromkeys(
            str(item.get('id') or '')
            for item in (values if isinstance(values, list) else [])
            if isinstance(item, dict)
            and item.get('type') == 'knowledge_base'
            and item.get('id')
        ))

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
    def _external_agent_executor(
        provider_context: dict[str, Any] | None,
        request_id: str,
    ) -> tuple[str | None, dict[str, str] | None]:
        if not isinstance(provider_context, dict):
            return None, None
        binding = provider_context.get('external_agent_binding')
        if not isinstance(binding, dict):
            return None, None
        conversation_id = str(binding.get('conversation_id') or '').strip()
        thread_id = str(binding.get('provider_thread_id') or '').strip()
        provider = str(binding.get('provider') or 'codex').strip().lower()
        if not conversation_id or not thread_id:
            return None, None
        return conversation_id, {
            'kind': 'external_agent',
            'provider': provider,
            'provider_thread_id': thread_id,
            'request_id': request_id,
        }

    @staticmethod
    def _chat_plugin_mode(
        provider: str,
        executor: dict[str, str] | None,
    ) -> str | None:
        if provider == 'feishu' and executor is None:
            return 'auto'
        return None

    @staticmethod
    def _workspace_capability_policy(
        provider: str,
        provider_context: dict[str, Any] | None,
        catalog: dict[str, Any],
    ) -> tuple[tuple[str, ...], bool | None, tuple[str, ...]]:
        if provider != 'feishu' or not isinstance(provider_context, dict):
            return (), None, ()
        values = provider_context.get('workspace_resources')
        resources = [
            item
            for item in (values if isinstance(values, list) else [])
            if isinstance(item, dict)
        ]
        selected = {
            (str(item.get('type') or ''), str(item.get('id') or ''))
            for item in resources
            if item.get('type') and item.get('id')
        }
        selected_plugins = tuple(dict.fromkeys(
            str(item.get('id') or '')
            for item in resources
            if item.get('type') == 'plugin' and item.get('id')
        ))
        selected_tools = {
            resource_id
            for resource_type, resource_id in selected
            if resource_type == 'tool'
        }
        knowledge_selected = any(
            resource_type == 'knowledge_base'
            for resource_type, _resource_id in selected
        )
        disabled_tools: list[str] = []
        tools = catalog.get('tool')
        for item in tools if isinstance(tools, list) else []:
            if not isinstance(item, dict):
                continue
            tool_id = str(item.get('id') or '')
            if not tool_id or tool_id in selected_tools:
                continue
            label = str(item.get('name') or '').lower()
            if knowledge_selected and (
                'knowledge' in label
                or '知识库' in label
            ):
                continue
            disabled_tools.append(tool_id)
        if not any(kind == 'skill' for kind, _resource_id in selected):
            disabled_tools.append('skill')
        return (
            tuple(dict.fromkeys(disabled_tools)),
            bool(selected_plugins),
            selected_plugins,
        )

    def _prepare_workspace_plugins(
        self,
        *,
        plugin_refs: Sequence[str],
        catalog: dict[str, Any],
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> None:
        refs = tuple(dict.fromkeys(ref for ref in plugin_refs if ref))
        if not refs:
            return
        workflows = catalog.get('workflow')
        items = workflows if isinstance(workflows, list) else []
        for position, workflow_ref in enumerate(refs):
            workflow = next(
                (
                    item
                    for item in items
                    if isinstance(item, dict)
                    and str(item.get('id') or '') == workflow_ref
                ),
                None,
            )
            if workflow is not None and not bool(workflow.get('enabled', False)):
                self._client.set_workflow_enabled(
                    owner_user_id=owner_user_id,
                    workflow_ref=workflow_ref,
                    enabled=True,
                    request_id=f'{request_id}_enable_plugin_{position}',
                )
                workflow['enabled'] = True
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

    @staticmethod
    def _workflow(
        workflow_ref: str,
        catalog: dict[str, Any],
        *,
        allow_disabled: bool = False,
    ) -> dict[str, Any]:
        workflows = catalog.get('workflow')
        if isinstance(workflows, list):
            for item in workflows:
                if (
                    isinstance(item, dict)
                    and (allow_disabled or bool(item.get('enabled', False)))
                    and str(item.get('id') or '') == workflow_ref
                ):
                    return item
        raise ActionMessage('所选工作流当前不可用，请重新查看可用能力。')
