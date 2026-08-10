"""Artifact-path adapters for the unified writer workflow.

The workflow owns orchestration only. Writing, revision, document conversion, and
provider synchronization continue to use the existing LazyMind/LazyLLM writer
tooling and the existing workflow artifact mechanism.
"""
from __future__ import annotations

import json
import re
import tempfile
import uuid
from copy import deepcopy
from pathlib import Path
from typing import Any, Callable, Mapping

from lazyllm import AutoModel
from lazyllm.tools.writer.data_models import (
    ContentRef,
    ModifyInstruction,
    ModifyPlan,
    MediaAssetLibrary,
    PatchResult,
    PatchSet,
    StringReplaceSet,
    TargetDocument,
    WriterDocument,
)
from lazyllm.tools.writer.tools import WriterResourceTools, WriterRevisionTools
from lazyllm.tools.writer.tools.revision_tools import apply_patch_to_ir
from lazyllm.tools.writer.utils import (
    load_artifact_json,
    parse_document_markdown,
    render_document_markdown,
    save_artifact_json,
)
from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.engine.prompts.writer_media import WRITER_IMAGE_ACQUISITION_PROMPT
from lazymind.chat.engine.tools.writer import (
    DraftMarkdownStreamEventEmitter,
    WriterCreateToolkit,
    WriterResourceToolkit,
    WriterRevisionToolkit,
    WriterToolkitBase,
    writer_schema,
)
from lazymind.chat.engine.tools.multimodal import image_generator
from lazymind.model_config import is_model_role_available


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


def _action_context(document: Any) -> dict:
    return {
        'context_id': f'selection-{uuid.uuid4().hex}',
        'doc_id': document.get('document_id') if isinstance(document, dict) else None,
        'meta': {'source': 'rewrite_selection_action'},
    }


def _action_root(artifact_store: str, name: str) -> Path:
    base = Path(artifact_store) if artifact_store else Path(tempfile.gettempdir())
    root = base / 'writer-workflow' / f'{name}-{uuid.uuid4().hex}'
    root.mkdir(parents=True, exist_ok=True)
    return root


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
    extra_meta: dict[str, Any] | None = None,
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
        extra_meta=extra_meta,
    )


def _emit_draft_markdown_preview(document_path: str) -> None:
    """Publish a saved writer document through the draft Markdown stream."""
    try:
        document = _read_json_file(document_path)
        markdown = (
            document
            if isinstance(document, str)
            else render_document_markdown(WriterDocument.model_validate(document))
        )
    except Exception:
        return
    if not markdown:
        return

    events = DraftMarkdownStreamEventEmitter(require_context().emit)
    for offset in range(0, len(markdown), 8192):
        events.feed(markdown[offset:offset + 8192])
    events.end()


def _markdown_filename(title: str) -> str:
    filename = re.sub(r'[\\/:*?"<>|\x00-\x1f]+', '_', title).strip(' ._')
    return f'{filename[:80] or "文稿"}.md'


def _save_publish_payload(payload: dict, root: Path) -> dict:
    draft_document = payload.get('draft_document') or {}
    publish_result = payload.get('publish_result') or {}
    if isinstance(publish_result, dict):
        publish_result = {
            **publish_result,
            'success': bool(publish_result.get('success', draft_document)),
        }
    return {
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
                    'provider': 'feishu',
                    'source': 'initial_auto',
                },
            },
        ),
        'published_link': str(payload.get('published_link') or ''),
    }


def writer_build_writing_task(query: str, representation: str = 'markdown') -> str:
    """Build a WritingTask artifact from the user's complete request."""
    if representation not in {'ir', 'markdown'}:
        raise ValueError("representation must be 'ir' or 'markdown'.")
    workflow_session_id = str(require_context().params.get('session_id') or '').strip()
    if not workflow_session_id:
        raise RuntimeError('writer workflow session_id is required to build a stable WritingTask')
    task = _json_loads(WriterCreateToolkit().build_writing_task(
        query=query, task_id=workflow_session_id,
    ), {})
    task['output'] = {**(task.get('output') or {}), 'representation': representation}
    content = json.dumps(task, ensure_ascii=False)
    return _save_json_artifact('writing_task', content, writer_schema('task.WritingTask'))


