"""Writer Workflow entry points, private state, and step orchestration.

Reusable document execution lives in :mod:`lazymind.document_tools`; this
module contains only YAML adapters and Writer-Workflow-private control.
"""
from __future__ import annotations

import hashlib
import json
import logging
import uuid
from pathlib import Path
from typing import Any, Literal, Mapping

from pydantic import BaseModel, ConfigDict

from lazymind.chat.engine.subagent.context import require_context
from lazymind.document_tools import execution as _DOCUMENT_EXECUTION
from lazymind.document_tools import WriterResourceToolkit
from lazymind.document_tools.artifacts import persist_artifact_json, writer_schema
from lazymind.document_tools.resources import (
    find_provider_locator,
    provider_reference,
)
from lazymind.document_tools.revision import modify_plan_needs_media
from lazymind.document_tools.writing import (
    classify_document_structure,
    resolve_prepare_control,
)


class WriterCommand(BaseModel):
    """Workflow-private control decision for one user writing request."""

    model_config = ConfigDict(extra='forbid')

    action: Literal['create', 'use_outline', 'rewrite', 'revise', 'read']
    source_role: Literal['none', 'outline', 'document']
    target_stage: Literal['prepared', 'outline', 'document']
    next_step: Literal['outline', 'write_flat_document', 'write_document', '__end__']
    structure_mode: Literal['flat', 'sectioned'] = 'sectioned'
    user_instruction: str
    source_ref: str | None = None
    target_ref: str | None = None
    request_fingerprint: str


WriterCommand.model_rebuild(_types_namespace={'Literal': Literal})


def _state_request_fingerprint(user_input: str) -> str:
    normalized = ' '.join(str(user_input or '').split())
    return hashlib.sha256(normalized.encode('utf-8')).hexdigest()


def _state_authoritative_user_input(context: Any, supplied: str = '') -> str:
    authoritative = str((context.params or {}).get('user_input') or '').strip()
    return authoritative or str(supplied or '').strip()


def _state_has_verified_kb_evidence(
    context: Any, evidence_tool_names: set[str], log: Any
) -> bool:
    """Return whether the prepare step has a successful KB retrieval result."""
    try:
        steps = context.db.load_steps(context.task_id)
    except Exception as exc:  # noqa: BLE001 - missing provenance must fail closed.
        log.warning('[Writer] Cannot verify knowledge_text provenance: %s', exc)
        return False
    for step in steps:
        if step.get('role') != 'tool':
            continue
        for result in (step.get('content') or {}).get('tool_results') or []:
            if result.get('name') not in evidence_tool_names:
                continue
            raw = result.get('result')
            try:
                payload = json.loads(raw) if isinstance(raw, str) else raw
            except (TypeError, ValueError):
                payload = None
            if isinstance(payload, dict) and payload.get('success') is True:
                return True
            if isinstance(raw, str) and raw.startswith(
                '[Large result offloaded to file'
            ):
                return True
    return False


def _state_verified_knowledge_text(
    context: Any, knowledge_text: str, evidence_tool_names: set[str], log: Any
) -> str:
    normalized = str(knowledge_text or '').strip()
    if not normalized:
        return ''
    if _state_has_verified_kb_evidence(context, evidence_tool_names, log):
        return normalized
    log.warning(
        '[Writer] Ignoring knowledge_text without a preceding successful KB retrieval.'
    )
    return ''


def _state_authoritative_input_path(
    context: Any,
    key: str | tuple[str, ...],
    supplied_path: str = '',
    *,
    require_workflow_binding: bool = False,
) -> str:
    """Resolve a path from immutable Workflow bindings, never an agent guess."""
    remote_inputs = (context.params or {}).get('remote_inputs') or {}
    keys = (key,) if isinstance(key, str) else key
    authoritative = next(
        (
            str(remote_inputs.get(candidate) or '').strip()
            for candidate in keys
            if str(remote_inputs.get(candidate) or '').strip()
        ),
        '',
    )
    step_id = str((context.params or {}).get('step_id') or '').strip()
    if step_id in {'outline', 'write_flat_document', 'write_document'}:
        if require_workflow_binding and not authoritative:
            raise ValueError(
                f'{keys[0]} is missing from authoritative workflow inputs.'
            )
        return authoritative
    return authoritative or str(supplied_path or '').strip()


def _state_workspace_root(context: Any) -> Path:
    root = Path(context.workspace_path) if context.workspace_path else Path('/tmp')
    root.mkdir(parents=True, exist_ok=True)
    return root


def _state_run_root(context: Any, name: str) -> Path:
    root = _state_workspace_root(context) / 'writer-workflow' / f'{name}-{uuid.uuid4().hex}'
    root.mkdir(parents=True, exist_ok=True)
    return root


def _state_workspace_fingerprint(**values: str) -> str:
    payload = json.dumps(values, ensure_ascii=False, sort_keys=True)
    return hashlib.sha256(payload.encode('utf-8')).hexdigest()


def _state_load_workspace_state(
    context: Any | None,
    kind: str,
    fingerprint: str,
    *,
    allow_without_context: bool = False,
) -> tuple[dict[str, Any], Path | None]:
    path = None
    if context is not None:
        path = (
            _state_workspace_root(context)
            / 'writer-workflow'
            / f'{kind}-workspace-{fingerprint}.json'
        )
    elif not allow_without_context:
        raise RuntimeError('Writer Workflow context is required for checkpointing.')
    if path and path.exists():
        try:
            state = json.loads(path.read_text(encoding='utf-8'))
        except (OSError, json.JSONDecodeError):
            state = {}
        if state.get('fingerprint') == fingerprint:
            return state, path
    return {
        'schema_version': 1,
        'fingerprint': fingerprint,
        'result': {},
        'completed': False,
    }, path


def _state_persist_workspace_state(
    state: dict[str, Any],
    path: Path | None,
    *,
    completed: bool = False,
) -> None:
    state['completed'] = completed
    if path is None:
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f'.{path.name}.{uuid.uuid4().hex}.tmp')
    temporary.write_text(
        json.dumps(state, ensure_ascii=False, indent=2),
        encoding='utf-8',
    )
    temporary.replace(path)


