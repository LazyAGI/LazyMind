"""Path resolution shared by explicitly declared model-facing file consumers.

Resolvers only normalize inputs and declare access. They never open a model-selected
file; permission checks must happen before the tool reads any of these paths.
"""
from __future__ import annotations

import os
import ntpath
import tempfile
from contextvars import ContextVar
from pathlib import Path
from urllib.parse import unquote, urlsplit
from urllib.request import url2pathname

from lazyllm.tools.agent import HostFileIntent, HostFileResolution, ToolExecutionError

from lazymind.chat.engine.tools.workspace_context import (
    canonical_host_path, get_workspace_permission_context, get_tool_resolution_context,
)


_STAGED_INPUTS = ContextVar('approved_input_directories', default=(None, ()))


def validate_storage_id(value: object) -> None:
    if isinstance(value, str) and (value in {'.', '..'} or any(c in value for c in ('/', '\\', '\0'))):
        raise ToolExecutionError('Storage identifiers must not contain path components.')


def managed_path(path: str) -> bool:
    request = get_workspace_permission_context()
    resolution = get_tool_resolution_context()
    roots = list(resolution.managed_roots) if resolution else []
    staging_request, staging_roots = _STAGED_INPUTS.get()
    if request is not None and staging_request is request:
        roots.extend(staging_roots)
    canonical = os.path.realpath(path)
    if resolution and canonical in resolution.managed_files:
        return True
    for root in roots:
        try:
            if os.path.commonpath([canonical, root]) == root:
                return True
        except ValueError:
            continue
    return False


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
        parsed = urlsplit(raw) if not ntpath.isabs(raw) else None
        if parsed is not None and parsed.scheme:
            if parsed.scheme != 'file' or parsed.netloc not in ('', 'localhost'):
                raise ToolExecutionError(f'Unsupported local file protocol: {parsed.scheme!r}.')
            raw = url2pathname(parsed.path) if os.name == 'nt' else unquote(parsed.path)
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
                        raise ToolExecutionError(
                            'Writer output stores must not contain links outside the declared directory.')
        return directory

    def media(self, value: str, *, remote: bool = True) -> str:
        from lazymind.chat.engine.tools.infra.image_generation_support import _image_url_registry
        from lazymind.chat.service.utils.static_file_url import local_path_from_static_file_url

        raw = str(value or '').strip()
        resolution = get_tool_resolution_context()
        citation = resolution.citation_state if resolution else _image_url_registry()
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
    if os.name == 'nt':
        before = os.stat(path, follow_symlinks=False)
        if os.path.realpath(path) != os.path.abspath(path):
            raise ToolExecutionError('Input must not be a symbolic link')
        stream = open(path, 'rb')
        if (not os.path.samestat(before, os.fstat(stream.fileno()))
                or os.path.realpath(path) != os.path.abspath(path)):
            stream.close()
            raise ToolExecutionError('Input changed after authorization')
        return stream
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
    base = resolution.managed_roots[0] if resolution and resolution.managed_roots else None
    if base and guard is not None:
        import uuid
        directory = os.path.join(os.path.realpath(base), '.approved-inputs', uuid.uuid4().hex)
        os.makedirs(directory, exist_ok=True)
    else:
        directory = os.path.realpath(tempfile.mkdtemp(prefix='lazymind-approved-input-'))
    destination = os.path.join(directory, os.path.basename(path))
    try:
        with open_input_file(path) as source:
            target = open(destination, 'xb')
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
    os.makedirs(workspace, exist_ok=True)
    with open_input_file(source) as incoming:
        if guard is not None or os.name == 'nt':
            if os.path.islink(destination):
                raise ToolExecutionError('Artifact destination must not be a symbolic link')
            with open(destination, 'wb') as outgoing:
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
