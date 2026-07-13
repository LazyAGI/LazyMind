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
from typing import Any, Dict, List, Optional

from lazyllm.tools.agent.base import _write_agent_data


_OTHER_OPTION = '其他'
_BOOLEAN_CHOICES = ['是', '否']
_VALID_TYPES = {'boolean', 'single', 'multiple', 'text'}


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
            # Clean and validate each choice; discard blank entries.
            choices = [str(c).strip() for c in choices if str(c).strip()]
            if not choices:
                raise ValueError(
                    f'Question {i} of type {q_type!r} requires at least one non-empty'
                    ' choice.'
                )
            if _OTHER_OPTION not in choices:
                choices.append(_OTHER_OPTION)
        else:  # text
            choices = []

        normalised.append({'text': text, 'type': q_type, 'choices': choices})
    return normalised


def ask_user(
    questions: List[Dict[str, Any]],
    title: Optional[str] = None,
    description: Optional[str] = None,
) -> str:
    """MANDATORY: ask the user through a structured UI card.

    Whenever you want the user to answer a question, call `ask_user` instead
    of writing the question in plain assistant text. This includes
    clarification, confirmation, follow-up, preference, quiz, test, exam,
    interview, and knowledge-check questions.

    Prefer `boolean`, `single`, or `multiple` when plausible answers are
    known; use `text` only when they are not. Single- and multiple-choice
    questions automatically allow a custom response through the appended
    "Other" option.

    Ask related questions in one manageable batch. If there are many
    questions, split them into multiple batches. After the user answers, call
    `ask_user` again if the issue remains unresolved, new questions arise, or
    another quiz or test batch is needed.

    Calling this tool ends the current turn. Do not continue after calling it.

    Question types:
      "boolean"  — Yes / No toggle buttons. Omit choices (auto-set).
      "single"   — Radio buttons; pick exactly one. "Other" appended automatically.
      "multiple" — Checkboxes; pick one or more. "Other" appended automatically.
      "text"     — Free-form text area. Omit choices.

    Args:
        questions: Non-empty list of question dicts. Each must have:
            text    (str)  : Question text shown to the user.
            type    (str)  : "boolean" | "single" | "multiple" | "text".
            choices (list) : Required for "single"/"multiple". Omit otherwise.
        title: Short group heading shown above the wizard card (optional).
            Example: "Weekly report setup"
        description: One-sentence subtitle shown below the title (optional).
            Example: "Answer a few questions so I can draft your weekly report."

    Example:
        questions=[
            {"text": "Which image style do you prefer?", "type": "single",
             "choices": ["Photorealistic", "Illustration", "Minimalist"]},
            {"text": "Do you need a portrait (vertical) composition?", "type": "boolean"},
            {"text": "Any other special requirements?", "type": "text"},
        ],
        title="Image generation settings",
        description="Answer these questions and I will generate your image."

    Returns:
        A placeholder confirmation string. ReAct exits immediately.
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
