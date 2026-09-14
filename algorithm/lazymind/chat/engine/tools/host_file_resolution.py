"""Path resolution shared by explicitly declared model-facing file consumers.

Resolvers only normalize inputs and declare access. They never open a model-selected
file; permission checks must happen before the tool reads any of these paths.
"""
from __future__ import annotations

import os
import tempfile
from contextvars import ContextVar
from pathlib import Path
from urllib.parse import unquote, urlsplit

from lazyllm.tools.agent import HostFileIntent, HostFileResolution, ToolExecutionError

from lazymind.chat.engine.tools.workspace_context import (
    canonical_host_path, get_workspace_permission_context, get_tool_resolution_context,
)


_STAGED_INPUTS = ContextVar('approved_input_directories', default=(None, ()))

def validate_storage_id(value: object) -> None:
    if isinstance(value, str) and (value in {'.', '..'} or any(c in value for c in ('/', '\\', '\0'))):
        raise ToolExecutionError('Storage identifiers must not contain path components.')


def managed_path(path: str) -> bool:
    from lazymind.chat.engine.subagent.context import get_context
    from lazymind.chat.engine.tools.local_file.workspace import chat_agent_workspace
    from lazymind.chat.service.utils.static_file_url import local_path_from_static_file_url

    roots = []
    request = get_workspace_permission_context()
    resolution = get_tool_resolution_context()
    config = resolution.config if resolution else {}
    if resolution is not None:
        task_workspace = config.get('_subagent_workspace')
    else:
        context = get_context()
        task_workspace = context.workspace_path if context else None
    if task_workspace:
        roots.append(task_workspace)
    user, conversation = config.get('user_id'), config.get('conversation_id')
    if user and conversation:
        validate_storage_id(str(user))
        validate_storage_id(str(conversation))
        roots.append(chat_agent_workspace(str(user), str(conversation)))
    if config.get('_writer_workspace'):
        roots.append(config['_writer_workspace'])
    staging_request, staging_roots = _STAGED_INPUTS.get()
    if request is not None and staging_request is request:
        roots.extend(staging_roots)
    canonical = os.path.realpath(path)
    attachments = list(config.get('files') or ())
    for values in (config.get('history_files_per_turn') or {}).values():
        attachments.extend(values or ())
    for attachment in attachments:
        if not isinstance(attachment, str):
            continue
        local = local_path_from_static_file_url(attachment)
        if not local and not urlsplit(attachment).scheme and os.path.isabs(attachment):
            local = attachment
        if local and os.path.realpath(local) == canonical:
            return True
    return any(os.path.commonpath([canonical, os.path.realpath(root)]) == os.path.realpath(root)
               for root in roots if root)


class FileResolution:
    def __init__(self, *, default_root: str | None = None):
        self.default_root = default_root
        self.files: list[HostFileIntent] = []

    def local(self, value: str, operation: str = 'read') -> str:
        raw = str(value or '').strip()
        if not raw or '\0' in raw:
            raise ToolExecutionError('A non-empty local file path is required.')
        if raw.startswith(('/static-files/', '/var/lib/lazymind/uploads/')):
            from lazymind.chat.service.utils.static_file_url import local_path_from_static_file_url
            raw = local_path_from_static_file_url(raw)
            if not raw:
                raise ToolExecutionError('Invalid managed file path.')
        parsed = urlsplit(raw)
        if parsed.scheme:
            if parsed.scheme != 'file' or parsed.netloc not in ('', 'localhost'):
                raise ToolExecutionError(f'Unsupported local file protocol: {parsed.scheme!r}.')
            raw = unquote(parsed.path)
        path = canonical_host_path(raw, default_root=self.default_root)
        if not managed_path(path):
            intent = HostFileIntent(path, operation)
            if intent not in self.files:
                self.files.append(intent)
        return path

    def output_directory(self, value: str) -> str:
        """Declare generated descendants without allowing pre-existing link escapes.

        Output filenames can depend on generated content. An existing link inside
        the store must not redirect that later write outside its declared tree.
        Only directory metadata is inspected here, never file contents.
        """
        directory = self.local(value, 'write')
        visited = 0
        for root, directories, filenames in os.walk(directory, followlinks=False):
            for name in [*directories, *filenames]:
                visited += 1
                if visited > 4096:
                    raise ToolExecutionError('Writer output store exceeds the bounded path validation limit.')
                candidate = os.path.join(root, name)
                if os.path.islink(candidate):
                    target = os.path.realpath(candidate)
                    if os.path.commonpath([directory, target]) != directory:
                        raise ToolExecutionError('Writer output stores must not contain links outside the declared directory.')
        return directory

    def media(self, value: str, *, remote: bool = True) -> str:
        from lazymind.chat.engine.tools.infra.image_generation_support import _image_url_registry
        from lazymind.chat.service.utils.static_file_url import local_path_from_static_file_url

        raw = str(value or '').strip()
        resolution = get_tool_resolution_context()
        citation = (resolution.config.get('citation_state') or {}) if resolution else _image_url_registry()
        raw = str((citation.get('_image_url_registry') or {}).get(raw) or raw)
        # Reject malformed managed locators before the permissive legacy resolver can
        # reinterpret them as ordinary local paths (or HTTP references).
        managed_locator = raw.startswith(('/static-files/', '/var/lib/lazymind/uploads/'))
        if managed_locator:
            local = local_path_from_static_file_url(raw)
            if not local:
                raise ToolExecutionError('Invalid managed media path.')
            return self.local(local)
        resolved = raw
        parsed = urlsplit(resolved)
        if parsed.scheme in {'http', 'https'}:
            local = local_path_from_static_file_url(resolved)
            if local:
                return self.local(local)
            if '/var/lib/lazymind/uploads/' in resolved:
                raise ToolExecutionError('Invalid managed media URL.')
            if remote and parsed.hostname:
                return resolved
            raise ToolExecutionError('This tool requires a local media file.')
        return self.local(resolved)

    def finish(self, arguments: dict) -> HostFileResolution:
        return HostFileResolution(arguments, tuple(self.files))


