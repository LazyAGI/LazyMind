"""ask_user — ChatAgent-only stop-tool for interactive clarification.

Suspends the current ReAct turn and presents one or more questions to the
user.  The tool is registered as a stop-tool so ReAct exits immediately after
invocation.  The user's answers arrive as plain text in the next chat turn's
query; no special ask_response parameter is needed.

Supported question types:
  boolean   — yes/no question rendered as two buttons (Yes / No)
  single    — single-choice question; "Other" is automatically appended
  multiple  — multi-choice question; "Other" is automatically appended
  text      — free-text input field

This tool is intentionally NOT added to DEFAULT_TOOLS, so SubAgents never
receive it (SubAgent tool resolution falls back to DEFAULT_TOOLS).
"""
from __future__ import annotations

import uuid
from typing import Any, Dict, List, Optional, Union

from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.base import _write_agent_data


_OTHER_OPTION = '其他'
_BOOLEAN_CHOICES = ['是', '否']
_VALID_TYPES = {'boolean', 'single', 'multiple', 'text'}


def _normalise_choice(raw: Any) -> str:
    """Accept the declared string format and repair common model wrappers."""
    if isinstance(raw, dict):
        raw = raw.get('label') or raw.get('value') or raw.get('text') or ''
    return str(raw).strip()


def _normalise_questions(raw: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """Validate and normalise the questions list.

    - Ensures required fields are present.
    - For boolean: overwrites choices with ['Yes', 'No'].
    - For single/multiple: appends 'Other' if not already present.
    - For text: clears choices.
    """
    normalised = []
    for i, q in enumerate(raw):
        if not isinstance(q, dict):
            raise ToolExecutionError(
                f'Question {i} must be a dict, got {type(q).__name__}'
            )
        text = str(q.get('text', '')).strip()
        if not text:
            raise ToolExecutionError(f'Question {i} is missing required field "text"')
        q_type = str(q.get('type', 'text')).strip().lower()
        if q_type not in _VALID_TYPES:
            raise ToolExecutionError(
                f'Question {i} has invalid type {q_type!r}. '
                f'Must be one of: {", ".join(sorted(_VALID_TYPES))}'
            )
        raw_choices = q.get('choices') or []
        if not isinstance(raw_choices, (list, tuple)):
            raise ToolExecutionError(
                f'Question {i} field "choices" must be a list of strings.'
            )
        choices = list(raw_choices)

        if q_type == 'boolean':
            choices = list(_BOOLEAN_CHOICES)
        elif q_type in ('single', 'multiple'):
            # Clean and validate each choice; discard blank entries.
            choices = [choice for item in choices if (choice := _normalise_choice(item))]
            if not choices:
                raise ToolExecutionError(
                    f'Question {i} of type {q_type!r} requires at least one non-empty'
                    ' choice.'
                )
            allow_other = q.get('allow_other', True)
            if not isinstance(allow_other, bool):
                raise ToolExecutionError(
                    f'Question {i} field "allow_other" must be a boolean.'
                )
            choices = [choice for choice in choices if choice != _OTHER_OPTION]
            if allow_other:
                choices.append(_OTHER_OPTION)
        else:  # text
            choices = []

        question = {'text': text, 'type': q_type, 'choices': choices}
        if q_type in ('single', 'multiple'):
            question['allow_other'] = allow_other
        normalised.append(question)
    return normalised


def ask_user(
    questions: List[Dict[str, Union[bool, str, List[str]]]],
    title: Optional[str] = None,
    description: Optional[str] = None,
) -> str:
    """Ask the user through an interactive UI card and end the current turn.

    Use this for user-facing questions on the first assistant turn as well as
    later clarification or follow-up turns. No earlier card or tool call is
    required.

    Args:
        questions: Items with `text` and type `boolean`, `single`, `multiple`,
            or `text`. Use one item when asked to question the user one at a time.
            Use `text` without `choices` when useful suggestions are unavailable.
            Otherwise use choice types with a few editable string recommendations;
            "Other" is automatic unless `allow_other=false`. Follow-up questions,
            including those after an earlier card, use another call.
        title: Optional card heading.
        description: Optional subtitle.
    """
    if not isinstance(questions, list) or len(questions) == 0:
        raise ToolExecutionError('"questions" must be a non-empty list of question dicts.')

    normalised = _normalise_questions(questions)
    ask_id = str(uuid.uuid4())
    payload: Dict[str, Any] = {'ask_id': ask_id, 'questions': normalised}
    if title and str(title).strip():
        payload['title'] = str(title).strip()
    if description and str(description).strip():
        payload['description'] = str(description).strip()
    _write_agent_data('ask_pending', **payload)
    return f'Question sent to user (ask_id={ask_id}). Waiting for answer on next turn.'
