"""Pure request-local policy for prepared host-file intents."""
from __future__ import annotations

import os
from enum import Enum

from lazymind.chat.engine.tools.approved_local_io import sensitive_path


class WorkspacePolicyDecision(str, Enum):
    ALLOW = 'allow'
    ASK = 'ask'
    DENY = 'deny'


def _within(root: str, path: str) -> bool:
    if not root or not os.path.isabs(path):
        return False
    try:
        return os.path.commonpath((os.path.realpath(root), os.path.realpath(path))) == os.path.realpath(root)
    except (OSError, ValueError):
        return False


def _invalid_path(path: str) -> bool:
    if not path or '\x00' in path or len(path) > 4096:
        return True
    slash = path.replace(os.sep, '/')
    if os.altsep:
        slash = slash.replace(os.altsep, '/')
    return any(
        part.lower() == '.git' or part.endswith(('.', ' '))
        for part in slash.split('/')
        if part not in {'', '.'}
    )


def decide_host_file_access(permission, intents) -> WorkspacePolicyDecision:
    """Apply the immutable run snapshot to one prepared tool call."""
    if not permission.bound or permission.permission_mode not in {
        'always_ask', 'ask_as_needed', 'allow_all',
    }:
        return WorkspacePolicyDecision.DENY
    decision = WorkspacePolicyDecision.ALLOW
    for intent in intents:
        if (intent.operation not in {'read', 'write', 'delete'}
                or not os.path.isabs(intent.path) or _invalid_path(intent.path)):
            return WorkspacePolicyDecision.DENY
        if intent.operation != 'read' and sensitive_path(intent.path):
            return WorkspacePolicyDecision.DENY
        if not _within(permission.root, intent.path):
            decision = WorkspacePolicyDecision.ASK
            continue
        if permission.permission_mode == 'allow_all':
            continue
        if intent.operation == 'read':
            continue
        if permission.permission_mode == 'always_ask':
            decision = WorkspacePolicyDecision.ASK
    return decision
