"""Immutable permission snapshots explicitly owned by one agent run."""
from __future__ import annotations

import copy
import os
from collections.abc import Mapping
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass, field
from types import MappingProxyType
from typing import Any


_WORKFLOW_FULL_TRUST = ContextVar('workflow_full_trust', default=False)


@contextmanager
def workflow_execution_scope(enabled=True):
    """Executor-owned trust, never read from model arguments or persisted config."""
    token = _WORKFLOW_FULL_TRUST.set(bool(enabled))
    try:
        yield
    finally:
        _WORKFLOW_FULL_TRUST.reset(token)


def thaw(value):
    if isinstance(value, Mapping):
        return {key: thaw(item) for key, item in value.items()}
    if isinstance(value, tuple):
        return [thaw(item) for item in value]
    return copy.deepcopy(value)


@dataclass(frozen=True)
class WorkspaceContext:
    local_runtime: bool = True
    workspace_id: str = ''
    root: str = ''
    directory_identity: str = ''
    workspace_version: int = 0
    permission_mode: str = 'always_ask'
    permission_version: int = 0
    user_id: str = ''
    conversation_id: str = ''
    execution: Mapping = field(default_factory=lambda: MappingProxyType({}))
    trusted_local: bool = False
    active: bool = False
    cwd: str = ''
    opaque_tool_grants: frozenset[str] = frozenset()
    workflow_full_trust: bool = False

    @property
    def bound(self):
        return bool(self.workspace_id)

    @classmethod
    def from_snapshot(cls, snapshot: Any, *, user_id='', conversation_id='', execution=None,
                      trusted_local=False, cwd='', local_runtime=True):
        if hasattr(snapshot, 'model_dump'):
            snapshot = snapshot.model_dump()
        snapshot = snapshot if isinstance(snapshot, Mapping) else {}
        raw_root = snapshot.get('root')
        root = os.path.realpath(raw_root) if isinstance(raw_root, str) and os.path.isabs(raw_root) else ''
        identity = execution if isinstance(execution, Mapping) else {}
        identity = {key: identity[key] for key in (
            'history_id', 'run_id', 'task_id', 'generation', 'attempt_id', 'lease_token',
        ) if key in identity}
        if not cwd and user_id and conversation_id:
            from .conversation_workspace import chat_agent_workspace
            cwd = chat_agent_workspace(str(user_id), str(conversation_id))
        return cls(
            local_runtime=local_runtime is not False,
            workspace_id=str(snapshot.get('workspace_id') or ''),
            root=root,
            directory_identity=str(snapshot.get('directory_identity') or ''),
            workspace_version=int(snapshot.get('workspace_version') or 0),
            permission_mode=str(snapshot.get('permission_mode') or 'always_ask'),
            permission_version=int(snapshot.get('permission_version') or 0),
            user_id=str(user_id or ''),
            conversation_id=str(conversation_id or ''),
            execution=MappingProxyType(identity),
            trusted_local=bool(trusted_local),
            active=bool(snapshot),
            cwd=root or cwd,
            opaque_tool_grants=frozenset(snapshot.get('opaque_tool_grants') or ()),
            workflow_full_trust=_WORKFLOW_FULL_TRUST.get(),
        )

    @classmethod
    def from_config(cls, config: Any, *, trusted_local=False):
        """Compatibility constructor for trusted internal subagent configuration."""
        config = config if isinstance(config, Mapping) else {}
        parents = [item for item in (config, config.get('parent_agentic_config')) if isinstance(item, Mapping)]
        snapshot = next((item.get(key) for item in parents for key in (
            '_core_workspace_context', 'workspace_context',
        ) if isinstance(item.get(key), Mapping) and item[key]), {})
        return cls.from_snapshot(
            snapshot,
            local_runtime=config.get('_core_local_runtime', next(
                (item['_core_local_runtime'] for item in parents if '_core_local_runtime' in item), True)),
            user_id=config.get('user_id') or next((item.get('user_id') for item in parents if item.get('user_id')), ''),
            conversation_id=(config.get('conversation_id')
                             or next((item.get('conversation_id') for item in parents
                                      if item.get('conversation_id')), '')),
            execution=config.get('_workspace_execution'),
            trusted_local=trusted_local,
        )


@dataclass(frozen=True)
class ToolResolutionContext:
    managed_roots: tuple[str, ...] = ()
    managed_files: frozenset[str] = frozenset()
    citation_state: dict = field(default_factory=dict)


def normalize_managed_roots(roots):
    return tuple(dict.fromkeys(os.path.realpath(root) for root in roots if root))


def normalize_managed_files(values):
    from lazymind.chat.service.utils.static_file_url import local_path_from_static_file_url

    files = set()
    for value in values:
        if isinstance(value, str):
            local = local_path_from_static_file_url(value)
            if not local and os.path.isabs(value):
                local = value
            if local:
                files.add(os.path.realpath(local))
    return frozenset(files)


_PERMISSION = ContextVar('workspace_permission_context', default=None)
_TOOL_RESOLUTION = ContextVar('tool_resolution_context', default=None)


def get_workspace_permission_context():
    return _PERMISSION.get()


def get_tool_resolution_context():
    return _TOOL_RESOLUTION.get()


@contextmanager
def workspace_permission_scope(context):
    token = _PERMISSION.set(context)
    try:
        # Tool execution can run on another thread. Restore the immutable run
        # state there so nested tools/SubAgents inherit this execution only.
        with workflow_execution_scope(context.workflow_full_trust if context else False):
            yield
    finally:
        _PERMISSION.reset(token)


@contextmanager
def tool_resolution_scope(context):
    token = _TOOL_RESOLUTION.set(context)
    try:
        yield
    finally:
        _TOOL_RESOLUTION.reset(token)


def canonical_host_path(value, default_root=None):
    if not isinstance(value, str) or '\0' in value:
        raise ValueError('invalid host file path')
    context = get_workspace_permission_context()
    root = default_root or (context.cwd if context else '') or os.getcwd()
    value = os.path.expanduser(value)
    return os.path.realpath(value if os.path.isabs(value) else os.path.join(root, value))
