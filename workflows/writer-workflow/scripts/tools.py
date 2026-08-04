"""Artifact-path adapters for the unified writer workflow.

The workflow owns orchestration only. Writing, revision, document conversion, and
provider synchronization continue to use the existing LazyMind/LazyLLM writer
tooling and the existing workflow artifact mechanism.
"""
from __future__ import annotations

import json
import re
import uuid
from pathlib import Path
from typing import Any

from lazyllm.tools.writer.data_models import WriterDocument
from lazyllm.tools.writer.utils import save_artifact_json

from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.engine.tools.writer import (
    WriterCreateToolkit,
    WriterResourceToolkit,
    WriterRevisionToolkit,
    WriterToolkitBase,
    build_writer_status_ir,
    writer_schema,
)


def _workspace_root() -> Path:
    ctx = require_context()
    root = Path(ctx.workspace_path) if ctx.workspace_path else Path('/tmp')
    root.mkdir(parents=True, exist_ok=True)
    return root


def _run_root(name: str) -> Path:
    root = _workspace_root() / 'writer-workflow' / f'{name}-{uuid.uuid4().hex}'
    root.mkdir(parents=True, exist_ok=True)
    return root


def _read_json_file(path: str) -> Any:
    if Path(path).suffix.lower() in {'.md', '.markdown'}:
        return Path(path).read_text(encoding='utf-8')
    with open(path, 'r', encoding='utf-8') as fh:
        raw = json.load(fh)
    if isinstance(raw, dict) and 'data' in raw:
        return raw['data']
    return raw


def _read_json_string(path: str) -> str:
    content = _read_json_file(path)
    return content if isinstance(content, str) else json.dumps(content, ensure_ascii=False)


def _json_loads(value: str, default: Any = None) -> Any:
    text = (value or '').strip()
    if not text:
        return default
    parsed = json.loads(text)
    if isinstance(parsed, dict) and 'data' in parsed:
        return parsed['data']
    return parsed


def _writer_document_json(
    value: str | dict,
    *,
    expected_stage: str | None = None,
    editable: bool = False,
) -> str:
    """Normalize IR while leaving Markdown content unchanged."""
    if isinstance(value, str):
        try:
            payload = _json_loads(value, {})
        except json.JSONDecodeError:
            return value
    else:
        payload = dict(value or {})
    if isinstance(payload, str):
        return payload
    document = WriterDocument.model_validate(payload)
    if expected_stage is not None and document.stage != expected_stage:
        raise ValueError(
            f'WriterDocument must have stage={expected_stage!r}; got {document.stage!r}.',
        )
    if document.metadata.get('kind') == 'step_status':
        raise ValueError('A writer status placeholder cannot be used as a document artifact.')
    if expected_stage == 'outline' and len(document.blocks) < 3:
        raise ValueError('An outline WriterDocument must contain at least three top-level blocks.')
    if editable:
        document.ui_editable = True
    return document.model_dump_json(exclude_defaults=True)


def _save_json_artifact(
    name: str,
    content_json: str,
    schema_name: str,
    *,
    directory: Path | None = None,
) -> str:
    root = directory or _workspace_root()
    root.mkdir(parents=True, exist_ok=True)
    extension = (
        '.lmd'
        if schema_name in {
            WriterToolkitBase.WRITER_IR_SCHEMA,
            WriterToolkitBase.WRITER_BLOCK_SCHEMA,
        }
        else '.json'
    )
    return save_artifact_json(
        _json_loads(content_json, {}),
        str(root / f'{name}{extension}'),
        schema_name=schema_name,
        created_by='writer-workflow-wrapper',
    )


