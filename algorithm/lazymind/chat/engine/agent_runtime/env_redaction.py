from __future__ import annotations

import json
from collections.abc import Mapping
from typing import Any


SESSION_ENV_TOOL_NAME = 'set_session_env'
USER_ENV_TOOL_NAME = 'set_user_env'
REDACTED_ENV_VALUE = '<redacted>'
VOCABULARY_ASK_TOOL_NAME = 'ask_words'


def redact_session_env_arguments(tool_name: str, arguments: Any) -> Any:
    if str(tool_name or '') == VOCABULARY_ASK_TOOL_NAME and isinstance(arguments, Mapping):
        redacted = dict(arguments)
        questions = []
        for raw_question in arguments.get('questions') or []:
            if not isinstance(raw_question, Mapping):
                questions.append(raw_question)
                continue
            question = dict(raw_question)
            if 'correct_answer' in question:
                question['correct_answer'] = REDACTED_ENV_VALUE
            # Criteria can trivially repeat the answer (for example "must equal
            # diverse"), so it is sensitive for cloze questions as well.
            if str(arguments.get('type') or '').strip().lower() == 'cloze':
                question.pop('grading_criteria', None)
            questions.append(question)
        if 'questions' in redacted:
            redacted['questions'] = questions
        return redacted
    if str(tool_name or '') not in {SESSION_ENV_TOOL_NAME, USER_ENV_TOOL_NAME}:
        return arguments
    if isinstance(arguments, str):
        try:
            arguments = json.loads(arguments)
        except Exception:  # noqa: BLE001
            return REDACTED_ENV_VALUE if arguments else arguments
    if not isinstance(arguments, Mapping):
        return REDACTED_ENV_VALUE if arguments is not None else None
    redacted = dict(arguments)
    for key, value in list(redacted.items()):
        if key == 'name' or value in (None, REDACTED_ENV_VALUE):
            continue
        redacted[key] = REDACTED_ENV_VALUE
    return redacted
