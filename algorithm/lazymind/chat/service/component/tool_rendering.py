from __future__ import annotations

import json
import re
from html import escape
from typing import Any

from .tool_render_templates import (
    KB_EMPTY_RESULT_MESSAGES,
    TOOL_RENDER_FALLBACKS,
    TOOL_RENDER_PROFILES,
)

_TOOL_PREVIEW_TAG = 'tp'
_TOOL_RESULT_PREVIEW_TAG = 'trp'
_TOOL_CALL_TAG = 'tool_call'
_TOOL_RESULT_TAG = 'tool_result'

_SEARCH_TOOL_RE = re.compile(
    r'^(?P<class_name>[A-Za-z0-9]+Search|WikipediaToolkit)_'
    r'(?P<method>search|get_content|get_contents|meta_search|meta_catalog)$'
)


def _humanize_search_brand(class_name: str) -> str:
    if class_name.endswith('Search'):
        stem = class_name[:-len('Search')]
    elif class_name.endswith('Toolkit'):
        stem = class_name[:-len('Toolkit')]
    else:
        stem = class_name
    stem = re.sub(r'([A-Z]+)([A-Z][a-z])', r'\1 \2', stem)
    return re.sub(r'([a-z0-9])([A-Z])', r'\1 \2', stem)


def _search_tool_match(tool_name: str) -> re.Match | None:
    for candidate in _tool_name_suffixes(tool_name):
        match = _SEARCH_TOOL_RE.fullmatch(candidate)
        if match:
            return match
    return None


def _render_tool_context(tool_name: str) -> tuple[str, dict[str, str]]:
    match = _search_tool_match(tool_name)
    if not match:
        return tool_name, {}
    brand = _humanize_search_brand(match.group('class_name'))
    method = match.group('method')
    return f'search_provider_{method}', {'brand': brand, 'method': method.replace('_', ' ')}


_REPRESENTATIVE_TOOL_ARGUMENTS = {
    name: profile['argument']
    for name, profile in TOOL_RENDER_PROFILES.items()
    if profile.get('argument')
}
_TOOL_CALL_PREVIEW_TEMPLATES = {
    name: profile['call']['en']
    for name, profile in TOOL_RENDER_PROFILES.items()
    if profile.get('call')
}
_ZH_TOOL_CALL_PREVIEW_TEMPLATES = {
    name: profile['call']['zh']
    for name, profile in TOOL_RENDER_PROFILES.items()
    if profile.get('call')
}
_TOOL_RESULT_PREVIEW_TEMPLATES = {
    name: profile['success']['en']
    for name, profile in TOOL_RENDER_PROFILES.items()
    if profile.get('success')
}
_ZH_TOOL_RESULT_PREVIEW_TEMPLATES = {
    name: profile['success']['zh']
    for name, profile in TOOL_RENDER_PROFILES.items()
    if profile.get('success')
}
_TOOL_RESULT_FAILURE_TEMPLATES = {
    name: profile['failure']['en']
    for name, profile in TOOL_RENDER_PROFILES.items()
    if profile.get('failure')
}
_ZH_TOOL_RESULT_FAILURE_TEMPLATES = {
    name: profile['failure']['zh']
    for name, profile in TOOL_RENDER_PROFILES.items()
    if profile.get('failure')
}
_TOOL_RESULT_APPROVAL_TEMPLATES = {
    name: profile['approval']['en']
    for name, profile in TOOL_RENDER_PROFILES.items()
    if profile.get('approval')
}
_ZH_TOOL_RESULT_APPROVAL_TEMPLATES = {
    name: profile['approval']['zh']
    for name, profile in TOOL_RENDER_PROFILES.items()
    if profile.get('approval')
}

_TOOL_CALL_FALLBACK_TEMPLATE = TOOL_RENDER_FALLBACKS['call']['en']
_ZH_TOOL_CALL_FALLBACK_TEMPLATE = TOOL_RENDER_FALLBACKS['call']['zh']
_TOOL_RESULT_FALLBACK_TEMPLATE = TOOL_RENDER_FALLBACKS['success']['en']
_ZH_TOOL_RESULT_FALLBACK_TEMPLATE = TOOL_RENDER_FALLBACKS['success']['zh']
_TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE = TOOL_RENDER_FALLBACKS['failure']['en']
_ZH_TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE = TOOL_RENDER_FALLBACKS['failure']['zh']
_TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE = TOOL_RENDER_FALLBACKS['approval']['en']
_ZH_TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE = TOOL_RENDER_FALLBACKS['approval']['zh']
_KB_EMPTY_RESULT_MESSAGES = KB_EMPTY_RESULT_MESSAGES

