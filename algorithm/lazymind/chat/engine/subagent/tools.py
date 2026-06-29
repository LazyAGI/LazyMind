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
        src = str(value)
        if os.path.isabs(src):
            # Copy into workspace; keep absolute path so Go core can sign a URL for it.
            dst_rel = ctx.copy_into_workspace(src)
            dst_abs = os.path.join(ctx.workspace_path, dst_rel)
            return {'path': dst_abs}, 'image'
        return {'path': src}, 'image'
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
    # Write draft so patch_artifact can operate on the latest committed content.
    # list_index is embedded in built when sort_order resolved successfully.
    _write_artifact_draft(ctx, key, ct, actual_ct, built, built.get('list_index'))
    msg = f"Artifact '{key}' saved."
    if out_of_range_warning:
        msg += f' WARNING: {out_of_range_warning}'
    return tool_success('save_artifact', {'status': 'ok', 'message': msg})


def _write_artifact_draft(
    ctx: Any, key: str, original_type: str, actual_ct: str,
    built: Dict[str, Any], list_index: Optional[int],
) -> None:
    """Persist the just-saved artifact content as a draft file for future patch_artifact calls.

    Only text and json artifacts are drafted (images/files are not patchable).
    For large artifacts the content lives in a workspace file; we read it back
    and write it to the draft directory so patches operate on plain text.
    """
    if original_type not in ('text', 'json'):
        return
    try:
        if actual_ct == 'file':
            # Large artifact: content stored in workspace/large/*.txt
            rel_path = built.get('path', '')
            if not rel_path:
                return
            abs_path = os.path.join(ctx.workspace_path, rel_path)
            if not os.path.exists(abs_path):
                return
            with open(abs_path, 'r', encoding='utf-8') as fh:
                content = fh.read()
        elif original_type == 'text':
            content = built.get('text', '')
        else:  # json, DB-inline
            content = json.dumps(built.get('data', ''), ensure_ascii=False)
        ctx.write_draft(key, original_type, content, list_index)
    except Exception:
        pass  # draft write failure is non-fatal


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
def get_artifact(key: str, sort_order: Optional[int] = None, task_ref: Optional[str] = None,
                 start_line: Optional[int] = None, end_line: Optional[int] = None) -> Dict[str, Any]:
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
        start_line (int): Optional. 1-based line number to start reading from (inclusive).
            When specified together with end_line, returns only the selected line range.
            The response also includes total_lines for the full content.
        end_line (int): Optional. 1-based line number to stop reading at (inclusive).
            When omitted but start_line is given, reads to the end of the file.

    Returns:
        The artifact content (text, file path, or JSON description).
        When start_line/end_line are given, returns a line-range view with total_lines.
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
        result = _get_plugin_artifact_by_sort_order(ctx, key, plugin_session_id, sort_order)
    elif plugin_session_id and sort_order is None:
        result = _get_plugin_artifact_all(ctx, key, plugin_session_id)
    elif sort_order is not None:
        # Ordinary SubAgent: read from sub_agent_artifacts.
        rows = ctx.local_artifacts(keys=[key]) or ctx.db.load_artifacts(ctx.task_id, keys=[key])
        matched = [r for r in rows if r.get('seq') == sort_order]
        if matched:
            result = tool_success('get_artifact', {'status': 'ok', 'key': key, 'artifacts': matched})
        else:
            return tool_success('get_artifact', {
                'status': 'empty',
                'message': f"No artifact found for key '{key}' at sort_order={sort_order}.",
            })
    else:
        rows = ctx.local_artifacts(keys=[key]) or ctx.db.load_artifacts(ctx.task_id, keys=[key])
        if not rows:
            return tool_success('get_artifact', {'status': 'empty', 'message': f"No artifact found for key '{key}'."})
        result = tool_success('get_artifact', {'status': 'ok', 'key': key, 'artifacts': rows})

    # Apply draft overlay and line-range slicing.
    if start_line is not None or end_line is not None:
        return _apply_line_range(ctx, key, result, start_line, end_line)
    return _apply_draft_overlay(ctx, key, result)


