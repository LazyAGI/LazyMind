"""Artifact-path adapters for the unified writer plugin.

The plugin owns orchestration only. Writing, revision, document conversion, and
provider synchronization continue to use the existing LazyMind/LazyLLM writer
tooling and the existing plugin artifact mechanism.
"""
from __future__ import annotations

import json
import os
import re
import uuid
from pathlib import Path
from typing import Any

from lazyllm.tools.writer.data_models import TargetDocument, WriterAuthoring, WriterDocument
from lazyllm.tools.writer.tools import WriterResourceTools
from lazyllm.tools.writer.utils import save_artifact_json

from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.engine.tools.writer import (
    WriterCreateToolkit,
    WriterRevisionToolkit,
    WriterToolkitBase,
    build_writer_status_ir,
    writer_schema,
)


_FEISHU_URL_RE = re.compile(
    r"https?://[^\s<>\"']*(?:feishu\.(?:cn|com)|larksuite\.com)/"
    r"[^\s<>\"'，。；！？、）】》」』]+",
    re.IGNORECASE,
)


def _workspace_root() -> Path:
    ctx = require_context()
    root = Path(ctx.workspace_path) if ctx.workspace_path else Path('/tmp')
    root.mkdir(parents=True, exist_ok=True)
    return root


def _run_root(name: str) -> Path:
    root = _workspace_root() / 'writer-plugin' / f'{name}-{uuid.uuid4().hex}'
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


def _save_json_artifact(
    name: str,
    content_json: str,
    schema_name: str,
    *,
    directory: Path | None = None,
) -> str:
    root = directory or _workspace_root()
    root.mkdir(parents=True, exist_ok=True)
    return save_artifact_json(
        _json_loads(content_json, {}),
        str(root / f'{name}.json'),
        schema_name=schema_name,
        created_by='writer-plugin-wrapper',
    )


def _result_path(result: dict, key: str = '') -> str:
    if key:
        path = str(((result.get('metadata') or {}).get('artifact_paths') or {}).get(key) or '')
    else:
        path = str(result.get('artifact_path') or '')
    if not path:
        raise ValueError(f'LazyLLM writer tool did not return artifact path {key!r}: {result!r}')
    return path


def _feishu_url(user_input: str) -> str:
    match = _FEISHU_URL_RE.search(user_input or '')
    if not match:
        raise ValueError('A Feishu/Lark document URL is required.')
    return match.group(0).rstrip(').,;!?]}，。；！？】》」』')


def _set_document_editable(value: Any, *, stage: str | None = None) -> str:
    document = WriterDocument.model_validate(value)
    if stage is not None:
        document.stage = stage
    document.ui_editable = True

    def update_blocks(blocks: list[Any], level: int = 1) -> None:
        for block in blocks:
            block.editable = True
            if stage is not None:
                block.stage = stage
            if block.type == 'heading':
                heading_level = block.numbering.get('level')
                if (
                    not isinstance(heading_level, int)
                    or isinstance(heading_level, bool)
                    or not 1 <= heading_level <= 9
                ):
                    block.numbering['level'] = min(level, 9)
            update_blocks(block.children, level + 1)

    update_blocks(document.blocks)

    return document.model_dump_json()


def _document_text(value: Any) -> str:
    document = WriterDocument.model_validate(value)
    lines: list[str] = []

    def collect(blocks: list[Any]) -> None:
        for block in blocks:
            if block.content:
                lines.append(block.content)
            collect(block.children)

    collect(document.blocks)
    return '\n'.join(lines)


def _prepare_supplied_outline(value: Any) -> WriterDocument:
    document = WriterDocument.model_validate(value)
    for block in document.blocks:
        if block.type != 'paragraph':
            continue
        lines = [line.strip() for line in block.content.splitlines() if line.strip()]
        if not lines:
            continue
        block.type = 'heading'
        block.content = lines[0]
        block.spans = []
        block.numbering['level'] = 1
        block.authoring = block.authoring or WriterAuthoring()
        if len(lines) > 1 and not block.authoring.constraints.section_goal:
            block.authoring.constraints.section_goal = '\n'.join(lines[1:])
    return document


def _target_from_document(value: Any) -> TargetDocument | None:
    document = WriterDocument.model_validate(value)
    source = document.metadata.get('source')
    if not isinstance(source, dict):
        return None
    try:
        target = TargetDocument.model_validate(source)
    except Exception:
        return None
    if not (target.uri or target.doc_id):
        return None
    return target