_FALLBACK_REPRESENTATIVE_RESULT_KEYS = (
    'result',
    'content',
    'text',
    'reason',
    'message',
    'stdout',
    'stderr',
    'status',
    'path',
)

_FALLBACK_REPRESENTATIVE_ARGUMENT_KEYS = (
    'query',
    'keyword',
    'keywords',
    'url',
    'urls',
    'path',
    'file',
    'filename',
    'rel_path',
    'name',
    'title',
    'topic',
    'pattern',
    'target',
    'node_id',
    'id',
    'src',
    'dst',
    'text',
    'content',
)

_LOW_SIGNAL_ARGUMENT_KEYS = {
    'include_content',
    'include_metadata',
    'include_raw',
    'max_results',
    'limit',
    'top_k',
    'k',
    'page',
    'page_size',
    'offset',
}

_MAX_REPRESENTATIVE_RESULT_LENGTH = 200
_MAX_TOOL_RESULT_PREVIEW_LENGTH = 50
_MAX_TOOL_RESULT_FAILURE_DETAIL_LENGTH = 300
_MAX_SUCCESS_RESULT_NORMALIZATION_DEPTH = 6

_ZH_PREVIEW_RE = re.compile('[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]')


def _tool_name_suffixes(tool_name: str) -> list[str]:
    if not tool_name:
        return []
    parts = tool_name.split('_')
    return ['_'.join(parts[i:]) for i in range(len(parts))]


def _resolve_tool_key(tool_name: str, mapping: dict[str, Any]) -> Any:
    """Look up *tool_name* in *mapping*, falling back to suffix match for
    class-registered methods like ``KBToolkit_kb_search`` and explicitly
    prefixed nested Toolkit methods."""
    if not tool_name or not mapping:
        return None
    for suffix in _tool_name_suffixes(tool_name):
        if suffix in mapping:
            return mapping[suffix]
    return None


def _resolve_tool_key_regex(
    tool_name: str, mapping: dict[str, Any]
) -> tuple[Any, re.Match | None]:
    """Look up *tool_name* in *mapping* using regex keys prefixed with ``regex:``.

    Returns ``(value, match)`` when a pattern matches, otherwise ``(None, None)``.
    Regex keys have lower priority than exact keys and are only tried when
    :func:`_resolve_tool_key` returns nothing.
    """
    for key, value in mapping.items():
        if key.startswith('regex:'):
            m = re.fullmatch(key[len('regex:'):], tool_name)
            if m:
                return value, m
    return None, None


def _tool_name_is(tool_name: str, base_name: str) -> bool:
    """Return True when *tool_name* equals *base_name* or is a prefixed
    variant like ``GroupName_<base_name>``."""
    if tool_name == base_name:
        return True
    return tool_name.endswith('_' + base_name)


def _tool_name_starts(tool_name: str, prefix: str) -> bool:
    """Like ``str.startswith`` but works through group prefixes."""
    if tool_name.startswith(prefix):
        return True
    parts = tool_name.split('_')
    for i in range(1, len(parts)):
        if '_'.join(parts[i:]).startswith(prefix):
            return True
    return False


def _preview_language(value: Any) -> str:
    text = '' if value is None else str(value)
    return 'zh' if _ZH_PREVIEW_RE.search(text) else 'en'


def _language_templates(
    language: str,
    en_templates: dict[str, str],
    zh_templates: dict[str, str],
) -> dict[str, str]:
    return zh_templates if language == 'zh' else en_templates


def _language_fallback(language: str, en_fallback: str, zh_fallback: str) -> str:
    return zh_fallback if language == 'zh' else en_fallback


