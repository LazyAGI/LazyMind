from __future__ import annotations

import json
import os
from typing import Any, Dict, List, Optional

from lazymind.chat.engine.tools.infra import handle_tool_errors, tool_success

from .context import require_context, LARGE_ARTIFACT_THRESHOLD

# Valid artifact content types.
_CONTENT_TYPES = {'text', 'json', 'image', 'file', 'file_list'}


def _build_artifact_value(value: Any, content_type: str):
    """Build the artifact value dict and return (value_dict, actual_content_type).

    actual_content_type is 'file' when the content is offloaded to the workspace
    filesystem (large text/json), so the DB content_type column correctly reflects
    the storage form.  The value dict then carries {"type": "<original_type>", "path": ...}
    so readers can recover the true render type via value["type"].
    """
    ctx = require_context()
    if content_type == 'text':
        text = str(value)
        if len(text.encode('utf-8', errors='replace')) > LARGE_ARTIFACT_THRESHOLD:
            abs_path = ctx.write_large_content(text, hint='artifact_text')
            rel = os.path.relpath(abs_path, ctx.workspace_path)
            return {'type': 'text', 'path': rel, 'size': os.path.getsize(abs_path)}, 'file'
        return {'text': text}, 'text'
    if content_type == 'json':
        if isinstance(value, str):
            try:
                value = json.loads(value)
            except ValueError:
                pass
        serialized = json.dumps(value, ensure_ascii=False, default=str)
        if len(serialized.encode('utf-8', errors='replace')) > LARGE_ARTIFACT_THRESHOLD:
            abs_path = ctx.write_large_content(serialized, hint='artifact_json')
            rel = os.path.relpath(abs_path, ctx.workspace_path)
            return {'type': 'json', 'path': rel, 'size': os.path.getsize(abs_path)}, 'file'
        return {'data': value}, 'json'
    if content_type == 'image':
        rel = ctx.copy_into_workspace(str(value)) if os.path.isabs(str(value)) else str(value)
        return {'path': rel}, 'image'
    if content_type == 'file':
        abs_path = str(value)
        rel = ctx.copy_into_workspace(abs_path) if os.path.isabs(abs_path) else abs_path
        size = 0
        full = os.path.join(ctx.workspace_path, rel)
        if os.path.exists(full):
            size = os.path.getsize(full)
        return {'filename': os.path.basename(rel), 'path': rel, 'size': size}, 'file'
    if content_type == 'file_list':
        items = value if isinstance(value, list) else [value]
        paths: List[str] = []
        for item in items:
            p = str(item)
            paths.append(ctx.copy_into_workspace(p) if os.path.isabs(p) else p)
        return {'paths': paths}, 'file_list'
    return {'text': str(value)}, 'text'


@handle_tool_errors
def save_artifact(key: str, value: Any, content_type: str = 'text',
                  source_tool: Optional[str] = None,
                  sort_order: Optional[int] = None,
                  caption: Optional[str] = None) -> Dict[str, Any]:
    """Save an output artifact produced by this SubAgent.

    File-type values must be local absolute paths; the framework copies them into the
    workspace and converts to relative paths. The same key may be saved multiple times
    (each call appends a row with an incremented seq), which is how variable-count outputs
    such as per-image generation are streamed to the frontend.

    For list-cardinality slots:
    - Omit sort_order to append a new item.
    - Pass sort_order=N (1-based) to overwrite the item currently at display position N
      (partial retry: only that position is replaced, others remain untouched).

    Args:
        key (str): Artifact key. Must be one of the declared output_artifact_keys.
        value (Any): The artifact value. For text: a string. For json: a dict/list.
            For image/file: a local absolute path. For file_list: a list of absolute paths.
        content_type (str): One of text, json, image, file, file_list. Default text.
        source_tool (str): Optional name of the tool that produced this artifact,
            e.g. 'web_search', 'wikipedia', 'image_generation'. Used for display only.
        sort_order (int): Optional. 1-based display position within a list slot. When
            provided, signals that this artifact should replace the existing item at that
            position rather than being appended. For single-cardinality slots this parameter
            is ignored. Do NOT pass list_index directly — always use sort_order.
        caption (str): Optional human-readable description for image/file artifacts.
            Stored in sub_agent_artifacts.caption and used in artifact_summary.

    Returns:
        A confirmation that the artifact was saved.
    """
    ctx = require_context()
    ct = content_type if content_type in _CONTENT_TYPES else 'text'
    built, actual_ct = _build_artifact_value(value, ct)
    if source_tool:
        built['_source_tool'] = str(source_tool)
    # Translate sort_order → list_index via Go core API.
    if sort_order is not None:
        list_index = _resolve_list_index_from_sort_order(key, sort_order)
        if list_index is not None:
            built['list_index'] = list_index
    if caption is not None:
        built['caption'] = str(caption)
    seq = ctx.next_artifact_seq(key)
    ctx.record_local_artifact(key, actual_ct, built, seq)
    ctx.emit({
        'type': 'artifact',
        'artifact_key': key,
        'content_type': actual_ct,
        'seq': seq,
        'value': built,
    })
    return tool_success('save_artifact', {'status': 'ok', 'message': f"Artifact '{key}' saved."})


