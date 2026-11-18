"""Render and publish a resume using the conversation's managed file workspace."""
from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
from typing import Any, Dict, Literal, Optional
import uuid

from lazyllm.tools import fc_register
from lazyllm.tools.agent import ToolExecutionError

from .chat_artifact import _safe_filename, save_chat_artifact
from .conversation_workspace import _current_artifact_scope, _resolve_workspace_path
from .host_access_guard import get_host_access_guard
from .host_file_resolution import FileResolution, managed_path, open_input_file
from .workspace_context import get_workspace_permission_context


_MAX_SOURCE_BYTES = 1024 * 1024
_MAX_PHOTO_BYTES = 10 * 1024 * 1024
_TEMPLATES = {'business', 'modern', 'sidebar', 'editorial', 'campus', 'minimal'}


def resolve_resume_files(arguments):
    permission = get_workspace_permission_context()
    files = FileResolution(default_root=permission.cwd if permission else None)
    if arguments.get('photo_path'):
        arguments['photo_path'] = files.media(arguments['photo_path'], remote=False)
    # source_path is a managed, conversation-scoped locator, never an arbitrary
    # host document. The function validates it before reading.
    return files.finish(arguments)


def _read_source(source_path, user_id, conversation_id):
    workspace, source = _resolve_workspace_path(source_path, user_id, conversation_id)
    expected_root = os.path.join(workspace, '.generated_resumes')
    if os.path.commonpath([expected_root, source]) != expected_root:
        raise ToolExecutionError('source_path must be a resume_data_path returned in this conversation')
    with open_input_file(source) as handle:
        encoded = handle.read(_MAX_SOURCE_BYTES + 1)
    if len(encoded) > _MAX_SOURCE_BYTES:
        raise ToolExecutionError('Saved resume data exceeds the supported size')
    source_data = json.loads(encoded)
    if not isinstance(source_data, dict) or source_data.get('schema_version') != 1:
        raise ToolExecutionError('Unsupported saved resume data')
    return source_data


