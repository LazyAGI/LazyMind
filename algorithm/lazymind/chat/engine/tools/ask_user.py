"""ask_user — ChatAgent-only stop-tool for interactive clarification.

Suspends the current ReAct turn and presents one or more questions to the
user.  The tool is registered as a stop-tool so ReAct exits immediately after
invocation.  The user's answers arrive as plain text in the next chat turn's
query; no special ask_response parameter is needed.

Supported question types:
  boolean   — yes/no question rendered as two buttons (choices always ['是', '否'])
  single    — single-choice question; '其他' is automatically appended
  multiple  — multi-choice question; '其他' is automatically appended
  text      — free-text input field

This tool is intentionally NOT added to DEFAULT_TOOLS, so SubAgents never
receive it (SubAgent tool resolution falls back to DEFAULT_TOOLS).
"""
from __future__ import annotations

import uuid
from typing import Any, Dict, List, Optional

from lazyllm.tools.agent.base import _write_agent_data

from lazymind.chat.engine.tools.infra import handle_tool_errors

_OTHER_OPTION = '其他'
_BOOLEAN_CHOICES = ['是', '否']
_VALID_TYPES = {'boolean', 'single', 'multiple', 'text'}


def _normalise_questions(raw: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """Validate and normalise the questions list.

    - Ensures required fields are present.
    - For boolean: overwrites choices with ['是', '否'].
    - For single/multiple: appends '其他' if not already present.
    - For text: clears choices.
    """
    normalised = []
    for i, q in enumerate(raw):
        if not isinstance(q, dict):
            raise ValueError(f'Question {i} must be a dict, got {type(q).__name__}')
        text = str(q.get('text', '')).strip()
        if not text:
            raise ValueError(f'Question {i} is missing required field "text"')
        q_type = str(q.get('type', 'text')).strip().lower()
        if q_type not in _VALID_TYPES:
            raise ValueError(
                f'Question {i} has invalid type {q_type!r}. '
                f'Must be one of: {", ".join(sorted(_VALID_TYPES))}'
            )
        choices = list(q.get('choices') or [])

        if q_type == 'boolean':
            choices = list(_BOOLEAN_CHOICES)
        elif q_type in ('single', 'multiple'):
            if _OTHER_OPTION not in choices:
                choices.append(_OTHER_OPTION)
        else:  # text
            choices = []

        normalised.append({'text': text, 'type': q_type, 'choices': choices})
    return normalised


@handle_tool_errors
def ask_user(
    questions: List[Dict[str, Any]],
    title: Optional[str] = None,
    description: Optional[str] = None,
) -> str:
    """Ask the user one or more questions and end the current ReAct turn.

    Use this tool INSTEAD of writing questions as plain text whenever you need
    information from the user before completing a task.  It renders a structured
    UI card (buttons, radio buttons, checkboxes, or text inputs), which is faster
    and clearer for the user than reading a numbered list.

    WHEN to use:
    - Before starting a task: missing critical inputs the user must supply.
    - Mid-task: need a decision or clarification only the user can resolve.
    - Collect ALL missing information in ONE call (multiple questions allowed).
    - After calling ask_user, stop — do NOT continue until the user answers.
    - Do NOT write questions as numbered text when ask_user is available.

    Question types:
      "boolean"  — yes/no; rendered as two clickable buttons (是 / 否).
                   Do not pass choices; they are set automatically.
      "single"   — pick exactly one option; '其他' is appended automatically.
                   Pass your options in the choices list.
      "multiple" — pick one or more options; '其他' is appended automatically.
                   Pass your options in the choices list.
      "text"     — free-form text input. No choices needed.

    Args:
        questions: Non-empty list of question dicts.  Each must contain:
            text    (str)           : The question text to display.
            type    (str)           : One of "boolean", "single", "multiple", "text".
            choices (list[str], optional): Required for "single" and "multiple".
                Leave empty or omit for "boolean" (auto-filled) and "text".
        title: Optional group title displayed at the top of the wizard card
            (e.g. "收集周报信息").  Omit to show no title.
        description: Optional subtitle / description shown below the title
            (e.g. "我将逐项收集信息，填写完成后生成你的周报").  Omit to show no description.

    Example:
        questions=[
            {"text": "你偏好哪种图片风格？", "type": "single",
             "choices": ["写实", "插画", "极简"]},
            {"text": "是否需要竖版构图？", "type": "boolean"},
            {"text": "还有其他特殊要求吗？", "type": "text"},
        ],
        title="图片生成设置",
        description="请回答以下问题，我将为你生成个性化图片。"

    Returns:
        Placeholder string; ReAct exits immediately after this call.
        The user's answers arrive as plain text in the next turn's query.
    """
    if not isinstance(questions, list) or len(questions) == 0:
        raise ValueError('"questions" must be a non-empty list of question dicts.')

    normalised = _normalise_questions(questions)
    ask_id = str(uuid.uuid4())
    payload: Dict[str, Any] = {'ask_id': ask_id, 'questions': normalised}
    if title and str(title).strip():
        payload['title'] = str(title).strip()
    if description and str(description).strip():
        payload['description'] = str(description).strip()
    _write_agent_data('ask_pending', **payload)
    return f'Question sent to user (ask_id={ask_id}). Waiting for answer on next turn.'