def _resolve_artifact_text(
    ctx: Any, key: str, sort_order: Optional[int] = None
) -> tuple[Optional[str], str]:
    """Return *(text_content, original_type)* for the currently authoritative version of *key*.

    Resolution priority:
    1. Draft file (reflects latest patch_artifact edits in this step).
    2. Plugin session selected revision via load_slot_artifact_by_sort_order —
       this is the ONLY path that surfaces human-edited artifacts stored in
       plugin_human_artifacts; must be checked before sub_agent_artifacts.
    3. Local in-memory cache (same step, sub_agent_artifacts).
    4. DB sub_agent_artifacts (previous steps).

    Returns (None, 'text') when no content is found.
    """
    # 1. Draft takes priority.
    draft = ctx.read_draft(key)
    if draft is not None:
        return draft[0], draft[1]

    # 2. Plugin session: use the full resolution chain that knows about human edits.
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

    if plugin_session_id:
        so = sort_order if sort_order is not None else 1
        row = ctx.db.load_slot_artifact_by_sort_order(plugin_session_id, key, so)
        if row is not None:
            value, content_type = ctx.db.resolve_slot_revision_value(row)
            if value is not None:
                original_type = 'json' if content_type == 'json' else 'text'
                if content_type == 'file':
                    original_type = value.get('type', 'text')
                text = _extract_text_from_value(ctx, value, original_type)
                if text is not None:
                    return text, original_type

    # 3. Local in-memory cache (same step).
    rows = ctx.local_artifacts(keys=[key])
    if not rows:
        # 4. DB sub_agent_artifacts.
        rows = ctx.db.load_artifacts(ctx.task_id, keys=[key])
    if not rows:
        return None, 'text'

    last_row = rows[-1]
    value = last_row.get('value') or {}
    ct = last_row.get('content_type', 'text')
    original_type = 'json' if ct == 'json' else 'text'
    if ct == 'file':
        original_type = value.get('type', 'text')
    text = _extract_text_from_value(ctx, value, original_type)
    return text, original_type


def _extract_text_from_value(ctx: Any, value: Dict[str, Any], original_type: str) -> Optional[str]:
    """Extract plain text from an artifact value dict (inline or file-stored)."""
    if 'text' in value:
        return value['text']
    if 'data' in value:
        return json.dumps(value['data'], ensure_ascii=False)
    # File-stored large artifact.
    rel_path = value.get('path', '')
    if rel_path and ctx.workspace_path:
        abs_path = os.path.join(ctx.workspace_path, rel_path)
        if os.path.exists(abs_path):
            try:
                with open(abs_path, 'r', encoding='utf-8') as fh:
                    return fh.read()
            except OSError:
                pass
    return None


def _apply_draft_overlay(ctx: Any, key: str, result: Dict[str, Any]) -> Dict[str, Any]:
    """If a draft exists for *key*, replace the artifact text in *result* with draft content."""
    draft = ctx.read_draft(key)
    if draft is None:
        return result
    content, original_type = draft
    data = result.get('data') or {}
    artifacts = data.get('artifacts') or []
    if not artifacts:
        return result
    # Replace content in the last artifact entry.
    last = dict(artifacts[-1])
    value = dict(last.get('value') or {})
    if original_type == 'json':
        try:
            value['data'] = json.loads(content)
        except Exception:
            value['text'] = content
    else:
        value['text'] = content
        value.pop('path', None)
        value.pop('size', None)
    last['value'] = value
    last['_from_draft'] = True
    new_artifacts = artifacts[:-1] + [last]
    new_data = dict(data)
    new_data['artifacts'] = new_artifacts
    return dict(result, data=new_data)


