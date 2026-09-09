"""Structured prompt composition and shared agent execution for LazyMind."""

from .executor import AgentExecutor, AgentInvocation
from .attachments import AttachmentRef, normalize_attachments, render_attachment_content
from .models import (
    AgentExecutionOptions,
    AgentRole,
    AgentRunPlan,
    PromptBundle,
    PromptSection,
    ContextUsageCategory,
    ContextUsageItem,
    ContextUsageReport,
)
from .cancellation import UserCancelledError, make_cancel_stop_condition
from .prompt_builder import PromptBuilder
from .context_estimator import (
    estimate_context_usage,
    estimate_tokens,
    render_context_markdown,
    report_to_dict,
    attach_window_budget,
)

__all__ = [
    'AgentExecutionOptions',
    'AgentExecutor',
    'AgentInvocation',
    'AgentRole',
    'AgentRunPlan',
    'PromptBuilder',
    'PromptBundle',
    'PromptSection',
    'ContextUsageCategory',
    'ContextUsageItem',
    'ContextUsageReport',
    'estimate_context_usage',
    'estimate_tokens',
    'render_context_markdown',
    'report_to_dict',
    'attach_window_budget',
    'AttachmentRef',
    'normalize_attachments',
    'render_attachment_content',
    'make_cancel_stop_condition',
    'UserCancelledError',
]