def _save_writer_document(
    name: str,
    value: str | dict,
    *,
    expected_stage: str | None = None,
    editable: bool = False,
    directory: Path | None = None,
) -> str:
    """Persist a document as .lmd or .md according to its representation."""
    content = _writer_document_json(
        value,
        expected_stage=expected_stage,
        editable=editable,
    )
    try:
        _json_loads(content, {})
    except json.JSONDecodeError:
        root = directory or _workspace_root()
        root.mkdir(parents=True, exist_ok=True)
        path = root / f'{name}.md'
        path.write_text(content, encoding='utf-8')
        return str(path)
    return _save_json_artifact(
        name, content, WriterToolkitBase.WRITER_IR_SCHEMA, directory=directory,
    )


def _markdown_filename(title: str) -> str:
    filename = re.sub(r'[\\/:*?"<>|\x00-\x1f]+', '_', title).strip(' ._')
    return f'{filename[:80] or "文稿"}.md'


def _save_publish_payload(payload: dict, root: Path) -> dict:
    return {
        'publish_result': _save_json_artifact(
            'publish_result',
            json.dumps(payload.get('publish_result') or {}, ensure_ascii=False),
            writer_schema('revision.PatchResult'),
            directory=root,
        ),
        'published_document': _save_writer_document(
            'published_document',
            payload.get('published_document') or {},
            editable=True,
            directory=root,
        ),
        'published_link': str(payload.get('published_link') or ''),
    }


def writer_build_writing_task(query: str) -> str:
    """Build a WritingTask artifact from the user's complete request."""
    content = WriterCreateToolkit().build_writing_task(query=query)
    return _save_json_artifact('writing_task', content, writer_schema('task.WritingTask'))


def writer_load_document(user_input: str, stage: str = 'final') -> dict:
    """Load a Feishu/Lark document as source IR and preserve its target binding."""
    root = _run_root('load-document')
    payload = _json_loads(
        WriterResourceToolkit().load_document(user_input=user_input, stage=stage),
        {},
    )
    return {
        'source_ir': _save_writer_document(
            'source_ir',
            payload.get('source_document') or {},
            expected_stage=stage,
            directory=root,
        ),
        'target_document': _save_json_artifact(
            'target_document',
            json.dumps(payload.get('target_document') or {}, ensure_ascii=False),
            writer_schema('task.TargetDocument'),
            directory=root,
        ),
    }


def writer_profile_resources(
    writing_task_path: str,
    user_input: str,
    source_ir_path: str = '',
    knowledge_text: str = '',
) -> str:
    """Profile attachments, a loaded source document, and retrieved KB evidence."""
    toolkit = WriterCreateToolkit()
    files_by_turn = require_context().params.get('history_files_per_turn') or {}
    file_paths = [path for paths in files_by_turn.values() for path in paths]
    resources = toolkit.build_resources(
        file_paths_json=json.dumps(file_paths, ensure_ascii=False),
        source_document_json=(
            _read_json_string(source_ir_path) if source_ir_path else ''
        ),
        knowledge_text=knowledge_text,
    )
    content = toolkit.profile_resources(
        writing_task_json=_read_json_string(writing_task_path),
        user_input=user_input,
        resources_json=resources,
    )
    return _save_json_artifact(
        'resource_profiles', content, writer_schema('resource.ResourceProfile'),
    )


def writer_create_writing_context(
    writing_task_path: str,
    resource_profiles_path: str,
    source_ir_path: str = '',
) -> dict[str, str]:
    """Create WritingContext, optionally incorporating an existing WriterDocument."""
    content = WriterCreateToolkit().create_writing_context(
        writing_task_json=_read_json_string(writing_task_path),
        resource_profiles_json=_read_json_string(resource_profiles_path),
        writer_document_json=_read_json_string(source_ir_path) if source_ir_path else '',
    )
    return {
        'writing_context': _save_json_artifact(
            'writing_context', content, writer_schema('context.WritingContext'),
        ),
        'context_ir': _save_json_artifact(
            'context_ir',
            build_writer_status_ir(
                'context_ready', '已成功构造写作上下文', source='writer-workflow',
            ),
            WriterToolkitBase.WRITER_IR_SCHEMA,
        ),
    }