def writer_load_local_document(filename: str = '') -> str:
    """Load one supplied Markdown, text, or Writer IR file as the working document."""
    files_by_turn = require_context().params.get('history_files_per_turn') or {}
    candidates = [
        Path(path)
        for paths in files_by_turn.values()
        for path in paths
        if Path(path).suffix.lower() in {'.md', '.markdown', '.txt', '.lmd'}
    ]
    if filename:
        candidates = [path for path in candidates if path.name == filename]
    if len(candidates) != 1:
        raise ValueError('Exactly one matching Markdown, text, or .lmd source file is required.')
    source = candidates[0]
    return _save_writer_document(
        'source_document',
        _read_json_file(str(source)),
        directory=_run_root('load-local-document'),
    )


def writer_load_document(user_input: str, stage: str = 'final') -> dict:
    """Load a Feishu/Lark document as source IR and preserve its target binding."""
    root = _run_root('load-document')
    payload = _json_loads(
        WriterResourceToolkit().load_document(user_input=user_input, stage=stage),
        {},
    )
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
    }


def writer_profile_resources(
    writing_task_path: str,
    user_input: str,
    source_document_path: str = '',
    knowledge_text: str = '',
    profile_input_resources_path: str = '',
) -> str:
    """Profile attachments, a loaded source document, and retrieved KB evidence."""
    toolkit = WriterCreateToolkit()
    if profile_input_resources_path:
        resources = _read_json_file(profile_input_resources_path)
        resources.extend(_json_loads(toolkit.build_resources(
            file_paths_json='[]',
            source_document_json=(
                _read_json_string(source_document_path) if source_document_path else ''
            ),
            knowledge_text=knowledge_text,
        ), []))
    else:
        files_by_turn = require_context().params.get('history_files_per_turn') or {}
        file_paths = [path for paths in files_by_turn.values() for path in paths]
        resources = _json_loads(toolkit.build_resources(
            file_paths_json=json.dumps(file_paths, ensure_ascii=False),
            source_document_json=(
                _read_json_string(source_document_path) if source_document_path else ''
            ),
            knowledge_text=knowledge_text,
        ), [])
    content = toolkit.profile_resources(
        writing_task_json=_read_json_string(writing_task_path),
        user_input=user_input,
        resources_json=json.dumps(resources, ensure_ascii=False),
    )
    return _save_json_artifact(
        'resource_profiles', content, writer_schema('resource.ResourceProfile'),
    )


def writer_collect_available_media(writing_task_path: str) -> dict:
    """Collect user-attached images into the task's authoritative media library."""
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

    toolkit = WriterCreateToolkit()
    resources = _json_loads(
        toolkit.build_resources(
            file_paths_json=json.dumps(file_paths, ensure_ascii=False),
        ),
        [],
    )
    root = _run_root('collect-media')
    media_root = root / 'media'
    media_root.mkdir(parents=True, exist_ok=True)
    writing_task_json = _read_json_string(writing_task_path)
    try:
        payload = _json_loads(toolkit.collect_available_media(
            writing_task_json=writing_task_json,
            input_resources_json=json.dumps(resources, ensure_ascii=False),
            media_store=str(media_root),
            use_vision_model=is_model_role_available('vlm'),
        ), {})
    except Exception as exc:
        task_id = str((_json_loads(writing_task_json, {}) or {}).get('task_id') or uuid.uuid4().hex)
        payload = {
            'media_assets': {
                'library_id': f'media-library-{task_id}',
                'assets': {},
            },
            'profile_input_resources': resources,
            'warnings': [
                f'Image collection failed: {type(exc).__name__}: {exc}',
            ],
        }
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