def _state_save_workspace_artifacts(context: Any, result: Mapping[str, Any]) -> list[str]:
    from lazymind.chat.engine.subagent.tools import save_artifacts

    allowed = set(context.output_slots or [])
    entries: list[dict[str, Any]] = []
    saved_keys: list[str] = []
    for key, value in dict(result).items():
        if allowed and key not in allowed:
            continue
        if key == 'draft_blocks' and isinstance(value, list):
            for path in value:
                if isinstance(path, str) and Path(path).is_file():
                    entries.append({'key': key, 'value': path, 'content_type': 'file'})
                    saved_keys.append(key)
            continue
        if isinstance(value, str) and Path(value).is_file():
            entries.append({'key': key, 'value': value, 'content_type': 'file'})
            saved_keys.append(key)
    if not entries:
        raise RuntimeError('Draft workspace produced no saveable artifacts.')
    saved = save_artifacts(entries)
    if saved.get('status') != 'ok':
        raise RuntimeError(f'Failed to save draft workspace artifacts: {saved!r}')
    return list(dict.fromkeys(saved_keys))


def _state_workspace_completion(
    result: Mapping[str, Any],
    saved_keys: list[str],
) -> dict[str, Any]:
    draft_blocks = result.get('draft_blocks')
    return {
        'status': 'completed',
        'operation': result.get('operation'),
        'representation': result.get('representation'),
        'draft_section_count': (
            len(draft_blocks) if isinstance(draft_blocks, list) else None
        ),
        'saved_artifact_keys': saved_keys,
        'warnings': list(result.get('warnings') or []),
        'artifacts_saved': True,
        'control': {'next_step': '__end__'},
    }


LOG = logging.getLogger(__name__)

WRITER_DEFAULT_STRUCTURE_MODE: Literal['flat', 'sectioned'] = 'sectioned'

_KB_EVIDENCE_TOOL_NAMES = {
    'KBToolkit_kb_search',
    'KBToolkit_kb_keyword_search',
    'KBToolkit_kb_get_parent_node',
    'KBToolkit_kb_get_window_nodes',
}


WriterCommand.model_rebuild(_types_namespace={'Literal': Literal})


def _writer_request_fingerprint(user_input: str) -> str:
    return _state_request_fingerprint(user_input)


def _emit_writer_progress(current_phase: str, **details: Any) -> None:
    """Publish writer internals through the existing task phase channel."""
    require_context().emit({
        'type': 'progress',
        # The subagent runner keeps progress monotonic. Reusing its initial value
        # updates only current_phase and leaves the existing percentage policy intact.
        'progress': 5,
        'current_phase': current_phase,
        **details,
    })


def _authoritative_writer_user_input(user_input: str) -> str:
    """Prefer the immutable workflow request over an agent paraphrase."""
    return _state_authoritative_user_input(require_context(), user_input)


def _verified_knowledge_text(knowledge_text: str) -> str:
    return _state_verified_knowledge_text(
        require_context(), knowledge_text, _KB_EVIDENCE_TOOL_NAMES, LOG
    )


def _authoritative_writer_input_path(
    key: str | tuple[str, ...],
    supplied_path: str = '',
    *,
    require_workflow_binding: bool = False,
) -> str:
    """Resolve a path from immutable Workflow bindings, never an agent guess."""
    return _state_authoritative_input_path(
        require_context(),
        key,
        supplied_path,
        require_workflow_binding=require_workflow_binding,
    )


def _load_writer_command(path: str) -> WriterCommand:
    if not path:
        raise ValueError('writer_command_path is required.')
    return WriterCommand.model_validate(_read_json_file(path))


def writer_classify_structure(user_input: str) -> Literal['flat', 'sectioned']:
    """Classify a new document inside prepare; uncertainty keeps the legacy route."""
    return classify_document_structure(
        user_input, default=WRITER_DEFAULT_STRUCTURE_MODE,
    )


def writer_resolve_command(
    user_input: str,
    action: Literal['create', 'use_outline', 'rewrite', 'revise', 'read'],
    source_role: Literal['none', 'outline', 'document'],
    target_stage: Literal['prepared', 'outline', 'document'] = 'document',
    source_ref: str = '',
    target_ref: str = '',
    existing_writer_command_path: str = '',
) -> str:
    """Create or reuse the sole Writer control decision for the current request."""
    ctx = require_context()
    step_id = str((ctx.params or {}).get('step_id') or '').strip()
    if step_id and step_id != 'prepare':
        raise ValueError('WriterCommand can only be created during the prepare step.')
    user_input = _authoritative_writer_user_input(user_input)
    existing_writer_command_path = _authoritative_writer_input_path(
        'writer_command',
        existing_writer_command_path,
    )
    fingerprint = _writer_request_fingerprint(user_input)
    if existing_writer_command_path:
        existing = _load_writer_command(existing_writer_command_path)
        if existing.request_fingerprint == fingerprint:
            return existing_writer_command_path

    if action == 'create' and source_role != 'none':
        raise ValueError('create requires source_role="none".')
    if action == 'use_outline' and source_role != 'outline':
        raise ValueError('use_outline requires source_role="outline".')
    if action == 'rewrite' and source_role != 'document':
        raise ValueError('rewrite requires source_role="document".')
    if action == 'revise' and source_role not in {'outline', 'document'}:
        raise ValueError('revise requires source_role="outline" or "document".')
    if action == 'read' and target_stage != 'prepared':
        raise ValueError('read requires target_stage="prepared".')
    if target_stage == 'prepared' and action != 'read':
        raise ValueError('target_stage="prepared" requires action="read".')

    structure_mode = (
        writer_classify_structure(user_input)
        if action == 'create' and target_stage == 'document'
        else WRITER_DEFAULT_STRUCTURE_MODE
    )
    if action == 'read':
        next_step = '__end__'
    elif action == 'create' and target_stage == 'document' and structure_mode == 'flat':
        next_step = 'write_flat_document'
    elif action == 'rewrite' or (action == 'revise' and source_role == 'document'):
        next_step = 'write_document'
    else:
        next_step = 'outline'

    command = WriterCommand(
        action=action,
        source_role=source_role,
        target_stage=target_stage,
        next_step=next_step,
        structure_mode=structure_mode,
        user_instruction=user_input,
        source_ref=source_ref or None,
        target_ref=target_ref or None,
        request_fingerprint=fingerprint,
    )
    root = _run_root('command')
    return persist_artifact_json(
        command,
        str(root / 'writer_command.json'),
        schema_name='writer-workflow.WriterCommand',
        created_by='writer-workflow-wrapper',
    )


_LOCAL_WRITER_DOCUMENT_SUFFIXES = {'.md', '.markdown', '.txt', '.lmd'}


def _provider_document_locator(value: str) -> str:
    return find_provider_locator(value)


def _provider_document_reference(value: str) -> str:
    return provider_reference(value)


