"""Structured prompt composition and shared agent execution for LazyMind."""

from .executor import AgentExecutor
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
from .prompt_builder import PromptBuilder
from .context_estimator import (
    estimate_context_usage,
    estimate_tokens,
    render_context_markdown,
    report_to_dict,
)

__all__ = [
    'AgentExecutionOptions',
    'AgentExecutor',
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
    'AttachmentRef',
    'normalize_attachments',
    'render_attachment_content',
]