@fc_register(host_file=resolve_resume_files)
def render_resume(
    resume: Optional[Dict[str, Any]] = None,
    template: Literal['business', 'modern', 'sidebar', 'editorial', 'campus', 'minimal'] = 'business',
    photo_path: Optional[str] = None,
    photo_fit: Optional[Literal['contain', 'cover']] = None,
    include_photo: bool = True,
    filename: Optional[str] = None,
    source_path: Optional[str] = None,
) -> Dict[str, Any]:
    """Create real styled PDF, editable Word and HTML resume attachments.

    Use for resume/CV creation and optimization. The six templates have distinct
    layouts: business (classic blue), modern (color header), sidebar (profile
    column), editorial (magazine), campus (fresh graduate), minimal (monochrome).
    Only include facts supplied or confirmed by the user. Keep advice and missing
    information outside the resume. Do not turn job requirements into skills.

    Args:
        resume: Structured content: {"basics":{"name":"...","title":"...",
            "email":"...","phone":"...","location":"...","links":[]},
            "summary":"...","sections":[{"title":"Experience","items":[{
            "title":"Company / project","subtitle":"Role","date":"2025–2026",
            "bullets":["User-supported action and result"]}]}]}.
            Supply all relevant real experience. For a template-only change,
            omit this and pass the previous resume_data_path as source_path.
        template: User-selected layout ID. Default business when unspecified.
        photo_path: Local locator of the photo the user explicitly chose for
            their resume; never use an unrelated image or resume screenshot.
            Omit when no photo was provided. Remote URLs are not supported.
            Pass the existing uploaded attachment path directly. This tool
            stages its own safe copy; do not move or modify the user's upload.
        photo_fit: contain keeps the entire photo; cover crops centrally without
            stretching. Omit to retain the saved choice, or default to contain
            for a new resume. Portraits and landscapes are supported.
        include_photo: False removes a previously added photo, including when
            reusing source_path. No empty photo placeholder is rendered.
        filename: Optional plain base filename, without a directory.
        source_path: resume_data_path returned by an earlier successful call in
            this conversation. Reuses content and the selected photo.

    Return the actual file_markdown links for PDF and Word in the final answer.
    PDF is the primary preview and submission artifact; Word is editable and
    may paginate differently. Do not replace these downloads with Markdown.
    """
    if template not in _TEMPLATES:
        raise ToolExecutionError('Choose an available resume template')
    if photo_fit is not None and photo_fit not in {'contain', 'cover'}:
        raise ToolExecutionError('photo_fit must be contain or cover')
    user_id, conversation_id = _current_artifact_scope()
    saved = _read_source(source_path, user_id, conversation_id) if source_path else {}
    selected_fit = photo_fit or saved.get('photo_fit', 'contain')
    data = resume if resume is not None else saved.get('resume')
    if not isinstance(data, dict):
        raise ToolExecutionError('Provide structured resume content or a previous resume_data_path')
    encoded = json.dumps(data, ensure_ascii=False).encode('utf-8')
    if len(encoded) > _MAX_SOURCE_BYTES:
        raise ToolExecutionError('Resume content exceeds the supported size')

    basics = data.get('basics') or {}
    if not isinstance(basics, dict):
        raise ToolExecutionError('resume.basics must be an object')
    base = filename or f"{str(basics.get('name') or '简历')}-简历"
    # A supplied extension is normalized; a path or unsafe name is rejected.
    base = _safe_filename(base, 'file')
    if Path(base).suffix.lower() in {'.pdf', '.docx', '.html', '.md'}:
        base = Path(base).stem
    _safe_filename(base + '.docx', 'file')
    relative_dir = f'.generated_resumes/{uuid.uuid4().hex}'
    _, output_dir = _resolve_workspace_path(relative_dir, user_id, conversation_id)
    os.makedirs(output_dir, exist_ok=False)
    selected_photo = None
    try:
        source_photo = photo_path or saved.get('photo_path')
        if include_photo and source_photo:
            guard = get_host_access_guard()
            if photo_path and (guard is not None or managed_path(str(photo_path))):
                if guard is not None:
                    guard.validate()
                photo_source = str(photo_path)
            else:
                _, photo_source = _resolve_workspace_path(source_photo, user_id, conversation_id)
            # Copy approved input through the guarded reader, so Pillow never
            # opens an unvalidated model-selected host path.
            selected_photo = os.path.join(output_dir, 'selected-photo.bin')
            with open_input_file(photo_source) as incoming:
                photo_bytes = incoming.read(_MAX_PHOTO_BYTES + 1)
            if len(photo_bytes) > _MAX_PHOTO_BYTES:
                raise ToolExecutionError('Resume photo exceeds the 10 MiB limit')
            with open(selected_photo, 'xb') as target:
                target.write(photo_bytes)

        from lazymind.document_tools.resume.renderer import TEMPLATES, render_resume_files
        result = render_resume_files(
            data, output_dir, template=template, photo_path=selected_photo,
            photo_fit=selected_fit, filename=base,
        )
        saved_data = {
            'schema_version': 1, 'resume': data,
            'photo_path': f'{relative_dir}/selected-photo.bin' if selected_photo else None,
            'photo_fit': selected_fit,
        }
        source = os.path.join(output_dir, 'resume-data.json')
        with open(source, 'x', encoding='utf-8') as handle:
            json.dump(saved_data, handle, ensure_ascii=False)
    except Exception as exc:
        shutil.rmtree(output_dir, ignore_errors=True)
        if isinstance(exc, ToolExecutionError):
            raise
        raise ToolExecutionError(f'Resume rendering failed: {exc}') from exc

    files = []
    for item in result['files']:
        published = save_chat_artifact(
            filename=item['filename'], content=item['path'], content_type='file',
            caption=f"{TEMPLATES[template]['name']} · {Path(item['filename']).suffix[1:].upper()}",
            logical_key=f"resume:{template}:{Path(item['filename']).suffix}",
        )
        files.append({**published, 'mime': item.get('mime')})
    return {
        'template': template, 'photo_included': bool(selected_photo),
        'photo_fit': selected_fit, 'page_count': result.get('page_count'),
        'warnings': result.get('warnings', []), 'files': files,
        'resume_data_path': f'{relative_dir}/resume-data.json',
        'message': 'Use the PDF as the finished preview. Copy the actual PDF and Word file_markdown links to the user.',
    }
