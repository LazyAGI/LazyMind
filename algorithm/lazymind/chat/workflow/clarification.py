"""Pure startup field resolution. No model, history scan, file or network reads."""
from __future__ import annotations

import json
import re
import uuid
from decimal import Decimal, InvalidOperation
from typing import Any

from lazymind.chat.engine.tools.ask_user import question_card_payload

_UNRESOLVED = object()
_NEGATION = re.compile(
    r'(?:(?:不要|不用|无需|不必|不想|不需要|禁止|别|不)\s*(?:再\s*)?(?:使用|采用|选择|用)?|\bnot|\bwithout)\s*$',
    re.IGNORECASE,
)


def fields_for(runtime: Any) -> list[dict]:
    return [dict(field) for field in (runtime or {}).get('clarification_fields', [])
            if isinstance(field, dict) and field.get('id') and field.get('question')]


def managed(runtime: Any) -> bool:
    return any('required' in field or 'default' in field for field in fields_for(runtime))


def _present(value: Any) -> bool:
    return value is not None and (not isinstance(value, str) or bool(value.strip())) and value != []


def _canonical(field: dict, value: Any) -> Any:
    aliases = field.get('value_aliases') or {}
    if isinstance(value, list):
        return [_canonical({**field, 'type': 'single'}, item) for item in value]
    key = str(value).lower() if isinstance(value, bool) else str(value).strip()
    return aliases.get(key, value)


def _number_literal(raw: str) -> str:
    """Normalize explicit Chinese/Arabic quantity notation, never infer a count."""
    raw = raw.replace(',', '').replace('，', '')
    try:
        number = Decimal(raw[:-1]) * 10000 if raw.endswith('万') else Decimal(raw)
        return str(int(number)) if number == number.to_integral_value() else raw
    except InvalidOperation:
        pass
    digits = {'零': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5,
              '六': 6, '七': 7, '八': 8, '九': 9}
    units = {'十': 10, '百': 100, '千': 1000, '万': 10000}
    if not raw or any(char not in digits and char not in units for char in raw):
        return raw
    total = section = digit = 0
    for char in raw:
        if char in digits:
            digit = digits[char]
        elif char == '万':
            total += (section + digit or 1) * 10000
            section = digit = 0
        else:
            section += (digit or 1) * units[char]
            digit = 0
    return str(total + section + digit)


def _extract(field: dict, request: str) -> Any:
    # Patterns are package-owned and validated by the compiler. Never execute
    # patterns supplied by the model or generated from the user's text.
    extracted = []
    for pattern in field.get('patterns') or []:
        for match in re.finditer(pattern, request, re.IGNORECASE):
            # Noncapturing alternatives consume quantities belonging to a
            # different scope (e.g. an abstract), without extracting them.
            if match.group(1) is None:
                continue
            raw = match.group(1).strip()
            if '[0-9' in pattern and re.fullmatch(r'[0-9.,，零一二三四五六七八九十百千万两]+', raw):
                raw = _number_literal(raw)
            value = _canonical(field, raw)
            if value not in extracted:
                extracted.append(value)
    if extracted:
        return extracted[0] if len(extracted) == 1 else _UNRESOLVED
    # Alias matching is restricted to domain-specific phrases. Generic yes/no
    # belongs to a submitted card, never to an unrelated sentence in the brief.
    aliases = field.get('value_aliases') or {}
    generic_answers = {'true', 'false', 'yes', 'no', '是', '否'}
    phrases = {alias: value for alias, value in aliases.items()
               if len(alias) > 1 and not alias.startswith('__ask_user_') and alias.lower() not in generic_answers}
    for choice in field.get('choices') or []:
        if len(choice) > 1:
            phrases.setdefault(choice, _canonical(field, choice))
    occupied, candidates, excluded = [], [], []
    declared = list(aliases.values())
    for phrase in sorted(phrases, key=len, reverse=True):
        for match in re.finditer(r'(?<![a-z0-9_.])' + re.escape(phrase) + r'(?![a-z0-9_])',
                                 request, re.IGNORECASE):
            if any(match.start() < end and match.end() > start for start, end in occupied):
                continue
            occupied.append(match.span())
            value = phrases[phrase]
            if _NEGATION.search(request[:match.start()]):
                # Only invert declared on/off semantics, never infer an
                # arbitrary alternative such as Chinese from "not English".
                opposite = {'enabled': 'disabled', 'disabled': 'enabled', 'true': 'false', 'false': 'true'}
                if value not in opposite or opposite[value] not in declared:
                    excluded.append(value)
                    continue
                value = opposite[value]
            if value not in candidates:
                candidates.append(value)
    if len(candidates) == 1 and candidates[0] not in excluded:
        return candidates[0]
    return _UNRESOLVED if candidates or excluded else None


