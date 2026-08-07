from __future__ import annotations

import json
from typing import Any


_MAX_CODEX_CARD_TEXT_CHARS = 64000


def is_user_facing_codex_thread(value: dict[str, Any]) -> bool:
    name = value.get('name')
    if isinstance(name, dict):
        name = name.get('value')
    return bool(
        str(name or value.get('title') or '').strip()
        or value.get('managed_by_lazymind')
        or value.get('managed')
        or str(value.get('source') or '').lower() == 'cli'
    )


def codex_turns_for_card(value: Any) -> list[dict[str, str]]:
    turns: list[dict[str, str]] = []
    if isinstance(value, (bytes, bytearray)):
        try:
            value = json.loads(value.decode('utf-8'))
        except (TypeError, ValueError):
            return turns
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except (TypeError, ValueError):
            return turns
    if not isinstance(value, list):
        return turns
    for item in value:
        if not isinstance(item, dict):
            continue
        items = item.get('items') if isinstance(item.get('items'), list) else []
        user_text = ''
        final_answers: list[str] = []
        assistant_fallback = ''
        for entry in items:
            if not isinstance(entry, dict):
                continue
            entry_type = str(entry.get('type') or '')
            text = _codex_item_text(entry)
            if not text:
                continue
            if 'user' in entry_type.lower() or entry.get('role') == 'user':
                user_text = text
            elif 'agent' in entry_type.lower() or entry.get('role') in {
                'assistant',
                'agent',
            }:
                assistant_fallback = text
                if str(entry.get('phase') or '') == 'final_answer':
                    final_answers.append(text)
        assistant_text = '\n\n'.join(final_answers) or assistant_fallback
        if not user_text and not assistant_text:
            role = str(item.get('role') or '')
            text = _codex_item_text(item)
            if role and text:
                turns.append({
                    'role': role[:30],
                    'text': text[:_MAX_CODEX_CARD_TEXT_CHARS],
                })
            continue
        if user_text:
            turns.append({
                'role': 'user',
                'text': user_text[:_MAX_CODEX_CARD_TEXT_CHARS],
            })
        if assistant_text:
            turns.append({
                'role': 'assistant',
                'text': assistant_text[:_MAX_CODEX_CARD_TEXT_CHARS],
            })
    return turns[-4:]


def _codex_item_text(value: Any) -> str:
    if isinstance(value, str):
        return value.strip()
    if isinstance(value, list):
        return '\n'.join(
            text
            for item in value
            if (text := _codex_item_text(item))
        ).strip()
    if not isinstance(value, dict):
        return ''
    item_type = str(value.get('type') or '').lower()
    if item_type in {
        'reasoning',
        'contextcompaction',
        'commandexecution',
        'filechange',
    }:
        return ''
    for key in ('text', 'message', 'content'):
        text = _codex_item_text(value.get(key))
        if text:
            return text
    return ''