def _resolve_prepare_control(
    user_input: str,
    suggested_operation: str,
    *,
    has_document_source: bool,
) -> tuple[str, str]:
    """Resolve prepare operation and terminal target from authoritative facts."""
    return resolve_prepare_control(
        user_input,
        suggested_operation,
        has_document_source=has_document_source,
    )


def _workspace_root() -> Path:
    return _state_workspace_root(require_context())


def _run_root(name: str) -> Path:
    return _state_run_root(require_context(), name)


def _read_json_file(path: str) -> Any:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_read_json_file', locals(),
    )


def _json_loads(value: str, default: Any = None) -> Any:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_json_loads', locals(),
    )


def _save_json_artifact(
    name: str,
    content_json: str,
    schema_name: str,
    *,
    directory: Path | None = None,
    extra_meta: dict[str, Any] | None = None,
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_save_json_artifact', locals(),
    )


def writer_build_writing_task(query: str, representation: str = 'markdown') -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_build_writing_task', locals(),
    )


def writer_load_local_document(filename: str = '') -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_load_local_document', locals(),
    )


def writer_load_document(user_input: str, stage: str = 'final') -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_load_document', locals(),
    )


def writer_profile_resources(
    writing_task_path: str,
    user_input: str,
    source_document_path: str = '',
    knowledge_text: str = '',
    profile_input_resources_path: str = '',
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_profile_resources', locals(),
    )


def writer_collect_available_media(
    writing_task_path: str,
    source_document_path: str = '',
    input_resources_path: str = '',
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_collect_available_media', locals(),
    )


def writer_create_writing_context(
    writing_task_path: str,
    resource_profiles_path: str,
    source_document_path: str = '',
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_create_writing_context', locals(),
    )


def writer_prepare_workspace(
    operation: Literal[
        'create',
        'use_outline',
        'rewrite_document',
        'revise_document',
        'prepare_only',
    ] = 'create',
    source_filename: str = '',
    knowledge_text: str = '',
) -> dict:
    """Prepare one writing request.

    ``source_filename`` is only the basename of an uploaded Markdown, text, or LMD
    document. It may identify either a document to edit or reference material for a
    new document; the authoritative request resolves that distinction. Provider
    document locators belong in ``user_input`` and are resolved as cloud documents.
    """
    user_input = _authoritative_writer_user_input('')
    knowledge_text = _verified_knowledge_text(knowledge_text)
    supported_operations = {
        'create',
        'use_outline',
        'rewrite_document',
        'revise_document',
        'prepare_only',
    }
    if operation not in supported_operations:
        raise ValueError(
            'operation must be create, use_outline, rewrite_document, '
            'revise_document, or prepare_only.',
        )
    ctx = require_context()
    files_by_turn = ctx.params.get('history_files_per_turn') or {}
    local_candidates = [
        Path(path)
        for paths in files_by_turn.values()
        for path in paths or []
        if Path(path).suffix.lower() in _LOCAL_WRITER_DOCUMENT_SUFFIXES
    ]
    source_filename = str(source_filename or '').strip()
    cloud_source_ref = _provider_document_reference(user_input)
    has_cloud_source = bool(cloud_source_ref)

    # Models occasionally copy a provider locator into both fields.
    # Treat that as one cloud source, never as a local filename override.
    source_filename_is_cloud = bool(
        source_filename and _provider_document_locator(source_filename) == source_filename
    )
    if source_filename_is_cloud:
        if source_filename not in user_input:
            raise ValueError(
                'A cloud source URL must appear in the authoritative user_input; '
                'do not supply it only through source_filename.',
            )
        source_filename = ''
        has_cloud_source = True
    elif source_filename:
        source_path = Path(source_filename)
        if source_path.name != source_filename or source_path.suffix.lower() \
                not in _LOCAL_WRITER_DOCUMENT_SUFFIXES:
            raise ValueError(
                'source_filename must be the basename of an uploaded Markdown, text, '
                'or .lmd document.',
            )
        if has_cloud_source:
            raise ValueError(
                'The request contains both a provider document locator and a local source '
                'document. Specify exactly one document source.',
            )

    source_kind = 'cloud' if has_cloud_source else (
        'local' if source_filename or local_candidates else 'cloud'
    )
    source_ref = cloud_source_ref or source_filename

    # The model may suggest an operation for ambiguous supplied documents, but it
    # does not own the terminal target. Resolve impossible combinations from the
    # authoritative request and the actual source bindings before creating the
    # immutable WriterCommand.
    operation, target_stage = _resolve_prepare_control(
        user_input,
        operation,
        has_document_source=bool(
            has_cloud_source or source_filename or local_candidates
        ),
    )
    create_target = (
        _json_loads(WriterResourceToolkit().resolve_create_target(user_input), {})
        if operation == 'create' else {}
    )

    command_action = {
        'create': 'create',
        'use_outline': 'use_outline',
        'rewrite_document': 'rewrite',
        'revise_document': 'revise',
        'prepare_only': 'read',
    }[operation]
    source_role = {
        'create': 'none',
        'use_outline': 'outline',
        'rewrite_document': 'document',
        'revise_document': 'document',
        'prepare_only': 'document',
    }[operation]
    writer_command = writer_resolve_command(
        user_input=user_input,
        action=command_action,
        source_role=source_role,
        target_stage=target_stage,
        source_ref=source_ref,
        target_ref=str(create_target.get('target_ref') or ''),
    )
    command = _load_writer_command(writer_command)

    source_document = ''
    target_document = ''
    provider_input_resources = ''
    provider_warnings: list[str] = []
    representation = 'markdown'
    if create_target.get('target_document'):
        target_document = _save_json_artifact(
            'target_document',
            json.dumps(create_target['target_document'], ensure_ascii=False),
            writer_schema('task.TargetDocument'),
            directory=_run_root('resolve-create-target'),
        )
    if operation != 'create':
        if source_kind == 'local':
            source_document = writer_load_local_document(source_filename)
            representation = (
                'ir' if Path(source_document).suffix.lower() == '.lmd' else 'markdown'
            )
        else:
            source_stage = {
                'use_outline': 'outline',
                'rewrite_document': 'draft',
                'revise_document': 'draft',
                'prepare_only': 'final',
            }[operation]
            loaded = writer_load_document(user_input=user_input, stage=source_stage)
            source_document = loaded['source_document']
            target_document = loaded['target_document']
            representation = loaded['representation']
            provider_input_resources = loaded.get('input_resources') or ''
            provider_warnings = loaded.get('resource_warnings') or []

    writing_task = writer_build_writing_task(
        query=user_input,
        representation=representation,
    )
    media_result = writer_collect_available_media(
        writing_task_path=writing_task,
        source_document_path=source_document if operation != 'use_outline' else '',
        input_resources_path=provider_input_resources,
    )
    resource_profiles = writer_profile_resources(
        writing_task_path=writing_task,
        user_input=user_input,
        source_document_path=source_document,
        knowledge_text=knowledge_text,
        profile_input_resources_path=media_result['profile_input_resources'],
    )
    writing_context = writer_create_writing_context(
        writing_task_path=writing_task,
        resource_profiles_path=resource_profiles,
        source_document_path=source_document,
    )
    result = {
        'writer_command': writer_command,
        'writing_task': writing_task,
        'media_assets': media_result['media_assets'],
        'resource_profiles': resource_profiles,
        'writing_context': writing_context,
        'representation': representation,
        'structure_mode': command.structure_mode,
        'next_step': command.next_step,
        'control': {'next_step': command.next_step},
        'warnings': [*provider_warnings, *(media_result.get('warnings') or [])],
    }
    if source_document:
        result['source_document'] = source_document
    if target_document:
        result['target_document'] = target_document
    result['saved_artifact_keys'] = _save_draft_workspace_artifacts(result)
    result['artifacts_saved'] = True
    return result


