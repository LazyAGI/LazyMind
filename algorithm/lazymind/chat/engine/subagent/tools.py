from __future__ import annotations

import json
import os
from typing import Any, Dict, List, Optional

from lazymind.chat.engine.tools.infra import handle_tool_errors, tool_success

from .context import require_context, LARGE_ARTIFACT_THRESHOLD

# Valid artifact content types.
_CONTENT_TYPES = {'text', 'json', 'image', 'file', 'file_list'}


def _build_artifact_value(value: Any, content_type: str) -> Dict[str, Any]:
    ctx = require_context()
    if content_type == 'text':
        text = str(value)
        # Offload large text to workspace filesystem.
        if len(text.encode('utf-8', errors='replace')) > LARGE_ARTIFACT_THRESHOLD:
            abs_path = ctx.write_large_content(text, hint='artifact_text')
            rel = os.path.relpath(abs_path, ctx.workspace_path)
            return {'type': 'file', 'path': rel, 'size': os.path.getsize(abs_path)}
        return {'text': text}
    if content_type == 'json':
        if isinstance(value, str):
            try:
                value = json.loads(value)
            except ValueError:
                pass
        serialized = json.dumps(value, ensure_ascii=False, default=str)
        # Offload large JSON to workspace filesystem.
        if len(serialized.encode('utf-8', errors='replace')) > LARGE_ARTIFACT_THRESHOLD:
            abs_path = ctx.write_large_content(serialized, hint='artifact_json')
            rel = os.path.relpath(abs_path, ctx.workspace_path)
            return {'type': 'file', 'path': rel, 'size': os.path.getsize(abs_path)}
        return {'data': value}
    if content_type == 'image':
        rel = ctx.copy_into_workspace(str(value)) if os.path.isabs(str(value)) else str(value)
        return {'path': rel}
    if content_type == 'file':
        abs_path = str(value)
        rel = ctx.copy_into_workspace(abs_path) if os.path.isabs(abs_path) else abs_path
        size = 0
        full = os.path.join(ctx.workspace_path, rel)
        if os.path.exists(full):
            size = os.path.getsize(full)
        return {'filename': os.path.basename(rel), 'path': rel, 'size': size}
    if content_type == 'file_list':
        items = value if isinstance(value, list) else [value]
        paths: List[str] = []
        for item in items:
            p = str(item)
            paths.append(ctx.copy_into_workspace(p) if os.path.isabs(p) else p)
        return {'paths': paths}
    return {'text': str(value)}


@handle_tool_errors
def save_artifact(key: str, value: Any, content_type: str = 'text',
                  source_tool: Optional[str] = None,
                  list_index: Optional[int] = None) -> Dict[str, Any]:
    """Save an output artifact produced by this SubAgent.

    File-type values must be local absolute paths; the framework copies them into the
    workspace and converts to relative paths. The same key may be saved multiple times
    (each call appends a row with an incremented seq), which is how variable-count outputs
    such as per-image generation are streamed to the frontend.

    For list-cardinality slots, pass list_index to overwrite a specific existing item
    instead of appending a new one (partial retry). Omit list_index for normal append.

    Args:
        key (str): Artifact key. Must be one of the declared output_artifact_keys.
        value (Any): The artifact value. For text: a string. For json: a dict/list.
            For image/file: a local absolute path. For file_list: a list of absolute paths.
        content_type (str): One of text, json, image, file, file_list. Default text.
        source_tool (str): Optional name of the tool that produced this artifact,
            e.g. 'web_search', 'wikipedia', 'image_generation'. Used for display only.
        list_index (int): Optional. When provided, signals that this artifact should
            replace the existing list slot entry at this position (0-based) rather than
            being appended as a new entry. Use for partial retries only.

    Returns:
        A confirmation that the artifact was saved.
    """
    ctx = require_context()
    ct = content_type if content_type in _CONTENT_TYPES else 'text'
    built = _build_artifact_value(value, ct)
    if source_tool:
        built['_source_tool'] = str(source_tool)
    if list_index is not None:
        built['list_index'] = int(list_index)
    seq = ctx.next_artifact_seq(key)
    ctx.record_local_artifact(key, ct, built, seq)
    ctx.emit({
        'type': 'artifact',
        'artifact_key': key,
        'content_type': ct,
        'seq': seq,
        'value': built,
    })
    return tool_success('save_artifact', {'status': 'ok', 'message': f"Artifact '{key}' saved."})


@handle_tool_errors
def get_artifact(key: str, task_ref: Optional[str] = None) -> Dict[str, Any]:
    """Read a previously saved artifact by key.

    Args:
        key (str): The artifact key to read.
        task_ref (str): Optional task reference (title / "the Nth" / type name). When omitted,
            reads the latest artifact with this key from the current task.

    Returns:
        The artifact content (text, file path, or JSON description).
    """
    ctx = require_context()
    rows = ctx.local_artifacts(keys=[key]) or ctx.db.load_artifacts(ctx.task_id, keys=[key])
    if not rows:
        return tool_success('get_artifact', {'status': 'empty', 'message': f"No artifact found for key '{key}'."})
    return tool_success('get_artifact', {'status': 'ok', 'key': key, 'artifacts': rows})


@handle_tool_errors
def list_artifacts(task_ref: Optional[str] = None) -> Dict[str, Any]:
    """List the artifact keys produced so far in the current task.

    Args:
        task_ref (str): Optional task reference; when omitted lists artifacts of the current task.

    Returns:
        A summary of available artifact keys and their content types.
    """
    ctx = require_context()
    rows = ctx.local_artifacts() or ctx.db.load_artifacts(ctx.task_id)
    summary: Dict[str, str] = {}
    for r in rows:
        summary[r['artifact_key']] = r['content_type']
    parts = [f'{k} ({v})' for k, v in summary.items()]
    msg = '可用成果：' + ('、'.join(parts) if parts else '（暂无）')
    return tool_success('list_artifacts', {'status': 'ok', 'keys': summary, 'message': msg})