def _collect_resources(
    user_input: str,
    *,
    source_ir_path: str = '',
    knowledge_text: str = '',
) -> str:
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

    if source_ir_path:
        source = _read_json_file(source_ir_path)
        target = _target_from_document(source)
        resources.append({
            'resource_id': 'source_document',
            'resource_type': 'text',
            'inline_text': _document_text(source),
            'title': WriterDocument.model_validate(source).title or None,
            'summary': None,
            'meta': {
                'provider': target.adapter if target else None,
                'uri': target.uri if target else None,
                'role': 'background',
            },
        })
    else:
        seen_urls: set[str] = set()
        for idx, match in enumerate(_FEISHU_URL_RE.finditer(user_input or '')):
            url = match.group(0).rstrip(').,;!?]}，。；！？】》」』')
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

    if knowledge_text.strip():
        resources.append({
            'resource_id': 'knowledge_base_evidence',
            'resource_type': 'text',
            'inline_text': knowledge_text,
            'title': 'Knowledge base evidence',
            'summary': None,
            'meta': {'provider': 'knowledge_base', 'role': 'background'},
        })

    return json.dumps(resources, ensure_ascii=False)


def writer_build_writing_task(query: str) -> str:
    """Build a WritingTask artifact from the user's complete request."""
    content = WriterCreateToolkit().build_writing_task(query=query)
    return _save_json_artifact('writing_task', content, writer_schema('task.WritingTask'))


def writer_load_document(user_input: str, stage: str = 'final') -> dict:
    """Load a Feishu/Lark document as source IR and preserve its target binding."""
    if stage not in {'outline', 'draft', 'final'}:
        raise ValueError('stage must be outline, draft, or final.')
    root = _run_root('load-document')
    target = TargetDocument(
        uri=_feishu_url(user_input),
        adapter='feishu',
        meta={'stage': stage},
    )
    result = WriterResourceTools(llm=None, artifact_store=str(root)).document_to_docir(target)
    loaded = _read_json_file(_result_path(result))
    return {
        'source_ir': _save_json_artifact(
            'source_ir',
            WriterDocument.model_validate(loaded).model_dump_json(),
            WriterToolkitBase.WRITER_IR_SCHEMA,
            directory=root,
        ),
        'target_document': _save_json_artifact(
            'target_document',
            json.dumps(target.model_dump(), ensure_ascii=False),
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
    content = WriterCreateToolkit().profile_resources(
        writing_task_json=_read_json_string(writing_task_path),
        user_input=user_input,
        resources_json=_collect_resources(
            user_input,
            source_ir_path=source_ir_path,
            knowledge_text=knowledge_text,
        ),
    )
    return _save_json_artifact(
        'resource_profiles', content, writer_schema('resource.ResourceProfile'),
    )


def writer_create_writing_context(
    writing_task_path: str,
    resource_profiles_path: str,
    source_ir_path: str = '',
) -> dict:
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
                'context_ready', '已成功构造写作上下文', source='writer-plugin',
            ),
            WriterToolkitBase.WRITER_IR_SCHEMA,
        ),
    }


def writer_prepare_outline(source_ir_path: str) -> str:
    """Normalize a loaded outline document without regenerating its content."""
    prepared = _prepare_supplied_outline(_read_json_file(source_ir_path))
    content = _set_document_editable(prepared, stage='outline')
    return _save_json_artifact('outline_ir', content, WriterToolkitBase.WRITER_IR_SCHEMA)


def writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    """Generate an editable outline-stage WriterDocument."""
    generated = WriterCreateToolkit().generate_outline(
        writing_task_json=_read_json_string(writing_task_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    content = _set_document_editable(_json_loads(generated, {}), stage='outline')
    return _save_json_artifact('outline_ir', content, WriterToolkitBase.WRITER_IR_SCHEMA)


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
    """Generate the next draft WriterBlock, or return empty when complete."""
    section_instructions = _read_json_file(section_instructions_path)
    instructions = (
        section_instructions.get('instructions')
        if isinstance(section_instructions, dict) else None
    )
    if not isinstance(instructions, list):
        raise TypeError(
            'section_instructions_path must point to a SectionInstructionList artifact.',
        )

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
    """Combine draft WriterBlock artifacts into a draft WriterDocument."""
    anchor = (
        Path(draft_blocks_anchor_path)
        if draft_blocks_anchor_path else _workspace_root() / 'draft_blocks'
    )
    draft_blocks_dir = anchor if anchor.is_dir() else anchor.parent
    draft_block_paths = sorted(str(path) for path in draft_blocks_dir.glob('draft_block_*.json'))
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
    return _save_json_artifact('draft_document', content, WriterToolkitBase.WRITER_IR_SCHEMA)


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
    """Generate an editable final WriterDocument and its Markdown artifact."""
    draft_document = WriterDocument.model_validate(_read_json_file(draft_path))
    if draft_document.stage != 'draft':
        raise ValueError(
            f'draft_path must contain stage="draft", got {draft_document.stage!r}.',
        )
    content = WriterCreateToolkit().generate_final_document(
        draft_document_json=_read_json_string(draft_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    payload = _json_loads(content, {})
    final_document = _set_document_editable(
        payload.get('final_document') or {},
        stage='final',
    )
    final_document_path = _save_json_artifact(
        'final_document_ir',
        final_document,
        WriterToolkitBase.WRITER_IR_SCHEMA,
    )
    markdown_path = _workspace_root() / 'final_document.md'
    markdown_path.write_text(str(payload.get('final_document_md') or ''), encoding='utf-8')
    return {
        'final_document': final_document_path,
        'final_document_md': str(markdown_path),
    }


def writer_build_revision_task(query: str, base_ir_path: str) -> str:
    """Build a revision task for either an outline or a full document."""
    base_data = _read_json_file(base_ir_path)
    base_document = WriterDocument.model_validate(base_data)
    ctx = require_context()
    if (
        ctx.params.get('step_id') == 'write_document'
        and base_document.stage == 'outline'
    ):
        raise ValueError(
            'write_document cannot enter revision mode with an outline-stage IR. '
            'Generate the draft from outline_ir instead.',
        )
    target = _target_from_document(base_data)
    content = WriterRevisionToolkit().build_revise_task(
        query=query,
        target_document_json=(
            json.dumps(target.model_dump(), ensure_ascii=False) if target else ''
        ),
    )
    return _save_json_artifact(
        'revision_task', content, writer_schema('task.WritingTask'),
        directory=_run_root('revision-task'),
    )


def writer_plan_revision(
    base_ir_path: str,
    writing_context_path: str,
    revision_task_path: str,
) -> dict:
    """Locate revision targets and produce a structured PatchSet."""
    root = _run_root('revision-plan')
    writer = WriterRevisionToolkit()
    base_ir = _read_json_string(base_ir_path)
    context = _read_json_string(writing_context_path)
    task = _read_json_string(revision_task_path)
    located = writer.locate_revision_target(
        writing_task_json=task,
        writer_document_json=base_ir,
        writing_context_json=context,
    )
    plan = writer.generate_modify_plan(
        writing_task_json=task,
        writer_document_json=base_ir,
        locate_result_json=located,
        writing_context_json=context,
    )
    patch_set = writer.generate_patch_set(
        writer_document_json=base_ir,
        modify_plan_json=plan,
        writing_context_json=context,
    )
    return {
        'locate_result': _save_json_artifact(
            'locate_result', located, writer_schema('revision.LocateResult'), directory=root,
        ),
        'modify_plan': _save_json_artifact(
            'modify_plan', plan, writer_schema('revision.ModifyPlan'), directory=root,
        ),
        'patch_set': _save_json_artifact(
            'patch_set', patch_set, writer_schema('revision.PatchSet'), directory=root,
        ),
    }


def writer_apply_revision(
    base_ir_path: str,
    writing_context_path: str,
    patch_set_path: str,
) -> dict:
    """Apply one revision locally; body revisions are published in the publish step."""
    root = _run_root('apply-revision')
    base_data = _read_json_file(base_ir_path)
    base_document = WriterDocument.model_validate(base_data)
    ctx = require_context()
    if (
        ctx.params.get('step_id') == 'write_document'
        and base_document.stage == 'outline'
    ):
        raise ValueError(
            'write_document cannot revise an outline-stage IR in place. '
            'Generate section instructions and draft the final document from outline_ir.',
        )
    context = _read_json_string(writing_context_path)
    patch_set = _read_json_string(patch_set_path)
    applied = _json_loads(WriterRevisionToolkit().apply_patch(
        writer_document_json=json.dumps(base_data, ensure_ascii=False),
        patch_set_json=patch_set,
        writing_context_json=context,
    ), {})

    patch_result_path = _save_json_artifact(
        'patch_result',
        json.dumps(applied.get('patch_result') or {}, ensure_ascii=False),
        writer_schema('revision.PatchResult'),
        directory=root,
    )
    candidate_json = _set_document_editable(
        applied.get('revised_document') or {},
        stage=WriterDocument.model_validate(base_data).stage,
    )
    revised_path = _save_json_artifact(
        'revised_ir',
        candidate_json,
        WriterToolkitBase.WRITER_IR_SCHEMA,
        directory=root,
    )

    result = {
        'patch_result': patch_result_path,
        'revised_ir': revised_path,
        'write_result': '',
    }
    if (
        _target_from_document(base_data) is None
        or ctx.params.get('step_id') == 'write_document'
    ):
        return result

    write_back = WriterResourceTools(
        llm=None,
        artifact_store=str(root),
    ).apply_patch_to_document(
        source_document=base_data,
        patch_set=_read_json_file(patch_set_path),
    )
    persisted_path = _result_path(write_back, 'persisted_document')
    persisted_json = _set_document_editable(
        _read_json_file(persisted_path),
        stage=WriterDocument.model_validate(base_data).stage,
    )
    result['revised_ir'] = _save_json_artifact(
        'synced_ir',
        persisted_json,
        WriterToolkitBase.WRITER_IR_SCHEMA,
        directory=root,
    )
    result['write_result'] = _save_json_artifact(
        'write_result',
        _read_json_string(_result_path(write_back)),
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
    source_data = _read_json_file(source_ir_path)
    source_document = WriterDocument.model_validate(source_data)
    target = _target_from_document(source_data)
    if target is None:
        raise ValueError('source_ir_path must contain a cloud-bound source document.')

    write_back = WriterResourceTools(
        llm=None,
        artifact_store=str(root),
    ).apply_patch_to_document(
        source_document=source_data,
        patch_set=_read_json_file(patch_set_path),
    )
    persisted_json = _set_document_editable(
        _read_json_file(_result_path(write_back, 'persisted_document')),
        stage=source_document.stage,
    )
    published_link = str(
        target.meta.get('browser_url')
        or (target.uri if target.uri.startswith(('http://', 'https://')) else '')
    ).strip()
    if not published_link:
        raise ValueError('Feishu revision succeeded but no browser URL was returned.')
    return {
        'publish_result': _result_path(write_back),
        'published_document': _save_json_artifact(
            'published_document',
            persisted_json,
            WriterToolkitBase.WRITER_IR_SCHEMA,
            directory=root,
        ),
        'published_link': published_link,
    }


def writer_publish_document(
    content_path: str,
    target_document_path: str = '',
    target_uri: str = '',
    publish_outline: bool = False,
) -> dict:
    """Write a local WriterDocument to a Feishu target and return its confirmed IR."""
    root = _run_root('publish')
    content_data = _read_json_file(content_path)
    document = WriterDocument.model_validate(content_data)
    if document.stage == 'outline' and not publish_outline:
        raise ValueError(
            'Refusing to publish outline IR as the final document. '
            'Use final_document, or set publish_outline=true only for an explicit outline publish.',
        )
    target = _target_from_document(content_data)
    if target_document_path.strip():
        target = TargetDocument.model_validate(_read_json_file(target_document_path))
    if target_uri.strip():
        target = TargetDocument(uri=target_uri.strip(), adapter='feishu')
    if target is None:
        raise ValueError(
            'A target Feishu document is required. Confirm creation or provide a target first.',
        )

    publish_document = WriterDocument.model_validate(
        _json_loads(_set_document_editable(document, stage='final'), {}),
    )
    write_result = WriterResourceTools(
        llm=None,
        artifact_store=str(root),
    ).write_to_document(publish_document, target)
    refreshed = WriterResourceTools(
        llm=None,
        artifact_store=str(root),
    ).document_to_docir(TargetDocument(
        **target.model_dump(exclude={'meta'}),
        meta={**target.meta, 'stage': 'final'},
    ))
    published_json = _set_document_editable(
        _read_json_file(_result_path(refreshed)),
        stage='final',
    )
    published_link = str(
        target.meta.get('browser_url')
        or (target.uri if target.uri.startswith(('http://', 'https://')) else '')
    ).strip()
    if not published_link:
        raise ValueError('Feishu write succeeded but no browser URL was returned.')
    return {
        'publish_result': _result_path(write_result),
        'published_document': _save_json_artifact(
            'published_document',
            published_json,
            WriterToolkitBase.WRITER_IR_SCHEMA,
            directory=root,
        ),
        'published_link': published_link,
    }


def writer_create_document(
    title: str,
    parent_uri: str = '',
) -> str:
    """Create an empty Feishu document and return its target artifact."""
    root = _run_root('create-document')
    resolved_title = title.strip() or '未命名文档'
    result = WriterResourceTools(
        llm=None,
        artifact_store=str(root),
    ).create_document(
        title=resolved_title,
        parent_uri=parent_uri.strip(),
        adapter='feishu',
    )
    return _result_path(result)