def writer_prepare_outline(
    source_document_path: str,
    writing_task_path: str = '',
    writing_context_path: str = '',
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_prepare_outline', locals(),
    )


def writer_generate_outline(writing_task_path: str, writing_context_path: str) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_outline', locals(),
    )


def _outline_workspace_fingerprint(
    operation: str,
    writing_context_path: str,
    user_input: str,
    writing_task_path: str,
    source_document_path: str,
    outline_document_path: str,
) -> str:
    return _state_workspace_fingerprint(
        operation=operation,
        writing_context_path=writing_context_path,
        user_input=user_input,
        writing_task_path=writing_task_path,
        source_document_path=source_document_path,
        outline_document_path=outline_document_path,
    )


def _outline_workspace_state(fingerprint: str) -> tuple[dict[str, Any], Path | None]:
    try:
        context = require_context()
    except RuntimeError:
        context = None
    return _state_load_workspace_state(
        context,
        'outline',
        fingerprint,
        allow_without_context=True,
    )


def _persist_outline_workspace_state(
    state: dict[str, Any],
    path: Path | None,
    *,
    completed: bool = False,
) -> None:
    _state_persist_workspace_state(state, path, completed=completed)


def writer_outline_workspace() -> dict:
    """Run one existing outline workflow branch without changing its semantics."""
    user_input = _authoritative_writer_user_input('')
    writer_command_path = _authoritative_writer_input_path(
        'writer_command', require_workflow_binding=True,
    )
    writing_context_path = _authoritative_writer_input_path(
        ('writing_context_after_outline', 'writing_context'),
        require_workflow_binding=True,
    )
    writing_task_path = _authoritative_writer_input_path('writing_task')
    source_document_path = _authoritative_writer_input_path('source_document')
    outline_document_path = _authoritative_writer_input_path('outline_document')
    command = _load_writer_command(writer_command_path)
    if user_input and command.request_fingerprint != _writer_request_fingerprint(user_input):
        raise ValueError(
            'writer_command belongs to a different user request; restart from prepare.'
        )
    command_operation = {
        'create': 'generate',
        'use_outline': 'use_source',
        'revise': 'revise',
    }.get(command.action)
    if command_operation is None or (
        command.action == 'revise' and command.source_role != 'outline'
    ):
        raise ValueError(
            f'WriterCommand action={command.action!r} source_role={command.source_role!r} '
            'cannot execute the outline step.'
        )
    operation = command_operation
    if operation not in {'generate', 'use_source', 'revise'}:
        raise ValueError('operation must be generate, use_source, or revise.')
    if not writing_context_path:
        raise ValueError('writing_context_path is required.')

    fingerprint = _outline_workspace_fingerprint(
        operation,
        writing_context_path,
        user_input,
        writing_task_path,
        source_document_path,
        outline_document_path,
    )
    state, checkpoint_path = _outline_workspace_state(fingerprint)
    result: dict[str, Any] = dict(state.get('result') or {})
    result['operation'] = operation
    result['writer_command'] = writer_command_path
    result['control'] = {
        'next_step': 'write_document'
        if command.target_stage == 'document'
        else '__end__',
    }
    if state.get('completed'):
        _emit_writer_progress('正在复用已完成的大纲 checkpoint')
        if not state.get('artifacts_saved'):
            state['saved_artifact_keys'] = _save_draft_workspace_artifacts(result)
            state['artifacts_saved'] = True
            _persist_outline_workspace_state(state, checkpoint_path, completed=True)
        return result

    if operation == 'generate':
        if not writing_task_path:
            raise ValueError('writing_task_path is required for generate.')
        if not result.get('outline_document'):
            result['outline_document'] = writer_generate_outline(
                writing_task_path=writing_task_path,
                writing_context_path=writing_context_path,
            )
            result['outline_instructions_completed'] = True
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
    elif operation == 'use_source':
        if not source_document_path:
            raise ValueError('source_document_path is required for use_source.')
        if not result.get('outline_document'):
            _emit_writer_progress('正在解析并规范化已有大纲')
            result['outline_document'] = writer_prepare_outline(
                source_document_path,
                writing_task_path,
                writing_context_path,
            )
            result['outline_instructions_completed'] = True
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
    else:
        if not user_input:
            raise ValueError('user_input is required for revise.')
        if not outline_document_path:
            raise ValueError('outline_document_path is required for revise.')
        if not result.get('outline_revision_task'):
            _emit_writer_progress('正在解析大纲修改要求')
            result['outline_revision_task'] = writer_build_revision_task(
                query=user_input,
                base_document_path=outline_document_path,
            )
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
        if not result.get('outline_locate_result'):
            _emit_writer_progress('正在定位需要修改的大纲结构')
            result['outline_locate_result'] = writer_locate_revision_target(
                base_document_path=outline_document_path,
                writing_context_path=writing_context_path,
                revision_task_path=result['outline_revision_task'],
            )
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
        if not result.get('outline_modify_plan'):
            _emit_writer_progress('正在生成大纲修改计划')
            result['outline_modify_plan'] = writer_generate_modify_plan(
                base_document_path=outline_document_path,
                writing_context_path=writing_context_path,
                revision_task_path=result['outline_revision_task'],
                locate_result_path=result['outline_locate_result'],
            )
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
        if not result.get('outline_revision_set'):
            _emit_writer_progress('正在生成大纲修改内容')
            result['outline_revision_set'] = writer_generate_revision_set(
                base_document_path=outline_document_path,
                writing_context_path=writing_context_path,
                modify_plan_path=result['outline_modify_plan'],
            )
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)
        if not result.get('outline_document'):
            _emit_writer_progress('正在应用并校验大纲修改')
            applied = writer_apply_revision(
                base_document_path=outline_document_path,
                writing_context_path=writing_context_path,
                revision_set_path=result['outline_revision_set'],
            )
            result['outline_document'] = applied['outline_document']
            result['outline_revision_result'] = applied['revision_result']
            if applied.get('write_result'):
                result['outline_write_result'] = applied['write_result']
            state['result'] = result
            _persist_outline_workspace_state(state, checkpoint_path)

    if operation == 'revise' and not result.get('outline_instructions_completed'):
        _emit_writer_progress('正在补全大纲字数、上下文关联和子问题')
        result['outline_document'] = writer_prepare_outline(
            result['outline_document'], writing_task_path, writing_context_path,
        )
        result['outline_instructions_completed'] = True
        state['result'] = result
        _persist_outline_workspace_state(state, checkpoint_path)

    if not result.get('writing_context_after_outline'):
        _emit_writer_progress('大纲已完成，正在更新写作上下文')
        result['writing_context_after_outline'] = writer_update_writing_context(
            content_artifact_path=result['outline_document'],
            writing_context_path=writing_context_path,
        )
    state['result'] = result
    _emit_writer_progress('大纲处理完成，正在保存工作区结果')
    state['saved_artifact_keys'] = _save_draft_workspace_artifacts(result)
    state['artifacts_saved'] = True
    _persist_outline_workspace_state(state, checkpoint_path, completed=True)
    return result


