from __future__ import annotations

import hashlib
import json
import os
import shutil
import unicodedata
import uuid
from typing import Any, Dict, Optional

import lazyllm
from lazyllm.tools.agent.base import _write_agent_data
from lazyllm.tools.agent.file_tool import (
    list_dir as _list_dir,
    read_file as _read_file,
    write_file as _write_file,
)

from lazymind.config import config as _cfg
from lazymind.chat.engine.tools.infra import tool_success

_MAX_ARTIFACT_BYTES = 2 * 1024 * 1024
_CHAT_FILE_DIRECTORY = 'chat-artifacts'


def _safe_filename(filename: str, content_type: str) -> str:
    name = str(filename or '').strip()
    if (not name or name in {'.', '..'} or '/' in name or '\\' in name
            or os.path.basename(name) != name):
        raise ValueError('filename must be a plain file name without a directory path')
    if len(name) > 255 or any(unicodedata.category(char) == 'Cc' for char in name):
        raise ValueError('filename is invalid or too long')
    if content_type in {'text', 'json'} and '.' not in name:
        name += '.json' if content_type == 'json' else '.txt'
    return name


def _normalize_caption(caption: Optional[str]) -> Optional[str]:
    normalized = str(caption).strip() if caption else None
    if normalized and len(normalized) > 2000:
        raise ValueError('caption exceeds the 2000 character limit')
    return normalized


def _current_artifact_scope() -> tuple[str, str]:
    config = lazyllm.globals.get('agentic_config') or {}
    user_id = str(config.get('user_id') or '0').strip()
    conversation_id = str(config.get('conversation_id') or '').strip()
    if not conversation_id:
        raise RuntimeError('conversation_id is required to publish a chat file')
    return user_id, conversation_id


def _scope_hash(value: str) -> str:
    # 128 bits is ample for workspace isolation and keeps generated paths below
    # the legacy Windows MAX_PATH limit in packaged desktop installations.
    return hashlib.sha256(value.encode('utf-8')).hexdigest()[:32]


def _legacy_scope_hash(value: str) -> str:
    # Read-only compatibility for workspaces created before hashes were shortened.
    return hashlib.sha256(value.encode('utf-8')).hexdigest()


def chat_agent_workspace(user_id: str, conversation_id: str) -> str:
    """Return the isolated main-Agent workspace for one conversation."""
    workspace_root = os.path.realpath(_cfg['agentic_workspace'])
    current = os.path.join(
        workspace_root,
        _CHAT_FILE_DIRECTORY,
        _scope_hash(str(user_id or '0')),
        _scope_hash(str(conversation_id)),
    )
    legacy = os.path.join(
        workspace_root,
        _CHAT_FILE_DIRECTORY,
        _legacy_scope_hash(str(user_id or '0')),
        _legacy_scope_hash(str(conversation_id)),
    )
    # Prefer the 32-character layout whenever it has been initialized. Only an
    # existing legacy directory keeps an older conversation on the 64-character layout.
    if not os.path.exists(current) and os.path.isdir(legacy):
        return legacy
    return current


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


def _resolve_workspace_path(path: str, user_id: str, conversation_id: str) -> tuple[str, str]:
    workspace = os.path.realpath(chat_agent_workspace(user_id, conversation_id))
    candidate = path if os.path.isabs(path) else os.path.join(workspace, path)
    resolved = os.path.realpath(candidate)
    if _cfg['trusted_local_mode']:
        return workspace, resolved
    try:
        inside_workspace = os.path.commonpath((workspace, resolved)) == workspace
    except ValueError:
        # Windows raises ValueError when the workspace and requested path use
        # different drive letters. That is still an outside-workspace path.
        inside_workspace = False
    if not inside_workspace:
        raise ValueError('path must stay inside the current main-Agent workspace')
    return workspace, resolved


def _file_tool_root(workspace: str) -> Optional[str]:
    return None if _cfg['trusted_local_mode'] else workspace


def _resolve_source_file(path: str, user_id: str, conversation_id: str) -> str:
    raw_path = str(path or '').strip()
    if not raw_path:
        raise ValueError('path is required')
    _, source = _resolve_workspace_path(raw_path, user_id, conversation_id)
    if not os.path.isfile(source):
        raise ValueError('path must point to an existing regular file')
    return source


