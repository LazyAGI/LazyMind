"""Stateless document-tool execution for Workflow adapters.

This module owns document operation sequencing and result shaping.  A caller
supplies only Runtime-bound I/O hooks (context, artifact paths, persistence,
and event forwarding) through :func:`invoke`.
"""

from __future__ import annotations

from contextvars import ContextVar
import hashlib
import json
import re
import uuid
from pathlib import Path
from typing import Any, Mapping

from .artifacts import (
    WRITER_BLOCK_SCHEMA,
    WRITER_IR_SCHEMA,
    detach_provider_binding,
    markdown_filename,
    markdown_to_writer_document,
    normalize_writer_document,
    persist_artifact_json,
    render_document,
    writer_schema,
)
from .revision import (
    apply_document_revision,
    generate_revision_set,
)
from .toolkits import (
    DraftMarkdownStreamEventEmitter as _DraftMarkdownStreamEventEmitter,
    WriterCreateToolkit as _WriterCreateToolkit,
    WriterResourceToolkit as _WriterResourceToolkit,
    WriterRevisionToolkit as _WriterRevisionToolkit,
)
from .writing import (
    assemble_draft_document,
    assemble_markdown_document,
    collect_document_media,
    finalize_short_document,
    generate_outline,
    generate_short_visual_plan,
    generate_short_writing_plan,
    parse_writer_request_constraints as _parse_writer_request_constraints,
    profile_document_resources,
    resolve_visual_media,
    stream_short_document,
)


_HOST: ContextVar[Mapping[str, Any]] = ContextVar('document_execution_host')
_LOCAL_WRITER_DOCUMENT_SUFFIXES = {'.md', '.markdown', '.txt', '.lmd'}


def _host_call(name: str, *args: Any, **kwargs: Any) -> Any:
    return _HOST.get()[name](*args, **kwargs)


def invoke(host: Mapping[str, Any], name: str, arguments: dict[str, Any]) -> Any:
    """Run a stateless operation with Runtime-owned I/O hooks."""
    token = _HOST.set(host)
    try:
        return globals()[name](**arguments)
    finally:
        _HOST.reset(token)


def _execution_fingerprint(**values: Any) -> str:
    payload = json.dumps(values, ensure_ascii=False, sort_keys=True)
    return hashlib.sha256(payload.encode('utf-8')).hexdigest()


def require_context() -> Any:
    return _host_call('require_context')


def WriterCreateToolkit() -> Any:
    factory = _HOST.get().get('WriterCreateToolkit', _WriterCreateToolkit)
    return factory()


def WriterResourceToolkit() -> Any:
    factory = _HOST.get().get('WriterResourceToolkit', _WriterResourceToolkit)
    return factory()


def WriterRevisionToolkit() -> Any:
    factory = _HOST.get().get('WriterRevisionToolkit', _WriterRevisionToolkit)
    return factory()


def DraftMarkdownStreamEventEmitter(*args: Any, **kwargs: Any) -> Any:
    factory = _HOST.get().get(
        'DraftMarkdownStreamEventEmitter', _DraftMarkdownStreamEventEmitter
    )
    return factory(*args, **kwargs)


def _emit_writer_progress(*args: Any, **kwargs: Any) -> Any:
    return _host_call('_emit_writer_progress', *args, **kwargs)


def _run_root(*args: Any, **kwargs: Any) -> Any:
    return _host_call('_run_root', *args, **kwargs)


def _workspace_root(*args: Any, **kwargs: Any) -> Any:
    return _host_call('_workspace_root', *args, **kwargs)


def _writer_build_writing_task(query: str, representation: str = 'markdown') -> str:
    """Build a WritingTask artifact from the user's complete request."""
    if representation not in {'ir', 'markdown'}:
        raise ValueError("representation must be 'ir' or 'markdown'.")
    workflow_session_id = str(require_context().params.get('session_id') or '').strip()
    if not workflow_session_id:
        raise RuntimeError('writer workflow session_id is required to build a stable WritingTask')
    task = _json_loads(WriterCreateToolkit().build_writing_task(
        query=query, task_id=workflow_session_id,
    ), {})
    task['constraints'] = _parse_writer_request_constraints(query)
    task['output'] = {**(task.get('output') or {}), 'representation': representation}
    content = json.dumps(task, ensure_ascii=False)
    return _save_json_artifact('writing_task', content, writer_schema('task.WritingTask'))


def _writer_load_local_document(filename: str = '') -> str:
    """Load one supplied Markdown, text, or Writer IR file as the working document."""
    files_by_turn = require_context().params.get('history_files_per_turn') or {}
    candidates = [
        Path(path)
        for paths in files_by_turn.values()
        for path in paths
        if Path(path).suffix.lower() in _LOCAL_WRITER_DOCUMENT_SUFFIXES
    ]
    if filename:
        candidates = [path for path in candidates if path.name == filename]
    if len(candidates) != 1:
        raise ValueError('Exactly one matching Markdown, text, or .lmd source file is required.')
    source = candidates[0]
    try:
        document = _read_json_file(str(source))
        if source.suffix.lower() == '.lmd':
            document = detach_provider_binding(document)
    except (json.JSONDecodeError, ValueError) as exc:
        if source.suffix.lower() != '.lmd':
            raise
        raise ValueError(f'Cannot parse LMD file {source.name}: {exc}') from exc
    return _save_writer_document(
        'source_document',
        document,
        directory=_run_root('load-local-document'),
    )