def writer_generate_rewrite_outline(
    writing_task_path: str,
    source_document_path: str,
    writing_context_path: str,
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_rewrite_outline', locals(),
    )


def writer_generate_rewrite_section_instructions(
    writing_task_path: str,
    source_document_path: str,
    writing_context_path: str,
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_rewrite_section_instructions', locals(),
    )


def writer_generate_section_instructions(
    writing_task_path: str,
    outline_path: str,
    writing_context_path: str,
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_section_instructions', locals(),
    )


def writer_execute_writing_subtasks(
    outline_path: str,
    writing_context_path: str,
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_execute_writing_subtasks', locals(),
    )


def writer_generate_short_writing_plan(
    writing_task_path: str,
    writing_context_path: str,
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_short_writing_plan', locals(),
    )


def writer_generate_short_visual_plan(
    writing_task_path: str,
    short_writing_plan_path: str,
    writing_context_path: str,
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_short_visual_plan', locals(),
    )


def writer_generate_short_document(
    writing_task_path: str,
    short_writing_plan_path: str,
    writing_context_path: str,
    visual_plan_path: str = '',
    resolved_media_assets_path: str = '',
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_short_document', locals(),
    )


def writer_resolve_visual_media(
    visual_plan_path: str,
    media_assets_path: str,
    strict_required: bool = False,
    allowed_strategies_json: str = '',
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_resolve_visual_media', locals(),
    )


def writer_resolve_revision_media(
    modify_plan_path: str,
    media_assets_path: str,
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_resolve_revision_media', locals(),
    )


def writer_generate_draft_document(
    writing_task_path: str,
    section_instructions_path: str,
    writing_context_path: str,
    outline_path: str = '',
    visual_plan_path: str = '',
    resolved_media_assets_path: str = '',
    document_title: str = '',
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_draft_document', locals(),
    )


def writer_update_writing_context(
    content_artifact_path: str,
    writing_context_path: str,
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_update_writing_context', locals(),
    )


def writer_export_markdown(content_path: str) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_export_markdown', locals(),
    )


def writer_build_revision_task(query: str, base_document_path: str) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_build_revision_task', locals(),
    )


def writer_locate_revision_target(
    base_document_path: str,
    writing_context_path: str,
    revision_task_path: str,
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_locate_revision_target', locals(),
    )


def writer_generate_modify_plan(
    base_document_path: str,
    writing_context_path: str,
    revision_task_path: str,
    locate_result_path: str,
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_modify_plan', locals(),
    )


def writer_generate_revision_set(
    base_document_path: str,
    writing_context_path: str,
    modify_plan_path: str,
    media_assets_path: str = '',
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_generate_revision_set', locals(),
    )


def writer_apply_revision(
    base_document_path: str,
    writing_context_path: str,
    revision_set_path: str,
    media_assets_path: str = '',
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_apply_revision', locals(),
    )


def writer_convert_markdown_to_ir(content_path: str, stage: str = 'final') -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_convert_markdown_to_ir', locals(),
    )


def writer_publish_revision(
    source_document_path: str,
    revision_set_path: str,
    media_assets_path: str = '',
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_publish_revision', locals(),
    )


def writer_convert_document(
    content_path: str,
    provider: str = '',
    target_document_path: str = '',
    media_assets_path: str = '',
    output_format: str = 'native',
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_convert_document', locals(),
    )


def writer_write_document(
    converted_document_path: str,
    target_document_path: str = '',
    media_assets_path: str = '',
    title: str = '',
    parent_uri: str = '',
    mode: str = 'replace',
) -> dict:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_write_document', locals(),
    )


def writer_create_document(
    title: str,
    parent_uri: str = '',
    adapter: str = '',
) -> str:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_create_document', locals(),
    )


def _draft_workspace_fingerprint(
    operation: str,
    user_input: str,
    writing_task_path: str,
    writing_context_path: str,
    media_assets_path: str,
    outline_document_path: str,
    source_document_path: str,
    draft_document_path: str,
    target_document_path: str,
) -> str:
    return _state_workspace_fingerprint(
        operation=operation,
        user_input=user_input,
        writing_task_path=writing_task_path,
        writing_context_path=writing_context_path,
        media_assets_path=media_assets_path,
        outline_document_path=outline_document_path,
        source_document_path=source_document_path,
        draft_document_path=draft_document_path,
        target_document_path=target_document_path,
    )


def _draft_workspace_state(fingerprint: str) -> tuple[dict[str, Any], Path]:
    state, path = _state_load_workspace_state(
        require_context(),
        'draft',
        fingerprint,
    )
    assert path is not None
    return state, path


def _persist_draft_workspace_state(
    state: dict[str, Any],
    path: Path,
    *,
    completed: bool = False,
) -> None:
    _state_persist_workspace_state(state, path, completed=completed)


