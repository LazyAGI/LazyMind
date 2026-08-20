from __future__ import annotations

import os
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, List, Literal, Optional
from urllib.parse import urlparse

import lazyllm

from lazymind.chat.engine.attachment_reader import (
    is_chat_document_file,
    is_chat_image_file,
    is_chat_text_file,
    parse_attachment_content,
)
from lazymind.chat.service.utils.static_file_url import (
    local_path_from_static_file_url,
    resolve_local_image_path,
)

from .store import FileResourceStore, sha256_file

_ATTACHMENT_CACHE_DIR = 'attachment-text-cache'
_MAX_SOURCE_BYTES = 100 * 1024 * 1024


@dataclass(frozen=True)
class ResolvedTextResource:
    target: str
    path: str
    display_name: str
    kind: Literal['file_resource', 'attachment_text', 'attachment_document', 'workspace']
    workspace: str
    file_id: Optional[str] = None


def _agentic_config() -> Dict[str, Any]:
    try:
        value = lazyllm.globals.get('agentic_config') or {}
    except Exception:
        value = {}
    return value if isinstance(value, dict) else {}


def _display_name(path: str) -> str:
    raw = str(path or '')
    if raw.lower().startswith(('http://', 'https://')):
        return os.path.basename(urlparse(raw).path) or raw
    return os.path.basename(raw)


def _dedupe_turn(paths: List[str]) -> List[tuple[str, str]]:
    seen: Dict[str, int] = {}
    result: List[tuple[str, str]] = []
    for path in paths:
        base = _display_name(path)
        count = seen.get(base, 0)
        seen[base] = count + 1
        if count:
            stem, ext = os.path.splitext(base)
            display = f'{stem}-{count}{ext}'
        else:
            display = base
        result.append((display, path))
    return result


def resolve_attachment_path(
    filename: str,
    turn: Optional[int] = None,
    *,
    allow_partial: bool = False,
    prefer_newest: bool = False,
) -> tuple[Optional[str], Optional[str]]:
    """Resolve one attachment display name from the current conversation."""
    cfg = _agentic_config()
    files = [str(path) for path in (cfg.get('files') or []) if str(path).strip()]
    history = cfg.get('history_files_per_turn') or {}
    if not isinstance(history, dict):
        history = {}
    target = str(filename or '').strip()
    if not target:
        return None, 'filename is required'
    if not files and not history:
        return None, 'No attached files found in this conversation.'

    def matches(paths: List[str]) -> List[str]:
        found = []
        for display, path in _dedupe_turn(paths):
            exact = display == target
            partial = allow_partial and (str(path).endswith(target) or target in str(path))
            if exact or partial:
                found.append(str(path))
        return found

    if turn is not None:
        paths = [str(path) for path in (history.get(str(turn)) or [])]
        found = matches(paths)
        if len(found) == 1:
            return found[0], None
        if len(found) > 1:
            return None, f"Attachment target '{target}' is ambiguous in turn {turn}."
        available = [display for display, _ in _dedupe_turn(paths)]
        return None, (
            f"File '{target}' not found in turn {turn}. "
            f"Available: {', '.join(available)}"
        )

    candidates: List[str] = []
    for seq in sorted((int(key) for key in history if str(key).isdigit()), reverse=True):
        paths = [str(path) for path in (history.get(str(seq)) or [])]
        found = matches(paths)
        if found and prefer_newest:
            return found[0], None
        if len(found) > 1:
            return None, f"Attachment target '{target}' is ambiguous in turn {seq}."
        candidates.extend(found)

    found = matches(files)
    candidates.extend(found)
    candidates = list(dict.fromkeys(candidates))
    if len(candidates) == 1:
        return candidates[0], None
    if len(candidates) > 1:
        return None, f"Attachment target '{target}' is ambiguous."
    available = [display for display, _ in _dedupe_turn(files)]
    return None, (
        f"File '{target}' not found in attached files. "
        f"Available: {', '.join(available)}"
    )


def materialize_local_path(path: str) -> str:
    raw = str(path or '').strip()
    if not raw or raw.lower().startswith(('http://', 'https://')):
        return raw
    resolved = resolve_local_image_path(raw) or local_path_from_static_file_url(raw)
    if resolved and (resolved == raw or os.path.exists(resolved) or not os.path.exists(raw)):
        return resolved
    return raw


def _known_attachment_realpaths() -> set[str]:
    cfg = _agentic_config()
    raw_paths: List[str] = [str(path) for path in (cfg.get('files') or []) if str(path).strip()]
    history = cfg.get('history_files_per_turn') or {}
    if isinstance(history, dict):
        for items in history.values():
            raw_paths.extend(str(path) for path in (items or []) if str(path).strip())
    known: set[str] = set()
    for raw in raw_paths:
        if raw.lower().startswith(('http://', 'https://')):
            continue
        source = materialize_local_path(raw)
        if os.path.isfile(source):
            known.add(os.path.realpath(source))
    return known