def _apply_line_range(
    ctx: Any, key: str, result: Dict[str, Any],
    start_line: Optional[int], end_line: Optional[int],
) -> Dict[str, Any]:
    """Slice the artifact text to the requested line range and return a compact response.

    Bypasses LARGE_TOOL_RESULT_THRESHOLD — callers request a small slice explicitly.
    Always returns total_lines so the model can plan subsequent reads.
    """
    text, _ = _resolve_artifact_text(ctx, key)
    if text is None:
        return result  # fall back to unsliced result if we can't read content

    lines = text.splitlines(keepends=True)
    total = len(lines)
    sl = max(1, start_line) if start_line is not None else 1
    el = min(total, end_line) if end_line is not None else total
    slice_content = ''.join(lines[sl - 1:el])
    return tool_success('get_artifact', {
        'status': 'ok',
        'key': key,
        'content': slice_content,
        'start_line': sl,
        'end_line': el,
        'total_lines': total,
        '_from_draft': ctx.read_draft(key) is not None,
    })


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
def patch_artifact(
    key: str,
    patch: Any,
    patch_type: str = 'str_replace',
    sort_order: Optional[int] = None,
) -> Dict[str, Any]:
    """Apply a local patch to a previously saved artifact without committing a new revision.

    Edits are written to a draft file in the workspace. Call save_artifact when you are
    done patching to commit the changes and produce a new revision. If you do not call
    save_artifact, the framework will auto-flush all pending drafts when the step ends.

    Use patch_artifact for targeted edits (fix a paragraph, update a field). Use
    save_artifact directly when rewriting the whole artifact from scratch.

    To discard all uncommitted edits and revert to the last saved version, call
    discard_draft(key).

    Args:
        key (str): The artifact key to patch.
        patch (Any): The patch payload — format depends on patch_type:
            - str_replace: {"old_str": "exact original text", "new_str": "replacement"}
            - json_merge:  {"field": "new_value", "obsolete": None}  (RFC 7396; None = delete)
            - json_patch:  [{"op": "replace", "path": "/items/0/status", "value": "done"}] (RFC 6902)
        patch_type (str): One of str_replace (default), json_merge, json_patch.
        sort_order (int): 1-based display position for list-cardinality slots.
            Required when the slot has list cardinality; omit for single-cardinality slots.

    Returns:
        Confirmation of success, or an error with instructions for recovery.
    """
    ctx = require_context()

    # Resolve list_index from sort_order when provided.
    list_index: Optional[int] = None
    if sort_order is not None:
        list_index, resolve_err = _resolve_list_index_from_sort_order(key, sort_order)
        if resolve_err:
            return tool_success('patch_artifact', {'status': 'error', 'message': resolve_err})

    # Load draft or initialize from latest committed content.
    draft_result = ctx.read_draft(key, list_index)
    if draft_result is None:
        # Auto-initialize draft from latest committed artifact.
        # Plugin sessions: this path also checks plugin_human_artifacts (human edits).
        text, original_type = _resolve_artifact_text(ctx, key, sort_order)
        if text is None:
            return tool_success('patch_artifact', {
                'status': 'error',
                'message': (
                    f"No committed content found for artifact '{key}'. "
                    'Call save_artifact first to create the artifact before patching.'
                ),
            })
        ctx.write_draft(key, original_type, text, list_index)
        draft_result = (text, original_type)

    content, original_type = draft_result

    if patch_type == 'str_replace':
        new_content, err = _apply_str_replace(content, patch)
    elif patch_type == 'json_merge':
        new_content, err = _apply_json_merge(content, patch)
    elif patch_type == 'json_patch':
        new_content, err = _apply_json_patch(content, patch)
    else:
        return tool_success('patch_artifact', {
            'status': 'error',
            'message': f"Unknown patch_type '{patch_type}'. Use str_replace, json_merge, or json_patch.",
        })

    if err:
        return tool_success('patch_artifact', {'status': 'error', 'message': err})

    ctx.write_draft(key, original_type, new_content, list_index)
    lines_changed = abs(new_content.count('\n') - content.count('\n'))
    return tool_success('patch_artifact', {
        'status': 'ok',
        'message': (
            f"Draft for '{key}' updated ({lines_changed} line(s) changed). "
            'Call save_artifact to commit, or keep patching. '
            'The framework will auto-commit on step end if you forget.'
        ),
    })


