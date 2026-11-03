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

from lazyllm.tools import fc_register

import uuid
import math
from typing import Any, Dict, List, Optional

from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.base import _write_agent_data

from .ask_user_schema import (
    MAX_MULTIPLE_SUGGESTIONS, MAX_QUESTIONS, MAX_SINGLE_SUGGESTIONS, ModelQuestions, validate_model_questions,
)

_OTHER_OPTION = '__ask_user_other__'
_BOOLEAN_CHOICES = ['__ask_user_yes__', '__ask_user_no__']
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
    ids = set()
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
        policy = q.get('choice_policy', 'seed')
        if policy not in {'seed', 'subset', 'fixed'}:
            raise ToolExecutionError('choice_policy must be seed, subset, or fixed.')

        if q_type == 'boolean':
            choices = list(_BOOLEAN_CHOICES)
        elif q_type in ('single', 'multiple'):
            # Clean and validate each choice; discard blank entries.
            choices = list(dict.fromkeys(
                choice for item in choices if (choice := _normalise_choice(item)) and choice != _OTHER_OPTION
            ))
            if not choices:
                raise ToolExecutionError(
                    f'Question {i} of type {q_type!r} requires at least one non-empty'
                    ' choice.'
                )
            limit = MAX_SINGLE_SUGGESTIONS if q_type == 'single' else MAX_MULTIPLE_SUGGESTIONS
            if policy != 'fixed' and len(choices) > limit:
                raise ToolExecutionError(
                    'Use at most 3 single-choice or 4 multiple-choice suggestions; do not invent choices.'
                )
            allow_other = q.get('allow_other', policy != 'fixed')
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
        question_id = str(q.get('id') or f'q{i + 1}').strip()
        if question_id in ids:
            raise ToolExecutionError('Question ids must be unique.')
        ids.add(question_id)
        question['id'] = question_id
        if 'required' in q and not isinstance(q['required'], bool):
            raise ToolExecutionError('required must be a boolean.')
        question['required'] = q.get('required') is True
        if q.get('reason'):
            question['reason'] = str(q['reason']).strip()
        if q_type == 'boolean' and q.get('boolean_labels') is not None:
            labels = q['boolean_labels']
            if (not isinstance(labels, dict)
                    or any(not isinstance(labels.get(key), str) or not labels[key].strip() for key in ('yes', 'no'))
                    or labels['yes'].strip() == labels['no'].strip()):
                raise ToolExecutionError('Provide two different, complete yes/no button labels.')
            question['boolean_labels'] = {key: str(labels[key]).strip() for key in ('yes', 'no')}
        if q.get('validation') is not None:
            rule = q['validation']
            if not isinstance(rule, dict) or rule.get('format', '') not in {'', 'integer', 'positive_integer'}:
                raise ToolExecutionError('Invalid text validation format.')
            for key in ('min_length', 'max_length'):
                if key in rule and (type(rule[key]) is not int or rule[key] < 0):
                    raise ToolExecutionError('Length limits must be non-negative integers.')
            for key in ('min', 'max'):
                if key in rule and (type(rule[key]) not in (int, float) or not math.isfinite(rule[key])):
                    raise ToolExecutionError('Numeric bounds must be finite numbers.')
            if ((rule.get('max_length', 0) and rule.get('min_length', 0) > rule['max_length'])
                    or ('min' in rule and 'max' in rule and rule['min'] > rule['max'])):
                raise ToolExecutionError('Validation range is reversed.')
            question['validation'] = {key: rule[key] for key in (
                'format', 'example', 'min_length', 'max_length', 'min', 'max',
            ) if key in rule}
        if q_type in ('single', 'multiple'):
            question['allow_other'] = allow_other
            question['choice_policy'] = policy
        attachment = q.get('attachment')
        if attachment is not None:
            if q_type != 'single' or not isinstance(attachment, dict):
                raise ToolExecutionError(
                    f'Question {i} field "attachment" requires a single-choice question.'
                )
            when_choice = str(attachment.get('when_choice') or '').strip()
            material_id = str(attachment.get('material_id') or '').strip()
            accept = attachment.get('accept') or []
            max_files = attachment.get('max_files', 1)
            if when_choice not in choices:
                raise ToolExecutionError(
                    f'Question {i} attachment.when_choice must match a declared choice.'
                )
            if not material_id:
                raise ToolExecutionError(
                    f'Question {i} attachment.material_id is required.'
                )
            if not isinstance(accept, (list, tuple)) or not accept:
                raise ToolExecutionError(
                    f'Question {i} attachment.accept must be a non-empty list.'
                )
            if type(max_files) is not int or (max_files != -1 and max_files < 1):
                raise ToolExecutionError(
                    f'Question {i} attachment.max_files must be positive, or -1 for unlimited.'
                )
            question['attachment'] = {
                'when_choice': when_choice,
                'material_id': material_id,
                'accept': [str(value).strip().lower() for value in accept if str(value).strip()],
                'max_files': max_files,
                'required': attachment.get('required') is True,
            }
        normalised.append(question)
    return normalised


def question_card_payload(questions, title=None, description=None, workflow=None) -> Dict[str, Any]:
    """Build the same card for direct host checks and model-authored questions."""
    if not isinstance(questions, list) or not 1 <= len(questions) <= MAX_QUESTIONS:
        raise ToolExecutionError('Ask one to three questions per clarification card.')
    normalised = _normalise_questions(questions)
    if not str(description or '').strip() and any(not question.get('reason') for question in normalised):
        raise ToolExecutionError('Explain why confirmation is needed in reason or description.')
    payload = {'ask_id': str(uuid.uuid4()), 'questions': normalised}
    if title and str(title).strip():
        payload['title'] = str(title).strip()
    if description and str(description).strip():
        payload['description'] = str(description).strip()
    if workflow:
        payload['workflow'] = dict(workflow)
    return payload


def emit_question_card(questions, title=None, description=None, workflow=None) -> str:
    payload = question_card_payload(questions, title, description, workflow)
    _write_agent_data('ask_pending', **payload)
    return f'Question sent to user (ask_id={payload["ask_id"]}). Waiting for answer on next turn.'


@fc_register(host_file='NONE')
def ask_user(
    questions: ModelQuestions,
    title: Optional[str] = None,
    description: Optional[str] = None,
) -> str:
    """Ask the user through an interactive UI card and end the current turn.

    Use this for user-facing questions on the first assistant turn as well as
    later clarification or follow-up turns. Ask only for blocking information or
    an explicit interview; reuse context and safe defaults. Do not ask optional
    preferences merely because this tool exists. No earlier tool call is required.

    Args:
        questions: One to three items, each requiring id, text, reason, required, and type.
            Type is boolean, single, multiple, or text. Use one item when asked one question at a time.
            Use `text` without `choices` when useful suggestions are unavailable.
            Single suggestions have at most three choices, multiple suggestions at most four;
            do not pad choices. Multiple permits selecting only one item. Fixed enums
            retain all supported choices; the UI uses search when there are more than six.
            Boolean uses boolean_labels={yes: '具体同意内容', no: '具体拒绝内容'}.
            Boolean requires boolean_labels and must not supply choices. Text must not supply choices;
            it can supply validation with useful example, real min_length/max_length and numeric bounds.
            "Other" is automatic unless `allow_other=false`. Follow-up questions,
            including those after an earlier card, use another call. A host-governed
            conditional `attachment` may augment a single-choice question; it reuses
            normal conversation attachments rather than adding another question type.
        title: Optional card heading.
        description: Optional subtitle.
    """
    return emit_question_card(validate_model_questions(questions), title, description)
