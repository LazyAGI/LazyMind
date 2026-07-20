from __future__ import annotations

import re
import time
from functools import lru_cache
from pathlib import Path
from typing import List, Optional

import lazyllm
from lazyllm import AutoModel, LOG
from lazyllm.components.formatter import encode_query_with_filepaths
from lazyllm.tools.rag.readers.ocrReader import DynamicPDFReader

from lazymind.chat.config import CHAT_DOCUMENT_EXTENSIONS, CHAT_TEXT_EXTENSIONS, IMAGE_EXTENSIONS
from lazymind.chat.engine.prompts import VISION_EXTRACT_DEFAULT_INSTRUCTION
from lazymind.config import config as _cfg
from lazymind.model_config import is_model_role_available

_SUPPORTED_ATTACHMENT_LABEL = 'images, Office/PDF documents, and common plain-text files'
_PROMPT_TEMPLATE_PLACEHOLDER_RE = re.compile(r'\{(\w+)\}')
_MAX_TEXT_ATTACHMENT_CHARS = 200_000


def _sanitize_for_prompt_template(text: str) -> str:
    # ChatPrompter scans instruction for `{word}` placeholders. Escape attachment
    # bodies only at prompt-build time so OCR/PDF caches stay canonical.
    if not text:
        return text
    return _PROMPT_TEMPLATE_PLACEHOLDER_RE.sub(r'{ \1 }', text)


@lru_cache(maxsize=1)
def _get_document_reader() -> DynamicPDFReader:
    return DynamicPDFReader(
        image_cache_dir=_cfg['ocr_cache_dir'],
        timeout=3600,
    )


def _suffix(path: str) -> str:
    return Path(path).suffix.lower()


def is_chat_image_file(path: str) -> bool:
    return _suffix(path) in IMAGE_EXTENSIONS


def is_chat_document_file(path: str) -> bool:
    return _suffix(path) in CHAT_DOCUMENT_EXTENSIONS


def is_chat_text_file(path: str) -> bool:
    return _suffix(path) in CHAT_TEXT_EXTENSIONS


def is_chat_attachment_file(path: str) -> bool:
    return is_chat_image_file(path) or is_chat_document_file(path) or is_chat_text_file(path)


def filter_chat_image_files(files: List[str]) -> List[str]:
    return [path for path in files if is_chat_image_file(path)]


def filter_chat_document_files(files: List[str]) -> List[str]:
    return [path for path in files if is_chat_document_file(path)]


def _file_digest(path: str) -> str:
    try:
        stat = Path(path).stat()
        return f'mtime={stat.st_mtime:.0f},size={stat.st_size}'
    except OSError:
        return 'stat_unavailable'


def _log_parse_start(path: str, *, kind: str) -> float:
    reader_use_cache = bool(lazyllm.config['reader_use_cache'])
    name = Path(path).name
    LOG.info(
        f'[AttachmentReader] parse start file={name} kind={kind} '
        f'digest={_file_digest(path)} reader_use_cache={reader_use_cache} path={path}'
    )
    return time.perf_counter()


def _log_parse_done(
    path: str,
    *,
    kind: str,
    started_at: float,
    body: str,
) -> None:
    reader_use_cache = bool(lazyllm.config['reader_use_cache'])
    elapsed = time.perf_counter() - started_at
    name = Path(path).name
    LOG.info(
        f'[AttachmentReader] parse done file={name} kind={kind} '
        f'elapsed={elapsed:.3f}s chars={len(body)} reader_use_cache={reader_use_cache} '
        f'digest={_file_digest(path)} path={path}'
    )


def read_chat_document_text(file_path: str) -> str:
    started_at = _log_parse_start(file_path, kind='document')
    reader = _get_document_reader()
    nodes = reader(file_path)
    parts: List[str] = []
    for node in nodes or []:
        text = str(getattr(node, 'text', '') or '').strip()
        if text:
            parts.append(text)
    body = '\n\n'.join(parts)
    _log_parse_done(file_path, kind='document', started_at=started_at, body=body)
    return body