def _normalize_text(text: str) -> str:
    """Normalize punctuation and whitespace for fuzzy matching fallback."""
    _FULL_HALF = str.maketrans(
        '\uff0c\u3002\u201c\u201d\u2018\u2019\uff08\uff09\u300a\u300b',
        ',.""\'\'\u0028\u0029<>',
    )
    result = []
    for line in text.splitlines(keepends=True):
        normalized = ' '.join(line.translate(_FULL_HALF).split())
        # Preserve trailing newline if original line had one.
        if line.endswith('\n'):
            normalized += '\n'
        result.append(normalized)
    return ''.join(result)


def _apply_str_replace(content: str, patch: Any) -> tuple[Optional[str], Optional[str]]:
    """Apply str_replace patch. Returns (new_content, error_message)."""
    if not isinstance(patch, dict):
        return None, 'patch must be a dict with old_str and new_str keys.'
    old_str = patch.get('old_str', '')
    new_str = patch.get('new_str', '')
    if not old_str:
        return None, 'patch.old_str must not be empty.'

    # Layer 1: exact match.
    count = content.count(old_str)
    if count == 1:
        return content.replace(old_str, new_str, 1), None
    if count > 1:
        return None, (
            f'old_str matches {count} locations — it must be unique. '
            'Expand old_str with more surrounding context to make it unique, then retry.'
        )

    # Layer 2: normalize both sides and match.
    norm_content = _normalize_text(content)
    norm_old = _normalize_text(old_str)
    if norm_old and norm_content.count(norm_old) == 1:
        idx = norm_content.find(norm_old)
        # Map normalized index back to original content character position.
        # Walk both strings in sync until we reach idx in the normalized version.
        orig_idx = _map_norm_index_to_orig(content, norm_content, idx)
        orig_end = _map_norm_index_to_orig(content, norm_content, idx + len(norm_old))
        return content[:orig_idx] + new_str + content[orig_end:], None

    return None, (
        'old_str not found in the current draft content. '
        'Call get_artifact to read the current content, then construct old_str from the actual text.'
    )


def _map_norm_index_to_orig(orig: str, norm: str, norm_idx: int) -> int:
    """Map a character index in the normalized string back to the original string."""
    o, n = 0, 0
    while n < norm_idx and o < len(orig):
        o += 1
        n = len(_normalize_text(orig[:o]))
    return o


def _apply_json_merge(content: str, patch: Any) -> tuple[Optional[str], Optional[str]]:
    """Apply RFC 7396 JSON merge patch."""
    try:
        obj = json.loads(content)
    except ValueError as e:
        return None, f'Draft content is not valid JSON: {e}'
    if not isinstance(patch, dict):
        return None, 'json_merge patch must be a dict.'
    obj = _json_merge_apply(obj, patch)
    return json.dumps(obj, ensure_ascii=False, indent=2), None


def _json_merge_apply(target: Any, patch: Any) -> Any:
    if not isinstance(patch, dict):
        return patch
    if not isinstance(target, dict):
        target = {}
    result = dict(target)
    for k, v in patch.items():
        if v is None:
            result.pop(k, None)
        else:
            result[k] = _json_merge_apply(result.get(k), v)
    return result


def _apply_json_patch(content: str, patch: Any) -> tuple[Optional[str], Optional[str]]:
    """Apply RFC 6902 JSON Patch operations."""
    try:
        obj = json.loads(content)
    except ValueError as e:
        return None, f'Draft content is not valid JSON: {e}'
    if not isinstance(patch, list):
        return None, 'json_patch must be a list of operation objects.'
    try:
        obj = _json_patch_apply(obj, patch)
    except (KeyError, IndexError, TypeError, ValueError) as e:
        return None, f'json_patch failed: {e}'
    return json.dumps(obj, ensure_ascii=False, indent=2), None