def _resolved_from_local_file(
    source: str,
    display_name: str,
    target: str,
    workspace: str,
    store: FileResourceStore,
) -> ResolvedTextResource:
    if source.lower().startswith(('http://', 'https://')):
        raise ValueError('remote attachments must be fetched or downloaded before text reading')
    if not os.path.isfile(source):
        raise FileNotFoundError(source)
    if source.lower().endswith('.pdf'):
        manifest = store.find_by_source_path(source) or store.find_by_display_name(display_name)
        if not manifest:
            from .ingest import ingest_pdf_file
            manifest = ingest_pdf_file(
                source,
                source='upload',
                display_name=display_name,
                store=store,
            )
        return resolve_text_target(str(manifest.get('file_id')))
    if is_chat_image_file(source):
        raise ValueError('images are not text targets; use an image or vision tool')
    if is_chat_text_file(source):
        from .attachment_edit import effective_attachment_path
        source = effective_attachment_path(source)
        sha256_file(source, max_bytes=_MAX_SOURCE_BYTES)
        return ResolvedTextResource(
            target=target,
            path=os.path.realpath(source),
            display_name=display_name,
            kind='attachment_text',
            workspace=workspace,
        )
    if is_chat_document_file(source):
        parsed_path = _materialize_document_text(source, workspace)
        return ResolvedTextResource(
            target=target,
            path=parsed_path,
            display_name=display_name,
            kind='attachment_document',
            workspace=workspace,
        )
    raise ValueError(f"unsupported attachment type: {Path(source).suffix or '(none)'}")


def _materialize_document_text(path: str, workspace: str) -> str:
    digest = sha256_file(path, max_bytes=_MAX_SOURCE_BYTES)
    cache_dir = Path(workspace) / _ATTACHMENT_CACHE_DIR / digest
    parsed_path = cache_dir / 'parsed.txt'
    if parsed_path.is_file():
        return str(parsed_path)
    priority = int(_agentic_config().get('priority') or 0)
    text = parse_attachment_content(path, priority=priority)
    cache_dir.mkdir(parents=True, exist_ok=True)
    temporary = parsed_path.with_suffix(f'.{uuid.uuid4().hex}.tmp')
    temporary.write_text(str(text or ''), encoding='utf-8')
    os.replace(temporary, parsed_path)
    return str(parsed_path)


def _resolve_file_resource(
    store: FileResourceStore,
    target: str,
) -> Optional[Dict[str, Any]]:
    if target.startswith('fr_'):
        manifest = store.load_manifest(target)
        if not manifest:
            raise ValueError(f'file resource not found: {target}')
        return manifest
    matches = [
        item for item in store.load_index()
        if str(item.get('display_name') or '') == target
    ]
    if len(matches) > 1:
        ids = ', '.join(str(item.get('file_id')) for item in matches)
        raise ValueError(f"file target '{target}' is ambiguous; use one of: {ids}")
    if not matches:
        return None
    return store.load_manifest(str(matches[0].get('file_id')))


def resolve_text_target(
    target: str,
    *,
    allow_directory: bool = False,
    turn: Optional[int] = None,
) -> ResolvedTextResource:
    """Resolve a model-facing target to one safe text source."""
    key = str(target or '').strip()
    if not key:
        raise ValueError('target is required')
    from .workspace import (
        _current_artifact_scope,
        _resolve_workspace_path,
        chat_agent_workspace,
    )
    user_id, conversation_id = _current_artifact_scope()

    workspace = os.path.realpath(chat_agent_workspace(user_id, conversation_id))
    store = FileResourceStore(workspace)
    manifest = _resolve_file_resource(store, key)
    if manifest:
        if manifest.get('parse_status') != 'ready':
            raise ValueError(
                f"file {manifest.get('file_id')} is not ready "
                f"(parse_status={manifest.get('parse_status')}: {manifest.get('parse_error')})"
            )
        parsed_path = str(manifest.get('parsed_path') or '')
        if not os.path.isfile(parsed_path):
            raise FileNotFoundError(f"parsed text missing for {manifest.get('file_id')}")
        return ResolvedTextResource(
            target=key,
            path=parsed_path,
            display_name=str(manifest.get('display_name') or manifest.get('file_id')),
            kind='file_resource',
            workspace=workspace,
            file_id=str(manifest.get('file_id')),
        )

    attachment, attachment_error = resolve_attachment_path(key, turn)
    if attachment:
        source = materialize_local_path(attachment)
        return _resolved_from_local_file(source, key, key, workspace, store)

    materialized = materialize_local_path(key)
    if (
        os.path.isfile(materialized)
        and os.path.realpath(materialized) in _known_attachment_realpaths()
    ):
        return _resolved_from_local_file(
            materialized,
            os.path.basename(materialized),
            key,
            workspace,
            store,
        )

    _, resolved = _resolve_workspace_path(key, user_id, conversation_id)
    if os.path.isdir(resolved):
        if not allow_directory:
            raise ValueError('target must resolve to a text file')
    elif not os.path.isfile(resolved):
        detail = f' ({attachment_error})' if attachment_error else ''
        raise ValueError(f"target '{key}' was not found{detail}")
    elif resolved.lower().endswith('.pdf'):
        from .ingest import ingest_pdf_file
        manifest = ingest_pdf_file(
            resolved,
            source='workspace',
            display_name=os.path.basename(resolved),
            store=store,
        )
        return resolve_text_target(str(manifest.get('file_id')))
    elif is_chat_image_file(resolved):
        raise ValueError('images are not text targets; use an image or vision tool')
    elif is_chat_document_file(resolved):
        return ResolvedTextResource(
            target=key,
            path=_materialize_document_text(resolved, workspace),
            display_name=os.path.basename(resolved),
            kind='attachment_document',
            workspace=workspace,
        )
    return ResolvedTextResource(
        target=key,
        path=resolved,
        display_name=os.path.basename(resolved) or key,
        kind='workspace',
        workspace=workspace,
    )