def _representative_tool_argument(tool_name: str, arguments: Any) -> Any:
    render_name, _ = _render_tool_context(tool_name)
    expression = _resolve_tool_key(render_name, _REPRESENTATIVE_TOOL_ARGUMENTS)
    if not isinstance(arguments, dict):
        return arguments
    if expression:
        value = _representative_expression_value(arguments, expression)
        if _is_meaningful_preview_value(value):
            return value
    return _representative_mapping_value(arguments, _FALLBACK_REPRESENTATIVE_ARGUMENT_KEYS)


def _truncate_representative_result(value: Any) -> str:
    if value is None:
        text = ''
    elif isinstance(value, (dict, list)):
        text = json.dumps(value, ensure_ascii=False, separators=(',', ':'))
    else:
        text = str(value)
    if len(text) <= _MAX_REPRESENTATIVE_RESULT_LENGTH:
        return text
    return f'{text[:_MAX_REPRESENTATIVE_RESULT_LENGTH]}...'


def _is_meaningful_preview_value(value: Any) -> bool:
    if value is None:
        return False
    if isinstance(value, str):
        return bool(value.strip())
    if isinstance(value, (list, tuple, set, dict)):
        return bool(value)
    if isinstance(value, bool):
        return False
    return True


def _representative_mapping_value(mapping: dict[str, Any], preferred_keys: tuple[str, ...]) -> Any:
    for key in preferred_keys:
        value = mapping.get(key)
        if _is_meaningful_preview_value(value):
            return value
    for key, value in mapping.items():
        if key in _LOW_SIGNAL_ARGUMENT_KEYS:
            continue
        if _is_meaningful_preview_value(value):
            return value
    return ''


def _resolve_representative_path(value: Any, path: str) -> Any:
    if not path:
        return value
    current = value
    parts = path.split('.')
    for index, part in enumerate(parts):
        if isinstance(current, list):
            remaining_path = '.'.join(parts[index:])
            return [
                resolved for item in current
                if _is_meaningful_preview_value(
                    resolved := _resolve_representative_path(item, remaining_path)
                )
            ]
        if not isinstance(current, dict):
            return None
        current = current.get(part)
    return current


def _representative_expression_value(arguments: dict[str, Any], expression: str) -> Any:
    def expression_part_value(part: str) -> Any:
        value = _resolve_representative_path(arguments, part)
        if _is_meaningful_preview_value(value) or '.' not in part:
            return value
        head, leaf = part.split('.', 1)
        return (
            _resolve_representative_path(arguments, leaf)
            or _resolve_representative_path(arguments, head)
        )

    for separator in (' <-> ', '/'):
        if separator not in expression:
            continue
        parts = [part.strip() for part in expression.split(separator)]
        values = [expression_part_value(part) for part in parts]
        if any(isinstance(value, list) for value in values):
            max_count = max((len(value) for value in values if isinstance(value, list)), default=0)
            previews = []
            for index in range(min(max_count, 2)):
                item_parts = [
                    _tool_preview_value(value[index] if isinstance(value, list) and index < len(value) else value)
                    for value in values
                ]
                item_parts = [part for part in item_parts if part]
                if item_parts:
                    previews.append(separator.join(item_parts))
            if previews:
                text = ', '.join(previews)
                if max_count > 2:
                    return f'{text} and {max_count - 2} more'
                return text
        item_parts = [_tool_preview_value(value) for value in values]
        item_parts = [part for part in item_parts if part]
        if item_parts:
            return separator.join(item_parts)
    return _resolve_representative_path(arguments, expression)


def _friendly_preview_text(value: Any) -> str:
    if value is None:
        return ''
    if isinstance(value, str):
        return value
    if isinstance(value, bool):
        return ''
    if isinstance(value, dict):
        representative = _representative_mapping_value(
            value,
            _FALLBACK_REPRESENTATIVE_ARGUMENT_KEYS + _FALLBACK_REPRESENTATIVE_RESULT_KEYS,
        )
        if representative is value or not _is_meaningful_preview_value(representative):
            return 'the selected options'
        return _friendly_preview_text(representative)
    if isinstance(value, (list, tuple, set)):
        items = list(value)
        if not items:
            return ''
        friendly_items = [
            _friendly_preview_text(item)
            for item in items[:2]
            if _is_meaningful_preview_value(item)
        ]
        friendly_items = [item for item in friendly_items if item]
        if friendly_items:
            preview = ', '.join(friendly_items)
            if len(items) > 2:
                return f'{preview} and {len(items) - 2} more'
            return preview
        return f'{len(items)} items'
    return str(value)


