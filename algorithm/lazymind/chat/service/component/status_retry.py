from __future__ import annotations

import re
from typing import Any

from lazyllm import AutoModel

from lazymind.chat.engine.agent_core import build_react_agent


# Temporary guardrail for a known model behavior: sometimes the chat agent
# returns only a short progress promise such as "I will write it now" without
# using tools or producing the requested content. Keep this logic isolated so
# it can be removed after the underlying prompt/model issue is fixed.
_STATUS_ONLY_ANSWER_PATTERN = re.compile(
    r'^\s*(?:'
    r'(?:正在|我(?:会|将|来)|马上|接下来|下面)(?:为你|为您|帮你|帮您|给你|给您)?.{0,80}'
    r'|(?:I(?:\'ll| will| am going to)|Let me|I am now).{0,80}'
    r')\s*(?:[。.!！…]|……)?\s*$',
    re.IGNORECASE,
)


def is_status_only_answer(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    text = value.strip()
    if not text or len(text) > 120:
        return False
    return bool(_STATUS_ONLY_ANSWER_PATTERN.match(text))


def build_status_retry_query(agent_query: str) -> str:
    return (
        f'{agent_query}\n\n---\n\n'
        '## Correction\n'
        'Your previous final answer was only a status/progress promise. '
        'Do not say that you are about to write or generate content. '
        'Return the actual requested content directly now. '
        'If the user asked for a story, article, report, or draft, write the body itself.'
    )


def _new_react_agent(
    *,
    all_tools: list[Any],
    query: str,
    runtime_prompt: str,
    agent: Any,
    config: Any,
    fs: Any,
    stop_tools: list[str],
) -> Any:
    agent_obj = build_react_agent(
        llm=AutoModel(model='llm'),
        tools=all_tools,
        force_summarize_context=query,
        prompt=runtime_prompt,
        skills=agent.available_skills,
        workspace=config['agentic_workspace'],
        keep_full_turns=config['agentic_keep_full_turns'],
        fs=fs,
        skills_dir=config['skill_fs_url'],
    )
    agent_obj.set_stop_tools(stop_tools)
    return agent_obj
