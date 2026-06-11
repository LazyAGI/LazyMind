from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any


@dataclass
class TraceSummaryView:
    status: str
    latency_ms: float | None
    round_count: int
    tool_call_count: int
    retrieval_count: int
    rerank_count: int


class TraceIOKind(str, Enum):
    REQUEST_CONTEXT = 'request_context'
    CALL_INPUT = 'call_input'
    LLM_PROMPT = 'llm_prompt'
    LLM_MESSAGES = 'llm_messages'
    ASSISTANT_MESSAGE = 'assistant_message'
    TOOL_CALL_BATCH = 'tool_call_batch'
    TOOL_RESULT_BATCH = 'tool_result_batch'
    TOOL_ARGUMENTS = 'tool_arguments'
    TOOL_RESULT = 'tool_result'
    TOOL_ERROR = 'tool_error'
    CALCULATOR_RESULT = 'calculator_result'
    KB_SEARCH_RESULT = 'kb_search_result'
    WEB_SEARCH_RESULT = 'web_search_result'
    RETRIEVER_QUERY = 'retriever_query'
    RETRIEVER_RESULT = 'retriever_result'
    RERANK_QUERY = 'rerank_query'
    RERANK_RESULT = 'rerank_result'
    OBJECT = 'object'
    LIST = 'list'
    STRING = 'string'
    NUMBER = 'number'
    BOOLEAN = 'boolean'
    NULL = 'null'


@dataclass
class TraceIOView:
    kind: TraceIOKind
    summary: str
    data: dict[str, Any] = field(default_factory=dict)


@dataclass
class TraceNodeView:
    id: str
    name: str
    type: str
    status: str
    start_time: float
    end_time: float | None
    latency: float | None
    input: TraceIOView
    output: TraceIOView
    metadata: dict[str, Any] = field(default_factory=dict)
    children: list['TraceNodeView'] = field(default_factory=list)


@dataclass
class TraceView:
    trace_id: str
    metadata: dict[str, Any]
    root: TraceNodeView


@dataclass
class TraceDetailView:
    trace_id: str
    trace_status: str
    query: str
    summary: TraceSummaryView
    trace: TraceView | None


@dataclass
class TraceCompareView:
    query: str
    a: TraceDetailView
    b: TraceDetailView
