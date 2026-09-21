"""The product authorization policy, evaluated once by LazyLLM prepare."""
from __future__ import annotations

import os

from lazyllm.tools.agent import AuthorizationDecision, AuthorizationPolicy, HostFileAccess


def _within(root, path):
    if not root or not os.path.isabs(path):
        return False
    try:
        return os.path.commonpath((os.path.realpath(root), os.path.realpath(path))) == os.path.realpath(root)
    except (OSError, ValueError):
        return False


class WorkspaceAuthorizationPolicy(AuthorizationPolicy):
    def __init__(self, permission, run_grants, trusted_opaque):
        self.permission = permission
        self.run_grants = run_grants
        self.trusted_opaque = trusted_opaque

    def decide(self, prepared):
        if not prepared.ready:
            return AuthorizationDecision.DENY
        if self.permission.workflow_full_trust:
            return AuthorizationDecision.ALLOW
        if prepared.host_file_access is HostFileAccess.NONE:
            return AuthorizationDecision.ALLOW
        if not self.permission.active:
            return AuthorizationDecision.DENY
        mode = self.permission.permission_mode
        if mode not in {'always_ask', 'ask_as_needed', 'allow_all'}:
            return AuthorizationDecision.DENY
        if mode == 'allow_all':
            return AuthorizationDecision.ALLOW
        access = prepared.host_file_access
        if access is HostFileAccess.UNDECLARED:
            grant = 'tool:' + prepared.tool_identity
            grants = self.permission.opaque_tool_grants | self.run_grants
            return (AuthorizationDecision.ALLOW if mode == 'ask_as_needed' and grant in grants
                    else AuthorizationDecision.ASK)
        if access is HostFileAccess.OPAQUE:
            if self.trusted_opaque(prepared.tool_name):
                return AuthorizationDecision.ALLOW
            if prepared.tool_name == 'shell':
                grants = self.permission.opaque_tool_grants | self.run_grants
                return (AuthorizationDecision.ALLOW if mode == 'ask_as_needed' and 'shell' in grants
                        else AuthorizationDecision.ASK)
            return AuthorizationDecision.DENY
        mutations = [intent for intent in prepared.host_files if intent.operation != 'read']
        if not mutations:
            return AuthorizationDecision.ALLOW
        permission = self.permission
        if permission.permission_mode == 'ask_as_needed' and permission.bound and all(
            _within(permission.root, intent.path) for intent in mutations
        ):
            return AuthorizationDecision.ALLOW
        return AuthorizationDecision.ASK
