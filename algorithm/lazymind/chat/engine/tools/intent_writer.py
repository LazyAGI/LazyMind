from __future__ import annotations

import json
from typing import Any, Dict, List, Literal, Optional

from lazyllm.tools.agent.base import _write_agent_data


INTENT_FIELDS = {
    'goal', 'deliverable', 'execution_mode', 'constraints', 'corrections',
    'emphasized_points', 'superseded',
}
SCALAR_FIELDS = {'goal', 'deliverable', 'execution_mode'}
LIST_FIELDS = INTENT_FIELDS - SCALAR_FIELDS
VALID_OPERATIONS = {'set', 'add', 'remove', 'supersede'}


def normalize_intent_document(value: Any) -> Dict[str, Any]:
    if not isinstance(value, dict):
        return {}
    if isinstance(value.get('text'), str) and value['text'].strip():
        return {
            'version': 2,
            'revision': int(value.get('revision') or 0),
            'constraints': [value['text'].strip()],
        }
    result: Dict[str, Any] = {
        'version': 2,
        'revision': int(value.get('revision') or 0),
    }
    for field in SCALAR_FIELDS:
        text = str(value.get(field) or '').strip()
        if text:
            result[field] = text
    for field in LIST_FIELDS:
        items = value.get(field) or []
        if isinstance(items, list):
            clean = [str(item).strip() for item in items if str(item).strip()]
            if clean:
                result[field] = list(dict.fromkeys(clean))
    return result


def render_intent_section(title: str, value: Any) -> str:
    document = normalize_intent_document(value)
    visible = {k: v for k, v in document.items() if k not in {'version', 'revision'} and v}
    if not visible:
        return ''
    return (
        f'## {title}\n'
        'This is an inherited baseline. Preserve every item the user does not explicitly change.\n'
        + json.dumps(visible, ensure_ascii=False, indent=2)
    )


def _description(plugin_enabled: bool) -> str:
    scopes = (
        '- conversation: persists for later turns in this conversation.\n'
        if not plugin_enabled else
        '- conversation: persists after the current plugin run ends.\n'
        '- plugin_session: applies only to the active plugin run.\n'
        '- plugin_step: applies only to one canonical step_id from the authoritative plugin context.\n'
    )
    return f'''Update durable user intent by applying a minimal patch.

Call this before the final answer when the user changes, corrects, rejects, pauses,
resumes, narrows, or persistently emphasizes a goal, deliverable, or constraint.
The stored intent is a baseline: preserve everything the user did not explicitly change.
Do not record assistant inferences, tool results, task progress, or one-off wording requests.

Available scopes for this turn:
{scopes}
Do not choose a plugin scope merely because a plugin is active. Use conversation when
the requirement must survive after the plugin ends. Plugin step discovery and lifecycle
belong to the plugin framework; do not invent a step_id.

Args:
    scope (str): One of the available scopes listed above.
    operations (List[Dict[str, str]]): Atomic patch operations. Each item must contain
        op (set/add/remove/supersede), field, value, and a short evidence excerpt copied
        from the current user request. Use set only for goal, deliverable, or
        execution_mode. Use add/remove/supersede for list fields.
    step_id (str, optional): Required only for plugin_step; use a canonical id already
        present in the authoritative plugin context.

Returns:
    A concise confirmation of the accepted intent patch.
'''


def build_intentwrite_tool(
    *, conversation_id: str, current_query: str, current_intent: Any = None,
) -> Any:
    config: Dict[str, Any] = {
        'conversation_id': conversation_id,
        'current_query': current_query,
        'current_intent': normalize_intent_document(current_intent),
        'plugin_session_id': '',
        'plugin_id': '',
        'valid_step_ids': set(),
    }

    def intentwrite(
        scope: str,
        operations: List[Dict[str, str]],
        step_id: Optional[str] = None,
    ) -> str:
        evidence_source = config['current_query']
        if not operations:
            raise ValueError('operations must not be empty.')
        clean: List[Dict[str, str]] = []
        for raw in operations:
            if not isinstance(raw, dict):
                raise ValueError('each operation must be an object.')
            op = str(raw.get('op') or '').strip()
            field = str(raw.get('field') or '').strip()
            value = str(raw.get('value') or '').strip()
            evidence = str(raw.get('evidence') or '').strip()
            if op not in VALID_OPERATIONS or field not in INTENT_FIELDS or not value:
                raise ValueError(f'invalid intent operation: {raw!r}.')
            if op == 'set' and field not in SCALAR_FIELDS:
                raise ValueError(f'set is not valid for list field {field!r}.')
            if op != 'set' and field not in LIST_FIELDS:
                raise ValueError(f'{op} is not valid for scalar field {field!r}.')
            if not evidence or evidence not in evidence_source:
                raise ValueError('operation evidence must be copied from the current user request.')
            clean.append({'op': op, 'field': field, 'value': value, 'evidence': evidence})

        allowed = {'conversation'}
        if config['plugin_session_id']:
            allowed.update({'plugin_session', 'plugin_step'})
        if scope not in allowed:
            raise ValueError(f'unknown scope {scope!r}; available scopes: {sorted(allowed)}.')
        if scope == 'plugin_step':
            if not step_id:
                raise ValueError('step_id is required for plugin_step.')
            if step_id not in config['valid_step_ids']:
                raise ValueError(f'unknown step_id {step_id!r} for the active plugin.')

        _write_agent_data('intent_updated', **{
            'conversation_id': config['conversation_id'],
            'session_id': config['plugin_session_id'] if scope != 'conversation' else '',
            'scope': scope,
            'operations': clean,
            'step_id': step_id or '',
        })
        return f'Intent updated for {scope}.'

    intentwrite.__doc__ = _description(False)
    intentwrite.__annotations__ = {
        'scope': Literal['conversation'],
        'operations': List[Dict[str, str]],
        'step_id': Optional[str],
        'return': str,
    }
    intentwrite._intentwriter_config = config  # type: ignore[attr-defined]
    return intentwrite


def enable_plugin_intent_scopes(tool: Any, *, session_id: str, plugin_id: str,
                                valid_step_ids: List[str]) -> Any:
    config = getattr(tool, '_intentwriter_config', None)
    if not isinstance(config, dict) or not session_id or not plugin_id:
        return tool
    config.update({
        'plugin_session_id': session_id,
        'plugin_id': plugin_id,
        'valid_step_ids': set(valid_step_ids),
    })
    tool.__doc__ = _description(True)
    tool.__annotations__ = {
        'scope': Literal['conversation', 'plugin_session', 'plugin_step'],
        'operations': List[Dict[str, str]],
        'step_id': Optional[str],
        'return': str,
    }
    return tool