def _tool_preview_value(value: Any) -> str:
    text = _truncate_representative_result(_friendly_preview_text(value))
    return text.replace('\n', ' ').strip()


def _tool_call_preview_value(tool_name: str, arguments: Any, language: str = 'en') -> str:
    preview = _tool_preview_value(_representative_tool_argument(tool_name, arguments))
    return preview


def _truncate_tool_result_preview(value: Any, max_length: int = _MAX_TOOL_RESULT_PREVIEW_LENGTH) -> str:
    text = _tool_preview_value(value)
    if len(text) <= max_length:
        return text
    return f'{text[:max_length]}...'


def _tool_result_status(result: Any) -> str:
    if not isinstance(result, dict):
        return 'ok'
    if result.get('ok') is False and result.get('needs_approval') is True:
        return 'needs_approval'
    if result.get('ok') is False:
        return 'failed'
    payload = result.get('value') if result.get('ok') is True and isinstance(result.get('value'), dict) else result
    if isinstance(payload, dict):
        if payload is not result:
            nested = _tool_result_status(payload)
            if nested != 'ok':
                return nested
        status = str(payload.get('status') or '').strip().lower()
        if status in {'failed', 'error', 'missing'}:
            return 'failed'
        if payload.get('missing_env') or payload.get('error_type') == 'MissingCredential':
            return 'failed'
    return 'ok'


def _tool_result_failure_detail(result: Any) -> str:
    if isinstance(result, dict) and result.get('ok') is True and isinstance(result.get('value'), dict):
        return _tool_result_failure_detail(result['value'])
    if isinstance(result, dict):
        error = result.get('error')
        if isinstance(error, dict):
            for key in ('reason', 'detail', 'type'):
                value = error.get(key)
                if value:
                    return _truncate_tool_result_preview(value, _MAX_TOOL_RESULT_FAILURE_DETAIL_LENGTH)
        missing_env = result.get('missing_env')
        if isinstance(missing_env, list):
            missing_env_text = ', '.join(str(item).strip() for item in missing_env if str(item).strip())
        else:
            missing_env_text = str(missing_env or '').strip()
        setup_commands = result.get('setup_commands')
        if isinstance(setup_commands, list):
            setup_commands_text = '; '.join(str(item).strip() for item in setup_commands if str(item).strip())
        else:
            setup_commands_text = str(setup_commands or '').strip()
        credential_parts = []
        if missing_env_text:
            credential_parts.append(f'missing_env: {missing_env_text}')
        api_key_url = str(result.get('api_key_url') or '').strip()
        if api_key_url:
            credential_parts.append(api_key_url)
        if setup_commands_text:
            credential_parts.append(f'setup_commands: {setup_commands_text}')
        if not credential_parts:
            credential_parts = [
                str(result.get(key) or '').strip()
                for key in ('hint', 'api_key_url')
                if str(result.get(key) or '').strip()
            ]
        if credential_parts:
            return _truncate_tool_result_preview(' '.join(credential_parts), _MAX_TOOL_RESULT_FAILURE_DETAIL_LENGTH)
        if result.get('ok') is False and result.get('value'):
            return _truncate_tool_result_preview(result['value'], _MAX_TOOL_RESULT_FAILURE_DETAIL_LENGTH)
        for key in ('reason', 'hint', 'stderr', 'error', 'message', 'stdout', 'path', 'status'):
            value = result.get(key)
            if value:
                return _truncate_tool_result_preview(value, _MAX_TOOL_RESULT_FAILURE_DETAIL_LENGTH)
    return _truncate_tool_result_preview(result, _MAX_TOOL_RESULT_FAILURE_DETAIL_LENGTH)


def _ensure_trailing_newline(text: str) -> str:
    return text if text.endswith('\n') else f'{text}\n'


class _SafeFormatContext(dict):
    def __missing__(self, key: str) -> '_MissingTemplateValue':
        return _MissingTemplateValue(key)