def _writer_load_document(user_input: str, stage: str = 'final') -> dict:
    """Load a provider document and preserve its representation and target binding."""
    root = _run_root('load-document')
    payload = _json_loads(
        WriterResourceToolkit().load_document(user_input=user_input, stage=stage),
        {},
    )
    input_resources = payload.get('input_resources') or []
    return {
        'source_document': _save_writer_document(
            'source_document',
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
        'representation': str(payload.get('representation') or ''),
        'input_resources': _save_json_artifact(
            'provider_input_resources',
            json.dumps(input_resources, ensure_ascii=False),
            writer_schema('task.InputResource'),
            directory=root,
        ) if input_resources else '',
        'resource_warnings': list(payload.get('resource_warnings') or []),
    }


def _writer_profile_resources(
    writing_task_path: str,
    user_input: str,
    source_document_path: str = '',
    knowledge_text: str = '',
    profile_input_resources_path: str = '',
) -> str:
    """Profile attachments, a loaded source document, and retrieved KB evidence."""
    if profile_input_resources_path:
        input_resources = _read_json_file(profile_input_resources_path)
        file_paths = []
    else:
        files_by_turn = require_context().params.get('history_files_per_turn') or {}
        file_paths = [path for paths in files_by_turn.values() for path in paths]
        input_resources = []
    profiles = profile_document_resources(
        _read_json_file(writing_task_path),
        user_input,
        file_paths=file_paths,
        source_document=(
            _read_json_file(source_document_path) if source_document_path else None
        ),
        knowledge_text=knowledge_text,
        input_resources=input_resources,
    )
    return _save_json_artifact(
        'resource_profiles', json.dumps(profiles, ensure_ascii=False),
        writer_schema('resource.ResourceProfile'),
    )


def _writer_collect_available_media(
    writing_task_path: str,
    source_document_path: str = '',
    input_resources_path: str = '',
) -> dict:
    """Collect attached and source-document images into the authoritative media library."""
    ctx = require_context()
    files_by_turn = ctx.params.get('history_files_per_turn') or {}
    file_paths: list[str] = []
    seen: set[str] = set()
    for paths in files_by_turn.values():
        for path in paths or []:
            normalized = str(path).strip()
            if normalized and normalized not in seen:
                seen.add(normalized)
                file_paths.append(normalized)

    root = _run_root('collect-media')
    media_root = root / 'media'
    media_root.mkdir(parents=True, exist_ok=True)
    payload = collect_document_media(
        _read_json_file(writing_task_path),
        file_paths=file_paths,
        input_resources=(
            _read_json_file(input_resources_path) if input_resources_path else []
        ),
        source_document=(
            _read_json_file(source_document_path) if source_document_path else None
        ),
        media_store=str(media_root),
    )
    media_assets_path = _save_json_artifact(
        'media_assets',
        json.dumps(payload.get('media_assets') or {}, ensure_ascii=False),
        writer_schema('multimodal.MediaAssetLibrary'),
        directory=root,
    )
    profile_input_resources_path = _save_json_artifact(
        'profile_input_resources',
        json.dumps(payload.get('profile_input_resources') or [], ensure_ascii=False),
        writer_schema('task.InputResource'),
        directory=root,
    )
    return {
        'media_assets': media_assets_path,
        'profile_input_resources': profile_input_resources_path,
        'warnings': payload.get('warnings') or [],
    }


def _writer_create_writing_context(
    writing_task_path: str,
    resource_profiles_path: str,
    source_document_path: str = '',
) -> str:
    """Create WritingContext, optionally incorporating an existing WriterDocument."""
    content = WriterCreateToolkit().create_writing_context(
        writing_task_json=_read_json_string(writing_task_path),
        resource_profiles_json=_read_json_string(resource_profiles_path),
        writer_document_json=(
            _read_json_string(source_document_path) if source_document_path else ''
        ),
    )
    return _save_json_artifact(
        'writing_context', content, writer_schema('context.WritingContext'),
    )


def _writer_prepare_outline(
    source_document_path: str,
    writing_task_path: str = '',
    writing_context_path: str = '',
) -> str:
    """Normalize a loaded outline document without regenerating its content."""
    content = WriterCreateToolkit().prepare_outline(
        source_document_json=_read_json_string(source_document_path),
        writing_task_json=_read_json_string(writing_task_path) if writing_task_path else '',
        writing_context_json=_read_json_string(writing_context_path)
        if writing_context_path else '',
    )
    return _save_writer_document(
        'outline_document', content, expected_stage='outline', editable=True,
    )


def _writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    """Generate an outline-stage artifact with a Markdown preview stream."""
    _emit_writer_progress('正在生成大纲')
    events = DraftMarkdownStreamEventEmitter(
        require_context().emit,
        slot='outline_document',
    )
    output_started = False

    def emit_delta(delta: str) -> None:
        nonlocal output_started
        if not output_started and str(delta).strip():
            output_started = True
            _emit_writer_progress('正在输出大纲内容')
        events.feed(str(delta))

    try:
        generated = generate_outline(
            _read_json_file(writing_task_path),
            _read_json_file(writing_context_path),
            on_delta=emit_delta,
            on_outline_generated=lambda: _emit_writer_progress(
                '大纲生成完成，正在补全写作指令'
            ),
            on_outline_prepared=lambda: _emit_writer_progress(
                '大纲指令已补全，正在校验并保存'
            ),
        )
        outline_path = _save_writer_document(
            'outline_document', generated, expected_stage='outline', editable=True,
        )
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return outline_path


def _writer_generate_rewrite_outline(
    writing_task_path: str,
    source_document_path: str,
    writing_context_path: str,
) -> str:
    """Generate a private outline used only to stream a complete document rewrite."""
    generated = WriterCreateToolkit().generate_rewrite_outline(
        writing_task_json=_read_json_string(writing_task_path),
        source_document_json=_read_json_string(source_document_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'rewrite_outline', generated, WRITER_IR_SCHEMA,
        directory=_run_root('rewrite-outline'),
    )


def _writer_generate_rewrite_section_instructions(
    writing_task_path: str,
    source_document_path: str,
    writing_context_path: str,
) -> dict:
    """Plan a complete IR or Markdown rewrite without creating an outline artifact."""
    payload = _json_loads(WriterCreateToolkit().generate_rewrite_section_instructions(
        writing_task_json=_read_json_string(writing_task_path),
        source_document_json=_read_json_string(source_document_path),
        writing_context_json=_read_json_string(writing_context_path),
    ), {})
    section_instructions = payload.get('section_instructions') or {}
    visual_plan = payload.get('visual_plan') or {'instructions': []}
    visual_needs = visual_plan.get('instructions') or []
    return {
        'section_instructions': _save_json_artifact(
            'section_instructions',
            json.dumps(section_instructions, ensure_ascii=False),
            writer_schema('planning.SectionInstructionList'),
        ),
        'visual_plan': _save_json_artifact(
            'visual_plan',
            json.dumps(visual_plan, ensure_ascii=False),
            writer_schema('multimodal.VisualPlan'),
        ),
        'visual_need_count': len(visual_needs),
        'section_count': len(section_instructions.get('instructions') or []),
        'visual_need_ids': [str(need.get('need_id') or '') for need in visual_needs],
        'document_title': payload.get('document_title') or '',
        'warnings': payload.get('warnings') or [],
    }


def _writer_generate_section_instructions(
    writing_task_path: str,
    outline_path: str,
    writing_context_path: str,
) -> dict:
    """Generate internal section instructions from the selected outline IR."""
    payload = _json_loads(WriterCreateToolkit().generate_section_instructions(
        writing_task_json=_read_json_string(writing_task_path),
        outline_json=_read_json_string(outline_path),
        writing_context_json=_read_json_string(writing_context_path),
    ), {})
    section_instructions = payload.get('section_instructions') or {}
    visual_plan = payload.get('visual_plan') or {'instructions': []}
    visual_needs = visual_plan.get('instructions') or []
    return {
        'section_instructions': _save_json_artifact(
            'section_instructions',
            json.dumps(section_instructions, ensure_ascii=False),
            writer_schema('planning.SectionInstructionList'),
        ),
        'visual_plan': _save_json_artifact(
            'visual_plan',
            json.dumps(visual_plan, ensure_ascii=False),
            writer_schema('multimodal.VisualPlan'),
        ),
        'visual_need_count': len(visual_needs),
        'section_count': len(section_instructions.get('instructions') or []),
        'visual_need_ids': [str(need.get('need_id') or '') for need in visual_needs],
        'warnings': payload.get('warnings') or [],
    }


def _writer_execute_writing_subtasks(
    outline_path: str,
    writing_context_path: str,
) -> str:
    """Execute outline-owned writing subtasks without adding a workflow step."""
    emit_progress = _HOST.get()['_emit_writer_progress']
    content = WriterCreateToolkit().execute_writing_subtasks(
        outline_json=_read_json_string(outline_path),
        writing_context_json=_read_json_string(writing_context_path),
        on_progress=lambda subtasks: emit_progress(
            '正在执行写作子任务', writing_subtasks=subtasks,
        ),
    )
    return _save_writer_document(
        'outline_document', content, expected_stage='outline', editable=True,
    )


def _writer_generate_short_writing_plan(
    writing_task_path: str,
    writing_context_path: str,
) -> str:
    """Generate and persist one whole-document plan for a flat article."""
    content = json.dumps(
        generate_short_writing_plan(
            writing_task_path,
            writing_context_path,
            artifact_store=str(_run_root('short-writing-plan-source')),
        ),
        ensure_ascii=False,
    )
    return _save_json_artifact(
        'short_writing_plan',
        content,
        writer_schema('planning.ShortWritingPlan'),
        directory=_run_root('short-writing-plan'),
    )


def _writer_generate_short_visual_plan(
    writing_task_path: str,
    short_writing_plan_path: str,
    writing_context_path: str,
) -> dict:
    """Generate and persist one visual plan for a flat article."""
    result = generate_short_visual_plan(
        writing_task_path,
        short_writing_plan_path,
        writing_context_path,
        artifact_store=str(_run_root('short-visual-plan-source')),
    )
    visual_plan = result['visual_plan']
    instructions = visual_plan['instructions']
    return {
        'visual_plan': _save_json_artifact(
            'visual_plan',
            json.dumps(visual_plan, ensure_ascii=False),
            writer_schema('multimodal.VisualPlan'),
            directory=_run_root('short-visual-plan'),
        ),
        'visual_need_count': len(instructions),
        'visual_need_ids': [str(need.get('need_id') or '') for need in instructions],
        'warnings': result['warnings'],
    }


def _writer_generate_short_document(
    writing_task_path: str,
    short_writing_plan_path: str,
    writing_context_path: str,
    visual_plan_path: str = '',
    resolved_media_assets_path: str = '',
) -> str:
    """Generate and persist one complete flat short document."""
    events = DraftMarkdownStreamEventEmitter(
        require_context().emit,
        slot='flat_draft_document',
    )
    try:
        document = stream_short_document(
            writing_task_path,
            short_writing_plan_path,
            writing_context_path,
            artifact_store=str(_run_root('short-document-source')),
            visual_plan_path=visual_plan_path,
            media_assets_path=resolved_media_assets_path,
            on_delta=events.feed,
        )
        document = finalize_short_document(
            document,
            (
                _read_json_file(resolved_media_assets_path)
                if resolved_media_assets_path else None
            ),
        )
        path = _save_writer_document(
            'draft_document',
            document,
            expected_stage='draft',
            editable=True,
            directory=_run_root('short-document'),
        )
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return path


def _writer_resolve_visual_media(
    visual_plan_path: str,
    media_assets_path: str,
    strict_required: bool = False,
    allowed_strategies_json: str = '',
) -> dict:
    """Resolve visual needs and materialize missing media through registered acquirers.

    allowed_strategies_json: optional JSON list restricting acquisition strategies.
    """
    root = _run_root('resolve-media')
    media_root = root / 'media'
    media_root.mkdir(parents=True, exist_ok=True)
    result = resolve_visual_media(
        _read_json_file(visual_plan_path),
        _read_json_file(media_assets_path),
        media_store=str(media_root),
        strict_required=strict_required,
        allowed_strategies=_json_loads(allowed_strategies_json, None),
    )
    resolved_path = persist_artifact_json(
        result['media_assets'],
        str(root / 'resolved_media_assets.json'),
        schema_name=writer_schema('multimodal.MediaAssetLibrary'),
        created_by='writer-workflow-wrapper',
    )
    return {
        'resolved_media_assets': resolved_path,
        'warnings': result['warnings'],
    }


def _writer_resolve_revision_media(
    modify_plan_path: str,
    media_assets_path: str,
) -> dict:
    """Resolve required image additions in a revision plan without partial success."""
    root = _run_root('revision-visual-plan')
    visual_plan_json = WriterRevisionToolkit().build_revision_visual_plan(
        modify_plan_json=_read_json_string(modify_plan_path),
    )
    visual_plan_path = _save_json_artifact(
        'visual_plan',
        visual_plan_json,
        writer_schema('multimodal.VisualPlan'),
        directory=root,
    )
    return _writer_resolve_visual_media(
        visual_plan_path=visual_plan_path,
        media_assets_path=media_assets_path,
        strict_required=True,
        allowed_strategies_json=json.dumps(['web_search', 'image_generation']),
    )


def _writer_generate_draft_blocks(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    visual_plan_path: str = '',
    media_assets_path: str = '',
    checkpoint_dir: str = '',
) -> list[str]:
    """Generate and persist all planned draft blocks."""
    context = require_context()
    events = DraftMarkdownStreamEventEmitter(context.emit)

    def emit_progress(payload: dict[str, Any]) -> None:
        context.emit({'type': 'progress', **payload})

    try:
        blocks = _json_loads(WriterCreateToolkit().stream_draft_blocks_ir(
            writing_task_json=_read_json_string(writing_task_path),
            section_instructions_json=_read_json_string(section_instructions_path),
            writing_context_json=_read_json_string(writing_context_path),
            visual_plan_json=(
                _read_json_string(visual_plan_path) if visual_plan_path else ''
            ),
            media_assets_json=(
                _read_json_string(media_assets_path) if media_assets_path else ''
            ),
            on_delta=events.feed,
            on_section_end=events.flush,
            on_progress=emit_progress,
            on_preview_restart=events.restart,
            checkpoint_dir=checkpoint_dir,
        ), [])
        root = _run_root('draft-blocks')
        paths = []
        for index, block in enumerate(blocks, start=1):
            paths.append(_save_json_artifact(
                f'draft_block_{index:04d}',
                json.dumps(block, ensure_ascii=False),
                WRITER_BLOCK_SCHEMA,
                directory=root,
            ))
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return paths


def _writer_generate_draft_blocks_markdown(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    visual_plan_path: str = '',
    checkpoint_dir: str = '',
) -> list[str]:
    """Generate and persist all planned draft sections as Markdown."""
    context = require_context()
    events = DraftMarkdownStreamEventEmitter(context.emit)

    def emit_progress(payload: dict[str, Any]) -> None:
        context.emit({'type': 'progress', **payload})

    try:
        sections = _json_loads(WriterCreateToolkit().stream_draft_blocks_markdown(
            writing_task_json=_read_json_string(writing_task_path),
            section_instructions_json=_read_json_string(section_instructions_path),
            writing_context_json=_read_json_string(writing_context_path),
            visual_plan_json=(
                _read_json_string(visual_plan_path) if visual_plan_path else ''
            ),
            on_delta=events.feed,
            on_section_end=events.flush,
            on_progress=emit_progress,
            on_preview_restart=events.restart,
            checkpoint_dir=checkpoint_dir,
        ), [])
        root = _run_root('draft-sections-markdown')
        paths = []
        for index, section in enumerate(sections, start=1):
            path = root / f'draft_section_{index:04d}.md'
            path.write_text(str(section), encoding='utf-8')
            paths.append(str(path))
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return paths


def _writer_update_writing_context(
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


def _writer_export_markdown(content_path: str) -> str:
    """Export the latest WriterDocument as a downloadable Markdown file."""
    payload = _json_loads(WriterCreateToolkit().render_markdown(
        writer_document_json=_read_json_string(content_path),
    ), {})
    output_path = _run_root('export-markdown') / markdown_filename(
        str(payload.get('title') or ''),
    )
    output_path.write_text(str(payload.get('markdown') or ''), encoding='utf-8')
    return str(output_path)


def _writer_render_document(artifact: Any) -> dict:
    """Render a Writer IR or Markdown artifact with automatic numbering."""
    return render_document(_action_artifact_data(artifact))


def _writer_build_revision_task(query: str, base_document_path: str) -> str:
    """Build a revision task for either an outline or a full document."""
    content = WriterRevisionToolkit().build_revision_task(
        query=query,
        writer_document_json=_read_json_string(base_document_path),
        allow_outline=require_context().params.get('step_id') != 'write_document',
    )
    return _save_json_artifact(
        'revision_task', content, writer_schema('task.WritingTask'),
        directory=_run_root('revision-task'),
    )


def _writer_locate_revision_target(
    base_document_path: str,
    writing_context_path: str,
    revision_task_path: str,
) -> str:
    """Locate the WriterDocument blocks affected by a revision task."""
    content = WriterRevisionToolkit().locate_revision_target(
        writing_task_json=_read_json_string(revision_task_path),
        writer_document_json=_read_json_string(base_document_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'locate_result', content, writer_schema('revision.LocateResult'),
        directory=_run_root('revision-locate'),
    )


def _writer_generate_modify_plan(
    base_document_path: str,
    writing_context_path: str,
    revision_task_path: str,
    locate_result_path: str,
) -> str:
    """Build a ModifyPlan for the located revision targets."""
    content = WriterRevisionToolkit().generate_modify_plan(
        writing_task_json=_read_json_string(revision_task_path),
        writer_document_json=_read_json_string(base_document_path),
        locate_result_json=_read_json_string(locate_result_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_json_artifact(
        'modify_plan', content, writer_schema('revision.ModifyPlan'),
        directory=_run_root('revision-plan'),
    )


def _writer_generate_revision_set(
    base_document_path: str,
    writing_context_path: str,
    modify_plan_path: str,
    media_assets_path: str = '',
) -> str:
    """Generate an IR PatchSet or Markdown StringReplaceSet from a ModifyPlan."""
    result = generate_revision_set(
        _read_json_file(base_document_path),
        _read_json_file(writing_context_path),
        _read_json_file(modify_plan_path),
        media_assets=(
            _read_json_file(media_assets_path) if media_assets_path else None
        ),
    )
    return _save_json_artifact(
        'revision_set', json.dumps(result['revision_set'], ensure_ascii=False),
        result['schema_name'],
        directory=_run_root('revision-patch'),
    )


def _writer_apply_revision(
    base_document_path: str,
    writing_context_path: str,
    revision_set_path: str,
    media_assets_path: str = '',
) -> dict:
    """Apply an IR patch or Markdown string replacements locally."""
    root = _run_root('apply-revision')
    is_body_step = require_context().params.get('step_id') == 'write_document'
    base_document = _read_json_file(base_document_path)
    applied = apply_document_revision(
        base_document,
        _read_json_file(writing_context_path),
        _read_json_file(revision_set_path),
        media_assets=(
            _read_json_file(media_assets_path) if media_assets_path else None
        ),
        sync_provider=not is_body_step,
        allow_outline=not is_body_step,
    )
    payload = applied['payload']
    is_markdown = isinstance(base_document, str)
    document_key = 'draft_document' if is_body_step else 'outline_document'
    revised_document = _save_writer_document(
        document_key,
        payload.get('revised_document') or {},
        expected_stage=(None if is_markdown or is_body_step else 'outline'),
        editable=is_body_step,
        directory=root,
    )
    if is_body_step:
        _emit_draft_markdown_preview(revised_document)

    result = {
        'revision_result': _save_json_artifact(
            'revision_result',
            json.dumps(
                applied['result'],
                ensure_ascii=False,
            ),
            applied['schema_name'],
            directory=root,
        ),
        document_key: revised_document,
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


def _writer_convert_markdown_to_ir(content_path: str, stage: str = 'final') -> str:
    """Convert the supported Markdown subset to Writer IR for provider delivery."""
    markdown = _read_json_string(content_path)
    document = markdown_to_writer_document(
        markdown,
        document_id=f'writer-document-{uuid.uuid4()}',
        stage=stage,
    )
    return _save_writer_document(
        'delivery_document',
        document.model_dump(exclude_defaults=True),
        expected_stage=stage,
        directory=_run_root('markdown-to-ir'),
    )


def _writer_publish_revision(
    source_document_path: str,
    revision_set_path: str,
    media_assets_path: str = '',
) -> dict:
    """Apply a prepared local revision to its bound source document."""
    root = _run_root('publish-revision')
    payload = _json_loads(WriterResourceToolkit().publish_revision(
        source_document_json=_read_json_string(source_document_path),
        patch_set_json=_read_json_string(revision_set_path),
        media_assets_json=(
            _read_json_string(media_assets_path) if media_assets_path else ''
        ),
    ), {})
    return _save_publish_payload(payload, root)


def _writer_convert_document(
    content_path: str,
    provider: str = '',
    target_document_path: str = '',
    media_assets_path: str = '',
    output_format: str = 'native',
    template: str = '',
) -> str:
    """Convert canonical Writer content to a copyable provider artifact."""
    if not provider and target_document_path:
        provider = str(
            _read_json_file(target_document_path).get('adapter') or ''
        ).strip()
    content = WriterResourceToolkit().convert_document(
        content_json=_read_json_string(content_path),
        provider=provider,
        output_format=output_format,
        target_document_json=(
            _read_json_string(target_document_path) if target_document_path else ''
        ),
        media_assets_json=(
            _read_json_string(media_assets_path) if media_assets_path else ''
        ),
        template=template,
    )
    if output_format != 'native':
        return content
    return _save_json_artifact(
        'converted_document', content,
        'lazyllm.tools.writer.provider.base.WriterProviderDocument',
        directory=_run_root('convert-document'),
    )


def _writer_write_document(
    converted_document_path: str,
    target_document_path: str = '',
    media_assets_path: str = '',
    title: str = '',
    parent_uri: str = '',
    mode: str = 'replace',
) -> dict:
    """Write one converted provider artifact and return confirmed content."""
    root = _run_root('write-document')
    payload = _json_loads(WriterResourceToolkit().write_document(
        converted_document_json=_read_json_string(converted_document_path),
        target_document_json=(
            _read_json_string(target_document_path) if target_document_path else ''
        ),
        media_assets_json=(
            _read_json_string(media_assets_path) if media_assets_path else ''
        ),
        title=title,
        parent_uri=parent_uri,
        mode=mode,
    ), {})
    return _save_publish_payload(payload, root)


def _writer_create_document(
    title: str,
    parent_uri: str = '',
    adapter: str = '',
) -> str:
    """Create an empty provider document and return its target artifact."""
    root = _run_root('create-document')
    content = WriterResourceToolkit().create_document(
        title=title,
        parent_uri=parent_uri,
        adapter=adapter,
    )
    return _save_json_artifact(
        'target_document',
        content,
        writer_schema('task.TargetDocument'),
        directory=root,
    )


def _writer_prepare_markdown_for_editor(
    document_path: str,
    target_document_path: str,
) -> tuple[str, str]:
    """Save the editor document and target returned by the Writer resource tool."""
    source = Path(str(document_path or ''))
    if source.suffix.lower() not in {'.md', '.markdown'} or not source.is_file() \
            or not target_document_path:
        return str(document_path), ''
    markdown = source.read_text(encoding='utf-8')
    payload = _json_loads(WriterResourceToolkit().prepare_markdown_for_editor(
        markdown=markdown,
        target_document_json=_read_json_string(target_document_path),
    ), {})
    prepared = str(payload.get('markdown') or '')
    updated_target = payload.get('target_document')
    if prepared == markdown and not updated_target:
        return str(document_path), ''
    root = _run_root('prepare-markdown-for-editor')
    prepared_path = root / source.name
    prepared_path.write_text(prepared, encoding='utf-8')
    target_path = (
        _save_json_artifact(
            'target_document', json.dumps(updated_target, ensure_ascii=False),
            writer_schema('task.TargetDocument'), directory=root,
        )
        if updated_target else ''
    )
    return str(prepared_path), target_path


def _read_json_file(path: str) -> Any:
    if Path(path).suffix.lower() in {'.md', '.markdown', '.txt'}:
        return Path(path).read_text(encoding='utf-8')
    with open(path, 'r', encoding='utf-8') as fh:
        raw = json.load(fh)
    if isinstance(raw, dict) and 'data' in raw:
        return raw['data']
    return raw


def _action_artifact_data(value: Any) -> Any:
    if isinstance(value, Mapping):
        if 'data' in value:
            return value['data']
        path = value.get('path')
        if isinstance(path, str) and path:
            return _read_json_file(path)
        return dict(value)
    if isinstance(value, str):
        candidate = Path(value)
        if candidate.is_file():
            return _read_json_file(value)
        try:
            return _json_loads(value, value)
        except json.JSONDecodeError:
            return value
    raise TypeError('artifact must be a JSON value, Markdown string, or file reference.')


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


def _save_json_artifact(
    name: str,
    content_json: str,
    schema_name: str,
    *,
    directory: Path | None = None,
    extra_meta: dict[str, Any] | None = None,
) -> str:
    root = directory or _workspace_root()
    root.mkdir(parents=True, exist_ok=True)
    extension = (
        '.lmd'
        if schema_name
        in {
            WRITER_IR_SCHEMA,
            WRITER_BLOCK_SCHEMA,
        }
        else '.json'
    )
    return persist_artifact_json(
        _json_loads(content_json, {}),
        str(root / f'{name}{extension}'),
        schema_name=schema_name,
        created_by='writer-workflow-wrapper',
        extra_meta=extra_meta,
    )


def _save_writer_document(
    name: str,
    value: str | dict,
    *,
    expected_stage: str | None = None,
    editable: bool = False,
    directory: Path | None = None,
    extra_meta: dict[str, Any] | None = None,
) -> str:
    """Persist a document as .lmd or .md according to its representation."""
    content = normalize_writer_document(
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
        name, content, WRITER_IR_SCHEMA, directory=directory,
        extra_meta=extra_meta,
    )


def _emit_draft_markdown_preview(document_path: str) -> None:
    """Publish a saved writer document through the draft Markdown stream."""
    try:
        document = _read_json_file(document_path)
        rendered = _writer_render_document(document)
        representation = rendered.get('representation')
        if representation == 'markdown':
            markdown = str(rendered.get('document') or '')
        elif representation == 'ir':
            preview = _json_loads(
                WriterCreateToolkit().render_markdown(
                    writer_document_json=json.dumps(
                        document, ensure_ascii=False,
                    ),
                ),
                {},
            )
            markdown = str(preview.get('markdown') or '')
        else:
            markdown = ''
    except Exception:
        return
    if not markdown:
        return

    events = DraftMarkdownStreamEventEmitter(require_context().emit)
    for offset in range(0, len(markdown), 8192):
        events.feed(markdown[offset:offset + 8192])
    events.end()


def _save_publish_payload(payload: dict, root: Path) -> dict:
    draft_document = payload.get('draft_document') or {}
    publish_result = payload.get('publish_result') or {}
    target_document = payload.get('target_document') or {}
    if isinstance(publish_result, dict):
        publish_result = {
            **publish_result,
            'success': bool(publish_result.get('success', draft_document)),
        }
    result = {
        'publish_result': _save_json_artifact(
            'publish_result',
            json.dumps(publish_result, ensure_ascii=False),
            writer_schema('revision.PatchResult'),
            directory=root,
        ),
        'draft_document': _save_writer_document(
            'draft_document',
            draft_document,
            editable=True,
            directory=root,
            extra_meta={
                'lazymind_provider_sync': {
                    'confirmed': True,
                    'provider': str(payload.get('provider') or ''),
                    'source': 'initial_auto',
                },
            },
        ),
        'published_link': str(payload.get('published_link') or ''),
    }
    if target_document:
        result['target_document'] = _save_json_artifact(
            'target_document',
            json.dumps(target_document, ensure_ascii=False),
            writer_schema('task.TargetDocument'),
            directory=root,
        )
    return result


def _assemble_draft_document_ir(
    draft_blocks_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
    document_title: str = '',
) -> str:
    """Combine draft WriterBlock artifacts into a draft WriterDocument."""
    anchor = (
        Path(draft_blocks_anchor_path)
        if draft_blocks_anchor_path
        else _workspace_root() / 'draft_blocks'
    )
    if not anchor.exists():
        candidates = sorted(
            (_workspace_root() / 'writer-workflow').glob('draft-blocks-*'),
            key=lambda path: path.stat().st_mtime,
            reverse=True,
        )
        if candidates:
            anchor = candidates[0]
    draft_blocks_dir = anchor if anchor.is_dir() else anchor.parent
    draft_block_paths = sorted(
        (str(path) for path in draft_blocks_dir.glob('draft_block_*.lmd')),
        key=lambda path: int(re.match(r'draft_block_(\d+)', Path(path).stem).group(1)),
    )
    if not draft_block_paths:
        raise ValueError(
            'draft_blocks_anchor_path must point to a generated draft block file or directory.',
        )

    draft_blocks = [_read_json_file(path) for path in draft_block_paths]
    content = assemble_draft_document(
        draft_blocks,
        _read_json_file(writing_context_path),
        outline=_read_json_file(outline_path) if outline_path else '',
        title=document_title,
        assembler=WriterCreateToolkit().generate_draft_document,
    )
    return _save_writer_document(
        'draft_document',
        content,
        expected_stage='draft',
        editable=True,
    )


def _assemble_draft_document_markdown(
    draft_sections_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
    document_title: str = '',
    resolved_media_assets_path: str = '',
) -> str:
    """Assemble Markdown sections and preserve the Markdown document."""
    anchor = (
        Path(draft_sections_anchor_path)
        if draft_sections_anchor_path
        else _workspace_root() / 'draft_sections'
    )
    if not anchor.exists():
        candidates = sorted(
            (_workspace_root() / 'writer-workflow').glob('draft-sections-*'),
            key=lambda path: path.stat().st_mtime,
            reverse=True,
        )
        if candidates:
            anchor = candidates[0]
    sections_dir = anchor if anchor.is_dir() else anchor.parent
    section_paths = sorted(
        sections_dir.glob('draft_section_*.md'),
        key=lambda path: int(re.match(r'draft_section_(\d+)', path.stem).group(1)),
    )
    if not section_paths:
        raise ValueError(
            'draft_sections_anchor_path must point to a generated Markdown section or directory.',
        )
    sections = [path.read_text(encoding='utf-8') for path in section_paths]
    markdown = assemble_markdown_document(
        sections,
        _read_json_file(writing_context_path),
        outline=_read_json_file(outline_path) if outline_path else '',
        title=document_title,
        resolved_media_assets=(
            _read_json_file(resolved_media_assets_path)
            if resolved_media_assets_path
            else None
        ),
        assembler=WriterCreateToolkit().generate_draft_document_markdown,
    )
    root = _run_root('draft-document-markdown')
    return _save_writer_document(
        'draft_document',
        markdown,
        expected_stage='draft',
        editable=True,
        directory=root,
    )


def _writer_generate_draft_document(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    outline_path: str = '',
    visual_plan_path: str = '',
    resolved_media_assets_path: str = '',
    document_title: str = '',
) -> dict:
    """Generate sections concurrently, stream in outline order, and assemble the draft."""
    task = _read_json_file(writing_task_path)
    representation = str(
        ((task.get('output') or {}).get('representation') or '')
    ).strip()
    checkpoint_key = _execution_fingerprint(
        version=1,
        task=_read_json_string(writing_task_path),
        instructions=_read_json_string(section_instructions_path),
        context=_read_json_string(writing_context_path),
        outline=_read_json_string(outline_path) if outline_path else '',
        visual_plan=_read_json_string(visual_plan_path) if visual_plan_path else '',
        media_assets=(
            _read_json_string(resolved_media_assets_path)
            if resolved_media_assets_path
            else ''
        ),
        document_title=document_title,
        representation=representation,
    )
    checkpoint_dir = str(
        _workspace_root() / 'writer-workflow' / f'draft-sections-{checkpoint_key}'
    )
    if representation == 'markdown':
        draft_blocks = _writer_generate_draft_blocks_markdown(
            writing_task_path=writing_task_path,
            section_instructions_path=section_instructions_path,
            writing_context_path=writing_context_path,
            visual_plan_path=visual_plan_path,
            checkpoint_dir=checkpoint_dir,
        )
        require_context().emit(
            {
                'type': 'progress',
                'progress': 5,
                'current_phase': '章节已生成，正在组装文档并校验编号与引用',
            }
        )
        draft_document = _assemble_draft_document_markdown(
            draft_sections_anchor_path=draft_blocks[0] if draft_blocks else '',
            writing_context_path=writing_context_path,
            outline_path=outline_path,
            document_title=document_title,
            resolved_media_assets_path=resolved_media_assets_path,
        )
    elif representation == 'ir':
        draft_blocks = _writer_generate_draft_blocks(
            writing_task_path=writing_task_path,
            section_instructions_path=section_instructions_path,
            writing_context_path=writing_context_path,
            visual_plan_path=visual_plan_path,
            media_assets_path=resolved_media_assets_path,
            checkpoint_dir=checkpoint_dir,
        )
        require_context().emit(
            {
                'type': 'progress',
                'progress': 5,
                'current_phase': '章节已生成，正在组装文档并校验编号与引用',
            }
        )
        draft_document = _assemble_draft_document_ir(
            draft_blocks_anchor_path=draft_blocks[0] if draft_blocks else '',
            writing_context_path=writing_context_path,
            outline_path=outline_path,
            document_title=document_title,
        )
    else:
        raise ValueError(
            "writing_task output representation must be 'markdown' or 'ir'."
        )
    require_context().emit(
        {
            'type': 'progress',
            'progress': 5,
            'current_phase': '文档组装完成，正在保存结果',
        }
    )
    return {
        'draft_blocks': draft_blocks,
        'draft_document': draft_document,
        'representation': representation,
    }
