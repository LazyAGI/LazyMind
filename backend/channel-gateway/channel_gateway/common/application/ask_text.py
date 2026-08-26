from __future__ import annotations

import re
from typing import Any


_NUMBERED_ANSWER = re.compile(r'^\s*(\d{1,2})\s*[:：.、]\s*(.+?)\s*$')
_MULTIPLE_ANSWER_SEPARATOR = re.compile(r'[\s,，、]+')


def render_text_ask(ask: dict[str, Any]) -> str:
    if ask.get('submittable') is False:
        return '需要补充信息，但当前问题无法通过纯文本提交，请重新发起任务。'
    questions = _questions(ask)
    if not questions:
        return ''
    lines = ['💬 需要你的回答']
    title = str(ask.get('title') or '').strip()
    description = str(ask.get('description') or '').strip()
    if title:
        lines.append(title)
    if description:
        lines.append(description)
    for question_index, question in enumerate(questions, start=1):
        lines.append(f'{question_index}. {question["text"]}')
        for choice_index, choice in enumerate(question['choices'], start=1):
            lines.append(f'   {choice_index}) {choice}')
    lines.append(
        '只有一题时可直接回复答案或选项编号；多题请逐行回复“题号: 答案”。'
    )
    return '\n'.join(lines)


def parse_text_ask_answer(
    text: str,
    selection: dict[str, Any] | None,
) -> dict[str, Any] | None:
    if not isinstance(selection, dict) or selection.get('kind') != 'ask':
        return None
    items = selection.get('items')
    ask = items[0] if isinstance(items, list) and items else None
    if not isinstance(ask, dict):
        return None
    questions = _questions(ask)
    if not questions:
        return None
    raw_answers: dict[int, str] = {}
    if len(questions) == 1:
        raw_answers[1] = text.strip()
    else:
        for line in text.splitlines():
            match = _NUMBERED_ANSWER.fullmatch(line)
            if match:
                raw_answers[int(match.group(1))] = match.group(2).strip()
    if len(raw_answers) != len(questions):
        return None

    answered: list[dict[str, Any]] = []
    for index, question in enumerate(questions, start=1):
        answer = _answer(question, raw_answers.get(index, ''))
        if answer is None:
            return None
        answered.append({
            'id': question['id'],
            'text': question['text'],
            'type': question['type'],
            'choices': question['choices'],
            'custom_choices': question['choices'],
            'answer': answer,
        })
    ask_id = str(ask.get('ask_id') or '')
    if not ask_id:
        return None
    return {'ask_id': ask_id, 'questions': answered}


def _questions(ask: dict[str, Any]) -> list[dict[str, Any]]:
    raw_questions = ask.get('questions')
    result: list[dict[str, Any]] = []
    for question in (
        raw_questions if isinstance(raw_questions, list) else []
    )[:10]:
        if not isinstance(question, dict):
            continue
        question_type = str(question.get('type') or 'text')
        if question_type not in {'text', 'single', 'multiple', 'boolean'}:
            continue
        choices = [
            str(choice)
            for choice in (
                question.get('choices')
                if isinstance(question.get('choices'), list)
                else []
            )
            if str(choice)
        ]
        if question_type == 'boolean' and not choices:
            choices = ['是', '否']
        question_text = str(question.get('text') or '').strip()
        if question_text:
            result.append({
                'id': str(question.get('id') or ''),
                'text': question_text,
                'type': question_type,
                'choices': choices,
            })
    return result


def _answer(
    question: dict[str, Any],
    raw_answer: str,
) -> dict[str, Any] | None:
    value = raw_answer.strip()
    if not value:
        return None
    question_type = question['type']
    choices = question['choices']
    if question_type == 'text':
        return {'type': 'text', 'value': value}
    if question_type == 'multiple':
        selected = [
            resolved
            for item in _MULTIPLE_ANSWER_SEPARATOR.split(value)
            if item and (resolved := _resolve_choice(item, choices))
        ]
        if not selected:
            return None
        return {'type': 'multiple', 'value': list(dict.fromkeys(selected))}
    selected = _resolve_choice(value, choices)
    if not selected:
        return None
    return {'type': question_type, 'value': selected}


def _resolve_choice(value: str, choices: list[str]) -> str:
    if value.isdigit():
        index = int(value) - 1
        return choices[index] if 0 <= index < len(choices) else ''
    return next((choice for choice in choices if choice == value), '')