def _host_guard():
    from lazymind.chat.engine.tools.host_access_guard import get_host_access_guard
    return get_host_access_guard()


def _pinned_parent(path: str):
    """Open every ancestor without following links; caller closes returned fd."""
    path = os.path.abspath(path)
    parts = Path(path).parts
    descriptor = os.open(parts[0], os.O_RDONLY | os.O_DIRECTORY)
    try:
        for part in parts[1:-1]:
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = child
        return descriptor, parts[-1]
    except BaseException:
        os.close(descriptor)
        raise


def open_input_file(path: str):
    """Read from an identity-checked fd, never reopen the approved pathname."""
    guard = _host_guard()
    if guard is not None:
        opened = guard.open_read(path)
        return os.fdopen(opened, 'rb') if isinstance(opened, int) else opened
    parent, name = _pinned_parent(path)
    try:
        return os.fdopen(os.open(name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=parent), 'rb')
    finally:
        os.close(parent)


def stage_input_file(path: str) -> str:
    """Give path-only model SDKs a private copy made from the approved open fd."""
    import shutil

    if urlsplit(path).scheme in {'http', 'https'}:
        return path
    # Outside the permission runtime, preserve direct-tool callers' path contracts.
    request = get_workspace_permission_context()
    guard = _host_guard()
    if request is None or not request.bound and guard is None:
        return path
    resolution = get_tool_resolution_context()
    config = resolution.config if resolution else {}
    base = config.get('_writer_workspace') or config.get('_subagent_workspace')
    if not base and config.get('user_id') and config.get('conversation_id'):
        from lazymind.chat.engine.tools.local_file.workspace import chat_agent_workspace
        validate_storage_id(str(config['user_id']))
        validate_storage_id(str(config['conversation_id']))
        base = chat_agent_workspace(str(config['user_id']), str(config['conversation_id']))
    if base and guard is not None:
        import uuid
        directory = os.path.join(os.path.realpath(base), '.approved-inputs', uuid.uuid4().hex)
        guard.makedirs(directory)
    else:
        directory = os.path.realpath(tempfile.mkdtemp(prefix='lazymind-approved-input-'))
    destination = os.path.join(directory, os.path.basename(path))
    try:
        with open_input_file(path) as source:
            target = guard.open_write(destination, 'x') if base and guard is not None else open(destination, 'xb')
            with target:
                shutil.copyfileobj(source, target)
    except BaseException:
        shutil.rmtree(directory)
        raise
    request = get_workspace_permission_context()
    previous_request, directories = _STAGED_INPUTS.get()
    _STAGED_INPUTS.set((request, (*directories, directory) if previous_request is request else (directory,)))
    return destination


def copy_artifact_input(source: str, workspace: str) -> str:
    """Copy the authorized input through pinned source and destination handles."""
    import shutil

    destination = os.path.join(workspace, os.path.basename(source))
    if os.path.abspath(source) == os.path.abspath(destination):
        return os.path.basename(destination)
    guard = _host_guard()
    request = get_workspace_permission_context()
    if guard is None and (request is None or not request.bound):
        os.makedirs(workspace, exist_ok=True)
        shutil.copy2(source, destination)
        return os.path.basename(destination)
    if guard is not None:
        guard.makedirs(workspace)
    else:
        os.makedirs(workspace, exist_ok=True)
    with open_input_file(source) as incoming:
        if guard is not None:
            with guard.open_write(destination, 'w') as outgoing:
                shutil.copyfileobj(incoming, outgoing)
        else:
            parent, name = _pinned_parent(destination)
            try:
                descriptor = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW, 0o600, dir_fd=parent)
                with os.fdopen(descriptor, 'wb') as outgoing:
                    outgoing.truncate(0)
                    shutil.copyfileobj(incoming, outgoing)
            finally:
                os.close(parent)
    return os.path.basename(destination)
