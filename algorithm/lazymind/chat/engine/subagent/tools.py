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

    ## sort_order: append vs. overwrite

    For list-cardinality slots (e.g. a list of reference images):

    - **Omit sort_order** (or pass None): append a brand-new item at the end of the list.
      Use this for normal full runs or when adding new content.

    - **Pass sort_order=N** (1-based display position): overwrite the item currently
      shown at position N in the UI. The existing item is replaced; nothing else changes.
      Use this whenever the user's instruction targets a specific item, for example:
        - "重新收集第二张图" → sort_order=2
        - "替换第三张参考图" → sort_order=3
        - "第一张和第三张都重新生成" → call save_artifact twice: sort_order=1, sort_order=3
      Do NOT pass list_index directly — always use sort_order (1-based visual position).

    For single-cardinality slots the sort_order parameter is ignored (single slots
    always overwrite the one existing value).

    ## IMPORTANT: obey the user's stated intent
    If the objective or runtime_instruction says "overwrite item N", "replace the Nth",
    or similar, you MUST pass sort_order=N. Omitting sort_order in that case appends
    a new item and leaves the original untouched, which is wrong.

    Args:
        key (str): Artifact key. Must be one of the declared output_artifact_keys.
        value (Any): The artifact value. For text: a string. For json: a dict/list.
            For image/file: a local absolute path. For file_list: a list of absolute paths.
        content_type (str): One of text, json, image, file, file_list. Default text.
        source_tool (str): Optional name of the tool that produced this artifact,
            e.g. 'web_search', 'wikipedia', 'image_generation'. Used for display only.
        sort_order (int): Optional. 1-based display position within a list slot.
            **1 = first item, 2 = second item, etc.**
            Omit (or pass None) to append; pass N to overwrite position N.
            If N is out of range, the artifact is appended and a warning is returned.
            See the sort_order section above for full guidance.
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
    out_of_range_warning: Optional[str] = None
    if sort_order is not None:
        list_index, resolve_err = _resolve_list_index_from_sort_order(key, sort_order)
        if list_index is not None:
            built['list_index'] = list_index
        elif resolve_err:
            out_of_range_warning = resolve_err
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
    msg = f"Artifact '{key}' saved."
    if out_of_range_warning:
        msg += f' WARNING: {out_of_range_warning}'
    return tool_success('save_artifact', {'status': 'ok', 'message': msg})


