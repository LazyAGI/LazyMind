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
    return hashlib.sha256(value.encode('utf-8')).hexdigest()


def chat_agent_workspace(user_id: str, conversation_id: str) -> str:
    """Return the isolated main-Agent workspace for one conversation."""
    return os.path.join(
        os.path.realpath(_cfg['agentic_workspace']),
        _CHAT_FILE_DIRECTORY,
        _scope_hash(str(user_id or '0')),
        _scope_hash(str(conversation_id)),
    )


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
        raise ValueError('path is required')
    workspace = chat_agent_workspace(user_id, conversation_id)
    candidate = raw_path if os.path.isabs(raw_path) else os.path.join(workspace, raw_path)
    source = os.path.realpath(candidate)
    try:
        in_workspace = os.path.commonpath((workspace, source)) == workspace
    except ValueError:
        in_workspace = False
    if not in_workspace:
        raise ValueError('path must point to a file inside the main Agent workspace')
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
    temporary = destination + f'.{uuid.uuid4().hex}.tmp'
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