def writer_prepare_markdown_for_editor(
    document_path: str,
    target_document_path: str,
) -> tuple[str, str]:
    return _DOCUMENT_EXECUTION.invoke(
        globals(), '_writer_prepare_markdown_for_editor', locals(),
    )


def _save_draft_workspace_artifacts(result: Mapping[str, Any]) -> list[str]:
    return _state_save_workspace_artifacts(require_context(), result)


def _draft_workspace_completion(
    result: Mapping[str, Any],
    saved_keys: list[str],
) -> dict[str, Any]:
    return _state_workspace_completion(result, saved_keys)


def writer_draft_workspace() -> dict:
    """Run one existing draft workflow branch through deterministic top-level tools."""
    _emit_writer_progress('正在读取成稿任务与已有 checkpoint')
    user_input = _authoritative_writer_user_input('')
    writer_command_path = _authoritative_writer_input_path(
        'writer_command', require_workflow_binding=True,
    )
    writing_task_path = _authoritative_writer_input_path(
        'writing_task', require_workflow_binding=True,
    )
    writing_context_path = _authoritative_writer_input_path(
        (
            'writing_context_after_draft',
            'writing_context_after_outline',
            'writing_context',
        ),
        require_workflow_binding=True,
    )
    media_assets_path = _authoritative_writer_input_path('media_assets')
    outline_document_path = _authoritative_writer_input_path('outline_document')
    source_document_path = _authoritative_writer_input_path('source_document')
    draft_document_path = _authoritative_writer_input_path('draft_document')
    target_document_path = _authoritative_writer_input_path('target_document')
    command = _load_writer_command(writer_command_path)
    continuing_completed_outline = (
        command.target_stage == 'outline'
        and command.action in {'create', 'use_outline'}
        and bool(outline_document_path)
    )
    if command.target_stage != 'document' and not continuing_completed_outline:
        raise ValueError('write_document requires writer_command.target_stage="document".')
    command_operation = {
        'create': 'generate',
        'use_outline': 'generate',
        'rewrite': 'rewrite',
        'revise': 'revise',
    }.get(command.action)
    if command_operation is None or (
        command.action == 'revise' and command.source_role != 'document'
    ):
        raise ValueError(
            f'WriterCommand action={command.action!r} source_role={command.source_role!r} '
            'cannot execute the document step.'
        )
    operation = command_operation
    if operation not in {'generate', 'rewrite', 'revise'}:
        raise ValueError('operation must be generate, rewrite, or revise.')
    if not writing_task_path or not writing_context_path:
        raise ValueError('writing_task_path and writing_context_path are required.')
    task = _read_json_file(writing_task_path)
    representation = str(((task.get('output') or {}).get('representation') or '')).strip()
    if continuing_completed_outline:
        # The immutable command still describes the original outline request.
        # A package-declared completed continuation reuses that request and the
        # latest selected outline revision; the UI's generic action text is not
        # a replacement writing brief.
        user_input = command.user_instruction
    if user_input and command.request_fingerprint != _writer_request_fingerprint(user_input):
        raise ValueError(
            'writer_command belongs to a different user request; restart from prepare.'
        )
    if operation == 'revise' and not user_input:
        user_input = command.user_instruction

    fingerprint = _draft_workspace_fingerprint(
        operation,
        user_input,
        writing_task_path,
        writing_context_path,
        media_assets_path,
        outline_document_path,
        source_document_path,
        draft_document_path,
        target_document_path,
    )
    state, checkpoint_path = _draft_workspace_state(fingerprint)
    result: dict[str, Any] = dict(state.get('result') or {})
    result['operation'] = operation
    result['writer_command'] = writer_command_path
    if state.get('completed'):
        _emit_writer_progress('正在复用已完成的成稿 checkpoint')
        saved_keys = list(state.get('saved_artifact_keys') or [])
        if not state.get('artifacts_saved'):
            saved_keys = _save_draft_workspace_artifacts(result)
            state['artifacts_saved'] = True
            state['saved_artifact_keys'] = saved_keys
            _persist_draft_workspace_state(state, checkpoint_path, completed=True)
        return _draft_workspace_completion(result, saved_keys)

    resolved_media = str(result.get('resolved_media_assets') or '')
    if operation in {'generate', 'rewrite'}:
        if operation == 'generate':
            if not outline_document_path:
                raise ValueError('outline_document_path is required for generate.')
            if not result.get('section_instructions'):
                _emit_writer_progress(
                    '正在执行大纲中的写作子任务'
                )
                result['outline_document'] = writer_execute_writing_subtasks(
                    outline_path=outline_document_path,
                    writing_context_path=writing_context_path,
                )
                _emit_writer_progress('子任务已完成，正在生成 section instructions')
                planning = writer_generate_section_instructions(
                    writing_task_path=writing_task_path,
                    outline_path=result['outline_document'],
                    writing_context_path=writing_context_path,
                )
                result.update({
                    'section_instructions': planning['section_instructions'],
                    'visual_plan': planning['visual_plan'],
                    'visual_need_count': planning['visual_need_count'],
                    'section_count': planning['section_count'],
                    'warnings': list(planning.get('warnings') or []),
                })
                state['result'] = result
                _persist_draft_workspace_state(state, checkpoint_path)
                _emit_writer_progress(
                    f"section instructions 已完成，共 {planning['section_count']} 章",
                    section_total=planning['section_count'],
                    visual_need_count=planning['visual_need_count'],
                )
            else:
                instructions = _read_json_file(result['section_instructions'])
                section_count = len((instructions or {}).get('instructions') or [])
                result['section_count'] = section_count
                _emit_writer_progress(
                    f'已复用 section instructions checkpoint，共 {section_count} 章',
                    section_total=section_count,
                )
            document_title = ''
            outline_path = str(result.get('outline_document') or outline_document_path)
        else:
            rewrite_base = draft_document_path or source_document_path
            if not rewrite_base:
                raise ValueError('draft_document_path or source_document_path is required for rewrite.')
            if not result.get('section_instructions'):
                _emit_writer_progress(
                    '正在规划全文重写 section instructions、视觉需求与辅助任务'
                )
                planning = writer_generate_rewrite_section_instructions(
                    writing_task_path=writing_task_path,
                    source_document_path=rewrite_base,
                    writing_context_path=writing_context_path,
                )
                result.update({
                    'section_instructions': planning['section_instructions'],
                    'visual_plan': planning['visual_plan'],
                    'visual_need_count': planning['visual_need_count'],
                    'section_count': planning['section_count'],
                    'document_title': planning.get('document_title') or '',
                    'warnings': list(planning.get('warnings') or []),
                })
                state['result'] = result
                _persist_draft_workspace_state(state, checkpoint_path)
                _emit_writer_progress(
                    f"重写 section instructions 已完成，共 {planning['section_count']} 章",
                    section_total=planning['section_count'],
                    visual_need_count=planning['visual_need_count'],
                )
            else:
                instructions = _read_json_file(result['section_instructions'])
                section_count = len((instructions or {}).get('instructions') or [])
                result['section_count'] = section_count
                _emit_writer_progress(
                    f'已复用重写 section instructions checkpoint，共 {section_count} 章',
                    section_total=section_count,
                )
            document_title = str(result.get('document_title') or '')
            outline_path = ''

        needs_media = representation == 'ir' or int(result.get('visual_need_count') or 0) > 0
        if needs_media and not resolved_media:
            if not media_assets_path:
                raise ValueError('media_assets_path is required when the draft has visual media.')
            _emit_writer_progress(
                f"正在准备 {int(result.get('visual_need_count') or 0)} 项视觉素材与辅助资源",
                visual_need_count=int(result.get('visual_need_count') or 0),
            )
            media = writer_resolve_visual_media(
                visual_plan_path=result['visual_plan'],
                media_assets_path=media_assets_path,
            )
            resolved_media = media['resolved_media_assets']
            result['resolved_media_assets'] = resolved_media
            result['warnings'] = [
                *(result.get('warnings') or []),
                *(media.get('warnings') or []),
            ]
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
            _emit_writer_progress('辅助资源已准备完成，正在启动章节生成')
        elif not needs_media:
            _emit_writer_progress('无需准备视觉素材，正在启动章节生成')

        if not result.get('draft_document'):
            _emit_writer_progress('章节准备完成，正在优先生成并流式输出第 1 章')
            generated = writer_generate_draft_document(
                writing_task_path=writing_task_path,
                section_instructions_path=result['section_instructions'],
                writing_context_path=writing_context_path,
                outline_path=outline_path,
                visual_plan_path=result['visual_plan'],
                resolved_media_assets_path=resolved_media,
                document_title=document_title,
            )
            result['draft_blocks'] = generated['draft_blocks']
            result['draft_document'] = generated['draft_document']
            result['representation'] = generated['representation']
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)

        should_write_back = representation == 'ir' and bool(target_document_path) and (
            operation == 'rewrite' or not draft_document_path
        )
        if should_write_back and not result.get('document_write_result'):
            _emit_writer_progress('成稿已组装，正在写回目标文档')
            converted_document = writer_convert_document(
                content_path=result['draft_document'],
                target_document_path=target_document_path,
                media_assets_path=resolved_media or media_assets_path,
            )
            published = writer_write_document(
                converted_document_path=converted_document,
                target_document_path=target_document_path,
                media_assets_path=resolved_media or media_assets_path,
            )
            result['document_write_result'] = published['publish_result']
            result['draft_document'] = published['draft_document']
            if published.get('target_document'):
                result['target_document'] = published['target_document']
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
    else:
        base_document = draft_document_path or source_document_path
        if not user_input or not base_document:
            raise ValueError('user_input and a draft or source document are required for revise.')
        if not result.get('document_revision_task'):
            result['document_revision_task'] = writer_build_revision_task(
                query=user_input,
                base_document_path=base_document,
            )
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if not result.get('document_locate_result'):
            result['document_locate_result'] = writer_locate_revision_target(
                base_document_path=base_document,
                writing_context_path=writing_context_path,
                revision_task_path=result['document_revision_task'],
            )
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if not result.get('document_modify_plan'):
            result['document_modify_plan'] = writer_generate_modify_plan(
                base_document_path=base_document,
                writing_context_path=writing_context_path,
                revision_task_path=result['document_revision_task'],
                locate_result_path=result['document_locate_result'],
            )
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if modify_plan_needs_media(
            _read_json_file(result['document_modify_plan'])
        ) and not resolved_media:
            if not media_assets_path:
                raise ValueError('media_assets_path is required for a visual revision.')
            media = writer_resolve_revision_media(
                modify_plan_path=result['document_modify_plan'],
                media_assets_path=media_assets_path,
            )
            resolved_media = media['resolved_media_assets']
            result['resolved_media_assets'] = resolved_media
            result['warnings'] = list(media.get('warnings') or [])
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if not result.get('document_revision_set'):
            result['document_revision_set'] = writer_generate_revision_set(
                base_document_path=base_document,
                writing_context_path=writing_context_path,
                modify_plan_path=result['document_modify_plan'],
                media_assets_path=resolved_media,
            )
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if not result.get('draft_document'):
            applied = writer_apply_revision(
                base_document_path=base_document,
                writing_context_path=writing_context_path,
                revision_set_path=result['document_revision_set'],
                media_assets_path=resolved_media,
            )
            result['document_revision_result'] = applied['revision_result']
            result['draft_document'] = applied['draft_document']
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)
        if representation == 'ir' and not draft_document_path and target_document_path \
                and not result.get('document_write_result'):
            published = writer_publish_revision(
                source_document_path=source_document_path,
                revision_set_path=result['document_revision_set'],
                media_assets_path=resolved_media or media_assets_path,
            )
            result['document_write_result'] = published['publish_result']
            result['draft_document'] = published['draft_document']
            if published.get('target_document'):
                result['target_document'] = published['target_document']
            state['result'] = result
            _persist_draft_workspace_state(state, checkpoint_path)

    if representation == 'markdown' and target_document_path:
        result.setdefault('target_document', target_document_path)
        if media_assets_path and not result.get('resolved_media_assets'):
            result['resolved_media_assets'] = resolved_media or media_assets_path
    if representation == 'markdown' and target_document_path \
            and result.get('draft_document') \
            and not result.get('markdown_editor_prepared'):
        prepared_draft, updated_target = writer_prepare_markdown_for_editor(
            str(result['draft_document']), target_document_path,
        )
        result['draft_document'] = prepared_draft
        if updated_target:
            result['target_document'] = updated_target
        result['markdown_editor_prepared'] = True
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)
    if not result.get('writing_context_after_draft'):
        _emit_writer_progress('正在更新成稿上下文')
        result['writing_context_after_draft'] = writer_update_writing_context(
            content_artifact_path=result['draft_document'],
            writing_context_path=writing_context_path,
        )
    state['result'] = result
    _persist_draft_workspace_state(state, checkpoint_path)
    _emit_writer_progress('成稿校验完成，正在保存工作区结果')
    saved_keys = _save_draft_workspace_artifacts(result)
    state['artifacts_saved'] = True
    state['saved_artifact_keys'] = saved_keys
    _persist_draft_workspace_state(state, checkpoint_path, completed=True)
    return _draft_workspace_completion(result, saved_keys)


