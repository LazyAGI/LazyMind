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
    WriterCreateToolkit,
    WriterToolkitBase,
    build_writer_status_ir,
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
    content = WriterCreateToolkit().build_writing_task(query=query)
    return _save_json_artifact('writing_task', content, writer_schema('task.WritingTask'))


def writer_profile_resources(writing_task_path: str, user_input: str) -> str:
    """Profile resources for a writing task and return the artifact file path."""
    content = WriterCreateToolkit().profile_resources(
        writing_task_json=_read_json_string(writing_task_path),
        user_input=user_input,
        resources_json=_collect_resources(user_input),
    )
    return _save_json_artifact('resource_profiles', content, writer_schema('resource.ResourceProfile'))


def writer_create_writing_context(writing_task_path: str, resource_profiles_path: str) -> dict:
    """Create the internal WritingContext and its UI-visible status IR."""
    content = WriterCreateToolkit().create_writing_context(
        writing_task_json=_read_json_string(writing_task_path),
        resource_profiles_json=_read_json_string(resource_profiles_path),
    )
    return {
        'writing_context': _save_json_artifact(
            'writing_context', content, writer_schema('context.WritingContext'),
        ),
        'context_ir': _save_json_artifact(
            'context_ir',
            build_writer_status_ir(
                'context_ready', '已成功构造写作上下文', source='writer-plugin',
            ),
            WriterToolkitBase.WRITER_IR_SCHEMA,
        ),
    }


def writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    """Generate an outline artifact and return its file path."""
    content = WriterCreateToolkit().generate_outline(
        writing_task_json=_read_json_string(writing_task_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact('outline_ir', content, WriterToolkitBase.WRITER_IR_SCHEMA)


def writer_generate_section_instructions(
    outline_path: str,
    writing_context_path: str,
) -> dict:
    """Generate internal section instructions and a UI-visible status IR."""
    content = WriterCreateToolkit().generate_section_instructions(
        outline_json=_read_json_string(outline_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return {
        'section_instructions': _save_json_artifact(
            'section_instructions',
            content,
            writer_schema('planning.SectionInstructionList'),
        ),
        'section_plan_ir': _save_json_artifact(
            'section_plan_ir',
            build_writer_status_ir(
                'sections_planned', '已成功规划写作章节', source='writer-plugin',
            ),
            WriterToolkitBase.WRITER_IR_SCHEMA,
        ),
    }


def writer_generate_draft_block(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
) -> str:
    """Generate the next draft WriterBlock and return its path, or empty string when complete."""
    section_instructions = _read_json_file(section_instructions_path)
    instructions = section_instructions.get('instructions') if isinstance(section_instructions, dict) else None
    if not isinstance(instructions, list):
        raise TypeError('section_instructions_path must point to a SectionInstructionList artifact.')

    draft_blocks_dir = _workspace_root() / 'draft_blocks'
    draft_blocks_dir.mkdir(parents=True, exist_ok=True)
    previous_paths = sorted(str(path) for path in draft_blocks_dir.glob('draft_block_*.json'))
    next_index = len(previous_paths)
    if next_index >= len(instructions):
        return ''

    previous_blocks = [_read_json_file(path) for path in previous_paths]
    block_content = WriterCreateToolkit().generate_draft_section(
        writing_task_json=_read_json_string(writing_task_path),
        section_instruction_json=json.dumps(instructions[next_index], ensure_ascii=False),
        writing_context_json=_read_json_string(writing_context_path),
        previous_blocks_json=json.dumps(previous_blocks, ensure_ascii=False),
    )
    return _save_json_artifact(
        f'draft_block_{next_index + 1}',
        block_content,
        WriterToolkitBase.WRITER_BLOCK_SCHEMA,
        directory=draft_blocks_dir,
    )


def writer_generate_draft_document(
    draft_blocks_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
) -> str:
    """Combine draft WriterBlock artifacts into a draft WriterDocument path."""
    anchor = Path(draft_blocks_anchor_path) if draft_blocks_anchor_path else _workspace_root() / 'draft_blocks'
    draft_blocks_dir = anchor if anchor.is_dir() else anchor.parent
    draft_block_paths = sorted(str(path) for path in draft_blocks_dir.glob('draft_block_*.json'))
    if not draft_block_paths:
        raise ValueError('draft_blocks_anchor_path must point to a generated draft block file or directory.')

    draft_blocks = [_read_json_file(path) for path in draft_block_paths]
    content = WriterCreateToolkit().generate_draft_document(
        draft_blocks_json=json.dumps(draft_blocks, ensure_ascii=False),
        writing_context_json=_read_json_string(writing_context_path),
        outline_json=_read_json_string(outline_path) if outline_path else '',
    )
    return _save_json_artifact('draft_document', content, WriterToolkitBase.WRITER_IR_SCHEMA)


def writer_update_writing_context(content_artifact_path: str, writing_context_path: str) -> str:
    """Update WritingContext from a content artifact and return the new context path."""
    content = WriterCreateToolkit().update_writing_context(
        content_artifact_json=_read_json_string(content_artifact_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact('writing_context', content, writer_schema('context.WritingContext'))


def writer_generate_final_document(
    draft_path: str,
    writing_context_path: str,
) -> dict:
    """Generate final WriterDocument and Markdown artifact paths."""
    content = WriterCreateToolkit().generate_final_document(
        draft_document_json=_read_json_string(draft_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    payload = _json_loads(content, {})
    final_document_path = save_artifact_json(
        payload.get('final_document') or {},
        str(_workspace_root() / 'final_document_ir.json'),
        schema_name=WriterToolkitBase.WRITER_IR_SCHEMA,
        created_by='writer-plugin-wrapper',
    )
    markdown_path = _workspace_root() / 'final_document.md'
    markdown_path.write_text(str(payload.get('final_document_md') or ''), encoding='utf-8')
    return {
        'final_document': final_document_path,
        'final_document_md': str(markdown_path),
    }
