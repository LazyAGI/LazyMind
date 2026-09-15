"""Pure request-local policy for prepared host-file intents."""
from __future__ import annotations

import os
from enum import Enum


def sensitive_path(path):
    path = path.replace('\\', '/').lower()
    name = path.rsplit('/', 1)[-1]
    return (any(part in {'.ssh', '.aws'} for part in path.split('/'))
            or (name.startswith('.env') and (name == '.env' or name.startswith('.env.'))
                and name not in {'.env.example', '.env.sample', '.env.template'})
            or name in {'id_rsa', 'id_ed25519'} or name.endswith(('.key', '.pem'))
            or 'credentials' in name or name.startswith('service-account'))


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
    if not permission.active or permission.permission_mode not in {
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
        if intent.operation == 'read' or permission.permission_mode == 'allow_all':
            continue
        if permission.permission_mode == 'always_ask' or not _within(permission.root, intent.path):
            decision = WorkspacePolicyDecision.ASK
    return decision
