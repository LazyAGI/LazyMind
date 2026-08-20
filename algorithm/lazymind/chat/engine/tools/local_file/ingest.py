from __future__ import annotations

import os
import shutil
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Tuple

from lazyllm import LOG

from .store import PARSED_NAME, FileResourceStore, new_file_id, sha256_file, workspace_for_request

_PAGE_MARKER = '<!-- page:{page} -->'


def _link_or_copy(src: str, dest: Path) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    if dest.exists():
        return
    try:
        os.link(src, dest)
    except OSError:
        shutil.copy2(src, dest)


def _page_from_meta(metadata: Dict[str, Any]) -> int:
    for key in ('page', 'page_idx', 'page_label'):
        raw = metadata.get(key)
        if raw is None:
            continue
        try:
            value = int(raw)
            if key == 'page_idx':
                value += 1
            return max(1, value)
        except (TypeError, ValueError):
            text = str(raw).strip()
            digits = ''.join(ch for ch in text if ch.isdigit())
            if digits:
                return max(1, int(digits))
    return 1


def _flatten_nodes(nodes: Sequence[Any]) -> List[Any]:
    out: List[Any] = []
    for node in nodes or []:
        inner = getattr(node, 'nodes', None)
        if inner:
            out.extend(_flatten_nodes(inner))
        else:
            out.append(node)
    return out


def parse_pdf_pages(file_path: str) -> List[Tuple[int, str]]:
    from lazymind.chat.engine.attachment_reader import _get_document_reader

    reader = _get_document_reader()
    nodes = reader(file_path)
    pages: List[Tuple[int, str]] = []
    for node in _flatten_nodes(nodes):
        text = str(getattr(node, 'text', '') or '').strip()
        if not text:
            continue
        metadata = getattr(node, 'metadata', {}) or {}
        if not isinstance(metadata, dict):
            metadata = {}
        pages.append((_page_from_meta(metadata), text))
    return pages


def _compose_parsed(pages: List[Tuple[int, str]]) -> str:
    by_page: Dict[int, List[str]] = {}
    for page, text in pages:
        by_page.setdefault(page, []).append(text)
    ordered_pages = sorted(by_page)
    if not ordered_pages:
        return ''
    sections: List[str] = []
    for page in ordered_pages:
        body = '\n\n'.join(by_page[page]).strip()
        sections.append(f'{_PAGE_MARKER.format(page=page)}\n{body}')
    return '\n\n'.join(sections).strip() + '\n'


def _ocr_type_from_runtime() -> Optional[str]:
    try:
        import lazyllm
        cfg = lazyllm.globals.config.get('dynamic_ocr_configs') or {}
        if isinstance(cfg, dict):
            value = str(cfg.get('ocr_type') or '').strip()
            return value or None
    except Exception:
        return None
    return None


def ingest_pdf_file(
    src_path: str,
    *,
    source: str = 'upload',
    source_url: Optional[str] = None,
    display_name: Optional[str] = None,
    turn_seq: Optional[int] = None,
    store: Optional[FileResourceStore] = None,
) -> Dict[str, Any]:
    src = os.path.realpath(str(src_path))
    if not os.path.isfile(src):
        raise FileNotFoundError(src)
    store = store or FileResourceStore(workspace_for_request())
    digest = sha256_file(src)
    existing = store.find_by_sha256(digest)
    if existing and existing.get('parse_status') == 'ready':
        return existing

    file_id = existing['file_id'] if existing else new_file_id()
    directory = store.resource_dir(file_id)
    original = directory / 'original.pdf'
    _link_or_copy(src, original)
    name = display_name or os.path.basename(src)
    manifest = store.empty_manifest(
        file_id=file_id,
        display_name=name,
        source=source,
        source_url=source_url,
        source_path=src,
        original_path=str(original),
        content_sha256=digest,
        bytes_count=os.path.getsize(src),
        turn_seq=turn_seq,
        parse_status='pending',
    )
    store.write_manifest(manifest)

    try:
        pages = parse_pdf_pages(str(original))
        parsed = _compose_parsed(pages)
        (directory / PARSED_NAME).write_text(parsed, encoding='utf-8')
        line_count = parsed.count('\n') if parsed else 0
        if parsed and not parsed.endswith('\n'):
            line_count += 1
        manifest['pages'] = max((page for page, _ in pages), default=0)
        manifest['line_count'] = line_count
        manifest['parse_status'] = 'ready'
        manifest['parse_error'] = None
        manifest['ocr_type'] = _ocr_type_from_runtime()
        LOG.info(
            f'[FileResource] ingest ready file_id={file_id} pages={manifest["pages"]} '
            f'lines={line_count} source={source} name={name}'
        )
    except Exception as exc:
        manifest['parse_status'] = 'failed'
        manifest['parse_error'] = str(exc)
        LOG.warning(f'[FileResource] ingest failed file_id={file_id} name={name} error={exc}')
    return store.write_manifest(manifest)


def ingest_upload_pdfs(
    files_map: Dict[str, List[str]],
    current_turn_seq: Optional[int] = None,
    store: Optional[FileResourceStore] = None,
) -> List[Dict[str, Any]]:
    manifests: List[Dict[str, Any]] = []
    store = store or FileResourceStore(workspace_for_request())
    for seq_key, paths in (files_map or {}).items():
        try:
            turn_seq = int(seq_key)
        except (TypeError, ValueError):
            turn_seq = current_turn_seq
        for path in paths or []:
            if Path(str(path).split('?', 1)[0]).suffix.lower() != '.pdf':
                continue
            try:
                manifests.append(ingest_pdf_file(
                    path,
                    source='upload',
                    display_name=os.path.basename(path),
                    turn_seq=turn_seq,
                    store=store,
                ))
            except Exception as exc:
                LOG.warning(f'[FileResource] skip upload ingest path={path} error={exc}')
    return manifests