_FLAT_OUTPUT_SLOTS = {
    'draft_document': 'flat_draft_document',
    'writing_context_after_draft': 'flat_writing_context_after_draft',
    'visual_plan': 'flat_visual_plan',
    'resolved_media_assets': 'flat_resolved_media_assets',
}


def _published_flat_draft_workspace_result(
    result: Mapping[str, Any],
) -> dict[str, Any]:
    published = dict(result)
    for source, target in _FLAT_OUTPUT_SLOTS.items():
        if source in published:
            published[target] = published.pop(source)
    return published


def writer_flat_draft_workspace() -> dict:
    """Generate one new flat document on the dedicated flat Workflow path."""
    _emit_writer_progress('正在读取短文成稿任务与已有 checkpoint')
    user_input = _authoritative_writer_user_input('')
    writer_command_path = _authoritative_writer_input_path(
        'writer_command', require_workflow_binding=True,
    )
    writing_task_path = _authoritative_writer_input_path(
        'writing_task', require_workflow_binding=True,
    )
    writing_context_path = _authoritative_writer_input_path(
        'writing_context', require_workflow_binding=True,
    )
    media_assets_path = _authoritative_writer_input_path('media_assets')
    target_document_path = _authoritative_writer_input_path('target_document')
    command = _load_writer_command(writer_command_path)
    if command.structure_mode != 'flat':
        raise ValueError(
            'write_flat_document requires writer_command.structure_mode="flat".'
        )
    if command.target_stage != 'document' or command.action != 'create':
        raise ValueError('write_flat_document only supports new flat document creation.')
    if not writing_task_path or not writing_context_path:
        raise ValueError('writing_task_path and writing_context_path are required.')
    if user_input and command.request_fingerprint != _writer_request_fingerprint(user_input):
        raise ValueError(
            'writer_command belongs to a different user request; restart from prepare.'
        )

    operation = 'generate'
    fingerprint = _draft_workspace_fingerprint(
        operation,
        user_input,
        writing_task_path,
        writing_context_path,
        media_assets_path,
        '',
        '',
        '',
        target_document_path,
    )
    state, checkpoint_path = _draft_workspace_state(fingerprint)
    result: dict[str, Any] = dict(state.get('result') or {})
    result['operation'] = operation
    result['writer_command'] = writer_command_path
    if state.get('completed'):
        _emit_writer_progress('正在复用已完成的短文成稿 checkpoint')
        saved_keys = list(state.get('saved_artifact_keys') or [])
        if not state.get('artifacts_saved'):
            saved_keys = _save_draft_workspace_artifacts(
                _published_flat_draft_workspace_result(result)
            )
            state['artifacts_saved'] = True
            state['saved_artifact_keys'] = saved_keys
            _persist_draft_workspace_state(state, checkpoint_path, completed=True)
        return _draft_workspace_completion(result, saved_keys)

    task = _read_json_file(writing_task_path)
    representation = str((task.get('output') or {}).get('representation') or '').strip()
    if representation not in {'markdown', 'ir'}:
        raise ValueError(
            "Flat short-document generation requires 'markdown' or 'ir' representation."
        )
    if not result.get('short_writing_plan'):
        result['short_writing_plan'] = writer_generate_short_writing_plan(
            writing_task_path=writing_task_path,
            writing_context_path=writing_context_path,
        )
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)
    if not result.get('visual_plan'):
        planning = writer_generate_short_visual_plan(
            writing_task_path=writing_task_path,
            short_writing_plan_path=result['short_writing_plan'],
            writing_context_path=writing_context_path,
        )
        result.update({
            'visual_plan': planning['visual_plan'],
            'visual_need_count': planning['visual_need_count'],
            'warnings': [
                *(result.get('warnings') or []),
                *(planning.get('warnings') or []),
            ],
        })
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)

    resolved_media = str(result.get('resolved_media_assets') or '')
    visual_need_count = int(result.get('visual_need_count') or 0)
    if visual_need_count > 0 and not resolved_media:
        if not media_assets_path:
            raise ValueError(
                'media_assets_path is required when the short draft has visual media.'
            )
        media = writer_resolve_visual_media(
            visual_plan_path=result['visual_plan'],
            media_assets_path=media_assets_path,
        )
        resolved_media = media['resolved_media_assets']
        result['resolved_media_assets'] = resolved_media
        result['warnings'] = [
            *(result.get('warnings') or []),
            *(media.get('warnings') or []),
        ]
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)
    if not result.get('draft_document'):
        result['draft_document'] = writer_generate_short_document(
            writing_task_path=writing_task_path,
            short_writing_plan_path=result['short_writing_plan'],
            writing_context_path=writing_context_path,
            visual_plan_path=result['visual_plan'],
            resolved_media_assets_path=resolved_media,
        )
        result['representation'] = representation
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)

    if representation == 'markdown' and target_document_path:
        result.setdefault('target_document', target_document_path)
        if media_assets_path and not result.get('resolved_media_assets'):
            result['resolved_media_assets'] = resolved_media or media_assets_path
    if representation == 'markdown' and target_document_path \
            and not result.get('markdown_editor_prepared'):
        prepared_draft, updated_target = writer_prepare_markdown_for_editor(
            str(result['draft_document']), target_document_path,
        )
        result['draft_document'] = prepared_draft
        if updated_target:
            result['target_document'] = updated_target
        result['markdown_editor_prepared'] = True
        state['result'] = result
        _persist_draft_workspace_state(state, checkpoint_path)
    if not result.get('writing_context_after_draft'):
        _emit_writer_progress('正在更新短文成稿上下文')
        result['writing_context_after_draft'] = writer_update_writing_context(
            content_artifact_path=result['draft_document'],
            writing_context_path=writing_context_path,
        )
    state['result'] = result
    _persist_draft_workspace_state(state, checkpoint_path)
    _emit_writer_progress('短文成稿校验完成，正在保存工作区结果')
    saved_keys = _save_draft_workspace_artifacts(
        _published_flat_draft_workspace_result(result)
    )
    state['artifacts_saved'] = True
    state['saved_artifact_keys'] = saved_keys
    _persist_draft_workspace_state(state, checkpoint_path, completed=True)
    return _draft_workspace_completion(result, saved_keys)
