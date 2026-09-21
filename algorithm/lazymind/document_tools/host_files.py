"""Host-file declarations and approved-input staging for document tools."""

from __future__ import annotations

import inspect
import json
import os
from contextvars import ContextVar
from copy import deepcopy
from functools import wraps
from typing import Any

from lazyllm.tools import fc_register
from lazyllm.tools.agent import ToolExecutionError
from lazymind.chat.engine.tools.host_file_resolution import (
    FileResolution,
    stage_input_file,
    validate_storage_id,
)

_WRITER_ADAPTERS = {
    'feishu',
    'github',
    'googledrive',
    'notion',
    'obsidian',
    'wechat',
}


def _json_dumps(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, indent=2)


def _resolve_writer_document_resource(uri: str, files: FileResolution) -> str:
    """Accept local documents and only the cloud filesystems Writer owns."""
    from lazyllm.tools.fs import (
        FeishuFS,
        FeishuWikiFS,
        GitHubRepoFS,
        GitHubWikiFS,
        GoogleDriveFS,
        NotionFS,
        ObsidianFS,
    )
    from lazyllm.tools.fs.client import FS
    from lazymind.common.integrations.remote_fs import RemoteFS
    from lazymind.config import config

    protocol, space, path = FS._parse(uri)
    if protocol == 'file':
        return files.local(path)
    expected = {
        'feishu': (FeishuFS, FeishuWikiFS),
        'githubrepo': (GitHubRepoFS,),
        'githubwiki': (GitHubWikiFS,),
        'googledrive': (GoogleDriveFS,),
        'notion': (NotionFS,),
        'obsidian': (ObsidianFS,),
        'remote': (RemoteFS,),
    }
    if protocol not in expected:
        raise ToolExecutionError(
            f'Unsupported Writer resource protocol: {protocol!r}.'
        )
    fs = FS._get_or_create_fs(protocol, space, path)
    if type(fs) not in expected[protocol]:
        raise ToolExecutionError(
            'Writer resource uses an undeclared filesystem implementation.'
        )
    if (
        protocol == 'remote'
        and fs.base_url
        != str(config['core_api_url'] or '').strip().rstrip('/')
    ):
        raise ToolExecutionError(
            'Writer remote resources must use the configured Core service.'
        )
    return uri


def resolve_writer_files(arguments: dict) -> object:
    """Canonicalize nested Writer resources, assets, and output stores."""
    resolved = deepcopy(arguments)
    files = FileResolution()

    def visit(value: Any) -> Any:
        if isinstance(value, list):
            return [visit(item) for item in value]
        if not isinstance(value, dict):
            return value
        result = {}
        for key, item in value.items():
            if key.endswith('_id'):
                validate_storage_id(item)
            result[key] = visit(item)
            if key == 'assets' and isinstance(result[key], dict):
                for asset in result[key].values():
                    if (
                        isinstance(asset, dict)
                        and asset.get('uri')
                        and not asset.get('local_path')
                    ):
                        asset['uri'] = files.media(asset['uri'])
        adapter = result.get('adapter')
        if adapter and adapter not in _WRITER_ADAPTERS:
            raise ToolExecutionError(
                'Writer target adapter has no declared host-file behavior.'
            )
        if result.get('local_path'):
            result['local_path'] = files.local(result['local_path'])
        uri = result.get('uri')
        resource_type = result.get('resource_type')
        if uri and resource_type == 'document':
            result['uri'] = _resolve_writer_document_resource(uri, files)
        elif uri and resource_type in {'file', 'table', 'slide'}:
            result['uri'] = files.local(uri)
        elif uri and (
            resource_type == 'image'
            or (result.get('media_asset_id') and not result.get('local_path'))
        ):
            result['uri'] = files.media(uri)
        return result

    adapter = resolved.get('adapter')
    if adapter and adapter not in _WRITER_ADAPTERS:
        raise ToolExecutionError(
            'Writer target adapter has no declared host-file behavior.'
        )
    for key, value in list(resolved.items()):
        if key.endswith('_id'):
            validate_storage_id(value)
        if key in {'media_store', 'checkpoint_dir'} and value:
            resolved[key] = files.output_directory(value)
        elif key.endswith('_json') and value:
            try:
                decoded = json.loads(value)
            except (ValueError, TypeError):
                continue
            if key == 'resource_profiles_json':
                payload = (
                    decoded.get('data')
                    if isinstance(decoded, dict) and 'data' in decoded
                    else decoded
                )
                if isinstance(payload, list):
                    payload = [
                        files.local(item) if isinstance(item, str) else visit(item)
                        for item in payload
                    ]
                    decoded = (
                        {**decoded, 'data': payload}
                        if isinstance(decoded, dict)
                        else payload
                    )
            elif key == 'file_paths_json':
                wrapped = isinstance(decoded, dict) and 'data' in decoded
                paths = decoded['data'] if wrapped else decoded
                if not isinstance(paths, list):
                    raise ToolExecutionError(
                        'file_paths_json must contain a JSON array.'
                    )
                canonical = [files.local(path) for path in paths]
                decoded = {**decoded, 'data': canonical} if wrapped else canonical
            else:
                payload = (
                    decoded.get('data')
                    if isinstance(decoded, dict) and 'data' in decoded
                    else decoded
                )
                if key in {'resources_json', 'input_resources_json'} and (
                    not isinstance(payload, list)
                    or any(not isinstance(item, dict) for item in payload)
                ):
                    raise ToolExecutionError(
                        'Writer resources must be inline JSON objects.'
                    )
                if key == 'acquired_resources_json' and (
                    not isinstance(payload, dict)
                    or any(not isinstance(item, dict) for item in payload.values())
                ):
                    raise ToolExecutionError(
                        'Acquired resources must be inline JSON objects.'
                    )
                decoded = visit(decoded)
            resolved[key] = _json_dumps(decoded)
    return files.finish(resolved)