def writer_prepare_outline(source_ir_path: str) -> str:
    """Normalize a loaded outline document without regenerating its content."""
    content = WriterCreateToolkit().prepare_outline(
        source_document_json=_read_json_string(source_ir_path),
    )
    return _save_writer_document(
        'outline_ir', content, expected_stage='outline', editable=True,
    )


def writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    """Generate an editable outline-stage WriterDocument."""
    generated = WriterCreateToolkit().generate_outline(
        writing_task_json=_read_json_string(writing_task_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_writer_document(
        'outline_ir', generated, expected_stage='outline', editable=True,
    )


def writer_generate_section_instructions(
    outline_path: str,
    writing_context_path: str,
) -> dict:
    """Generate internal section instructions from the selected outline IR."""
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
                'sections_planned', '已成功规划写作章节', source='writer-workflow',
            ),
            WriterToolkitBase.WRITER_IR_SCHEMA,
        ),
    }


def writer_generate_draft_blocks(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
) -> list[str]:
    """Generate and persist all planned draft blocks."""
    blocks = _json_loads(WriterCreateToolkit().generate_draft_blocks(
        writing_task_json=_read_json_string(writing_task_path),
        section_instructions_json=_read_json_string(section_instructions_path),
        writing_context_json=_read_json_string(writing_context_path),
    ), [])
    root = _run_root('draft-blocks')
    return [
        _save_json_artifact(
            f'draft_block_{index:04d}',
            json.dumps(block, ensure_ascii=False),
            WriterToolkitBase.WRITER_BLOCK_SCHEMA,
            directory=root,
        )
        for index, block in enumerate(blocks, start=1)
    ]


def writer_generate_draft_blocks_markdown(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
) -> list[str]:
    """Generate and persist all planned draft sections as Markdown."""
    sections = _json_loads(WriterCreateToolkit().generate_draft_blocks_markdown(
        writing_task_json=_read_json_string(writing_task_path),
        section_instructions_json=_read_json_string(section_instructions_path),
        writing_context_json=_read_json_string(writing_context_path),
    ), [])
    root = _run_root('draft-sections-markdown')
    paths = []
    for index, section in enumerate(sections, start=1):
        path = root / f'draft_section_{index:04d}.md'
        path.write_text(str(section), encoding='utf-8')
        paths.append(str(path))
    return paths


def writer_generate_draft_document(
    draft_blocks_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
) -> str:
    """Combine draft WriterBlock artifacts into a draft WriterDocument."""
    anchor = (
        Path(draft_blocks_anchor_path)
        if draft_blocks_anchor_path else _workspace_root() / 'draft_blocks'
    )
    draft_blocks_dir = anchor if anchor.is_dir() else anchor.parent
    draft_block_paths = sorted(
        (str(path) for path in draft_blocks_dir.glob('draft_block_*.lmd')),
        key=lambda path: int(Path(path).stem.rsplit('_', 1)[-1]),
    )
    if not draft_block_paths:
        raise ValueError(
            'draft_blocks_anchor_path must point to a generated draft block file or directory.',
        )

    draft_blocks = [_read_json_file(path) for path in draft_block_paths]
    content = WriterCreateToolkit().generate_draft_document(
        draft_blocks_json=json.dumps(draft_blocks, ensure_ascii=False),
        writing_context_json=_read_json_string(writing_context_path),
        outline_json=_read_json_string(outline_path) if outline_path else '',
    )
    return _save_writer_document(
        'draft_document', content, expected_stage='draft', editable=True,
    )