def _json_patch_apply(obj: Any, ops: List[Any]) -> Any:
    """Minimal RFC 6902 implementation supporting add/remove/replace/move/copy/test."""
    import copy
    obj = copy.deepcopy(obj)

    def _get(doc: Any, path: str) -> Any:
        parts = [p for p in path.split('/')[1:]]
        cur = doc
        for p in parts:
            p = p.replace('~1', '/').replace('~0', '~')
            cur = cur[int(p)] if isinstance(cur, list) else cur[p]
        return cur

    def _set(doc: Any, path: str, value: Any) -> None:
        parts = [p.replace('~1', '/').replace('~0', '~') for p in path.split('/')[1:]]
        cur = doc
        for p in parts[:-1]:
            cur = cur[int(p)] if isinstance(cur, list) else cur[p]
        last = parts[-1]
        if isinstance(cur, list):
            if last == '-':
                cur.append(value)
            else:
                cur[int(last)] = value
        else:
            cur[last] = value

    def _remove(doc: Any, path: str) -> None:
        parts = [p.replace('~1', '/').replace('~0', '~') for p in path.split('/')[1:]]
        cur = doc
        for p in parts[:-1]:
            cur = cur[int(p)] if isinstance(cur, list) else cur[p]
        last = parts[-1]
        if isinstance(cur, list):
            del cur[int(last)]
        else:
            del cur[last]

    for op in ops:
        operation = op.get('op')
        path = op.get('path', '')
        if operation == 'replace':
            _set(obj, path, op['value'])
        elif operation == 'add':
            _set(obj, path, op['value'])
        elif operation == 'remove':
            _remove(obj, path)
        elif operation == 'move':
            val = _get(obj, op['from'])
            _remove(obj, op['from'])
            _set(obj, path, val)
        elif operation == 'copy':
            _set(obj, path, _get(obj, op['from']))
        elif operation == 'test':
            if _get(obj, path) != op['value']:
                raise ValueError(f'test failed at {path}')
        else:
            raise ValueError(f'unsupported op: {operation}')
    return obj


@handle_tool_errors
def discard_draft(key: str, sort_order: Optional[int] = None) -> Dict[str, Any]:
    """Discard all uncommitted patch edits for an artifact and revert to the last saved version.

    Deletes the local draft file. The next read (get_artifact or patch_artifact) will
    load the last committed revision from the database. No new revision is created and
    no SSE event is emitted.

    Args:
        key (str): The artifact key whose draft should be discarded.
        sort_order (int): 1-based position for list-cardinality slots. Omit for single slots.

    Returns:
        Confirmation that the draft was discarded (or was already absent).
    """
    ctx = require_context()
    list_index: Optional[int] = None
    if sort_order is not None:
        list_index, resolve_err = _resolve_list_index_from_sort_order(key, sort_order)
        if resolve_err:
            return tool_success('discard_draft', {'status': 'error', 'message': resolve_err})
    existed = ctx.read_draft(key, list_index) is not None
    ctx.delete_draft(key, list_index)
    msg = (
        f"Draft for '{key}' discarded. Next read will use the last committed version."
        if existed else
        f"No draft found for '{key}' — nothing to discard."
    )
    return tool_success('discard_draft', {'status': 'ok', 'message': msg})


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


