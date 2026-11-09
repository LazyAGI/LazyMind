"""Pinned-package selection of audited external Workflow tool adapters.

Package Python is never executed here. Declaring a tool does not authorize code
execution: a matching server adapter and an exact supported binding are required.
"""
from __future__ import annotations

import base64
import hashlib
import inspect
from pathlib import PurePosixPath
from typing import Any, get_type_hints

from pydantic import BaseModel, ConfigDict, Field, ValidationError, create_model
import yaml

from . import external_ppt_tools
from .external_ppt_tools import ExternalToolError


PPT_BINDING = '''"""Audited external PPT primitives; this binding is not an agent executor."""
from lazymind.chat.workflow.external_ppt_tools import (
    ppt_export_html,
    ppt_inspect_pptx,
    ppt_render_html,
)
'''
_REGISTRY = {
    name: ('scripts/external_tools.py', PPT_BINDING, getattr(external_ppt_tools, name))
    for name in ('ppt_export_html', 'ppt_inspect_pptx', 'ppt_render_html')
}
MAX_PACKAGE_BYTES = 64 * 1024 * 1024


class ExternalToolRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')

    workflow_id: str = Field(min_length=1)
    revision_id: str = Field(min_length=1)
    tree_hash: str = Field(pattern=r'^[0-9a-f]{64}$')
    step_id: str = Field(min_length=1)
    package: dict[str, Any]


class ExternalToolCallRequest(ExternalToolRequest):
    tool_name: str = Field(min_length=1)
    arguments: dict[str, Any] = Field(default_factory=dict)


def package_tree_hash(files: dict[str, bytes]) -> str:
    digest = hashlib.sha256()
    for name in sorted(files):
        digest.update(name.encode() + b'\0' + hashlib.sha256(files[name]).hexdigest().encode() + b'\n')
    return digest.hexdigest()


def _decode_package(request: ExternalToolRequest) -> dict[str, bytes]:
    encoded = request.package.get('files')
    if not isinstance(encoded, dict) or not encoded or len(encoded) > 10000:
        raise ExternalToolError('INVALID_PACKAGE', 'A complete pinned package is required.')
    if request.package.get('tree_hash', request.tree_hash) != request.tree_hash:
        raise ExternalToolError('INVALID_PACKAGE', 'Package revision identity does not match.')
    files, size = {}, 0
    for name, content in encoded.items():
        if (not isinstance(name, str) or not name or '\\' in name or '\0' in name
                or PurePosixPath(name).is_absolute() or '..' in PurePosixPath(name).parts
                or str(PurePosixPath(name)) != name):
            raise ExternalToolError('INVALID_PACKAGE', 'Unsafe package path.')
        if content is not None and (not isinstance(content, str) or len(content) > MAX_PACKAGE_BYTES * 4 // 3 + 4):
            raise ExternalToolError('INVALID_PACKAGE', 'Package file exceeds the transfer limit.')
        try:
            raw = b'' if content is None else base64.b64decode(content, validate=True)
        except ValueError as exc:
            raise ExternalToolError('INVALID_PACKAGE', 'Invalid package file encoding.') from exc
        size += len(raw)
        if size > MAX_PACKAGE_BYTES:
            raise ExternalToolError('INVALID_PACKAGE', 'Package exceeds the 64 MiB limit.')
        files[name] = raw
    if package_tree_hash(files) != request.tree_hash:
        raise ExternalToolError('INVALID_PACKAGE', 'Package content does not match the pinned tree hash.')
    return files


def _selected(request: ExternalToolRequest, *, listing: bool = False) -> dict[str, Any]:
    files = _decode_package(request)
    try:
        workflow = yaml.safe_load(files['workflow.yaml'])
        runtime = workflow['runtime']
        if 'execution' in runtime and 'external_execution' in runtime:
            raise ValueError('ambiguous execution policy')
        external = runtime.get('execution', runtime.get('external_execution'))
        if str(external['version']) != '1':
            raise ValueError('unsupported version')
        step = external['steps'][request.step_id]
        tools = step.get('tools') or []
        declarations = workflow.get('tool_scripts') or []
    except (AttributeError, KeyError, TypeError, ValueError, yaml.YAMLError) as exc:
        raise ExternalToolError('INVALID_PACKAGE', 'The pinned step has no supported external tool contract.') from exc
    if not isinstance(tools, list) or not isinstance(declarations, list):
        raise ExternalToolError('INVALID_PACKAGE', 'Tool declarations must be lists.')
    owners: dict[str, str] = {}
    for declaration in declarations:
        if not isinstance(declaration, dict):
            raise ExternalToolError('INVALID_PACKAGE', 'Invalid package tool declaration.')
        functions = declaration.get('functions') or []
        if not isinstance(functions, list) or any(not isinstance(name, str) for name in functions):
            raise ExternalToolError('INVALID_PACKAGE', 'Package functions must be explicit names.')
        for name in functions:
            if name in owners:
                raise ExternalToolError('INVALID_PACKAGE', 'Duplicate package tool declaration.')
            owners[name] = declaration.get('path')
    selected = {}
    for tool in tools:
        if not isinstance(tool, dict) or tool.get('source') != 'package':
            # Global capabilities have a separate authenticated service adapter.
            continue
        name = tool.get('name')
        if not isinstance(name, str):
            raise ExternalToolError('INVALID_PACKAGE', 'Invalid external tool name.')
        entry = _REGISTRY.get(name)
        if entry is None:
            if listing or name != getattr(request, 'tool_name', None):
                # Core serves its separately audited data adapters from the same
                # pinned declaration. They must not hide Python's own catalog.
                continue
            raise ExternalToolError('TOOL_ADAPTER_UNAVAILABLE',
                                    f'Package tool {name!r} needs an explicit LazyMind service adapter.')
        path, binding, function = entry
        if owners.get(name) != path or files.get(path) != binding.encode():
            raise ExternalToolError('TOOL_NOT_ALLOWED', 'The package tool binding is not an audited service adapter.')
        selected[name] = function
    return selected


def _arguments_model(function):
    hints = get_type_hints(function)
    fields = {}
    for name, parameter in inspect.signature(function).parameters.items():
        fields[name] = (hints.get(name, Any), ... if parameter.default is inspect.Parameter.empty else parameter.default)
    return create_model(f'{function.__name__}_Arguments', __config__=ConfigDict(extra='forbid', strict=True), **fields)


def list_external_tools(request: ExternalToolRequest) -> dict[str, Any]:
    return {'tools': [
        {'name': name, 'description': inspect.getdoc(function) or name, 'available': True,
         'input_schema': _arguments_model(function).model_json_schema()}
        for name, function in _selected(request, listing=True).items()
    ]}


def call_external_tool(request: ExternalToolCallRequest) -> dict[str, Any]:
    function = _selected(request).get(request.tool_name)
    if function is None:
        raise ExternalToolError('TOOL_NOT_ALLOWED', 'This tool is not available to the pinned external step.')
    try:
        arguments = _arguments_model(function).model_validate(request.arguments).model_dump()
    except ValidationError as exc:
        raise ExternalToolError('INVALID_ARGUMENT', 'Arguments do not match the declared tool schema.') from exc
    return {'result': function(**arguments)}
