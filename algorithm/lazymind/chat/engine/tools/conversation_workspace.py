from __future__ import annotations

import hashlib
import os
import lazyllm
from lazyllm.tools.agent import ToolExecutionError
from lazymind.config import config as _cfg

_CHAT_FILE_DIRECTORY = 'chat-artifacts'


def _current_artifact_scope() -> tuple[str, str]:
    config = lazyllm.globals.get('agentic_config') or {}
    user_id = str(config.get('user_id') or '').strip()
    conversation_id = str(config.get('conversation_id') or '').strip()
    if not user_id or not conversation_id:
        raise ToolExecutionError(
            'user_id and conversation_id are required to access chat files; conversation context is unavailable.'
        )
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


def _resolve_workspace_path(path: str, user_id: str, conversation_id: str) -> tuple[str, str]:
    workspace = os.path.realpath(chat_agent_workspace(user_id, conversation_id))
    candidate = path if os.path.isabs(path) else os.path.join(workspace, path)
    resolved = os.path.realpath(candidate)
    try:
        inside_workspace = os.path.commonpath((workspace, resolved)) == workspace
    except ValueError:
        # Windows raises ValueError when the workspace and requested path use
        # different drive letters. That is still an outside-workspace path.
        inside_workspace = False
    if not inside_workspace:
        raise ToolExecutionError('path must stay inside the current main-Agent workspace')
    return workspace, resolved