def _grounded_counts(field: dict, request: str, candidates: Any) -> Any:
    """Validate model-extracted totals; semantic scope is owned by the model.

    Exact quotes establish provenance, not semantic correctness. Never scan the
    request again for numbers: ordinals and section lengths are not deck totals.
    """
    if not isinstance(candidates, list) or not candidates:
        return []
    matches = []
    for candidate in candidates:
        if not isinstance(candidate, dict):
            return _UNRESOLVED
        value, evidence = candidate.get('value'), candidate.get('evidence')
        if not isinstance(value, str) or not isinstance(evidence, str) or not evidence.strip():
            return _UNRESOLVED
        if evidence not in request:
            return _UNRESOLVED
        value = value.strip()
        if not re.fullmatch(r'[1-9][0-9]*', value) and value not in (field.get('value_aliases') or {}).values():
            return _UNRESOLVED
        matches.append((request.index(evidence), value))
    # Keep validated alternatives in original-request order for the question card.
    return list(dict.fromkeys(value for _, value in sorted(matches, key=lambda item: item[0])))


def resolve_fields(runtime: Any, request: str, supplied: dict | None = None,
                   previous: dict | None = None, extractions: dict | None = None) -> tuple[dict, list[dict]]:
    values: dict[str, Any] = {}
    missing = []
    previous = previous or {}
    for field in fields_for(runtime):
        if (field.get('attachment') or {}).get('material_id') == field['id']:
            # Core checks an actual resource, not the card's upload selection.
            continue
        condition = field.get('applies_when')
        if condition:
            parent = values.get(condition['field'])
            if not _present(parent):
                continue
            if 'equals' in condition and parent != condition['equals']:
                continue
            if 'not_equals' in condition and parent == condition['not_equals']:
                continue
        value = previous.get(field['id'])
        if field.get('resolution') == 'grounded_count':
            if not _present(value):
                counts = _grounded_counts(field, request, (extractions or {}).get(field['id']))
                if counts is _UNRESOLVED:
                    value = _UNRESOLVED
                elif len(counts) == 1:
                    value = counts[0]
                else:
                    value = _UNRESOLVED if counts else None
                    if counts and field.get('choice_policy') != 'fixed':
                        labels = {value: label for label, value in (field.get('value_aliases') or {}).items()}
                        field = {**field, 'choices': [labels.get(count, count) for count in counts]}
                scalar = (supplied or {}).get(field['id'])
                if _present(scalar) and scalar != value:
                    value = _UNRESOLVED
            if value is _UNRESOLVED or not _present(value):
                if field.get('required') or value is _UNRESOLVED:
                    missing.append(field)
            else:
                values[field['id']] = _canonical(field, value)
            continue
        extracted = None if field.get('resolution') == 'model' else _extract(field, request)
        if not _present(value) and extracted is _UNRESOLVED:
            # An explicit conflict cannot be replaced with a model guess or
            # a default, even for an otherwise optional field.
            missing.append(field)
            continue
        if not _present(value) and _present(extracted):
            value = extracted
        attachment = field.get('attachment') or {}
        if (not _present(value) and attachment.get('material_id')
                and _present((supplied or {}).get(attachment['material_id']))):
            # A typed template/source binding is evidence for its associated
            # choice. An arbitrary PPTX attachment alone is not a template.
            value = attachment['when_choice']
        if not _present(value) and field.get('resolution') != 'explicit':
            value = (supplied or {}).get(field['id'])
        if not _present(value):
            value = field.get('default')
        if _present(value):
            values[field['id']] = _canonical(field, value)
        elif field.get('required'):
            missing.append(field)
    return values, missing


def startup_metadata(workflow_ref: str, revision_id: str, request: str, values: dict,
                     previous: dict | None = None) -> dict[str, str]:
    previous = previous or {}
    return {'workflow_ref': workflow_ref, 'revision_id': revision_id,
            'startup_request_id': previous.get('startup_request_id') or str(uuid.uuid4()),
            'original_request': previous.get('original_request') or request,
            'values_json': json.dumps(values, ensure_ascii=False)}


def startup_card(missing: list[dict], metadata: dict[str, str]) -> dict:
    questions = []
    for field in missing:
        question = {key: field[key] for key in (
            'id', 'type', 'reason', 'choices', 'choice_policy', 'allow_other', 'boolean_labels',
            'validation', 'attachment',
        ) if key in field}
        question.update(text=field['question'], required=True)
        if question.get('choice_policy') != 'fixed' and question.get('choices'):
            question['choices'] = question['choices'][:4 if question.get('type') == 'multiple' else 3]
        questions.append(question)
    return question_card_payload(questions, title='补充必要信息',
                                 description='这些信息会影响本次任务的交付内容。', workflow=metadata)
