"""Writer plugin path adapters.

These tools keep the plugin workflow on artifact-file paths while delegating
all writing logic to the common chat writer tool group.
"""
from __future__ import annotations

import json
import os
import re
from pathlib import Path
from typing import Any

from lazyllm.tools.writer.utils import save_artifact_json

from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.engine.tools.writer import (
    WriterToolGroup,
    writer_schema,
)


def _workspace_root() -> Path:
    ctx = require_context()
    root = Path(ctx.workspace_path) if ctx.workspace_path else Path('/tmp')
    root.mkdir(parents=True, exist_ok=True)
    return root


def _read_json_file(path: str) -> Any:
    with open(path, 'r', encoding='utf-8') as fh:
        raw = json.load(fh)
    if isinstance(raw, dict) and 'data' in raw:
        return raw['data']
    return raw


def _read_json_string(path: str) -> str:
    return json.dumps(_read_json_file(path), ensure_ascii=False)


def _json_loads(value: str, default: Any = None) -> Any:
    text = (value or '').strip()
    if not text:
        return default
    parsed = json.loads(text)
    if isinstance(parsed, dict) and 'data' in parsed:
        return parsed['data']
    return parsed


def _save_json_artifact(name: str, content_json: str, schema_name: str, *, directory: Path | None = None) -> str:
    root = directory or _workspace_root()
    root.mkdir(parents=True, exist_ok=True)
    data = _json_loads(content_json, {})
    return save_artifact_json(
        data,
        str(root / f'{name}.json'),
        schema_name=schema_name,
        created_by='writer-plugin-wrapper',
    )


def _collect_resources(user_input: str) -> str:
    ctx = require_context()
    files_by_turn = ctx.params.get('history_files_per_turn') or {}
    all_files = [path for paths in files_by_turn.values() for path in paths]

    resources: list[dict] = []
    for abs_path in all_files:
        resources.append({
            'resource_id': os.path.basename(abs_path),
            'resource_type': 'file',
            'uri': abs_path,
            'title': os.path.basename(abs_path),
            'mime_type': None,
            'summary': None,
            'meta': {},
        })

    pattern = re.compile(r'https?://[A-Za-z0-9.\-]+\.feishu\.cn/\S+')
    seen_urls: set[str] = set()
    for idx, match in enumerate(pattern.finditer(user_input or '')):
        url = match.group(0)
        if url in seen_urls:
            continue
        seen_urls.add(url)
        resources.append({
            'resource_id': f'feishu_{idx}',
            'resource_type': 'url',
            'uri': url,
            'title': None,
            'mime_type': None,
            'summary': None,
            'meta': {'provider': 'feishu', 'role': 'background'},
        })

    return json.dumps(resources, ensure_ascii=False)


def writer_build_writing_task(query: str) -> str:
    """Build a WritingTask artifact and return its file path."""
    content = WriterToolGroup().build_writing_task(query=query)
    return _save_json_artifact('writing_task', content, writer_schema('task.WritingTask'))


def writer_profile_resources(writing_task_path: str, user_input: str) -> str:
    """Profile resources for a writing task and return the artifact file path."""
    content = WriterToolGroup().profile_resources(
        writing_task_json=_read_json_string(writing_task_path),
        user_input=user_input,
        resources_json=_collect_resources(user_input),
    )
    return _save_json_artifact('resource_profiles', content, writer_schema('resource.ResourceProfile'))


def writer_create_writing_context(writing_task_path: str, resource_profiles_path: str) -> str:
    """Create a WritingContext artifact and return its file path."""
    content = WriterToolGroup().create_writing_context(
        writing_task_json=_read_json_string(writing_task_path),
        resource_profiles_json=_read_json_string(resource_profiles_path),
    )
    return _save_json_artifact('writing_context', content, writer_schema('context.WritingContext'))


def writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    """Generate an outline artifact and return its file path."""
    content = WriterToolGroup().generate_outline(
        writing_task_json=_read_json_string(writing_task_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact('outline', content, writer_schema('writing.WritingOutline'))


def writer_generate_section_instructions(
    outline_path: str,
    writing_context_path: str,
    review_report_path: str = '',
) -> str:
    """Generate section instructions and return the artifact file path."""
    review_json = _read_json_string(review_report_path) if review_report_path else ''
    content = WriterToolGroup().generate_section_instructions(
        outline_json=_read_json_string(outline_path),
        writing_context_json=_read_json_string(writing_context_path),
        review_report_json=review_json,
    )
    return _save_json_artifact(
        'section_instructions',
        content,
        writer_schema('writing.SectionInstructionList'),
    )


def writer_generate_draft_section(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
) -> str:
    """Generate the next draft section and return its file path, or empty string when complete."""
    section_instructions = _read_json_file(section_instructions_path)
    instructions = section_instructions.get('instructions') if isinstance(section_instructions, dict) else None
    if not isinstance(instructions, list):
        raise TypeError('section_instructions_path must point to a SectionInstructionList artifact.')

    draft_sections_dir = _workspace_root() / 'draft_sections'
    draft_sections_dir.mkdir(parents=True, exist_ok=True)
    previous_paths = sorted(str(path) for path in draft_sections_dir.glob('draft_section_*.json'))
    next_index = len(previous_paths)
    if next_index >= len(instructions):
        return ''

    previous_sections = [_read_json_file(path) for path in previous_paths]
    section_content = WriterToolGroup().generate_draft_section(
        writing_task_json=_read_json_string(writing_task_path),
        section_instruction_json=json.dumps(instructions[next_index], ensure_ascii=False),
        writing_context_json=_read_json_string(writing_context_path),
        previous_sections_json=json.dumps(previous_sections, ensure_ascii=False),
    )
    return _save_json_artifact(
        f'draft_section_{next_index + 1}',
        section_content,
        writer_schema('writing.DraftSection'),
        directory=draft_sections_dir,
    )


def writer_assemble_draft_document(
    draft_sections_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
) -> str:
    """Assemble draft section artifacts into a draft document artifact path."""
    anchor = Path(draft_sections_anchor_path)
    draft_sections_dir = anchor if anchor.is_dir() else anchor.parent
    draft_section_paths = sorted(str(path) for path in draft_sections_dir.glob('draft_section_*.json'))
    if not draft_section_paths:
        raise ValueError('draft_sections_anchor_path must point to a generated draft section file or directory.')

    draft_sections = [_read_json_file(path) for path in draft_section_paths]
    content = WriterToolGroup().assemble_draft_document(
        draft_sections_json=json.dumps(draft_sections, ensure_ascii=False),
        writing_context_json=_read_json_string(writing_context_path),
        outline_json=_read_json_string(outline_path) if outline_path else '',
    )
    return _save_json_artifact('draft_document', content, writer_schema('writing.DraftDocument'))


def writer_update_writing_context(content_artifact_path: str, writing_context_path: str) -> str:
    """Update WritingContext from a content artifact and return the new context path."""
    content = WriterToolGroup().update_writing_context(
        content_artifact_json=_read_json_string(content_artifact_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact('writing_context', content, writer_schema('context.WritingContext'))


def writer_check_consistency(draft_path: str, writing_context_path: str) -> dict:
    """Review a draft document and return review_report path plus review_summary text."""
    content = WriterToolGroup().check_consistency(
        draft_document_json=_read_json_string(draft_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    payload = _json_loads(content, {})
    review_report_path = save_artifact_json(
        payload.get('review_report') or {},
        str(_workspace_root() / 'review_report.json'),
        schema_name=writer_schema('quality.ReviewReport'),
        created_by='writer-plugin-wrapper',
    )
    return {
        'review_report': review_report_path,
        'review_summary': payload.get('review_summary') or '',
    }


def writer_generate_writing_output(
    draft_path: str,
    review_report_path: str,
    writing_context_path: str,
) -> dict:
    """Generate final writing output and return structured/markdown artifact paths."""
    content = WriterToolGroup().generate_writing_output(
        draft_document_json=_read_json_string(draft_path),
        review_report_json=_read_json_string(review_report_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    payload = _json_loads(content, {})
    output_path = save_artifact_json(
        payload.get('writing_output') or {},
        str(_workspace_root() / 'writing_output.json'),
        schema_name=writer_schema('writing.WritingOutput'),
        created_by='writer-plugin-wrapper',
    )
    markdown_path = _workspace_root() / 'writing_output.md'
    markdown_path.write_text(str(payload.get('writing_output_md') or ''), encoding='utf-8')
    return {
        'writing_output': output_path,
        'writing_output_md': str(markdown_path),
    }


def writer_build_revise_task(query: str) -> str:
    """Build a revise-type WritingTask artifact and return its file path."""
    content = WriterToolGroup().build_revise_task(query=query)
    return _save_json_artifact('revise_task', content, writer_schema('task.WritingTask'))


def writer_generate_patch_set(
    revise_task_path: str,
    draft_document_path: str,
    writing_context_path: str,
) -> dict:
    """Generate a patch set and return the patch_set and doc_ir artifact paths."""
    group = WriterToolGroup()
    task_json = _read_json_string(revise_task_path)
    context_json = _read_json_string(writing_context_path)

    doc_ir_json = group.draft_to_doc_ir(
        draft_document_json=_read_json_string(draft_document_path),
    )
    locate_result_json = group.locate_revision_target(
        writing_task_json=task_json,
        doc_ir_json=doc_ir_json,
        writing_context_json=context_json,
    )
    modify_plan_json = group.generate_modify_plan(
        writing_task_json=task_json,
        doc_ir_json=doc_ir_json,
        locate_result_json=locate_result_json,
        writing_context_json=context_json,
    )
    patch_set_json = group.generate_patch_set(
        doc_ir_json=doc_ir_json,
        modify_plan_json=modify_plan_json,
        writing_context_json=context_json,
    )
    return {
        'patch_set': _save_json_artifact('patch_set', patch_set_json, writer_schema('revision.PatchSet')),
        'doc_ir': _save_json_artifact('doc_ir', doc_ir_json, writer_schema('docir.DocIR')),
    }


def writer_validate_patch_set(
    patch_set_path: str,
    revise_task_path: str,
    writing_context_path: str,
) -> dict:
    """Validate a patch set and return the patch_set_review path plus patch_set_review_summary text."""
    content = WriterToolGroup().validate_patch_set(
        patch_set_json=_read_json_string(patch_set_path),
        writing_context_json=_read_json_string(writing_context_path),
        writing_task_json=_read_json_string(revise_task_path),
    )
    payload = _json_loads(content, {})
    patch_set_review_path = save_artifact_json(
        payload.get('patch_set_review') or {},
        str(_workspace_root() / 'patch_set_review.json'),
        schema_name=writer_schema('quality.AuditResult'),
        created_by='writer-plugin-wrapper',
    )
    return {
        'patch_set_review': patch_set_review_path,
        'patch_set_review_summary': payload.get('patch_set_review_summary') or '',
    }


def writer_apply_patch(
    doc_ir_path: str,
    patch_set_path: str,
    writing_context_path: str,
) -> dict:
    """Apply a patch set to the DocIR and return the patch_result and draft_document artifact paths."""
    content = WriterToolGroup().apply_patch(
        doc_ir_json=_read_json_string(doc_ir_path),
        patch_set_json=_read_json_string(patch_set_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    payload = _json_loads(content, {})
    root = _workspace_root()
    patch_result_path = save_artifact_json(
        payload.get('patch_result') or {},
        str(root / 'patch_result.json'),
        schema_name=writer_schema('revision.PatchResult'),
        created_by='writer-plugin-wrapper',
    )
    draft_json = WriterToolGroup().doc_ir_to_draft(
        doc_ir_json=json.dumps(payload.get('revised_doc_ir') or {}, ensure_ascii=False),
    )
    return {
        'patch_result': patch_result_path,
        'draft_document': _save_json_artifact('draft_document', draft_json, writer_schema('writing.DraftDocument')),
    }