def save_chat_artifact(
    filename: str,
    content: Any,
    content_type: str = 'text',
    caption: Optional[str] = None,
) -> Dict[str, Any]:
    """Save a downloadable artifact produced in the current main-chat turn.

    Text and JSON values are stored directly. For any other generated attachment, use
    ``content_type='file'`` and pass its main-Agent workspace path as ``content``. Call
    once for each requested artifact. This does not create a SubAgent task.

    Args:
        filename: Download filename, for example ``notes.txt``. Directory paths are rejected.
        content: Text, a JSON-compatible value, or a workspace path for a file artifact.
        content_type: One of ``text``, ``json``, or ``file``.
        caption: Optional short human-readable description.
    """
    normalized_type = str(content_type or 'text').strip().lower()
    if normalized_type not in {'text', 'json', 'file'}:
        raise ValueError("content_type must be 'text', 'json', or 'file'")
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
        raise ValueError('artifact content exceeds the 2 MiB limit')

    artifact_id = str(uuid.uuid4())
    _write_agent_data(
        'artifact_created',
        artifact_id=artifact_id,
        filename=safe_name,
        content_type=normalized_type,
        value=value,
        caption=normalized_caption,
    )
    return tool_success('save_chat_artifact', {
        'artifact_id': artifact_id,
        'filename': safe_name,
        'content_type': normalized_type,
        'message': f"Saved downloadable artifact '{safe_name}'.",
    })


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
        shutil.copy2(source, temporary)
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

    return tool_success('save_chat_artifact', {
        'artifact_id': artifact_id,
        'filename': filename,
        'content_type': 'file',
        'size': size,
        'message': f"Saved downloadable artifact '{filename}'.",
    })


def write_file(
    path: str,
    content: str,
    mode: str = 'overwrite',
    encoding: str = 'utf-8',
    create_parents: bool = True,
    allow_unsafe: bool = False,
) -> Dict[str, Any]:
    """Write a text file in the current chat workspace or an allowed host path.

    Args:
        path: Workspace-relative path. In trusted local mode, absolute host paths are also allowed.
        content: Text to write.
        mode: "overwrite" or "append".
        encoding: Text encoding.
        create_parents: Create parent directories when needed.
        allow_unsafe: Allow overwriting an existing file. Append mode does not require it.
    """
    user_id, conversation_id = _current_artifact_scope()
    workspace, target = _resolve_workspace_path(path, user_id, conversation_id)
    return _write_file(
        target,
        content,
        mode=mode,
        encoding=encoding,
        root=_file_tool_root(workspace),
        create_parents=create_parents,
        allow_unsafe=allow_unsafe,
    )


def read_file(
    path: str,
    start_line: Optional[int] = None,
    end_line: Optional[int] = None,
    encoding: str = 'utf-8',
    errors: str = 'replace',
    max_chars: int = 200000,
) -> Dict[str, Any]:
    """Read a text file from the current chat workspace or an allowed host path.

    Args:
        path: Workspace-relative path. In trusted local mode, absolute host paths are also allowed.
        start_line: Optional 1-based first line.
        end_line: Optional 1-based last line.
        encoding: Text encoding.
        errors: Decode error handling.
        max_chars: Maximum returned characters.
    """
    user_id, conversation_id = _current_artifact_scope()
    workspace, source = _resolve_workspace_path(path, user_id, conversation_id)
    return _read_file(
        source,
        start_line=start_line,
        end_line=end_line,
        encoding=encoding,
        errors=errors,
        root=_file_tool_root(workspace),
        max_chars=max_chars,
    )


def list_dir(path: str = '.', recursive: bool = False, max_depth: int = 5) -> Dict[str, Any]:
    """List files in the current chat workspace or an allowed host path.

    Args:
        path: Workspace-relative directory. In trusted local mode, absolute host paths are also allowed.
        recursive: Recursively include descendants.
        max_depth: Maximum recursive depth.
    """
    user_id, conversation_id = _current_artifact_scope()
    workspace, directory = _resolve_workspace_path(path, user_id, conversation_id)
    return _list_dir(
        directory,
        recursive=recursive,
        max_depth=max_depth,
        root=_file_tool_root(workspace),
    )
