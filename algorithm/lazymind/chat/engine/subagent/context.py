from __future__ import annotations

import hashlib
import json
import os
import shutil
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional, Tuple

import lazyllm

from .db import SubAgentDB

# Key under which the SubAgent execution context is stored in lazyllm.globals.
_CTX_KEY = 'subagent_ctx'

# Artifacts larger than this byte threshold are offloaded to the workspace filesystem.
# value persisted to DB / emitted over SSE becomes a {"type": "file", "path": "..."} reference.
LARGE_ARTIFACT_THRESHOLD = 32 * 1024  # 32 KB

# Tool results larger than this are truncated before being fed back to the LLM.
# The full content is written to a workspace file; the LLM receives the path + a size hint.
LARGE_TOOL_RESULT_THRESHOLD = 16 * 1024  # 16 KB


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

    # ------------------------------------------------------------------
    # Draft file management
    # Draft files live under workspace/drafts/ and store the in-progress
    # text content of an artifact between patch_artifact calls.
    # A sidecar .meta file holds the original content_type ('text'/'json').
    # ------------------------------------------------------------------

    def _drafts_dir(self) -> str:
        return os.path.join(self.workspace_path, 'drafts')

    def draft_path(self, key: str, list_index: Optional[int] = None) -> str:
        """Return the absolute path of the draft file for *key*."""
        name = f'{key}_{list_index}.draft' if list_index is not None else f'{key}.draft'
        return os.path.join(self._drafts_dir(), name)

    def _meta_path(self, key: str, list_index: Optional[int] = None) -> str:
        name = f'{key}_{list_index}.draft.meta' if list_index is not None else f'{key}.draft.meta'
        return os.path.join(self._drafts_dir(), name)

    def read_draft(self, key: str, list_index: Optional[int] = None) -> Optional[Tuple[str, str]]:
        """Return *(content, original_type)* or None if no draft exists."""
        path = self.draft_path(key, list_index)
        meta = self._meta_path(key, list_index)
        if not os.path.exists(path):
            return None
        with open(path, 'r', encoding='utf-8') as fh:
            content = fh.read()
        original_type = 'text'
        if os.path.exists(meta):
            try:
                original_type = json.loads(open(meta).read()).get('original_type', 'text')
            except Exception:
                pass
        return content, original_type

    def write_draft(self, key: str, original_type: str, content: str,
                    list_index: Optional[int] = None) -> None:
        """Write *content* to the draft file and record *original_type* in the sidecar."""
        os.makedirs(self._drafts_dir(), exist_ok=True)
        with open(self.draft_path(key, list_index), 'w', encoding='utf-8') as fh:
            fh.write(content)
        with open(self._meta_path(key, list_index), 'w', encoding='utf-8') as fh:
            json.dump({'original_type': original_type}, fh)

    def delete_draft(self, key: str, list_index: Optional[int] = None) -> None:
        """Delete the draft file and its sidecar (silent if missing)."""
        for p in (self.draft_path(key, list_index), self._meta_path(key, list_index)):
            try:
                os.remove(p)
            except FileNotFoundError:
                pass

    def list_pending_drafts(self) -> List[Tuple[str, str, str]]:
        """Return *(key, original_type, content)* for every draft that still exists."""
        drafts_dir = self._drafts_dir()
        if not os.path.isdir(drafts_dir):
            return []
        results = []
        for name in os.listdir(drafts_dir):
            if not name.endswith('.draft'):
                continue
            stem = name[:-len('.draft')]
            # Reconstruct key (and optional list_index) from filename.
            # Filenames: {key}.draft  or  {key}_{list_index}.draft
            # We only need key for the flush call; list_index is encoded in the name.
            key = stem
            path = os.path.join(drafts_dir, name)
            meta_path = path + '.meta'
            original_type = 'text'
            if os.path.exists(meta_path):
                try:
                    original_type = json.loads(open(meta_path).read()).get('original_type', 'text')
                except Exception:
                    pass
            try:
                with open(path, 'r', encoding='utf-8') as fh:
                    content = fh.read()
            except OSError:
                continue
            results.append((key, original_type, content))
        return results

    def ensure_workspace(self) -> None:
        if self.workspace_path:
            os.makedirs(self.workspace_path, exist_ok=True)

    def write_large_content(self, content: str, hint: str = 'content') -> str:
        """Write *content* to a file in the workspace and return the absolute path.

        The filename is derived from a short SHA-256 of the content so that
        identical content is deduplicated automatically.  The file is stored
        under ``<workspace_path>/large/<hint>_<sha8>.txt``.

        Returns the absolute path to the written file.
        Raises ``RuntimeError`` if the workspace path is not set.
        """
        if not self.workspace_path:
            raise RuntimeError('workspace_path is not set — cannot offload large content')
        sha8 = hashlib.sha256(content.encode('utf-8', errors='replace')).hexdigest()[:8]
        large_dir = os.path.join(self.workspace_path, 'large')
        os.makedirs(large_dir, exist_ok=True)
        safe_hint = ''.join(c if c.isalnum() or c in '-_' else '_' for c in hint)[:40]
        filename = f'{safe_hint}_{sha8}.txt'
        abs_path = os.path.join(large_dir, filename)
        if not os.path.exists(abs_path):
            with open(abs_path, 'w', encoding='utf-8') as fh:
                fh.write(content)
        return abs_path

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
