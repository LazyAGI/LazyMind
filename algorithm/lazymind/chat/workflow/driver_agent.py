"""LazyMind-only DriverAgent for evaluating terminal Workflow attempts."""
from __future__ import annotations

import re
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import AutoModel, LOG

from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
)
from lazymind.model_config import inject_model_config

_THINK_RE = re.compile(r'<(?:redacted_thinking|think)>.*?</(?:redacted_thinking|think)>', re.S | re.I)
_OUTPUT_SECTION_RE = re.compile(r'\n##\s*Output format[\s\S]*$', re.I)
_DEFAULT_PROMPT = """You are the quality evaluator for one Workflow step.
Assess the terminal result against its acceptance criteria and available artifacts.
If acceptable, state what completed. If unacceptable, identify what is missing or wrong,
the likely cause, and whether ChatAgent should retry this step or rewind to a named upstream step.
Write 1-2 plain sentences under 60 words. Do not emit verdict codes, tags, headings, or analysis."""
_OUTPUT_CONSTRAINT = """

Your response becomes the next simulated user message. Return only 1-2 concise natural-language
sentences. Never claim success when the terminal status or required artifacts indicate failure.
Never ask the user a question and never directly invoke a Workflow transition.
"""


class DriverEvaluationError(Exception):
    """DriverAgent could not produce a usable assessment."""


def _clean_message(text: str) -> str:
    text = _THINK_RE.sub('', text)
    text = re.sub(r'<[^>]+>', '', text).strip()
    if len(text) > 300:
        text = text[:300].rstrip() + '...'
    return text


def evaluate_step(
    workflow_id: str,
    step_id: str,
    step_result: str,
    *,
    acceptance: str = '',
    driver_prompt: str = '',
    session_id: Optional[str] = None,
    user_files: Optional[List[str]] = None,
    llm_config: Optional[Dict[str, Any]] = None,
    workflow_artifacts_summary: Optional[str] = None,
) -> Dict[str, Any]:
    """Evaluate one terminal attempt using the pinned Workflow's Driver policy."""
    base = _OUTPUT_SECTION_RE.sub('', driver_prompt).strip() if driver_prompt.strip() else _DEFAULT_PROMPT
    builder = PromptBuilder.for_role(AgentRole.DRIVER)
    builder.system('driver_policy', '', base + _OUTPUT_CONSTRAINT, 'workflow.driver', priority=10)
    builder.system(
        'driver_acceptance', f'Acceptance criteria for step {step_id!r}',
        acceptance or 'No explicit criteria; require successful terminal status and declared outputs.',
        'workflow.state', priority=20,
    )
    builder.runtime(
        'driver_step', 'Workflow Step', f'Workflow: {workflow_id}\nStep: {step_id}',
        'workflow.runtime', priority=10, authoritative=True, content_kind='state',
    ).runtime(
        'driver_result', 'Terminal Attempt Result', step_result,
        'attempt.summary', priority=20, authoritative=True, content_kind='state',
    ).runtime(
        'driver_artifacts', 'Session Artifacts', workflow_artifacts_summary,
        'workflow.artifacts', priority=30, content_kind='reference',
    ).runtime(
        'driver_attachments', 'Available Attachments', ', '.join(user_files or []),
        'request.attachments', priority=40, content_kind='reference',
    ).input(
        content=(
            'Evaluate this terminal attempt. Diagnose failures and recommend retrying this exact '
            'step or rewinding to a named upstream step when appropriate. Do not perform either action.'
        ),
        source='platform.driver',
    )
    plan = AgentRunPlan(
        role=AgentRole.DRIVER, prompt=builder.build(), history=[], tools=[],
        execution_options=AgentExecutionOptions(),
    )
    try:
        sid = session_id or f'driver_{workflow_id}_{step_id}'
        lazyllm.globals._init_sid(sid=sid)
        lazyllm.locals._init_sid(sid=sid)
        inject_model_config(llm_config)
        response = AgentExecutor().run(AutoModel(model='llm'), plan)
        cleaned = _clean_message(str(response or ''))
        if not cleaned:
            raise DriverEvaluationError('DriverAgent returned an empty assessment.')
        return {'message': cleaned}
    except DriverEvaluationError:
        raise
    except Exception as exc:
        LOG.warning('[DriverAgent] workflow=%s step=%s failed: %s', workflow_id, step_id, exc)
        raise DriverEvaluationError(str(exc)) from exc