def writer_generate_draft_document_markdown(
    draft_sections_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
) -> dict:
    """Assemble Markdown sections and preserve the Markdown document."""
    anchor = (
        Path(draft_sections_anchor_path)
        if draft_sections_anchor_path else _workspace_root() / 'draft_sections'
    )
    sections_dir = anchor if anchor.is_dir() else anchor.parent
    section_paths = sorted(
        sections_dir.glob('draft_section_*.md'),
        key=lambda path: int(path.stem.rsplit('_', 1)[-1]),
    )
    if not section_paths:
        raise ValueError(
            'draft_sections_anchor_path must point to a generated Markdown section or directory.',
        )
    sections = [path.read_text(encoding='utf-8') for path in section_paths]
    payload = _json_loads(WriterCreateToolkit().generate_draft_document_markdown(
        draft_sections_json=json.dumps(sections, ensure_ascii=False),
        writing_context_json=_read_json_string(writing_context_path),
        outline_json=_read_json_string(outline_path) if outline_path else '',
    ), {})
    root = _run_root('draft-document-markdown')
    markdown_path = root / 'draft_document.md'
    markdown_path.write_text(str(payload.get('draft_document_md') or ''), encoding='utf-8')
    return {
        'draft_document': _save_writer_document(
            'draft_document',
            payload.get('draft_document') or {},
            expected_stage='draft',
            editable=True,
            directory=root,
        ),
        'draft_document_md': str(markdown_path),
    }


