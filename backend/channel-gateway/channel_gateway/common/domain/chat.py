from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Literal


def retainable_provider_context(
    provider_context: dict[str, Any] | None,
) -> dict[str, Any]:
    """Return delivery state without one-turn Core inputs or answers."""
    retained = dict(provider_context or {})
    retained.pop('channel_execution', None)
    retained.pop('channel_error', None)
    return retained


@dataclass(frozen=True, slots=True)
class ChannelAttachment:
    input_type: Literal['image', 'file']
    input_base64: str = ''
    uri: str = ''

    @classmethod
    def from_dict(cls, value: Any) -> ChannelAttachment | None:
        if not isinstance(value, dict):
            return None
        input_type = str(value.get('input_type') or '')
        if input_type not in {'image', 'file'}:
            return None
        attachment = cls(
            input_type=input_type,
            input_base64=str(value.get('input_base64') or ''),
            uri=str(value.get('uri') or ''),
        )
        if not attachment.input_base64 and not attachment.uri:
            return None
        return attachment

    def to_dict(self) -> dict[str, str]:
        value = {'input_type': self.input_type}
        if self.input_base64:
            value['input_base64'] = self.input_base64
        if self.uri:
            value['uri'] = self.uri
        return value


@dataclass(frozen=True, slots=True)
class ChannelExecutionContext:
    """Provider-neutral, typed inputs for one routed channel command."""

    attachments: tuple[ChannelAttachment, ...] = ()
    ask_answers_structured: dict[str, Any] | None = None
    thinking_depth: Literal['low', 'medium', 'high', 'max'] | None = None
    include_capability_settings: bool = False
    include_assistant_catalog: bool = False

    @classmethod
    def from_provider_context(
        cls,
        provider_context: dict[str, Any] | None,
    ) -> ChannelExecutionContext:
        if not isinstance(provider_context, dict):
            return cls()
        return cls.from_dict(provider_context.get('channel_execution'))

    @classmethod
    def from_dict(cls, value: Any) -> ChannelExecutionContext:
        if not isinstance(value, dict) or value.get('schema_version') != '1':
            return cls()
        raw_attachments = value.get('attachments')
        attachments = tuple(
            attachment
            for item in (
                raw_attachments if isinstance(raw_attachments, list) else []
            )[:10]
            if (attachment := ChannelAttachment.from_dict(item)) is not None
        )
        raw_answers = value.get('ask_answers_structured')
        thinking_depth = str(value.get('thinking_depth') or '')
        if thinking_depth not in {'low', 'medium', 'high', 'max'}:
            thinking_depth = ''
        return cls(
            attachments=attachments,
            ask_answers_structured=(
                dict(raw_answers) if isinstance(raw_answers, dict) else None
            ),
            thinking_depth=thinking_depth or None,
            include_capability_settings=(
                value.get('include_capability_settings') is True
            ),
            include_assistant_catalog=(
                value.get('include_assistant_catalog') is True
            ),
        )

    def to_dict(self) -> dict[str, Any]:
        return {
            'schema_version': '1',
            'attachments': [item.to_dict() for item in self.attachments],
            'ask_answers_structured': self.ask_answers_structured,
            'thinking_depth': self.thinking_depth or '',
            'include_capability_settings': (
                self.include_capability_settings
            ),
            'include_assistant_catalog': self.include_assistant_catalog,
        }


@dataclass
class ChatOptions:
    inputs: list[dict[str, str]] = field(default_factory=list)
    search_config: dict[str, Any] | None = None
    mentions: list[dict[str, str]] = field(default_factory=list)
    workflow_mode: Literal['auto', 'dynamic'] | None = None
    use_memory: bool | None = None
    disabled_tools: list[str] = field(default_factory=list)
    filters: dict[str, Any] | None = None
    ask_answers_structured: dict[str, Any] | None = None
    thinking_depth: Literal['low', 'medium', 'high', 'max'] | None = None
    enable_workflow: bool | None = None

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> 'ChatOptions':
        search_config = value.get('search_config')
        mentions = value.get('mentions')
        disabled_tools = value.get('disabled_tools')
        filters = value.get('filters')
        workflow_mode = value.get('workflow_mode')
        return cls(
            search_config=(
                search_config if isinstance(search_config, dict) else None
            ),
            mentions=(
                [dict(item) for item in mentions if isinstance(item, dict)]
                if isinstance(mentions, list)
                else []
            ),
            workflow_mode=(
                workflow_mode
                if workflow_mode in {'auto', 'dynamic'}
                else None
            ),
            enable_workflow=(
                value.get('enable_workflow')
                if isinstance(value.get('enable_workflow'), bool)
                else None
            ),
            use_memory=(
                value.get('use_memory')
                if isinstance(value.get('use_memory'), bool)
                else None
            ),
            disabled_tools=(
                [str(item) for item in disabled_tools if str(item)]
                if isinstance(disabled_tools, list)
                else []
            ),
            filters=filters if isinstance(filters, dict) else None,
        )

    def to_dict(self) -> dict[str, Any]:
        return {
            'search_config': self.search_config,
            'mentions': self.mentions,
            'workflow_mode': self.workflow_mode,
            'enable_workflow': self.enable_workflow,
            'use_memory': self.use_memory,
            'disabled_tools': self.disabled_tools,
            'filters': self.filters,
        }

    def merged(self, override: 'ChatOptions') -> 'ChatOptions':
        mentions = {
            (
                str(mention.get('type') or ''),
                str(mention.get('resource_id') or ''),
            ): mention
            for mention in [*self.mentions, *override.mentions]
        }
        disabled_tools = set(self.disabled_tools)
        for mention in override.mentions:
            if mention.get('type') == 'tool':
                disabled_tools.discard(str(mention.get('resource_id') or ''))
        for tool_id in override.disabled_tools:
            disabled_tools.add(tool_id)
            mentions.pop(('tool', tool_id), None)
        return ChatOptions(
            search_config=(
                override.search_config
                if override.search_config is not None
                else self.search_config
            ),
            mentions=list(mentions.values()),
            workflow_mode=(
                override.workflow_mode
                if override.workflow_mode is not None
                else self.workflow_mode
            ),
            enable_workflow=(
                override.enable_workflow
                if override.enable_workflow is not None
                else self.enable_workflow
            ),
            use_memory=(
                override.use_memory
                if override.use_memory is not None
                else self.use_memory
            ),
            disabled_tools=list(disabled_tools),
            filters=(
                override.filters
                if override.filters is not None
                else self.filters
            ),
        )


@dataclass(frozen=True, slots=True)
class CoreEvent:
    source: Literal['chat', 'task', 'conversation']
    type: str
    payload: dict[str, Any]

    def to_dict(self) -> dict[str, Any]:
        return {
            'source': self.source,
            'type': self.type,
            'payload': self.payload,
        }


@dataclass(frozen=True, slots=True)
class CoreStreamUpdate:
    """Provider-neutral, user-visible snapshot of one streamed answer."""

    thinking: str = ''
    answer: str = ''
    thinking_seconds: int | None = None
    conversation_id: str = ''
    history_id: str = ''
    task_created: dict[str, Any] | None = None
    task_progress: str = ''


@dataclass(frozen=True, slots=True)
class CoreRunTerminal:
    status: Literal['completed', 'interrupted', 'failed', 'cancelled']
    reason: str
    code: str = ''
    partial_output: bool = False


@dataclass(frozen=True, slots=True)
class CoreTurnResult:
    conversation_id: str
    history_id: str
    answer: str
    run_terminal: CoreRunTerminal
    sources: tuple[Any, ...] = ()
    events: tuple[CoreEvent, ...] = ()
