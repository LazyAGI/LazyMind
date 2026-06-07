from __future__ import annotations

from typing import Any

from .models import ValidationIssue


def validate_params(intent_id: str, params: dict, schema: dict) -> list[ValidationIssue]:
    if not schema:
        return []
    issues: list[ValidationIssue] = []
    if schema.get('type') == 'object' and not isinstance(params, dict):
        return [_issue(intent_id, 'invalid_type', 'params must be object')]
    required = schema.get('required', [])
    for name in required:
        if name not in params:
            issues.append(_issue(intent_id, 'missing_required_param', f'missing required param: {name}'))
    properties = schema.get('properties', {})
    if schema.get('additionalProperties') is False:
        for name in sorted(set(params) - set(properties)):
            issues.append(_issue(intent_id, 'unknown_param', f'unknown param: {name}'))
    for name, value in params.items():
        if name in properties:
            issues.extend(_validate_value(intent_id, name, value, properties[name]))
    return issues


def _validate_value(intent_id: str, name: str, value: Any, schema: dict) -> list[ValidationIssue]:
    issues: list[ValidationIssue] = []
    expected = schema.get('type')
    if expected and not _matches_type(value, expected):
        issues.append(_issue(intent_id, 'invalid_param_type', f'{name} must be {expected}'))
        return issues
    if 'enum' in schema and value not in schema['enum']:
        issues.append(_issue(intent_id, 'invalid_enum', f"{name} must be one of: {', '.join(map(str, schema['enum']))}"))
    if isinstance(value, str) and 'minLength' in schema and len(value) < int(schema['minLength']):
        issues.append(_issue(intent_id, 'min_length', f"{name} length must be >= {schema['minLength']}"))
    return issues


def _matches_type(value: Any, expected: str) -> bool:
    if expected == 'string':
        return isinstance(value, str)
    if expected == 'number':
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    if expected == 'integer':
        return isinstance(value, int) and not isinstance(value, bool)
    if expected == 'boolean':
        return isinstance(value, bool)
    if expected == 'object':
        return isinstance(value, dict)
    if expected == 'array':
        return isinstance(value, list)
    return True


def _issue(intent_id: str, code: str, message: str) -> ValidationIssue:
    return ValidationIssue(code, intent_id, 'clarify', message)