def writer_update_writing_context(
    content_artifact_path: str,
    writing_context_path: str,
) -> str:
    """Update WritingContext from a WriterDocument or WriterBlock."""
    content = WriterCreateToolkit().update_writing_context(
        content_artifact_json=_read_json_string(content_artifact_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'writing_context', content, writer_schema('context.WritingContext'),
    )


def writer_generate_final_document(
    draft_path: str,
    writing_context_path: str,
) -> dict:
    """Generate final artifacts without changing the draft representation."""
    content = WriterCreateToolkit().generate_final_document(
        draft_document_json=_read_json_string(draft_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    payload = _json_loads(content, {})
    final_document_path = _save_writer_document(
        'final_document',
        payload.get('final_document') or {},
        expected_stage='final',
        editable=True,
    )
    markdown_path = _workspace_root() / 'final_document.md'
    markdown_path.write_text(str(payload.get('final_document_md') or ''), encoding='utf-8')
    return {
        'final_document': final_document_path,
        'final_document_md': str(markdown_path),
    }


def writer_export_markdown(content_path: str) -> str:
    """Export the latest WriterDocument as a downloadable Markdown file."""
    payload = _json_loads(WriterCreateToolkit().render_markdown(
        writer_document_json=_read_json_string(content_path),
    ), {})
    output_path = _run_root('export-markdown') / _markdown_filename(
        str(payload.get('title') or ''),
    )
    output_path.write_text(str(payload.get('markdown') or ''), encoding='utf-8')
    return str(output_path)


def writer_build_revision_task(query: str, base_ir_path: str) -> str:
    """Build a revision task for either an outline or a full document."""
    content = WriterRevisionToolkit().build_revision_task(
        query=query,
        writer_document_json=_read_json_string(base_ir_path),
        allow_outline=require_context().params.get('step_id') != 'write_document',
    )
    return _save_json_artifact(
        'revision_task', content, writer_schema('task.WritingTask'),
        directory=_run_root('revision-task'),
    )


def writer_locate_revision_target(
    base_ir_path: str,
    writing_context_path: str,
    revision_task_path: str,
) -> str:
    """Locate the WriterDocument blocks affected by a revision task."""
    content = WriterRevisionToolkit().locate_revision_target(
        writing_task_json=_read_json_string(revision_task_path),
        writer_document_json=_read_json_string(base_ir_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'locate_result', content, writer_schema('revision.LocateResult'),
        directory=_run_root('revision-locate'),
    )


def writer_generate_modify_plan(
    base_ir_path: str,
    writing_context_path: str,
    revision_task_path: str,
    locate_result_path: str,
) -> str:
    """Build a ModifyPlan for the located revision targets."""
    content = WriterRevisionToolkit().generate_modify_plan(
        writing_task_json=_read_json_string(revision_task_path),
        writer_document_json=_read_json_string(base_ir_path),
        locate_result_json=_read_json_string(locate_result_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'modify_plan', content, writer_schema('revision.ModifyPlan'),
        directory=_run_root('revision-plan'),
    )


def writer_generate_patch_set(
    base_ir_path: str,
    writing_context_path: str,
    modify_plan_path: str,
) -> str:
    """Generate a PatchSet directly from a ModifyPlan."""
    content = WriterRevisionToolkit().generate_patch_set(
        writer_document_json=_read_json_string(base_ir_path),
        modify_plan_json=_read_json_string(modify_plan_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'patch_set', content, writer_schema('revision.PatchSet'),
        directory=_run_root('revision-patch'),
    )


def writer_apply_revision(
    base_ir_path: str,
    writing_context_path: str,
    patch_set_path: str,
) -> dict:
    """Apply one revision locally; body revisions are published in the publish step."""
    root = _run_root('apply-revision')
    is_body_step = require_context().params.get('step_id') == 'write_document'
    payload = _json_loads(WriterRevisionToolkit().apply_revision(
        writer_document_json=_read_json_string(base_ir_path),
        patch_set_json=_read_json_string(patch_set_path),
        writing_context_json=_read_json_string(writing_context_path),
        sync_provider=not is_body_step,
        allow_outline=not is_body_step,
    ), {})
    result = {
        'patch_result': _save_json_artifact(
            'patch_result',
            json.dumps(payload.get('patch_result') or {}, ensure_ascii=False),
            writer_schema('revision.PatchResult'),
            directory=root,
        ),
        'revised_ir': _save_writer_document(
            'revised_ir',
            payload.get('revised_document') or {},
            expected_stage='final' if is_body_step else 'outline',
            editable=True,
            directory=root,
        ),
        'write_result': '',
    }
    if payload.get('write_result'):
        result['write_result'] = _save_json_artifact(
            'write_result',
            json.dumps(payload['write_result'], ensure_ascii=False),
            writer_schema('revision.PatchResult'),
            directory=root,
        )
    return result


def writer_publish_revision(
    source_ir_path: str,
    patch_set_path: str,
) -> dict:
    """Apply a prepared local revision to its bound source document."""
    root = _run_root('publish-revision')
    payload = _json_loads(WriterResourceToolkit().publish_revision(
        source_document_json=_read_json_string(source_ir_path),
        patch_set_json=_read_json_string(patch_set_path),
    ), {})
    return _save_publish_payload(payload, root)


def writer_replace_document(
    content_path: str,
    source_ir_path: str,
    target_document_path: str = '',
    target_uri: str = '',
) -> dict:
    """Replace a bound cloud source with the selected final WriterDocument."""
    root = _run_root('replace-document')
    payload = _json_loads(WriterResourceToolkit().replace_document(
        content_json=_read_json_string(content_path),
        source_document_json=_read_json_string(source_ir_path),
        target_document_json=(
            _read_json_string(target_document_path) if target_document_path else ''
        ),
        target_uri=target_uri,
    ), {})
    return _save_publish_payload(payload, root)


def writer_append_document(
    content_path: str,
    target_document_path: str = '',
    target_uri: str = '',
    publish_outline: bool = False,
) -> dict:
    """Append a local WriterDocument to a Feishu target and return its confirmed IR."""
    root = _run_root('append-document')
    payload = _json_loads(WriterResourceToolkit().append_document(
        content_json=_read_json_string(content_path),
        target_document_json=(
            _read_json_string(target_document_path) if target_document_path else ''
        ),
        target_uri=target_uri,
        publish_outline=publish_outline,
    ), {})
    return _save_publish_payload(payload, root)


def writer_create_document(
    title: str,
    parent_uri: str = '',
) -> str:
    """Create an empty Feishu document and return its target artifact."""
    root = _run_root('create-document')
    content = WriterResourceToolkit().create_document(
        title=title,
        parent_uri=parent_uri,
    )
    return _save_json_artifact(
        'target_document',
        content,
        writer_schema('task.TargetDocument'),
        directory=root,
    )
