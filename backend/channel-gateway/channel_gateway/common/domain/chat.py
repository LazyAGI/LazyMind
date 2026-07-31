from dataclasses import dataclass, field
from typing import Any, Literal


@dataclass(frozen=True, slots=True)
class ChannelFeatureProfile:
    enable_ask: bool = False
    enable_plugin: bool = False
    enable_skill: bool = False
    enable_subagent: bool = False
    enable_tasks: bool = False

    @property
    def basic_chat_only(self) -> bool:
        return not (
            self.enable_ask
            or self.enable_plugin
            or self.enable_skill
            or self.enable_subagent
            or self.enable_tasks
        )

    @property
    def enabled_feature_labels(self) -> tuple[str, ...]:
        labels: list[str] = []
        if self.enable_skill:
            labels.append('Skill')
        if self.enable_plugin:
            labels.append('Plugin')
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
        if not self.enable_plugin:
            tools.append('plugin')
        if not self.enable_subagent:
            tools.append('subagent')
        if not self.enable_tasks:
            tools.extend(('schedule', 'task', 'task_center'))
        if not self.enable_skill:
            tools.append('skill')
        return tuple(tools)


BASIC_CHAT_FEATURES = ChannelFeatureProfile()


@dataclass
class ChatOptions:
    search_config: dict[str, Any] | None = None
    mentions: list[dict[str, str]] = field(default_factory=list)
    plugin_mode: Literal['auto', 'dynamic'] | None = None
    use_memory: bool | None = None
    disabled_tools: list[str] = field(default_factory=list)
    filters: dict[str, Any] | None = None
    ask_answers_structured: dict[str, Any] | None = None
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


@dataclass(frozen=True, slots=True)
class CoreTurnResult:
    conversation_id: str
    history_id: str
    answer: str
    finish_reason: str
    sources: tuple[Any, ...] = ()
    events: tuple[CoreEvent, ...] = ()