class _MissingTemplateValue:
    """Preserve unresolved dotted placeholders without raising AttributeError."""

    def __init__(self, path: str) -> None:
        self._path = path

    def __getattr__(self, key: str) -> '_MissingTemplateValue':
        return _MissingTemplateValue(f'{self._path}.{key}')

    def __format__(self, _spec: str) -> str:
        return f'{{{self._path}}}'

    def __str__(self) -> str:
        return f'{{{self._path}}}'


class _TemplateResult:
    """Attribute-access wrapper used by dotted result template paths."""

    def __init__(self, value: dict[str, Any], path: str = 'result') -> None:
        self._value = value
        self._path = path

    def __getattr__(self, key: str) -> Any:
        if key not in self._value:
            return f'{{{self._path}.{key}}}'
        value = self._value[key]
        if isinstance(value, dict):
            return _TemplateResult(value, f'{self._path}.{key}')
        text = _truncate_representative_result(
            _friendly_preview_text(value)
        ).replace('\n', ' ').strip()
        return f'**{text}**' if text else f'{{{self._path}.{key}}}'


def _render_preview_template(
    tool_name: str,
    value: str,
    template_map: dict[str, str],
    fallback_template: str,
    result: Any = None,
) -> str:
    render_name, render_context = _render_tool_context(tool_name)
    template = template_map.get(render_name)
    match_group = None
    if template is None:
        template, m = _resolve_tool_key_regex(render_name, template_map)
        if m:
            match_group = m.group(1) if m.lastindex else m.group(0)
    if template is None:
        template = _resolve_tool_key(render_name, template_map)
    template = template or fallback_template
    preview_value = value or 'the current item'
    context = {
        key: f'**{item}**'
        for key, item in render_context.items()
    }
    business_value = _normalized_success_business_value(result)
    result_mapping = _tool_result_mapping(result)
    if result_mapping is None and result is not None:
        result_mapping = {
            'outcome': _tool_result_status(result),
            'reason': _tool_result_failure_detail(result),
        }
    if result_mapping is not None:
        context['result'] = _TemplateResult(result_mapping)
    output = _truncate_tool_result_preview(business_value)
    if output:
        context['output'] = f'**{output}**'
    count = _tool_result_count(business_value)
    if count is not None:
        context['count'] = f'**{count}**'
    context['value'] = f'**{preview_value}**'
    context['tool_name'] = f'**{tool_name}**'
    context['match'] = f'**{match_group or render_name}**'
    return _ensure_trailing_newline(template.format_map(_SafeFormatContext(context)))


def _tool_call_preview(tool_name: str, preview_value: str, language: str = 'en') -> str:
    return _render_preview_template(
        tool_name,
        preview_value,
        _language_templates(language, _TOOL_CALL_PREVIEW_TEMPLATES, _ZH_TOOL_CALL_PREVIEW_TEMPLATES),
        _language_fallback(language, _TOOL_CALL_FALLBACK_TEMPLATE, _ZH_TOOL_CALL_FALLBACK_TEMPLATE),
    )


def _normalized_success_business_value(value: Any, depth: int = 0) -> Any:
    """Normalize JSON-serialized successful business values for rendering."""
    if depth >= _MAX_SUCCESS_RESULT_NORMALIZATION_DEPTH:
        return value
    if isinstance(value, dict) and value.get('ok') is False:
        return value
    if isinstance(value, dict) and value.get('ok') is True and 'value' in value:
        return _normalized_success_business_value(value.get('value'), depth + 1)
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except (TypeError, ValueError):
            return value
        if isinstance(parsed, (dict, list)):
            return _normalized_success_business_value(parsed, depth + 1)
        return value
    if isinstance(value, dict) and isinstance(value.get('result'), (dict, str)):
        nested = _normalized_success_business_value(value.get('result'), depth + 1)
        if isinstance(nested, (dict, list)):
            return nested
    return value


def _tool_result_mapping(value: Any) -> dict[str, Any] | None:
    """Build the stable mapping consumed by dotted result templates."""
    if isinstance(value, dict) and value.get('ok') is False:
        return {
            **value,
            'outcome': 'failed',
            'reason': _tool_result_failure_detail(value) or value.get('value') or 'Tool call failed',
        }
    payload = _normalized_success_business_value(value)
    return payload if isinstance(payload, dict) else None


