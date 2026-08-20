from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Literal


@dataclass(frozen=True, slots=True)
class ChannelFeatureProfile:
    enable_ask: bool = False
    enable_workflow: bool = False
    enable_skill: bool = False
    enable_subagent: bool = False
    enable_tasks: bool = False

    @property
    def basic_chat_only(self) -> bool:
        return not (
            self.enable_ask
            or self.enable_workflow
            or self.enable_skill
            or self.enable_subagent
            or self.enable_tasks
        )

    @property
    def enabled_feature_labels(self) -> tuple[str, ...]:
        labels: list[str] = []
        if self.enable_skill:
            labels.append('Skill')
        if self.enable_workflow:
            labels.append('Workflow')
        if self.enable_subagent:
            labels.append('SubAgent')
        if self.enable_ask:
            labels.append('Ask')
        if self.enable_tasks:
            labels.append('Task')
        return tuple(labels)

    @property
    def disabled_tools(self) -> tuple[str, ...]:
        tools: list[str] = []
        if not self.enable_ask:
            tools.append('ask_user')
        if not self.enable_subagent:
            tools.append('subagent')
        if not self.enable_tasks:
            tools.extend(('schedule', 'task', 'task_center'))
        if not self.enable_skill:
            tools.append('skill')
        return tuple(tools)


BASIC_CHAT_FEATURES = ChannelFeatureProfile()


def retainable_provider_context(
    provider_context: dict[str, Any] | None,
) -> dict[str, Any]:
    """Return delivery state without one-turn Core inputs or answers."""
    retained = dict(provider_context or {})
    retained.pop('channel_execution', None)
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
    features: ChannelFeatureProfile = BASIC_CHAT_FEATURES


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
class CoreTurnResult:
    conversation_id: str
    history_id: str
    answer: str
    finish_reason: str
    sources: tuple[Any, ...] = ()
    events: tuple[CoreEvent, ...] = ()
