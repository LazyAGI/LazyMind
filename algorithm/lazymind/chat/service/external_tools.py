"""Registry-driven external tools, isolated from interactive Chat execution state."""

import inspect
from typing import Any

import lazyllm
from lazyllm.common.globals import init_session
from lazyllm.tools import ToolManager

from lazymind.chat.engine.tool_auth import inject_tool_config
from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, _instance_is_active, tool_is_active
from lazymind.model_config import inject_model_config


# These groups require context/approval that a stateless external call does not own.
# They remain visible, but cannot execute until that context has an explicit adapter.
_CONTEXT_REQUIREMENTS = {
    'kb': 'knowledge base access context', 'temp_kb': 'conversation attachments',
    'data_sources': 'user data-source context', 'external_db': 'user database context',
    'writer_create': 'artifact workspace', 'writer_revision': 'artifact workspace',
    'image_editor': 'authorized input files', 'video_generator': 'authorized input files',
    'multimodal': 'authorized input files',
    'video_to_gif': 'authorized input files', 'vocab_learn': 'user vocabulary context',
    'memory': 'user memory context', 'skill_editor': 'skill workspace',
    'local_fs': 'authorized filesystem workspace', 'cloud_files': 'cloud account context',
    'mail': 'mail account and approval context', 'schedule': 'user scheduling context',
}


def _registered_callables(target, description):
    """Adapt public Toolkit methods without requiring each provider to duplicate docs."""
    if isinstance(target, dict):
        children = [tool for tool in target.get('tools', []) if _instance_is_active(tool)]
        if target.get('pick_first_valid'):
            children = children[:1]
        return [method for child in children for method in _registered_callables(child, description)]
    if not hasattr(target, '__public_apis__'):
        return [target]
    methods = []
    for name in target.__public_apis__:
        bound = getattr(target, name)

        def method(_bound=bound, **kwargs):
            return _bound(**kwargs)

        method.__name__ = f'{target.__class__.__name__}_{name.strip("_")}'
        method.__doc__ = inspect.getdoc(bound) or description
        method.__signature__ = inspect.signature(bound)
        method.__annotations__ = getattr(bound, '__annotations__', {})
        methods.append(method)
    return methods


def _resolve(config):
    reason = _CONTEXT_REQUIREMENTS.get(config.name)
    if reason:
        return None, [], f'Requires {reason}; not available for stateless external calls'
    if not tool_is_active(config):
        return None, [], 'Required connection/model is not configured or verified'
    try:
        manager = ToolManager(_registered_callables(config.tool, config.description))
        # Resolve actual executable leaves, including pick-first-valid provider groups.
        methods = [
            {'name': name, 'parameters': tool.params_schema.model_json_schema()}
            for name, tool in manager.tools_info.items()
        ]
        if not methods:
            return None, [], 'No executable methods are available'
        return manager, methods, ''
    except Exception:
        return None, [], 'Tool schema is not available for external execution'


def _schema(methods):
    if len(methods) == 1:
        return methods[0]['parameters']
    return {
        'type': 'object', 'additionalProperties': False,
        'properties': {
            'method': {'type': 'string', 'enum': [item['name'] for item in methods]},
            'arguments': {'type': 'object'},
        },
        'required': ['method', 'arguments'],
        'oneOf': [
            {'properties': {'method': {'const': item['name']}, 'arguments': item['parameters']}}
            for item in methods
        ],
    }


def run_external_tools(payload: dict, *, execute: bool = False) -> dict:
    """Use a fresh request session so one user's injected keys cannot reach another."""
    init_session()
    try:
        inject_model_config(payload.get('llm_config') or {})
        inject_tool_config(payload.get('tool_config') or {})
        disabled = set(payload.get('disabled_tools') or [])
        if execute:
            return _execute(payload, disabled)
        items = []
        for config in DEFAULT_TOOLS:
            _, methods, reason = _resolve(config)
            if config.name in disabled:
                reason = 'Tool is disabled in LazyMind settings'
            items.append({
                'name': config.name, 'description': config.description,
                'input_schema': _schema(methods), 'available': not reason, 'reason': reason,
            })
        return {'items': items}
    finally:
        lazyllm.globals.clear()


def _execute(payload, disabled):
    name = str(payload.get('tool_name') or '')
    config = next((item for item in DEFAULT_TOOLS if item.name == name), None)
    if config is None or name in disabled:
        raise ValueError('Tool is unavailable or disabled')
    manager, methods, reason = _resolve(config)
    if reason:
        raise ValueError(reason)
    arguments = payload.get('arguments', {})
    if not isinstance(arguments, dict):
        raise ValueError('Arguments must be an object')
    if len(methods) == 1:
        method = methods[0]['name']
    else:
        method, arguments = arguments.get('method'), arguments.get('arguments')
    if method not in manager.tools_info or not isinstance(arguments, dict):
        raise ValueError('Unknown tool method or invalid arguments')
    tool = manager.tools_info[method]
    if not tool.validate_parameters(arguments):
        raise ValueError('Invalid tool arguments')
    result = manager.execute_with_records(
        [{'type': 'function', 'function': {'name': method, 'arguments': arguments}}],
        allowed_tool_names={method},
    ).results[0]
    if isinstance(result, dict) and (result.get('ok') is False or result.get('isError') is True):
        raise RuntimeError('Tool execution failed; check its connection and arguments')
    if isinstance(result, dict) and result.get('ok') is True and 'value' in result:
        result = result['value']
    if isinstance(result, dict) and (result.get('ok') is False or result.get('isError') is True):
        raise RuntimeError('Tool execution failed; check its connection and arguments')
    return {'tool_name': name, 'result': _public_result(result)}


def _public_result(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: _public_result(item) for key, item in value.items() if key != 'local_path'}
    if isinstance(value, list):
        return [_public_result(item) for item in value]
    return value