def _tool_result_count(value: Any) -> int | None:
    payload = _normalized_success_business_value(value)
    if not isinstance(payload, list):
        return None
    return sum(
        1 for item in payload
        if not (
            isinstance(item, dict)
            and str(item.get('title') or '').strip().lower() == 'summary'
            and not str(item.get('url') or '').strip()
        )
    )


def _tool_result_preview(tool_name: str, result: Any, value: str = '', language: str = 'en') -> str:
    status = _tool_result_status(result)
    business_value = (
        _normalized_success_business_value(result)
        if status == 'ok'
        else result
    )
    if status == 'needs_approval':
        return _render_preview_template(
            tool_name,
            value or _tool_result_failure_detail(result),
            _language_templates(language, _TOOL_RESULT_APPROVAL_TEMPLATES, _ZH_TOOL_RESULT_APPROVAL_TEMPLATES),
            _language_fallback(
                language,
                _TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE,
                _ZH_TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE,
            ),
            result,
        )
    if status == 'failed':
        return _render_preview_template(
            tool_name,
            _tool_result_failure_detail(result) or value,
            _language_templates(language, _TOOL_RESULT_FAILURE_TEMPLATES, _ZH_TOOL_RESULT_FAILURE_TEMPLATES),
            _language_fallback(
                language,
                _TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE,
                _ZH_TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE,
            ),
            result,
        )
    if (
        isinstance(business_value, dict)
        and business_value.get('total') == 0
        and _tool_name_starts(tool_name, 'kb_')
    ):
        msg = _resolve_tool_key(tool_name, _KB_EMPTY_RESULT_MESSAGES)
        if msg:
            return _ensure_trailing_newline(msg.get(language) or msg.get('en', ''))
    return _render_preview_template(
        tool_name,
        value,
        _language_templates(language, _TOOL_RESULT_PREVIEW_TEMPLATES, _ZH_TOOL_RESULT_PREVIEW_TEMPLATES),
        _language_fallback(language, _TOOL_RESULT_FALLBACK_TEMPLATE, _ZH_TOOL_RESULT_FALLBACK_TEMPLATE),
        result,
    )


def _tool_call_frame_text(tool_call: dict[str, Any], language: str = 'en') -> tuple[str, str]:
    function = tool_call.get('function') or {}
    tool_call_id = str(tool_call.get('id') or '')
    tool_name = str(function.get('name', ''))
    raw_args = function.get('arguments', {})
    if isinstance(raw_args, str):
        try:
            arguments = json.loads(raw_args)
        except json.JSONDecodeError:
            arguments = raw_args
    else:
        arguments = raw_args
    preview_value = _tool_call_preview_value(tool_name, arguments, language)
    payload = {
        'id': tool_call_id,
        'name': tool_name,
        'arguments': arguments if isinstance(arguments, dict) else {},
    }
    preview = _tool_call_preview(tool_name, preview_value, language)
    text = (
        f'<{_TOOL_PREVIEW_TAG} id="{escape(tool_call_id, quote=True)}">{preview}</{_TOOL_PREVIEW_TAG}>'
        f'<{_TOOL_CALL_TAG}>{json.dumps(payload, ensure_ascii=False, separators=(",", ":"))}</{_TOOL_CALL_TAG}>'
    )
    return text, preview_value if tool_call_id else ''


def _tool_result_frame_text(tool_result: dict[str, Any], language: str = 'en', preview_value: str = '') -> str:
    tool_call_id = str(tool_result.get('id') or '')
    tool_name = str(tool_result.get('name', ''))
    result = tool_result.get('result')
    payload = {
        'id': tool_call_id,
        'name': tool_name,
        'result': result,
    }
    if _tool_name_is(tool_name, 'ask_user'):
        encoded = json.dumps(payload, ensure_ascii=False, separators=(',', ':'))
        return f'<{_TOOL_RESULT_TAG}>{encoded}</{_TOOL_RESULT_TAG}>'
    preview = _tool_result_preview(tool_name, result, preview_value, language)
    return (
        f'<{_TOOL_RESULT_PREVIEW_TAG} id="{escape(tool_call_id, quote=True)}">{preview}</{_TOOL_RESULT_PREVIEW_TAG}>'
        f'<{_TOOL_RESULT_TAG}>{json.dumps(payload, ensure_ascii=False, separators=(",", ":"))}</{_TOOL_RESULT_TAG}>'
    )