def writer_create_writing_context(
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


def writer_prepare_outline(source_document_path: str) -> str:
    """Normalize a loaded outline document without regenerating its content."""
    content = WriterCreateToolkit().prepare_outline(
        source_document_json=_read_json_string(source_document_path),
    )
    return _save_writer_document(
        'outline_document', content, expected_stage='outline', editable=True,
    )


def writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    """Generate an editable outline-stage WriterDocument."""
    generated = WriterCreateToolkit().generate_outline(
        writing_task_json=_read_json_string(writing_task_path),
        writing_context_json=_read_json_string(writing_context_path),
    )
    return _save_writer_document(
        'outline_document', generated, expected_stage='outline', editable=True,
    )


def writer_generate_rewrite_outline(
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
        'rewrite_outline', generated, WriterToolkitBase.WRITER_IR_SCHEMA,
        directory=_run_root('rewrite-outline'),
    )


def writer_generate_rewrite_section_instructions(
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
    return {
        'section_instructions': _save_json_artifact(
            'section_instructions',
            json.dumps(payload.get('section_instructions') or {}, ensure_ascii=False),
            writer_schema('planning.SectionInstructionList'),
        ),
        'visual_plan': _save_json_artifact(
            'visual_plan',
            json.dumps(payload.get('visual_plan') or {'instructions': []}, ensure_ascii=False),
            writer_schema('multimodal.VisualPlan'),
        ),
        'document_title': payload.get('document_title') or '',
        'warnings': payload.get('warnings') or [],
    }


def writer_generate_section_instructions(
    writing_task_path: str,
    outline_path: str,
    writing_context_path: str,
) -> str:
    """Generate internal section instructions from the selected outline IR."""
    payload = _json_loads(WriterCreateToolkit().generate_section_instructions(
        writing_task_json=_read_json_string(writing_task_path),
        outline_json=_read_json_string(outline_path),
        writing_context_json=_read_json_string(writing_context_path),
    ), {})
    return {
        'section_instructions': _save_json_artifact(
            'section_instructions',
            json.dumps(payload.get('section_instructions') or {}, ensure_ascii=False),
            writer_schema('planning.SectionInstructionList'),
        ),
        'visual_plan': _save_json_artifact(
            'visual_plan',
            json.dumps(payload.get('visual_plan') or {'instructions': []}, ensure_ascii=False),
            writer_schema('multimodal.VisualPlan'),
        ),
        'warnings': payload.get('warnings') or [],
    }


def _acquire_generated_image(
    request: Mapping[str, Any],
    *,
    generator: Callable[..., dict] | None = None,
) -> dict:
    visual_type = str(request.get('visual_type') or '')
    if visual_type not in {'image', 'diagram'}:
        raise ValueError(
            f'image generation does not support visual type {visual_type!r}',
        )
    prompt = WRITER_IMAGE_ACQUISITION_PROMPT.format(
        visual_type=visual_type,
        purpose=str(request.get('purpose') or ''),
    ).strip()
    result = (generator or image_generator)(
        prompt=prompt,
        image_size='1024x1024',
        batch_size=1,
    )
    local_path = str((result or {}).get('local_path') or '').strip()
    if not local_path:
        images = (result or {}).get('images') or []
        if images and isinstance(images[0], dict):
            local_path = str(images[0].get('local_path') or '').strip()
    if not local_path:
        raise ValueError('image_generator returned no local image path')
    return {
        'resource_id': f"acquired-{request.get('instruction_id') or uuid.uuid4().hex}",
        'resource_type': 'image',
        'uri': local_path,
        'title': Path(local_path).name,
        'summary': str(request.get('purpose') or ''),
        'meta': {
            'source_type': 'image_generation',
            'generation_prompt': prompt,
            'summary_source': 'generation_prompt',
            'semantic_status': 'unverified',
        },
    }


def _acquire_visual_media(
    request: Mapping[str, Any],
    acquirers: Mapping[str, Callable[[Mapping[str, Any]], dict]],
) -> dict:
    strategies = request['strategies']
    for strategy in strategies:
        acquirer = acquirers.get(strategy)
        if acquirer is None:
            continue
        resource = dict(acquirer(request))
        resource['meta'] = {
            **dict(resource.get('meta') or {}),
            'requested_strategy': strategies[0],
            'acquisition_strategy': strategy,
        }
        return resource
    raise ValueError(
        f"no media acquirer is available for visual instruction {request.get('instruction_id')!r} "
        f"({request.get('visual_type')}, strategies={strategies})",
    )


def writer_resolve_visual_media(
    visual_plan_path: str,
    media_assets_path: str,
    strict_required: bool = False,
    allowed_strategies_json: str = '',
) -> dict:
    """Resolve visual needs and materialize missing media through registered acquirers."""
    root = _run_root('resolve-media')
    media_root = root / 'media'
    media_root.mkdir(parents=True, exist_ok=True)
    toolkit = WriterCreateToolkit()
    acquirers = {}
    if is_model_role_available('image_generator'):
        acquirers['image_generation'] = _acquire_generated_image
    visual_plan_json = _read_json_string(visual_plan_path)
    media_assets_json = _read_json_string(media_assets_path)
    try:
        matched = _json_loads(toolkit.resolve_visual_needs(
            visual_plan_json=visual_plan_json,
            media_assets_json=media_assets_json,
            allowed_strategies_json=allowed_strategies_json,
        ), {})
    except Exception as exc:
        if strict_required:
            raise
        matched = {
            'media_assets': _json_loads(media_assets_json, {}),
            'acquisition_requests': [],
            'warnings': [
                f'Visual media resolution failed: {type(exc).__name__}: {exc}',
            ],
        }
    warnings = list(matched.get('warnings') or [])
    acquired_resources = {}
    acquired_by_purpose = {}
    for request in matched.get('acquisition_requests') or []:
        instruction_id = str(request['instruction_id'])
        key = (
            str(request.get('visual_type') or ''),
            ' '.join(str(request.get('purpose') or '').split()).casefold(),
        )
        try:
            resource = acquired_by_purpose.get(key)
            if resource is None:
                resource = _acquire_visual_media(request, acquirers)
                acquired_by_purpose[key] = resource
            acquired_resources[instruction_id] = resource
        except Exception as exc:
            if strict_required and request.get('required'):
                raise RuntimeError(
                    f'Failed to acquire required visual media for {instruction_id!r}: '
                    f'{request.get("purpose") or "current visual requirement"}'
                ) from exc
            message = (
                f'Failed to acquire visual instruction {instruction_id!r}: '
                f'{type(exc).__name__}: {exc}'
            )
            warnings.append(f'{message} (required={request.get("required", False)}).')

    try:
        outcome = _json_loads(toolkit.materialize_acquired_media(
            visual_plan_json=visual_plan_json,
            media_assets_json=json.dumps(matched.get('media_assets') or {}, ensure_ascii=False),
            acquired_resources_json=json.dumps(acquired_resources, ensure_ascii=False),
            media_store=str(media_root),
        ), {})
    except Exception as exc:
        if strict_required:
            raise
        outcome = {
            'media_assets': matched.get('media_assets') or {},
            'warnings': [
                f'Acquired media materialization failed: {type(exc).__name__}: {exc}',
            ],
        }
    warnings.extend(outcome.get('warnings') or [])
    resolved_path = save_artifact_json(
        outcome.get('media_assets') or {},
        str(root / 'resolved_media_assets.json'),
        schema_name=writer_schema('multimodal.MediaAssetLibrary'),
        created_by='writer-workflow-wrapper',
    )
    return {
        'resolved_media_assets': resolved_path,
        'warnings': warnings,
    }


def writer_resolve_revision_media(
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
    return writer_resolve_visual_media(
        visual_plan_path=visual_plan_path,
        media_assets_path=media_assets_path,
        strict_required=True,
        allowed_strategies_json=json.dumps(['image_generation']),
    )


def writer_generate_draft_blocks(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    visual_plan_path: str = '',
    media_assets_path: str = '',
) -> list[str]:
    """Generate and persist all planned draft blocks."""
    events = DraftMarkdownStreamEventEmitter(require_context().emit)
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
        ), [])
        root = _run_root('draft-blocks')
        paths = []
        for index, block in enumerate(blocks, start=1):
            paths.append(_save_json_artifact(
                f'draft_block_{index:04d}',
                json.dumps(block, ensure_ascii=False),
                WriterToolkitBase.WRITER_BLOCK_SCHEMA,
                directory=root,
            ))
    except Exception as exc:
        events.abort(str(exc))
        raise
    events.end()
    return paths


def writer_generate_draft_blocks_markdown(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
) -> list[str]:
    """Generate and persist all planned draft sections as Markdown."""
    events = DraftMarkdownStreamEventEmitter(require_context().emit)
    try:
        sections = _json_loads(WriterCreateToolkit().stream_draft_blocks_markdown(
            writing_task_json=_read_json_string(writing_task_path),
            section_instructions_json=_read_json_string(section_instructions_path),
            writing_context_json=_read_json_string(writing_context_path),
            on_delta=events.feed,
            on_section_end=events.flush,
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


def writer_generate_draft_document(
    draft_blocks_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
    document_title: str = '',
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
        title=document_title,
    )
    return _save_writer_document(
        'draft_document', content, expected_stage='draft', editable=True,
    )


def writer_generate_draft_document_markdown(
    draft_sections_anchor_path: str,
    writing_context_path: str,
    outline_path: str = '',
    document_title: str = '',
) -> str:
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
        title=document_title,
    ), {})
    root = _run_root('draft-document-markdown')
    return _save_writer_document(
        'draft_document',
        payload.get('draft_document') or {},
        expected_stage='draft',
        editable=True,
        directory=root,
    )


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


def writer_build_revision_task(query: str, base_document_path: str) -> str:
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


def writer_preview_selection_rewrite(
    artifact: Any,
    instruction: str,
    selection: Mapping[str, Any],
    artifact_store: str = '',
    slot: str = '',
) -> dict:
    """Preview a selected IR block or Markdown paragraph rewrite."""
    instruction = str(instruction or '').strip()
    if not instruction:
        raise ValueError('instruction must not be empty.')
    document = _action_artifact_data(artifact)
    context = _action_context(document)
    revision = WriterRevisionTools(
        llm=AutoModel(model='llm'),
        artifact_store=str(_action_root(artifact_store, 'rewrite-preview')),
    )
    if slot not in {'outline_document', 'draft_document'}:
        raise ValueError(
            'selection rewrite requires an outline_document or draft_document slot.',
        )
    selection_type = str((selection or {}).get('type') or '')
    if isinstance(document, Mapping):
        source = WriterDocument.model_validate(document)
        if selection_type != 'ir':
            raise ValueError("IR artifacts require selection.type='ir'.")
        node_id = str(selection.get('node_id') or '')
        target = source.block_by_id(node_id)
        if target is None:
            raise ValueError('The selected IR node no longer exists.')
        plan = ModifyPlan(scope='block', instructions=[ModifyInstruction(
            instruction_id='rewrite-selection',
            content_ref=ContentRef(node_id=node_id),
            modify_type='update',
            instruction=instruction,
        )])
        output = revision.generate_patch_set(source, plan, context)
        patch_set = load_artifact_json(_action_result_path(output), PatchSet)
        revised, _ = apply_patch_to_ir(source, patch_set)
        candidate_path = Path(_save_writer_document(
            slot, revised,
            expected_stage='outline' if slot == 'outline_document' else None,
            editable=slot == 'draft_document',
            directory=Path(revision.artifact_store),
        ))
        result = {
            'representation': 'ir',
            'target': {'type': 'block', 'block_type': target.type, 'node_id': node_id},
            'preview': {
                'old_text': target.content,
                'new_text': revised.block_by_id(node_id).content,
            },
            'patch': {'type': 'writer_ir_patch', 'payload': patch_set.model_dump()},
        }
    else:
        if selection_type != 'markdown':
            raise ValueError("Markdown artifacts require selection.type='markdown'.")
        replace_set = StringReplaceSet.model_validate(
            revision.build_selected_markdown_replace_set(
                document, instruction, str(selection.get('selected_text') or ''), context,
            ),
        )
        replacement = replace_set.replacements[0]
        output = revision.apply_string_replace(document, replace_set, context)
        candidate_path = Path(output['revised_document_md'])
        canonical_path = candidate_path.with_name(f'{slot}.md')
        if candidate_path != canonical_path:
            candidate_path.replace(canonical_path)
            candidate_path = canonical_path
        result = {
            'representation': 'markdown',
            'target': {'type': 'block', 'block_type': 'paragraph'},
            'preview': {
                'old_text': replacement.old_string,
                'new_text': replacement.new_string,
            },
            'patch': {'type': 'string_replace_set', 'payload': replace_set.model_dump()},
        }
    result['artifact'] = {
        'content_type': 'file',
        'value': {
            'path': str(candidate_path),
            'filename': candidate_path.name,
            'size': candidate_path.stat().st_size,
        },
    }
    return result


def writer_sync_document(
    source_document: Mapping[str, Any] | None = None,
    revised_document: Mapping[str, Any] | None = None,
    media_assets: Mapping[str, Any] | None = None,
    markdown_content: str = '',
    target_document: Mapping[str, Any] | None = None,
    title: str = '',
    artifact_store: str = '',
) -> dict:
    """Persist the selected IR or Markdown draft through its provider adapter."""
    if markdown_content:
        return _sync_markdown_document(
            markdown_content, target_document=target_document, title=title,
            artifact_store=artifact_store,
        )
    if source_document is None or revised_document is None:
        raise ValueError('source_document and revised_document are required for IR sync.')
    source = WriterDocument.model_validate(source_document)
    revised = WriterDocument.model_validate(revised_document)
    if source.document_id != revised.document_id:
        raise ValueError('WriterDocument document_id values must match.')
    for source_block in source.iter_blocks():
        revised_block = revised.block_by_id(source_block.node_id)
        if revised_block is None:
            continue
        revised_block.provider_binding = deepcopy(source_block.provider_binding)
        revised_block.provider_payload = deepcopy(source_block.provider_payload)
        revised_block.editable = source_block.editable
    media_library = MediaAssetLibrary.model_validate(media_assets) if media_assets else None
    root = _action_root(artifact_store, 'sync-document')
    revision = WriterRevisionTools(llm=None, artifact_store=str(root))
    patch_output = revision.build_patch_set_from_documents(source, revised, media_library)
    patch_set = load_artifact_json(_action_result_path(patch_output), PatchSet)
    candidate, local_result = apply_patch_to_ir(source, patch_set, media_assets=media_library)
    if not patch_set.hunks and patch_set.new_title is None:
        candidate.ui_editable = True
        local_result.message = 'No document changes.'
        return _sync_document_response(False, patch_set, local_result, candidate)
    write_output = WriterResourceTools(
        llm=None, artifact_store=str(root),
    ).apply_patch_to_document(patch_set, source, media_assets=media_library)
    persisted = load_artifact_json(
        _action_result_path(write_output, 'persisted_document'), WriterDocument,
    )
    result = load_artifact_json(
        _action_result_path(write_output, 'patch_result'), PatchResult,
    )
    persisted.ui_editable = True
    return _sync_document_response(True, patch_set, result, persisted)


def _sync_markdown_document(
    markdown_content: str,
    *,
    target_document: Mapping[str, Any] | None,
    title: str,
    artifact_store: str,
) -> dict:
    """Convert Markdown against a provider target, replace it, then read back IR."""
    markdown = markdown_content.strip()
    if not markdown:
        raise ValueError('Markdown draft is empty.')
    root = _action_root(artifact_store, 'sync-document')
    if target_document:
        target = TargetDocument.model_validate(target_document)
    else:
        heading = re.search(r'^#\s+(.+?)\s*$', markdown, flags=re.MULTILINE)
        document_title = (heading.group(1).strip() if heading else title.strip()) or '未命名文档'
        created = _json_loads(
            WriterResourceToolkit().create_document(title=document_title), {},
        )
        target = TargetDocument.model_validate(created)

    resource = WriterResourceTools(llm=None, artifact_store=str(root))
    write_output = resource.replace_document(markdown_content, target)
    write_result = _read_json_file(_action_result_path(write_output))
    refresh_target = target.model_copy(deep=True)
    refresh_target.meta = {**refresh_target.meta, 'stage': 'final'}
    refreshed_output = resource.document_to_docir(refresh_target)
    persisted = load_artifact_json(
        _action_result_path(refreshed_output), WriterDocument,
    )
    persisted.ui_editable = True
    result = PatchResult(
        success=True,
        message='Markdown converted to IR and document replaced.',
        meta={
            'mode': 'replace',
            'source_format': 'markdown',
            'write_result': write_result,
        },
    )
    return {
        'success': True,
        'changed': True,
        'feishu_synced': True,
        'patch_result': result.model_dump(),
        'persisted_document': persisted.model_dump(),
    }


def _action_result_path(result: dict, key: str | None = None) -> str:
    path = result.get('artifact_path') if key is None else (
        (result.get('metadata') or {}).get('artifact_paths') or {}
    ).get(key)
    if not path:
        raise ValueError(f'Writer tool did not return artifact {key or "primary"!r}.')
    return path


def _sync_document_response(
    changed: bool,
    patch_set: PatchSet,
    result: PatchResult,
    document: WriterDocument,
) -> dict:
    return {
        'success': result.success,
        'changed': changed,
        'feishu_synced': result.success,
        'patch_set': patch_set.model_dump(),
        'patch_result': result.model_dump(),
        'persisted_document': document.model_dump(),
    }


def writer_locate_revision_target(
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


def writer_generate_modify_plan(
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


def writer_generate_revision_set(
    base_document_path: str,
    writing_context_path: str,
    modify_plan_path: str,
    media_assets_path: str = '',
) -> str:
    """Generate an IR PatchSet or Markdown StringReplaceSet from a ModifyPlan."""
    document = _read_json_string(base_document_path)
    toolkit = WriterRevisionToolkit()
    is_markdown = Path(base_document_path).suffix.lower() in {'.md', '.markdown', '.txt'}
    if is_markdown:
        content = toolkit.generate_string_replace_set(
            markdown_document=document,
            modify_plan_json=_read_json_string(modify_plan_path),
            writing_context_json=_read_json_string(writing_context_path),
        )
        schema_name = writer_schema('revision.StringReplaceSet')
    else:
        content = toolkit.generate_patch_set(
            writer_document_json=document,
            modify_plan_json=_read_json_string(modify_plan_path),
            writing_context_json=_read_json_string(writing_context_path),
            media_assets_json=(
                _read_json_string(media_assets_path) if media_assets_path else ''
            ),
        )
        schema_name = writer_schema('revision.PatchSet')
    return _save_json_artifact(
        'revision_set', content, schema_name,
        directory=_run_root('revision-patch'),
    )


def writer_apply_revision(
    base_document_path: str,
    writing_context_path: str,
    revision_set_path: str,
    media_assets_path: str = '',
) -> dict:
    """Apply an IR patch or Markdown string replacements locally."""
    root = _run_root('apply-revision')
    is_body_step = require_context().params.get('step_id') == 'write_document'
    is_markdown = Path(base_document_path).suffix.lower() in {'.md', '.markdown', '.txt'}
    toolkit = WriterRevisionToolkit()
    if is_markdown:
        payload = _json_loads(toolkit.apply_string_replace(
            markdown_document=_read_json_string(base_document_path),
            string_replace_set_json=_read_json_string(revision_set_path),
            writing_context_json=_read_json_string(writing_context_path),
        ), {})
        result_schema = writer_schema('revision.StringReplaceResult')
    else:
        payload = _json_loads(toolkit.apply_revision(
            writer_document_json=_read_json_string(base_document_path),
            patch_set_json=_read_json_string(revision_set_path),
            writing_context_json=_read_json_string(writing_context_path),
            media_assets_json=(
                _read_json_string(media_assets_path) if media_assets_path else ''
            ),
            sync_provider=not is_body_step,
            allow_outline=not is_body_step,
        ), {})
        result_schema = writer_schema('revision.PatchResult')
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
                payload.get('string_replace_result') or payload.get('patch_result') or {},
                ensure_ascii=False,
            ),
            result_schema,
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


def writer_convert_markdown_to_ir(content_path: str, stage: str = 'final') -> str:
    """Convert the supported Markdown subset to Writer IR for provider delivery."""
    markdown = _read_json_string(content_path)
    document = parse_document_markdown(
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


def writer_publish_revision(
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


def writer_replace_document(
    content_path: str,
    source_document_path: str,
    target_document_path: str = '',
    target_uri: str = '',
    media_assets_path: str = '',
) -> dict:
    """Replace a bound cloud source with the selected final WriterDocument."""
    root = _run_root('replace-document')
    payload = _json_loads(WriterResourceToolkit().replace_document(
        content_json=_read_json_string(content_path),
        source_document_json=_read_json_string(source_document_path),
        target_document_json=(
            _read_json_string(target_document_path) if target_document_path else ''
        ),
        target_uri=target_uri,
        media_assets_json=(
            _read_json_string(media_assets_path) if media_assets_path else ''
        ),
    ), {})
    return _save_publish_payload(payload, root)


def writer_append_document(
    content_path: str,
    target_document_path: str = '',
    target_uri: str = '',
    publish_outline: bool = False,
    media_assets_path: str = '',
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
        media_assets_json=(
            _read_json_string(media_assets_path) if media_assets_path else ''
        ),
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
