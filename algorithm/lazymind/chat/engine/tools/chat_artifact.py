from __future__ import annotations

import json
import os
import shutil
import unicodedata
import uuid
from typing import Any, Dict, Literal, Optional
from lazyllm.tools import fc_register
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.base import _write_agent_data
from .host_file_resolution import FileResolution, open_input_file
from .host_access_guard import get_host_access_guard
from .workspace_context import get_workspace_permission_context
from .conversation_workspace import (
    _CHAT_FILE_DIRECTORY, _scope_hash, _current_artifact_scope, _resolve_workspace_path,
)

_MAX_ARTIFACT_BYTES = 2 * 1024 * 1024


def _safe_filename(filename: str, content_type: str) -> str:
    name = str(filename or '').strip()
    if (not name or name in {'.', '..'} or '/' in name or '\\' in name
            or os.path.basename(name) != name):
        raise ToolExecutionError(
            'filename must be a plain file name without a directory path'
        )
    if len(name) > 255 or any(unicodedata.category(char) == 'Cc' for char in name):
        raise ToolExecutionError('filename is invalid or too long')
    if content_type in {'text', 'json'} and '.' not in name:
        name += '.json' if content_type == 'json' else '.txt'
    return name


def _normalize_caption(caption: Optional[str]) -> Optional[str]:
    normalized = str(caption).strip() if caption else None
    if normalized and len(normalized) > 2000:
        raise ToolExecutionError('caption exceeds the 2000 character limit')
    return normalized


def _published_file_directory(user_id: str, conversation_id: str, artifact_id: str) -> str:
    workspace_root = os.path.realpath(
        os.environ.get('LAZYMIND_SUBAGENT_WORKSPACE')
        or os.environ.get('LAZYMIND_AGENTIC_WORKSPACE')
        or '/data/subagent',
    )
    return os.path.join(
        workspace_root,
        _CHAT_FILE_DIRECTORY,
        _scope_hash(user_id),
        _scope_hash(conversation_id),
        artifact_id,
    )


def _resolve_source_file(path: str, user_id: str, conversation_id: str) -> str:
    raw_path = str(path or '').strip()
    if not raw_path:
        raise ToolExecutionError('path is required')
    guard = get_host_access_guard()
    if guard is not None:
        guard.validate()
        source = raw_path
    else:
        _, source = _resolve_workspace_path(raw_path, user_id, conversation_id)
    if not os.path.isfile(source):
        raise ToolExecutionError(
            f'path must point to an existing regular file, got {path!r}.'
        )
    return source


def _file_markdown(filename: str, artifact_id: str) -> str:
    return f'[{filename}](file_id:{artifact_id})'


def resolve_chat_artifact(arguments):
    permission = get_workspace_permission_context()
    files = FileResolution(default_root=permission.cwd if permission else None)
    if arguments.get('content_type') == 'file':
        arguments['content'] = files.local(arguments.get('content'))
    return files.finish(arguments)


@fc_register(host_file=resolve_chat_artifact)
def save_chat_artifact(
    filename: str,
    content: Any,
    content_type: Literal['text', 'json', 'file'] = 'text',
    caption: Optional[str] = None,
) -> Dict[str, Any]:
    """Save a downloadable artifact produced in the current main-chat turn.

    Text and JSON values are stored directly. For any other generated attachment, use
    ``content_type='file'`` and pass its main-Agent workspace path as ``content``. Call
    once for each requested artifact. This does not create a SubAgent task.

    After a successful call, mention the saved file in the final answer by copying
    ``file_markdown`` verbatim so the filename appears as a downloadable Markdown
    link, for example ``[notes.txt](file_id:...)``. Do not paste workspace paths,
    unsigned URLs, or a bare filename without that link.

    Args:
        filename: Download filename, for example ``notes.txt``. Directory paths are rejected.
        content: Text, a JSON-compatible value, or a workspace path for a file artifact.
        content_type: Exactly one of ``text``, ``json``, or ``file``. Images and
            other binary attachments use ``file`` with a local path inside the
            current main-Agent workspace. ``image`` is not a valid value here.
        caption: Optional short human-readable description.
    """
    normalized_type = str(content_type or 'text').strip().lower()
    if normalized_type not in {'text', 'json', 'file'}:
        raise ToolExecutionError("content_type must be 'text', 'json', or 'file'")
    safe_name = _safe_filename(filename, normalized_type)
    normalized_caption = _normalize_caption(caption)
    if normalized_type == 'file':
        return save_chat_file(safe_name, str(content or ''), normalized_caption)
    if normalized_type == 'json':
        value = {'data': content}
    else:
        text = str(content if content is not None else '')
        value = {'text': text}
    # Measure the actual event value rather than only the raw content: JSON escaping
    # can make the persisted payload larger than its source string.
    encoded_value = json.dumps(
        value, ensure_ascii=False, separators=(',', ':'),
    ).encode('utf-8')
    if len(encoded_value) > _MAX_ARTIFACT_BYTES:
        raise ToolExecutionError('artifact content exceeds the 2 MiB limit')

    artifact_id = str(uuid.uuid4())
    _write_agent_data(
        'artifact_created',
        artifact_id=artifact_id,
        filename=safe_name,
        content_type=normalized_type,
        value=value,
        caption=normalized_caption,
    )
    return {
        'artifact_id': artifact_id,
        'filename': safe_name,
        'content_type': normalized_type,
        'file_markdown': _file_markdown(safe_name, artifact_id),
        'message': f"Saved downloadable artifact '{safe_name}'.",
    }


def save_chat_file(
    filename: str,
    path: str,
    caption: Optional[str],
    artifact_id: Optional[str] = None,
    replace_existing: bool = False,
) -> Dict[str, Any]:
    filename = _safe_filename(filename, 'file')
    user_id, conversation_id = _current_artifact_scope()
    source = _resolve_source_file(path, user_id, conversation_id)
    artifact_id = artifact_id or str(uuid.uuid4())
    destination_dir = _published_file_directory(user_id, conversation_id, artifact_id)
    destination = os.path.join(destination_dir, filename)
    temporary = os.path.join(destination_dir, f'.{uuid.uuid4().hex[:8]}.tmp')
    created_directory = False

    try:
        if replace_existing:
            os.makedirs(destination_dir, exist_ok=True)
        else:
            os.makedirs(destination_dir, exist_ok=False)
            created_directory = True
        with open_input_file(source) as incoming, open(temporary, 'xb') as outgoing:
            shutil.copyfileobj(incoming, outgoing)
        os.replace(temporary, destination)
        size = os.path.getsize(destination)
        value = {'filename': filename, 'path': destination, 'size': size}
        _write_agent_data(
            'artifact_created',
            artifact_id=artifact_id,
            filename=filename,
            content_type='file',
            value=value,
            caption=caption,
            replace_existing=replace_existing,
        )
    except Exception:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        if created_directory:
            shutil.rmtree(destination_dir, ignore_errors=True)
        raise

    return {
        'artifact_id': artifact_id,
        'filename': filename,
        'content_type': 'file',
        'size': size,
        'file_markdown': _file_markdown(filename, artifact_id),
        'message': f"Saved downloadable artifact '{filename}'.",
    }