def _resolve_attachment(
    filename: str,
    turn: Optional[int] = None,
) -> tuple[Optional[str], Optional[str]]:
    """Shared file-resolution logic for read_user_attachment and find_user_attachment.

    Returns (abs_path, error_message). On success, error_message is None.
    On failure, abs_path is None.

    Matching rules:
    - If turn is provided, only look in that turn's files.
    - If turn is omitted, search from newest turn to oldest (current first).
    - Within a turn, files are deduped: duplicates are addressed as
      report-1.pdf, report-2.pdf, etc. The display_name must match the
      deduplicated name shown in the context prompt.
    """
    try:
        import lazyllm
        cfg: Dict[str, Any] = {}
        try:
            cfg = lazyllm.globals.get('agentic_config') or {}
        except Exception:
            pass
        files: List[str] = cfg.get('files') or []
        history_files_per_turn: Dict[str, List[str]] = cfg.get('history_files_per_turn') or {}
    except Exception:
        return None, 'Could not read agentic_config.'

    if not files and not history_files_per_turn:
        return None, 'No attached files found in this conversation.'

    def _dedupe_turn(paths: List[str]) -> List[tuple[str, str]]:
        """Return (display_name, abs_path) pairs with intra-turn dedup (no size)."""
        seen: Dict[str, int] = {}
        result: List[tuple[str, str]] = []
        for path in paths:
            base = os.path.basename(path)
            name_no_ext, ext = os.path.splitext(base)
            if base not in seen:
                seen[base] = 0
                display = base
            else:
                seen[base] += 1
                display = f'{name_no_ext}-{seen[base]}{ext}'
            result.append((display, path))
        return result

    target = filename.strip()

    def _match_in_turn(paths: List[str]) -> Optional[str]:
        pairs = _dedupe_turn(paths)
        for display_name, abs_path in pairs:
            if display_name == target or abs_path.endswith(target) or target in abs_path:
                return abs_path
        return None

    if turn is not None:
        turn_key = str(turn)
        turn_paths = history_files_per_turn.get(turn_key) or []
        # Fallback: filter files list by turn position is unreliable; use per-turn map only.
        if not turn_paths:
            return None, f'No files found for turn {turn}.'
        matched = _match_in_turn(turn_paths)
        if matched:
            return matched, None
        available = [os.path.basename(p) for p in turn_paths]
        return None, (
            f"File '{target}' not found in turn {turn}. "
            f"Available: {', '.join(available)}"
        )

    # Search without turn: newest turn first (descending seq).
    all_seqs = sorted(
        (int(k) for k in history_files_per_turn if k.isdigit()),
        reverse=True,
    )
    for seq in all_seqs:
        paths = history_files_per_turn.get(str(seq)) or []
        matched = _match_in_turn(paths)
        if matched:
            return matched, None

    # Final fallback: scan the merged files list for partial match
    for path in files:
        if os.path.basename(path) == target or path.endswith(target) or target in path:
            return path, None

    all_names = [os.path.basename(p) for p in files]
    return None, (
        f"File '{target}' not found in attached files. "
        f"Available: {', '.join(all_names)}"
    )