def _resolve_list_index_from_sort_order(artifact_key: str, sort_order: int) -> Optional[int]:
    """Query Go core to translate sort_order → list_index for a list-slot artifact.

    Returns the list_index integer, or None on any error (in which case the artifact
    is appended as a new item rather than overwriting an existing one).
    """
    try:
        import httpx
        import lazyllm
        from lazymind.config import config as _cfg
        cfg = {}
        try:
            cfg = lazyllm.globals.get('agentic_config') or {}
        except Exception:
            pass
        session_id: str = cfg.get('plugin_session_id', '')
        if not session_id:
            return None
        # Look up slot_id from plugin_loader via artifact_key.
        plugin_id: str = cfg.get('plugin_id', '')
        if not plugin_id:
            return None
        from lazymind.chat.plugin import plugin_loader
        spec = plugin_loader.get_plugin(plugin_id)
        if not spec:
            return None
        slot_def = spec.get_slot_for_artifact_key(artifact_key)
        if not slot_def:
            return None
        slot_id = slot_def.get('id', '')
        if not slot_id:
            return None
        core_url = str(_cfg['core_api_url']).rstrip('/')
        url = (
            f'{core_url}/plugin-sessions/{session_id}'
            f'/slots/{slot_id}/order'
        )
        resp = httpx.get(url, timeout=3.0)
        if resp.status_code != 200:
            return None
        order_list: list = resp.json().get('data', {}).get('order_list', [])
        if not order_list or sort_order < 1 or sort_order > len(order_list):
            return None
        return int(order_list[sort_order - 1])
    except Exception:
        return None


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


@handle_tool_errors
def list_knowledge_bases() -> Dict[str, Any]:
    """List knowledge bases accessible to the current user.

    Returns a list of knowledge bases (id / name / type / tags) that can be
    passed to the kb_search tool.  Use this when you need to discover which
    knowledge bases exist before performing a search.

    Returns:
        A list of knowledge base summaries, each with id, name, type, and tags.
    """
    try:
        import httpx
        import lazyllm
        from lazymind.config import config as _cfg
        # Pick up user_id from agentic_config (injected by Go via X-User-Id).
        cfg: Dict[str, Any] = {}
        try:
            cfg = lazyllm.globals.get('agentic_config') or {}
        except Exception:
            pass
        user_id: str = cfg.get('user_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        headers = {}
        if user_id:
            headers['X-User-Id'] = user_id
        resp = httpx.get(f'{core_url}/kb/list', headers=headers, timeout=5.0)
        if resp.status_code != 200:
            return tool_success('list_knowledge_bases', {
                'status': 'error',
                'message': f'Failed to list knowledge bases: HTTP {resp.status_code}',
                'items': [],
            })
        # Go /kb/list returns {"code":0,"data":{"total":N,"list":[{id,name,visibility,...}]}}
        data = resp.json().get('data') or {}
        raw_items = data.get('list') or []
        simplified = [
            {
                'id': kb.get('id', ''),
                'name': kb.get('name', ''),
                'visibility': kb.get('visibility', ''),
                'permissions': kb.get('permissions', []),
            }
            for kb in raw_items
        ]
        return tool_success('list_knowledge_bases', {
            'status': 'ok',
            'message': f'Found {len(simplified)} knowledge base(s).',
            'items': simplified,
        })
    except Exception as e:
        return tool_success('list_knowledge_bases', {
            'status': 'error',
            'message': f'list_knowledge_bases failed: {e}',
            'items': [],
        })