_WRITER_STAGING = ContextVar('writer_approved_input_staging', default=None)


def _stage_writer_inputs(method):
    """Stage approved bytes once before passing paths to third-party parsers."""
    signature = inspect.signature(method)

    @wraps(method)
    def execute(*args, **kwargs):
        from lazymind.chat.engine.tools.host_access_guard import (
            get_host_access_guard,
        )
        from lazymind.chat.engine.tools.workspace_context import (
            get_workspace_permission_context,
        )

        permission = get_workspace_permission_context()
        if (
            permission is None
            or (not permission.bound and get_host_access_guard() is None)
            or _WRITER_STAGING.get() is not None
        ):
            return method(*args, **kwargs)
        staged = {}
        token = _WRITER_STAGING.set(staged)
        bound = signature.bind(*args, **kwargs)

        def stage(path):
            if not isinstance(path, str) or not os.path.isabs(path):
                return path
            if path not in staged:
                staged[path] = stage_input_file(path)
            return staged[path]

        def visit(value):
            if isinstance(value, list):
                return [visit(item) for item in value]
            if not isinstance(value, dict):
                return value
            result = {key: visit(item) for key, item in value.items()}
            if result.get('local_path'):
                result['local_path'] = stage(result['local_path'])
            if (
                result.get('resource_type')
                in {'file', 'table', 'slide', 'image', 'document'}
                and result.get('uri')
            ):
                result['uri'] = stage(result['uri'])
            if (
                result.get('media_asset_id')
                and not result.get('local_path')
                and result.get('uri')
            ):
                result['uri'] = stage(result['uri'])
            if isinstance(result.get('assets'), dict):
                for asset in result['assets'].values():
                    if (
                        isinstance(asset, dict)
                        and asset.get('uri')
                        and not asset.get('local_path')
                    ):
                        asset['uri'] = stage(asset['uri'])
            return result

        try:
            for name, value in list(bound.arguments.items()):
                if not name.endswith('_json') or not value:
                    continue
                try:
                    data = json.loads(value)
                except (ValueError, TypeError):
                    continue
                if name in {'file_paths_json', 'resource_profiles_json'}:
                    payload = (
                        data.get('data')
                        if isinstance(data, dict) and 'data' in data
                        else data
                    )
                    if isinstance(payload, list):
                        payload = [
                            stage(item) if isinstance(item, str) else visit(item)
                            for item in payload
                        ]
                        data = (
                            {**data, 'data': payload}
                            if isinstance(data, dict)
                            else payload
                        )
                else:
                    data = visit(data)
                bound.arguments[name] = _json_dumps(data)
            return method(*bound.args, **bound.kwargs)
        finally:
            _WRITER_STAGING.reset(token)

    return execute


def declare_writer_host_files(toolkit_class):
    """Attach one shared host-file contract to every document capability."""
    names = {
        name
        for base in toolkit_class.__mro__
        for name, value in vars(base).items()
        if not name.startswith('_') and inspect.isfunction(value)
    }
    for name in names:
        method = getattr(toolkit_class, name)
        setattr(
            toolkit_class,
            name,
            fc_register(host_file=resolve_writer_files)(
                _stage_writer_inputs(method)
            ),
        )
    return toolkit_class


__all__ = ['declare_writer_host_files', 'resolve_writer_files']
