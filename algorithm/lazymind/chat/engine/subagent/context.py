from __future__ import annotations

import os
import shutil
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional

import lazyllm

from .db import SubAgentDB

# Key under which the SubAgent execution context is stored in lazyllm.globals.
_CTX_KEY = 'subagent_ctx'


@dataclass
class SubAgentContext:
    task_id: str
    conversation_id: str
    agent_type: str
    objective: str
    params: Dict[str, Any]
    workspace_path: str
    input_artifact_keys: List[str]
    output_artifact_keys: List[str]
    db: SubAgentDB
    emit: Callable[[Dict[str, Any]], None]
    # artifact seq counters and local cache (Go persists to DB; this serves intra-task reads).
    _artifact_counts: Dict[str, int] = field(default_factory=dict)
    _local_artifacts: List[Dict[str, Any]] = field(default_factory=list)

    def next_artifact_seq(self, key: str) -> int:
        self._artifact_counts[key] = self._artifact_counts.get(key, 0) + 1
        return self._artifact_counts[key]

    def record_local_artifact(self, key: str, content_type: str, value: Dict[str, Any], seq: int) -> None:
        self._local_artifacts.append({
            'artifact_key': key, 'content_type': content_type, 'value': value, 'seq': seq,
        })

    def local_artifacts(self, keys: Optional[List[str]] = None) -> List[Dict[str, Any]]:
        if keys is None:
            return list(self._local_artifacts)
        keyset = set(keys)
        return [a for a in self._local_artifacts if a['artifact_key'] in keyset]

    def saved_keys(self) -> List[str]:
        return list(self._artifact_counts.keys())

    def ensure_workspace(self) -> None:
        if self.workspace_path:
            os.makedirs(self.workspace_path, exist_ok=True)

    def relativize(self, abs_path: str) -> str:
        if not abs_path:
            return abs_path
        try:
            return os.path.relpath(abs_path, self.workspace_path)
        except ValueError:
            return abs_path

    def copy_into_workspace(self, src_abs_path: str) -> str:
        self.ensure_workspace()
        filename = os.path.basename(src_abs_path)
        dst = os.path.join(self.workspace_path, filename)
        if os.path.abspath(src_abs_path) != os.path.abspath(dst):
            shutil.copy2(src_abs_path, dst)
        return self.relativize(dst)


def set_context(ctx: SubAgentContext) -> None:
    lazyllm.globals[_CTX_KEY] = ctx


def get_context() -> Optional[SubAgentContext]:
    try:
        return lazyllm.globals[_CTX_KEY]
    except Exception:
        return None


def require_context() -> SubAgentContext:
    ctx = get_context()
    if ctx is None:
        raise RuntimeError('SubAgent context is not initialized for this session.')
    return ctx