def _resolve_list_index_from_sort_order(
    artifact_key: str, sort_order: int
) -> tuple[Optional[int], Optional[str]]:
    """Query Go core to translate sort_order → list_index for a list-slot artifact.

    Returns (list_index, None) on success, or (None, error_message) when sort_order
    is out of range. Returns (None, None) on technical errors or non-list slots
    (caller should silently append in those cases).
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
            return None, None
        # Look up slot_id from plugin_loader via artifact_key.
        plugin_id: str = cfg.get('plugin_id', '')
        if not plugin_id:
            return None, None
        from lazymind.chat.plugin import plugin_loader
        spec = plugin_loader.get_plugin(plugin_id)
        if not spec:
            return None, None
        slot_def = spec.get_slot_for_artifact_key(artifact_key)
        if not slot_def:
            return None, None
        slot_id = slot_def.get('id', '')
        if not slot_id:
            return None, None
        core_url = str(_cfg['core_api_url']).rstrip('/')
        url = (
            f'{core_url}/plugin-sessions/{session_id}'
            f'/slots/{slot_id}/order'
        )
        resp = httpx.get(url, timeout=3.0)
        if resp.status_code != 200:
            return None, None
        order_list: list = resp.json().get('data', {}).get('order_list', [])
        if not order_list:
            # Single-cardinality slot — sort_order is meaningless, ignore silently.
            return None, None
        n = len(order_list)
        if sort_order < 1:
            return None, (
                f'sort_order must be >= 1 (sort_order is 1-based, where 1 is the first item). '
                f'Received sort_order={sort_order}. Artifact appended as a new item instead.'
            )
        if sort_order > n:
            return None, (
                f'sort_order={sort_order} is out of range — the list currently has {n} item(s) '
                f'(valid range: 1–{n}). Artifact appended as a new item instead. '
                f'If you intended to overwrite, use a sort_order between 1 and {n}.'
            )
        return int(order_list[sort_order - 1]), None
    except Exception:
        return None, None


@handle_tool_errors
def get_artifact(key: str, sort_order: Optional[int] = None, task_ref: Optional[str] = None) -> Dict[str, Any]:
    """Read a previously saved artifact by key.

    Args:
        key (str): The artifact key to read.
        sort_order (int): Optional. 1-based display position within a list slot.
            For plugin sessions: resolves via plugin_slot_order → fetches that specific
            selected revision (human or AI).
            For ordinary SubAgents: treated as seq, returns the artifact at that position.
            When omitted, returns all artifacts for this key (or the latest for single slots).
        task_ref (str): Optional task reference (title / "the Nth" / type name). When omitted,
            reads the latest artifact with this key from the current task.

    Returns:
        The artifact content (text, file path, or JSON description).
    """
    ctx = require_context()

    # Plugin session: resolve sort_order via DB lookup.
    try:
        import lazyllm
        cfg: Dict[str, Any] = {}
        try:
            cfg = lazyllm.globals.get('agentic_config') or {}
        except Exception:
            pass
        plugin_session_id: str = cfg.get('plugin_session_id', '')
    except Exception:
        plugin_session_id = ''

    if plugin_session_id and sort_order is not None:
        return _get_plugin_artifact_by_sort_order(ctx, key, plugin_session_id, sort_order)

    if plugin_session_id and sort_order is None:
        return _get_plugin_artifact_all(ctx, key, plugin_session_id)

    # Ordinary SubAgent: read from sub_agent_artifacts.
    if sort_order is not None:
        rows = ctx.local_artifacts(keys=[key]) or ctx.db.load_artifacts(ctx.task_id, keys=[key])
        matched = [r for r in rows if r.get('seq') == sort_order]
        if matched:
            return tool_success('get_artifact', {'status': 'ok', 'key': key, 'artifacts': matched})
        return tool_success('get_artifact', {
            'status': 'empty',
            'message': f"No artifact found for key '{key}' at sort_order={sort_order}.",
        })

    rows = ctx.local_artifacts(keys=[key]) or ctx.db.load_artifacts(ctx.task_id, keys=[key])
    if not rows:
        return tool_success('get_artifact', {'status': 'empty', 'message': f"No artifact found for key '{key}'."})
    return tool_success('get_artifact', {'status': 'ok', 'key': key, 'artifacts': rows})


def _get_plugin_artifact_by_sort_order(
    ctx: Any, key: str, session_id: str, sort_order: int
) -> Dict[str, Any]:
    """Fetch a single plugin slot artifact by sort_order via DB resolve."""
    row = ctx.db.load_slot_artifact_by_sort_order(session_id, key, sort_order)
    if row is None:
        return tool_success('get_artifact', {
            'status': 'empty',
            'message': (
                f"No artifact found for key '{key}' at sort_order={sort_order} "
                f'in plugin session {session_id}.'
            ),
        })

    value, content_type = ctx.db.resolve_slot_revision_value(row)
    if value is None:
        return tool_success('get_artifact', {
            'status': 'empty',
            'message': f"Artifact key '{key}' at sort_order={sort_order} resolved to null value.",
        })
    return tool_success('get_artifact', {
        'status': 'ok',
        'key': key,
        'sort_order': sort_order,
        'content_type': content_type,
        'artifacts': [{'artifact_key': key, 'content_type': content_type, 'value': value, 'sort_order': sort_order}],
    })


def _get_plugin_artifact_all(ctx: Any, key: str, session_id: str) -> Dict[str, Any]:
    """Return all selected revisions for a plugin slot key (sort_order=None)."""
    resolved_rows = ctx.db.load_selected_slot_artifacts_resolved_with_order(session_id)
    artifacts = [
        {
            'artifact_key': r['artifact_key'],
            'content_type': r.get('content_type'),
            'value': r['value'],
            'sort_order': r.get('sort_order'),
        }
        for r in resolved_rows
        if r.get('artifact_key') == key
    ]
    if not artifacts:
        return tool_success('get_artifact', {
            'status': 'empty',
            'message': f"No artifact found for key '{key}' in plugin session {session_id}.",
        })
    return tool_success('get_artifact', {'status': 'ok', 'key': key, 'artifacts': artifacts})


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


@handle_tool_errors
def read_user_attachment(filename: str) -> Dict[str, Any]:
    """Read the contents of a file previously uploaded by the user in this conversation.

    The list of available files is shown in the system prompt under '## Attached Files'.
    Use this tool to read a file's content when the user asks about it or when the task
    requires processing the file.

    Args:
        filename (str): The filename (basename) or partial path of the attachment to read.
            Must match one of the files listed in the system prompt.

    Returns:
        The file content as text, or a confirmation message with the absolute path for
        binary/image files that should be passed to other tools (e.g. vision_extractor).
    """
    try:
        import lazyllm
        cfg: Dict[str, Any] = {}
        try:
            cfg = lazyllm.globals.get('agentic_config') or {}
        except Exception:
            pass
        files: List[str] = cfg.get('files') or []
        if not files:
            return tool_success('read_user_attachment', {
                'status': 'error',
                'message': 'No attached files found in this conversation.',
            })
        # Match by basename or suffix.
        target = filename.strip()
        matched: Optional[str] = None
        for path in files:
            if os.path.basename(path) == target or path.endswith(target) or target in path:
                matched = path
                break
        if matched is None:
            available = [os.path.basename(p) for p in files]
            return tool_success('read_user_attachment', {
                'status': 'error',
                'message': (
                    f"File '{target}' not found in attached files. "
                    f"Available: {', '.join(available)}"
                ),
            })
        if not os.path.exists(matched):
            return tool_success('read_user_attachment', {
                'status': 'error',
                'message': f"File '{target}' was found in the index but is no longer on disk.",
            })
        # Binary / image files: return path only (caller should use vision_extractor etc.).
        binary_exts = {'.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp', '.pdf', '.zip'}
        ext = os.path.splitext(matched)[1].lower()
        if ext in binary_exts:
            return tool_success('read_user_attachment', {
                'status': 'ok',
                'filename': os.path.basename(matched),
                'path': matched,
                'message': (
                    f"Binary file '{os.path.basename(matched)}' is available at the above path. "
                    'Pass the path to an appropriate tool (e.g. vision_extractor for images).'
                ),
            })
        # Text file: read content.
        try:
            with open(matched, 'r', encoding='utf-8', errors='replace') as fh:
                content = fh.read()
        except OSError as e:
            return tool_success('read_user_attachment', {
                'status': 'error',
                'message': f"Could not read '{os.path.basename(matched)}': {e}",
            })
        return tool_success('read_user_attachment', {
            'status': 'ok',
            'filename': os.path.basename(matched),
            'content': content,
        })
    except Exception as e:
        return tool_success('read_user_attachment', {
            'status': 'error',
            'message': f'read_user_attachment failed: {e}',
        })
