from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Callable, Literal, Optional, Tuple


class AgentRole(str, Enum):
    CHAT = 'chat'
    SUBAGENT = 'subagent'
    DRIVER = 'driver'


PromptChannel = Literal['system', 'runtime']
ContentKind = Literal['instruction', 'state', 'reference']


@dataclass(frozen=True)
class PromptSection:
    section_id: str
    channel: PromptChannel
    title: str
    content: str
    source: str
    priority: int = 100
    authoritative: bool = False
    content_kind: ContentKind = 'instruction'


@dataclass(frozen=True)
class PromptBundle:
    sections: Tuple[PromptSection, ...]
    system_prompt: str
    current_input: str
    input_title: str
    input_content: str


@dataclass(frozen=True)
class AgentExecutionOptions:
    skills: Any = None
    workspace: Optional[str] = None
    keep_full_turns: Optional[int] = None
    fs: Any = None
    skills_dir: Optional[str] = None
    extra_stop_condition: Optional[Callable[..., Any]] = None
    max_retries: Optional[int] = None
    tool_failure_limits: Optional[dict[str, int]] = None


@dataclass
class AgentRunPlan:
    role: AgentRole
    prompt: PromptBundle
    history: list[dict[str, Any]] = field(default_factory=list)
    tools: list[Any] = field(default_factory=list)
    stop_tools: list[str] = field(default_factory=list)
    force_summarize_context: str = ''
    execution_options: AgentExecutionOptions = field(default_factory=AgentExecutionOptions)


@dataclass(frozen=True)
class ContextUsageItem:
    item_id: str
    category: str
    title: str
    source: str
    estimated_tokens: int
    char_count: int
    item_count: int = 1
    channel: Optional[str] = None
    content_kind: Optional[str] = None
    authoritative: bool = False
    content: str = ''


@dataclass(frozen=True)
class ContextUsageCategory:
    category_id: str
    title: str
    estimated_tokens: int
    char_count: int
    item_count: int
    items: Tuple[ContextUsageItem, ...]


@dataclass(frozen=True)
class ContextUsageReport:
    scope: Literal['next_request']
    estimated_tokens: int
    categories: Tuple[ContextUsageCategory, ...]
    estimation_version: str = 'unicode-weighted-v1'
