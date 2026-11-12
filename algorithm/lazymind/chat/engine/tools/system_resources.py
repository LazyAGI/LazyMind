"""Ownership boundaries for files stored and managed by LazyMind itself."""
from __future__ import annotations

import os
from pathlib import Path

import lazyllm


def safe_storage_part(value: object) -> str:
    """Match Core's safePathPart for owner-scoped upload directories."""
    text = str(value or '').strip().replace('..', '').replace('\\', '/').strip('/')
    if not text:
        return 'root'
    return ''.join('_' if char in '/:*?"<>|' else char for char in text)


def current_user_id() -> str:
    try:
        config = lazyllm.globals.get('agentic_config') or {}
    except Exception:
        config = {}
    return str(config.get('user_id') or '').strip() if isinstance(config, dict) else ''


def user_upload_root(user_id: str) -> str:
    from lazymind.chat.service.utils.static_file_url import _upload_root

    return os.path.realpath(os.path.join(
        _upload_root(), 'tmp', 'users', safe_storage_part(user_id),
    ))


def user_chat_workspace_roots(user_id: str) -> tuple[str, str]:
    from lazymind.chat.engine.tools.conversation_workspace import _legacy_scope_hash, _scope_hash
    from lazymind.config import config as runtime_config

    base = os.path.join(runtime_config['agentic_workspace'], 'chat-artifacts')
    return tuple(os.path.realpath(os.path.join(base, digest)) for digest in (
        _scope_hash(user_id), _legacy_scope_hash(user_id),
    ))


def user_managed_read_roots(user_id: str) -> tuple[str, ...]:
    """Roots readable through system ACL instead of host-workspace authorization.

    The authenticated user id selects both roots. They contain uploads and
    conversation outputs owned by that user, including resources from other
    conversations. These roots intentionally grant no write/delete exemption.
    """
    owner = str(user_id or '').strip()
    if not owner:
        return ()
    return (user_upload_root(owner), *user_chat_workspace_roots(owner))


def generated_upload_directory(category: str = 'generated') -> Path:
    owner = current_user_id()
    if not owner:
        raise RuntimeError('user_id is required to create a managed file')
    directory = Path(user_upload_root(owner)) / safe_storage_part(category)
    directory.mkdir(parents=True, exist_ok=True)
    return directory