@handle_tool_errors
def read_user_attachment(filename: str, turn: Optional[int] = None) -> Dict[str, Any]:
    """Read the contents of a file previously uploaded by the user in this conversation.

    The list of available files is shown in the system prompt under '## User Uploaded Files'.
    Use this tool to read a file's content when the user asks about it or when the task
    requires processing the file.

    Args:
        filename (str): The filename (basename) or display name of the attachment to read.
            Must match one of the files listed in the system prompt.
            For intra-turn duplicates use the deduplicated name (e.g. report-1.pdf).
        turn (int): Optional. The conversation turn number (1-based seq matching the
            'Turn N' labels in the system prompt, e.g. Turn 1, Turn 3).
            Omit to search from the current turn first, then historical turns newest-first.

    Returns:
        The file content as text, or a confirmation message with the absolute path for
        binary/image files that should be passed to other tools (e.g. vision_extractor).
    """
    matched, err = _resolve_attachment(filename, turn)
    if err:
        return tool_success('read_user_attachment', {'status': 'error', 'message': err})
    if not os.path.exists(matched):
        return tool_success('read_user_attachment', {
            'status': 'error',
            'message': f"File '{os.path.basename(matched)}' was found in the index but is no longer on disk.",
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


@handle_tool_errors
def find_user_attachment(filename: str, turn: Optional[int] = None) -> Dict[str, Any]:
    """Return the accessible URL or local path of a file uploaded by the user.

    Use this when you need to pass a file to another tool (e.g. super_pdf_reader, image tools)
    but do not need to read its text content directly.

    Args:
        filename (str): The filename (basename) or display name of the attachment to locate.
            For intra-turn duplicates use the deduplicated name (e.g. report-1.pdf).
        turn (int): Optional. The conversation turn number. Same semantics as
            read_user_attachment: 1-based for historical turns, or omit to search
            current turn first then historical turns newest-first.

    Returns:
        A dict with 'url' (signed HTTP URL from Go, preferred) and 'path' (local absolute path,
        fallback). Pass 'url' to other tools when available.
    """
    matched, err = _resolve_attachment(filename, turn)
    if err:
        return tool_success('find_user_attachment', {'status': 'error', 'message': err})
    if not os.path.exists(matched):
        return tool_success('find_user_attachment', {
            'status': 'error',
            'message': f"File '{os.path.basename(matched)}' was found in the index but is no longer on disk.",
        })

    # Try to get a signed URL from Go /static-files:sign.
    signed_url: Optional[str] = None
    try:
        import httpx
        from lazymind.config import config as _cfg
        core_url = str(_cfg['core_api_url']).rstrip('/')
        resp = httpx.post(
            f'{core_url}/static-files:sign',
            json={'path': matched},
            timeout=3.0,
        )
        if resp.status_code == 200:
            signed_url = resp.json().get('data', {}).get('url') or resp.json().get('url')
    except Exception:
        pass

    result: Dict[str, Any] = {
        'status': 'ok',
        'filename': os.path.basename(matched),
        'path': matched,
    }
    if signed_url:
        result['url'] = signed_url
    else:
        result['url'] = matched  # fallback to local path
        result['message'] = 'Signed URL unavailable; use the local path instead.'
    return tool_success('find_user_attachment', result)


@handle_tool_errors
def find_artifact(artifact_key: str, sort_order: Optional[int] = None) -> Dict[str, Any]:
    """Return the accessible URL or local path of a plugin artifact.

    Analogous to find_user_attachment but for plugin step outputs.
    Reads session_id and plugin_id from agentic_config (same as save_artifact / get_artifact).

    Args:
        artifact_key (str): The artifact key to look up (e.g. 'generated_image_url').
        sort_order (int): Optional 1-based display position for list-slot artifacts.
            Omit for single-slot artifacts.

    Returns:
        A dict with 'url' (signed HTTP URL, preferred) and 'path' (local absolute path,
        fallback). Pass 'url' to image tools when available.
    """
    import lazyllm
    try:
        cfg: Dict[str, Any] = lazyllm.globals.get('agentic_config') or {}
    except Exception:
        cfg = {}

    session_id: str = cfg.get('plugin_session_id', '')
    if not session_id:
        return tool_success('find_artifact', {
            'status': 'error',
            'message': 'No active plugin session found in agentic_config.',
        })

    ctx = require_context()

    if sort_order is not None:
        result_dict = _get_plugin_artifact_by_sort_order(ctx, artifact_key, session_id, sort_order)
    else:
        result_dict = _get_plugin_artifact_all(ctx, artifact_key, session_id)

    # Unwrap inner result to extract the path.
    inner = result_dict.get('result', result_dict)
    if inner.get('status') != 'ok':
        return result_dict

    artifacts = inner.get('artifacts') or []
    if not artifacts:
        return tool_success('find_artifact', {
            'status': 'error',
            'message': f"No artifact found for key '{artifact_key}'.",
        })

    # Use the first (or only) artifact to resolve the path.
    artifact = artifacts[0]
    value = artifact.get('value') or {}
    if isinstance(value, str):
        try:
            import json as _json
            value = _json.loads(value)
        except Exception:
            value = {}

    path: Optional[str] = value.get('path') or value.get('url') or value.get('text')
    if not path or not isinstance(path, str):
        return tool_success('find_artifact', {
            'status': 'error',
            'message': f"Artifact '{artifact_key}' has no resolvable path.",
        })

    # Try to get a signed URL from Go /static-files:sign.
    signed_url: Optional[str] = None
    try:
        import httpx
        from lazymind.config import config as _cfg
        core_url = str(_cfg['core_api_url']).rstrip('/')
        resp = httpx.post(
            f'{core_url}/static-files:sign',
            json={'path': path},
            timeout=3.0,
        )
        if resp.status_code == 200:
            signed_url = resp.json().get('data', {}).get('url') or resp.json().get('url')
    except Exception:
        pass

    out: Dict[str, Any] = {
        'status': 'ok',
        'artifact_key': artifact_key,
        'path': path,
    }
    if sort_order is not None:
        out['sort_order'] = sort_order
    if signed_url:
        out['url'] = signed_url
    else:
        out['url'] = path
        out['message'] = 'Signed URL unavailable; use the local path instead.'
    return tool_success('find_artifact', out)
