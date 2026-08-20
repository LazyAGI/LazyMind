from __future__ import annotations

import json
from typing import Any


MAX_ASK_QUESTION_CHARS = 500
MAX_ASK_CHOICE_CHARS = 80
ASK_OTHER_OPTION = '其他'
_MAX_ASK_ACTION_BYTES = 16 * 1024


def ask_action_within_budget(action: dict[str, Any]) -> bool:
    try:
        encoded = json.dumps(
            action,
            ensure_ascii=False,
            separators=(',', ':'),
        ).encode('utf-8')
    except (TypeError, ValueError):
        return False
    return len(encoded) <= _MAX_ASK_ACTION_BYTES


def _markdown_literal(value: Any) -> str:
    return ''.join(
        f'\\{character}'
        if character in '\\`*_{}[]()#>+-!|'
        else character
        for character in str(value or '')
    )


def ask_form(
    payload: dict[str, Any],
    questions: list[dict[str, Any]],
    provider_context: dict[str, Any],
) -> dict[str, Any] | None:
    usable = questions[:10]
    if not usable:
        return None
    fields: list[dict[str, Any]] = []
    schema: list[dict[str, Any]] = []
    for index, question in enumerate(usable, start=1):
        field_name = f'ask_q_{index}'
        question_text = str(question.get('text') or '')
        question_type = str(question.get('type') or 'text')
        choices = ask_choices(question)
        field = _ask_form_field(field_name, question_type, choices)
        if field is None:
            return None
        fields.extend([
            {
                'tag': 'markdown',
                'content': (
                    f'**{index}. '
                    f'{_markdown_literal(question_text[:MAX_ASK_QUESTION_CHARS])}**'
                ),
            },
            field,
        ])
        description = str(question.get('description') or '').strip()
        if description:
            fields.insert(-1, {
                'tag': 'markdown',
                'content': description[:1000],
            })
        other_name = ''
        if question_type in {'single', 'multiple'} and ASK_OTHER_OPTION in choices:
            other_name = f'{field_name}_other'
            fields.extend([
                {
                    'tag': 'markdown',
                    'content': (
                        '<font color="grey">'
                        '选择“其他”时请补充说明</font>'
                    ),
                },
                {
                    'tag': 'input',
                    'element_id': f'ask_other_{index}',
                    'name': other_name,
                    'required': False,
                    'input_type': 'text',
                    'width': 'fill',
                    'max_length': 1000,
                    'placeholder': {
                        'tag': 'plain_text',
                        'content': '请输入补充内容',
                    },
                },
            ])
        schema.append({
            'id': str(question.get('id') or ''),
            'name': field_name,
            'other_name': other_name,
            'text': question_text,
            'type': question_type,
            'choices': choices,
        })
    action = {
        'lazymind_action': 'ask',
        'ask_id': str(payload.get('ask_id') or ''),
        'ask_form_questions': schema,
        'intended_chat_id': str(provider_context.get('chat_id') or ''),
    }
    if not ask_action_within_budget(action):
        return None
    fields.append({
        'tag': 'column_set',
        'flex_mode': 'none',
        'horizontal_spacing': '8px',
        'columns': [{
            'tag': 'column',
            'width': 'weighted',
            'weight': 1,
            'elements': [{
                'tag': 'button',
                'name': 'ask_submit',
                'text': {
                    'tag': 'plain_text',
                    'content': '提交回答',
                },
                'type': 'primary',
                'width': 'fill',
                'action_type': 'form_submit',
                'value': action,
            }],
        }],
    })
    return {'tag': 'form', 'name': 'ask_form', 'elements': fields}


def ask_choices(question: dict[str, Any]) -> list[str]:
    raw = question.get('choices')
    choices = [
        str(choice)
        for choice in (raw if isinstance(raw, list) else [])
        if str(choice)
    ]
    if not choices and str(question.get('type') or '') == 'boolean':
        return ['是', '否']
    return choices


def _ask_form_field(
    field_name: str,
    question_type: str,
    choices: list[str],
) -> dict[str, Any] | None:
    common = {
        'element_id': f'{field_name}_field',
        'name': field_name,
        'required': True,
        'width': 'fill',
    }
    if question_type == 'text':
        return {
            'tag': 'input',
            **common,
            'input_type': 'multiline_text',
            'rows': 3,
            'auto_resize': True,
            'max_rows': 8,
            'max_length': 1000,
            'placeholder': {
                'tag': 'plain_text',
                'content': '请输入回答',
            },
        }
    if question_type in {'boolean', 'single'} and choices:
        return {
            'tag': 'select_static',
            **common,
            'placeholder': {'tag': 'plain_text', 'content': '请选择'},
            'options': _ask_select_options(choices),
        }
    if question_type == 'multiple' and choices:
        return {
            'tag': 'multi_select_static',
            **common,
            'placeholder': {'tag': 'plain_text', 'content': '可选择多项'},
            'options': _ask_select_options(choices),
        }
    return None


def _ask_select_options(choices: list[str]) -> list[dict[str, Any]]:
    return [
        {
            'text': {
                'tag': 'plain_text',
                'content': choice[:MAX_ASK_CHOICE_CHARS],
            },
            'value': choice,
        }
        for choice in choices[:20]
    ]