def read_chat_text_file(file_path: str, max_chars: int = _MAX_TEXT_ATTACHMENT_CHARS) -> str:
    started_at = _log_parse_start(file_path, kind='text')
    with open(file_path, 'r', encoding='utf-8', errors='strict') as file:
        body = file.read(max_chars + 1)
    if '\x00' in body:
        raise ValueError('Attachment contains NUL bytes and is not a plain-text file')
    if len(body) > max_chars:
        body = (
            body[:max_chars]
            + f'\n\n[Attachment truncated after {max_chars} characters.]'
        )
    _log_parse_done(file_path, kind='text', started_at=started_at, body=body)
    return body


def extract_image_description(
    file_path: str,
    *,
    priority: int = 0,
    instruction: Optional[str] = None,
) -> str:
    if not is_model_role_available('vlm'):
        raise RuntimeError('vlm model role is not configured')
    started_at = _log_parse_start(file_path, kind='image')
    prompt_instruction = (instruction or VISION_EXTRACT_DEFAULT_INSTRUCTION).strip()
    encoded_query = encode_query_with_filepaths(prompt_instruction, [file_path])
    vlm = AutoModel(model='vlm')
    out = vlm(
        encoded_query,
        stream_output=False,
        llm_chat_history=[],
        lazyllm_files=None,
        priority=priority,
    )
    body = str(out).strip()
    _log_parse_done(file_path, kind='image', started_at=started_at, body=body)
    return body


def parse_attachment_content(file_path: str, *, priority: int = 0) -> str:
    """Parse one chat attachment via VLM, OCR, or direct UTF-8 text reading."""
    path = str(Path(file_path).resolve())
    if not is_chat_attachment_file(path):
        raise ValueError(
            f'Unsupported attachment type: {Path(path).suffix or "(no extension)"}. '
            f'Supported: {_SUPPORTED_ATTACHMENT_LABEL}.'
        )
    if is_chat_image_file(path):
        if not is_model_role_available('vlm'):
            raise RuntimeError('vlm model role is not configured')
        return extract_image_description(path, priority=priority)
    if is_chat_text_file(path):
        return read_chat_text_file(path)
    return read_chat_document_text(path)


def _build_reference_section(file_path: str, body: str, *, kind: str) -> str:
    name = Path(file_path).name
    label = 'Image' if kind == 'image' else 'Document'
    safe_body = _sanitize_for_prompt_template(body.strip())
    return (
        f'## Attached {label} Reference: {name}\n'
        f'Source path: {file_path}\n\n'
        f'{safe_body}'
    )


def build_attachment_reference_prompt(files: List[str], *, priority: int = 0) -> str:
    sections: List[str] = []
    batch_started_at = time.perf_counter()
    for file_path in files:
        path = str(Path(file_path).resolve())
        try:
            if is_chat_image_file(path) and not is_model_role_available('vlm'):
                LOG.warning(f'[AttachmentReader] skip image (no vlm): {path}')
                continue
            if not is_chat_attachment_file(path):
                LOG.info(f'[AttachmentReader] unsupported attachment skipped: {path}')
                continue
            body = parse_attachment_content(path, priority=priority)
            if body:
                kind = 'image' if is_chat_image_file(path) else 'document'
                sections.append(_build_reference_section(path, body, kind=kind))
        except Exception as exc:
            LOG.warning(f'[AttachmentReader] failed to parse {path}: {exc}')
    batch_elapsed = time.perf_counter() - batch_started_at
    LOG.info(
        f'[AttachmentReader] batch done files={len(files)} sections={len(sections)} '
        f'elapsed={batch_elapsed:.3f}s'
    )
    if not sections:
        return ''
    header = (
        '# Attached File References\n'
        'The following content was extracted from user-attached files for this turn. '
        'Use it directly when answering; do not ask the user to re-upload or paste file contents.'
    )
    return header + '\n\n' + '\n\n'.join(sections)
