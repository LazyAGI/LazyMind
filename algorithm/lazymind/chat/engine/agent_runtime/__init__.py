"""Structured prompt composition and shared agent execution for LazyMind."""

from .executor import AgentExecutor
from .attachments import AttachmentRef, normalize_attachments, render_attachment_content
from .models import (
    AgentExecutionOptions,
    AgentRole,
    AgentRunPlan,
    PromptBundle,
    PromptSection,
)
from .prompt_builder import PromptBuilder

__all__ = [
    'AgentExecutionOptions',
    'AgentExecutor',
    'AgentRole',
    'AgentRunPlan',
    'PromptBuilder',
    'PromptBundle',
    'PromptSection',
    'AttachmentRef',
    'normalize_attachments',
    'render_attachment_content',
]
