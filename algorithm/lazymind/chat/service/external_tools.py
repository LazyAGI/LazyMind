"""Registry-driven external tools, isolated from interactive Chat execution state."""

import inspect
import logging
import base64
import binascii
import io
import tempfile
from contextlib import contextmanager
from pathlib import Path
from typing import Any

import lazyllm
from lazyllm.common.globals import init_session
from lazyllm.tools import ToolManager

from lazymind.chat.engine.tool_auth import inject_tool_config
from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, _instance_is_active, tool_is_active
from lazymind.chat.service.external_tool_errors import from_exception, from_result
from lazymind.model_config import inject_model_config
from lazymind.vision_model import VisionModelUnavailable, select_vision_model_role


LOG = logging.getLogger(__name__)


# These groups require context/approval that a stateless external call does not own.
# They remain visible, but cannot execute until that context has an explicit adapter.
_CONTEXT_REQUIREMENTS = {
    'github_prs': 'conversation-scoped PR source cache',
    'kb': 'knowledge base access context', 'temp_kb': 'conversation attachments',
    'data_sources': 'user data-source context', 'external_db': 'user database context',
    'writer_create': 'artifact workspace', 'writer_revision': 'artifact workspace',
    'video_to_gif': 'authorized input files', 'vocab_learn': 'user vocabulary context',
    'memory': 'user memory context', 'skill_editor': 'skill workspace',
    'local_fs': 'authorized filesystem workspace', 'cloud_files': 'cloud account context',
    'mail': 'mail account and approval context', 'schedule': 'user scheduling context',
}

_CONDITIONING_IMAGE_FIELDS = ('urls', 'reference_urls', 'first_frame_url', 'last_frame_url')
_EXTERNAL_MEDIA_FIELDS = {
    'image_editor': _CONDITIONING_IMAGE_FIELDS,
    'video_generator': _CONDITIONING_IMAGE_FIELDS,
    'multimodal': ('url',),
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
    role = getattr(config, 'model_role', None)
    configured_models = lazyllm.globals['config'].get('dynamic_model_configs') or {}
    if config.name == 'multimodal':
        try:
            role = select_vision_model_role()
        except VisionModelUnavailable:
            role = None
        # A local runtime default is not an authorized external model selection.
        if not configured_models.get(role):
            return None, [], 'Configure a verified VLM or vision-enabled main model in LazyMind Settings and retry'
    elif role and not configured_models.get(role):
        return None, [], 'Required model is not configured in LazyMind; configure this capability in Settings and retry'
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
    except Exception as exc:
        LOG.warning('External tool failed tool=%s stage=schema error_type=%s', config.name, type(exc).__name__)
        return None, [], 'Tool schema is not available for external execution'


def _schema(methods):
    if not methods:
        return {'type': 'object', 'properties': {}}
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
        if execute:
            return _execute(payload)
        items = []
        requested = str(payload.get('tool_name') or '')
        for config in DEFAULT_TOOLS:
            if requested and config.name != requested:
                continue
            _, methods, reason = _resolve(config)
            schema = _schema(methods)
            for key in _EXTERNAL_MEDIA_FIELDS.get(config.name, ()):
                prop = schema.get('properties', {}).get(key)
                if prop is not None:
                    prop['description'] = (
                        'Use resource:<id> from workflow.input.import for each image. '
                        'Local paths, remote URLs and credentials are not accepted.'
                    )
            items.append({
                'name': config.name, 'description': config.description,
                'input_schema': schema, 'available': not reason, 'reason': reason,
                'model_role': getattr(config, 'model_role', None),
            })
        return {'items': items}
    except Exception as exc:
        name = next((item.name for item in DEFAULT_TOOLS if item.name == payload.get('tool_name')), 'unknown')
        LOG.error('External tool request failed tool=%s execute=%s error_type=%s',
                  name, execute, type(exc).__name__)
        raise
    finally:
        lazyllm.globals.clear()


def _execute(payload):
    name = str(payload.get('tool_name') or '')
    config = next((item for item in DEFAULT_TOOLS if item.name == name), None)
    if config is None:
        raise ValueError('Tool is unavailable or disabled')
    manager, methods, reason = _resolve(config)
    if reason:
        LOG.warning('External tool unavailable tool=%s stage=availability', config.name)
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
    failures = []

    @contextmanager
    def capture_failure(_invocation):
        try:
            yield
        except Exception as exc:
            # Capture the structured provider response before ToolManager reduces
            # the exception to a text-only tool result. Keep its execution policy.
            failures.append(from_exception(exc, payload))
            raise

    with _media_arguments(name, arguments) as resolved:
        prepared = manager.prepare_tool_calls(
            [{'type': 'function', 'function': {'name': method, 'arguments': resolved}}],
            allowed_tool_names={method},
        )
        result = manager.execute_prepared(prepared, execution_context=capture_failure).results[0]
    if failures:
        raise failures[0]
    if isinstance(result, dict) and (result.get('ok') is False or result.get('isError') is True):
        LOG.error('External tool failed tool=%s stage=execution', config.name)
        raise from_result(result, payload)
    if isinstance(result, dict) and result.get('ok') is True and 'value' in result:
        result = result['value']
    if isinstance(result, dict) and (result.get('ok') is False or result.get('isError') is True):
        LOG.error('External tool failed tool=%s stage=execution', config.name)
        raise from_result(result, payload)
    if name == 'multimodal':
        description = result.get('description') if isinstance(result, dict) else None
        if not isinstance(description, str) or not description.strip():
            raise RuntimeError('The vision model did not return an image description')
        result = {'description': description}
    return {'tool_name': name, 'result': _public_result(result)}


@contextmanager
def _media_arguments(name, arguments):
    """Materialize only Core-authorized bytes for this call, never caller paths."""
    fields = _EXTERNAL_MEDIA_FIELDS.get(name)
    if not fields:
        yield arguments
        return
    from PIL import Image

    extensions = {'image/png': '.png', 'image/jpeg': '.jpg', 'image/gif': '.gif', 'image/webp': '.webp'}
    total = 0
    with tempfile.TemporaryDirectory(prefix='lazymind-external-media-') as directory:
        cache = {}

        def materialize(value):
            nonlocal total
            if not isinstance(value, str):
                raise ValueError('Media input must contain authorized image bytes')
            if value in cache:
                return cache[value]
            header, separator, encoded = value.partition(',')
            mime = header.removeprefix('data:').removesuffix(';base64')
            if not separator or header != f'data:{mime};base64' or mime not in extensions:
                raise ValueError('Media input must contain authorized image bytes')
            if len(encoded) > ((20 << 20) - total) * 4 // 3 + 4:
                raise ValueError('Media inputs exceed 20 MiB')
            try:
                content = base64.b64decode(encoded, validate=True)
                if not content or len(content) > (20 << 20) - total:
                    raise ValueError('Media inputs exceed 20 MiB')
                with Image.open(io.BytesIO(content)) as image:
                    image.verify()
            except (binascii.Error, OSError) as exc:
                raise ValueError('Invalid media input image') from exc
            total += len(content)
            path = Path(directory) / f'{len(cache)}{extensions[mime]}'
            path.write_bytes(content)
            cache[value] = str(path)
            return str(path)

        result = dict(arguments)
        for key in fields:
            value = result.get(key)
            if value is None or value == '':
                continue
            result[key] = [materialize(item) for item in value] if isinstance(value, list) else materialize(value)
        yield result


def _public_result(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: _public_result(item) for key, item in value.items() if key != 'local_path'}
    if isinstance(value, list):
        return [_public_result(item) for item in value]
    return value
