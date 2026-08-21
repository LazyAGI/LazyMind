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
PromptPlacement = Literal['before_input', 'after_input']


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
    placement: PromptPlacement = 'before_input'


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
    llm_config: Optional[dict[str, Any]] = None
    max_input_tokens: Optional[Any] = None
    history_compactor: Optional[Callable[[list[dict[str, Any]], int], list[dict[str, Any]]]] = None


CompressionTrigger = Literal['pre_turn', 'mid_turn']


@dataclass(frozen=True)
class ContextBudget:
    max_input_tokens: int
    reserved_output_tokens: int
    effective_input_budget: int
    trigger_tokens: int
    target_tokens: int
    trigger_ratio: float
    target_ratio: float
    source: str = 'fallback'


@dataclass(frozen=True)
class ToolPruneDetail:
    tool_name: str
    message_index: int
    before_tokens: int
    after_tokens: int
    compactor: str
    spill_path: str = ''
    spill_bytes: int = 0


@dataclass(frozen=True)
class PruneEvent:
    trigger: CompressionTrigger
    decision: Literal['pruned', 'skipped', 'abandoned', 'spilled']
    reason: str
    estimated_before: int
    estimated_after: int
    reclaimed_tokens: int
    budget: ContextBudget
    usage_ratio_before: float = 0.0
    usage_ratio_after: float = 0.0
    details: Tuple[ToolPruneDetail, ...] = ()


SummaryDecision = Literal['summarized', 'skipped', 'abandoned']


@dataclass(frozen=True)
class SummaryEvent:
    trigger: CompressionTrigger
    decision: SummaryDecision
    reason: str
    estimated_before: int
    estimated_after: int
    reclaimed_tokens: int
    budget: ContextBudget
    replaced_message_count: int = 0
    tail_tokens: int = 0
    summary_tokens: int = 0
    usage_ratio_before: float = 0.0
    usage_ratio_after: float = 0.0


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
