from __future__ import annotations

import asyncio
import json
import os
import re
import time
import base64
import types
import tempfile
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import LOG, AutoModel
from lazyllm.tools.tool_config_inject import inject_tool_config

from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions,
    AgentExecutor,
    AgentRole,
    AgentRunPlan,
    PromptBuilder,
    normalize_attachments,
    render_attachment_content,
    make_cancel_stop_condition,
)
from lazymind.chat.engine.prompts import add_standard_system_sections
from lazymind.chat.engine.tools.local_file.workspace import grep, read_file
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
from lazymind.chat.service.component.tool_registry import (
    ATTACHMENT_EDIT_TOOL_CONFIG,
    DEFAULT_TOOLS,
    USER_ATTACHMENT_TOOL_CONFIGS,
    collect_system_prompt_appendices,
    filter_tools,
    tool_is_active,
)
from lazymind.chat.service.utils import (
    materialize_source_views,
    register_existing_sources,
    reset_citation_state,
)
from lazymind.config import config as _cfg
from lazymind.model_config import inject_model_config

from . import SUBAGENT_ATTACHMENT_CONTEXT_KEY, SUBAGENT_CORE_TOOL_NAMES
from . import tools as subagent_tools
from .context import LARGE_TOOL_RESULT_THRESHOLD, SubAgentContext, set_context
from .db import MemorySubAgentStore, SubAgentDB

DRAFT_STREAM_EVENT_TYPES = frozenset({
    'artifact_stream_start',
    'artifact_stream',
    'artifact_stream_end',
    'artifact_stream_abort',
})


def _publisher_owns_outputs(ctx: 'SubAgentContext') -> bool:
    """Return whether this step's outputs are written by package publisher tools."""
    policy = (ctx.params or {}).get('workflow_runtime') or {}
    owned = {
        str(key).strip()
        for key in (policy.get('publisher_owned_slots') or [])
        if str(key).strip()
    } if isinstance(policy, dict) else set()
    slots = {str(key).strip() for key in ctx.output_slots if str(key).strip()}
    return (
        str(ctx.agent_type or '') == 'workflow_step'
        and bool(owned)
        and bool(slots)
        and slots.issubset(owned)
    )


async def merge_agent_and_stream_events(
    agent_events: AsyncIterator[Any],
    stream_events: asyncio.Queue[dict[str, Any]],
) -> AsyncIterator[tuple[str, Any]]:
    """Yield tool-thread stream events while the Agent iterator is still running."""
    iterator = agent_events.__aiter__()
    agent_task: asyncio.Task[Any] | None = asyncio.create_task(iterator.__anext__())
    stream_task: asyncio.Task[Any] | None = asyncio.create_task(stream_events.get())
    agent_error: BaseException | None = None
    try:
        while agent_task is not None:
            wait_for = {agent_task}
            if stream_task is not None:
                wait_for.add(stream_task)
            done, _ = await asyncio.wait(wait_for, return_when=asyncio.FIRST_COMPLETED)

            if stream_task is not None and stream_task in done:
                yield 'stream', stream_task.result()
                stream_task = asyncio.create_task(stream_events.get())

            if agent_task in done:
                try:
                    item = agent_task.result()
                except StopAsyncIteration:
                    agent_task = None
                except (asyncio.CancelledError, Exception) as exc:
                    agent_error = exc
                    agent_task = None
                else:
                    yield 'agent', item
                    agent_task = asyncio.create_task(iterator.__anext__())

        # Deliver callbacks queued immediately before the tool/agent future completed.
        await asyncio.sleep(0)
        if stream_task is not None and stream_task.done():
            yield 'stream', stream_task.result()
            stream_task = None
        while not stream_events.empty():
            yield 'stream', stream_events.get_nowait()
        if agent_error is not None:
            raise agent_error
    finally:
        for pending in (agent_task, stream_task):
            if pending is not None and not pending.done():
                pending.cancel()


def _build_artifact_context_section(
    ctx: 'SubAgentContext', db: 'SubAgentDB'
) -> List[str]:
    """Build a multi-line artifact summary block to inject into the objective prompt.

    Returns an empty list when there are no input artifacts.

    Workflow scenario (params contains session_id):
      Reads the public Artifact projection supplied by the Workflow runtime.

    Ordinary SubAgents receive their attachment context through params and do not
    participate in Workflow resource resolution.
    """
    params = ctx.params
    session_id: str = params.get('session_id', '')

    if session_id:
        try:
            import httpx
            from lazymind.config import config
            from lazymind.workflow_sdk import WorkflowClient
            response = WorkflowClient(
                str(config['core_api_url']).rstrip('/'),
                str(params.get('user_id') or ''), host='lazymind', transport=httpx,
            ).list_artifacts(session_id).result
            artifacts = response.get('artifacts') if isinstance(response, dict) else []
            if not artifacts:
                return []
            return [
                '## Workflow inputs and artifacts [AUTHORITATIVE public runtime]',
                json.dumps(artifacts, ensure_ascii=False, default=str),
            ]
        except Exception as exc:
            LOG.warning('[SubAgent] public Workflow Artifact read failed: %s', exc)
            return []

    return []


def _resolve_workflow_step_tools(params: Dict[str, Any]) -> Optional[List[str]]:
    """Use the immutable tool names supplied by public Attempt Context."""
    declared = params.get('legacy_tools') or params.get('tools') or []
    if not isinstance(declared, list):
        return None
    return list(dict.fromkeys([*SUBAGENT_CORE_TOOL_NAMES, *map(str, declared)]))


def _materialize_workflow_package(
    workflow_id: str,
    revision_id: str,
    tree_hash: str,
    files: Dict[str, Any],
) -> Path:
    """Materialize one immutable Workflow revision for path-based tool assets.

    Workflow tools may load sibling runtime files relative to ``__file__``.  Executing
    only scripts/*.py from an in-memory pseudo path breaks those tools even though Core
    returned the complete pinned package.  The tree hash makes this cache immutable.
    """
    safe_workflow = re.sub(r'[^0-9A-Za-z_.-]+', '_', workflow_id).strip('._') or 'workflow'
    safe_revision = re.sub(r'[^0-9A-Za-z_.-]+', '_', revision_id).strip('._') or 'revision'
    safe_tree = re.sub(r'[^0-9A-Za-z]+', '', tree_hash)[:64] or 'unhashed'
    root = Path(tempfile.gettempdir()) / 'lazymind-workflow-packages' / (
        f'{safe_workflow}@{safe_revision}-{safe_tree}'
    )
    root.mkdir(parents=True, exist_ok=True)
    resolved_root = root.resolve()
    for relative, encoded in files.items():
        relative_path = Path(str(relative))
        if relative_path.is_absolute() or '..' in relative_path.parts:
            raise RuntimeError(f'unsafe Workflow package path: {relative!r}')
        target = (root / relative_path).resolve()
        if target != resolved_root and resolved_root not in target.parents:
            raise RuntimeError(f'unsafe Workflow package path: {relative!r}')
        if encoded is None:
            # Core serializes empty blobs as null in the public package map.
            raw = b''
        else:
            raw = base64.b64decode(encoded) if isinstance(encoded, str) else bytes(encoded)
        if target.exists() and target.read_bytes() == raw:
            continue
        target.parent.mkdir(parents=True, exist_ok=True)
        temporary = target.with_name(f'.{target.name}.{os.getpid()}.tmp')
        temporary.write_bytes(raw)
        os.replace(temporary, target)
    return root


def load_workflow_tools(params: Dict[str, Any], names: List[str]) -> Dict[str, Any]:
    """Load declared callables from the exact published Workflow revision.

    Core is authoritative for both the pinned revision and the compiled
    ``legacy_tools`` list.  The model only sees the resulting callables; it
    cannot choose a package, revision, script, or extra function.
    """
    workflow_id = str(params.get('workflow_id') or '').strip()
    revision_id = str(params.get('revision_id') or '').strip()
    if not workflow_id or not revision_id or not names:
        return {}
    try:
        import httpx
        from lazymind.config import config
        from lazymind.workflow_sdk import WorkflowClient

        package = WorkflowClient(
            str(config['core_api_url']).rstrip('/'),
            str(params.get('user_id') or ''), host='lazymind', transport=httpx,
        ).get_workflow(workflow_id, revision_id).result
        if str(package.get('revision_id') or '') != revision_id:
            raise RuntimeError('Core returned a different Workflow revision')
        expected_hash = str(params.get('tree_hash') or '').strip()
        if expected_hash and str(package.get('tree_hash') or '') != expected_hash:
            raise RuntimeError('Core returned a Workflow package with a different tree hash')
        files = package.get('files') if isinstance(package.get('files'), dict) else {}
        package_root = _materialize_workflow_package(
            workflow_id,
            revision_id,
            str(package.get('tree_hash') or expected_hash),
            files,
        )
        remaining = set(names)
        resolved: Dict[str, Any] = {}
        for path in sorted(files):
            if not path.startswith('scripts/') or not path.endswith('.py'):
                continue
            script_path = package_root / path
            raw_source = script_path.read_bytes()
            source = raw_source.decode('utf-8')
            module = types.ModuleType(
                f'_lazymind_workflow_{revision_id.replace("-", "_")}_{len(resolved)}'
            )
            module.__file__ = str(script_path)
            exec(compile(source, module.__file__, 'exec'), module.__dict__)
            for name in tuple(remaining):
                candidate = module.__dict__.get(name)
                if callable(candidate):
                    # Published Workflow scripts can predate the tool runtime's
                    # docstring requirement. Their callable name, signature and
                    # annotations are already pinned by the immutable revision;
                    # provide a stable description so legacy revisions remain
                    # executable instead of failing before the first tool call.
                    if not str(getattr(candidate, '__doc__', '') or '').strip():
                        candidate.__doc__ = f'Execute the published Workflow tool {name}.'
                    resolved[name] = candidate
                    remaining.remove(name)
        if remaining:
            LOG.warning(
                '[SubAgent] Workflow revision %s does not provide declared tools %s',
                revision_id, sorted(remaining),
            )
        return resolved
    except Exception as exc:
        LOG.warning('[SubAgent] failed to load pinned Workflow script tools: %s', exc)
        return {}


def _resolve_runtime_tools(
    explicit: Optional[List[str]], params: Optional[Dict[str, Any]] = None,
) -> List[Any]:
    """Build the runtime tool list for a SubAgent.

    If explicit tool names are provided, each name is resolved in order:
      1. DEFAULT_TOOLS registry (framework / global tools).
    If a name is not found in either source it is silently skipped and a warning is logged.

    When explicit is None/empty, fall back to all DEFAULT_TOOLS.

    Note: artifact infrastructure tools are managed separately by _build_subagent_tools.
    Generic artifact writes are intentionally omitted for package-declared
    publisher-owned output slots.
    Names of base tools in the explicit list are silently ignored.
    """
    if explicit:
        core_tool_names = set(SUBAGENT_CORE_TOOL_NAMES)
        name_list = [
            name for item in explicit
            if (name := str(item).strip()) and name not in core_tool_names
        ]
        # Published Workflow script functions are resolved from the exact
        # revision before falling back to framework/global tools.
        package_by_name = load_workflow_tools(params or {}, name_list)
        # Build lookup from DEFAULT_TOOLS.
        default_by_name = {cfg.name: cfg for cfg in DEFAULT_TOOLS if tool_is_active(cfg)}
        result = []
        for name in name_list:
            if name in package_by_name:
                result.append(package_by_name[name])
            elif name in default_by_name:
                result.append(default_by_name[name].tool)
            else:
                LOG.warning('[SubAgent] public Attempt tool %r is unavailable on LazyMind Host', name)
        return result
    return [cfg.tool for cfg in filter_tools(DEFAULT_TOOLS)]


def _build_subagent_tools(
    extra_tools: Optional[List[Any]],
    attachment_configs: Optional[List[Any]] = None,
    *,
    include_artifact_writes: bool = True,
) -> List[Any]:
    """Combine mandatory SubAgent infra tools with optional domain tools.

    Read-only artifact and knowledge tools are always included regardless of the
    explicit tools list. Publisher-owned workflow steps can disable generic artifact
    writes so domain tools remain the only authority for their output slots.
    Attachment tools are included as one group when the parent task carries attachment
    context, so the runtime tool list and its system prompt stay consistent.
    """
    base = [
        subagent_tools.get_artifact,
        subagent_tools.list_artifacts,
        subagent_tools.list_knowledge_bases,
        grep,
        read_file,
        subagent_tools.find_artifact,
    ]
    if include_artifact_writes:
        base.extend([
            subagent_tools.save_artifacts,
            subagent_tools.patch_artifact,
            subagent_tools.discard_draft,
        ])
    if attachment_configs:
        base.extend(config.tool for config in attachment_configs)
    if extra_tools:
        base.extend(extra_tools)
    return base


def _tool_configs_for_runtime_tools(runtime_tools: List[Any]) -> list:
    runtime_ids = {id(tool) for tool in runtime_tools}
    return [cfg for cfg in DEFAULT_TOOLS if id(cfg.tool) in runtime_ids]


def _build_partial_sort_order_hints(
    partial_indices: 'Dict[str, List[int]]',
) -> str:
    """Translate partial_indices (0-based list_index) into sort_order guidance for the AI.

    Attempt Context list indexes are stable and zero-based; display order is one-based.
    """
    hints: List[str] = []
    for slot, list_indexes in partial_indices.items():
        sort_orders = [index + 1 for index in list_indexes if index >= 0]
        if sort_orders:
            hints.append(
                f'For slot "{slot}": overwrite sort_order='
                + ', '.join(str(value) for value in sort_orders)
                + '.'
            )
    if not hints:
        return ''
    return '## Partial retry instruction (AUTHORITATIVE Attempt Context)\n' + '\n'.join(hints)


def _build_intent_context_section(params: Dict[str, Any]) -> List[str]:
    """Render immutable instructions already present in public Attempt Context."""
    instruction = str(params.get('runtime_instruction') or '').strip()
    if not instruction:
        return []
    return ['', '## Effective Execution Intent', instruction]


_STRUCTURED_PARAM_KEYS = {
    # These values are rendered by dedicated sections below. Excluding only these
    # avoids duplicating large/internal representations while preserving arbitrary
    # task parameters supplied by workflow and ordinary SubAgent callers.
    'history_files_per_turn',
    SUBAGENT_ATTACHMENT_CONTEXT_KEY,
    'partial_indices',
    'required_output_artifact_keys',
    # Framework-owned Workflow routing/concurrency metadata. The effective
    # objective and dedicated artifact sections already contain everything the
    # SubAgent should act on; showing these fields invites it to re-interpret or
    # re-run the parent Workflow instead of completing its one assigned step.
    'workflow_id', 'workflow_ref', 'revision_id', 'revision_no', 'tree_hash',
    'remote_root', 'step_id', 'session_id', 'user_input', 'hand_off',
    'chat_session_id', 'workflow_mode', 'user_id', 'preflight_id',
    'legacy_tools', 'parent_agentic_config', 'filters',
}


def _attachment_context(params: Dict[str, Any]) -> Dict[str, Any]:
    value = params.get(SUBAGENT_ATTACHMENT_CONTEXT_KEY)
    return value if isinstance(value, dict) else {}


def _history_files_per_turn(params: Dict[str, Any]) -> Dict[str, List[str]]:
    context = _attachment_context(params)
    return context.get('history_files_per_turn') or params.get('history_files_per_turn') or {}


def _build_agentic_config(
    task: Dict[str, Any],
    params: Dict[str, Any],
    effective_agent_type: str,
) -> Dict[str, Any]:
    """Restore the request context needed by tools inside every SubAgent."""
    parent = params.get('parent_agentic_config')
    agentic_config = dict(parent) if isinstance(parent, dict) else {}
    attachment_context = _attachment_context(params)
    history_files_per_turn = (
        attachment_context.get('history_files_per_turn')
        or params.get('history_files_per_turn')
        or agentic_config.get('history_files_per_turn')
        or {}
    )
    all_files = attachment_context.get('files') or agentic_config.get('files')
    if not isinstance(all_files, list):
        all_files = [path for paths in history_files_per_turn.values() for path in paths]
    filters = dict(params.get('filters') or agentic_config.get('filters') or {})
    agentic_config.update({
        'query': str(params.get('user_input') or task.get('objective') or ''),
        'files': all_files,
        'history_files_per_turn': history_files_per_turn,
        'filters': filters,
        'user_id': str(
            attachment_context.get('user_id')
            or params.get('user_id')
            or agentic_config.get('user_id')
            or ''
        ).strip(),
        'conversation_id': str(
            task.get('conversation_id') or agentic_config.get('conversation_id') or ''
        ).strip(),
        'is_subagent': True,
        'agent_type': effective_agent_type,
        'thinking_depth': str(
            params.get('_thinking_depth') or agentic_config.get('thinking_depth') or 'medium'
        ),
    })
    if effective_agent_type == 'workflow_step':
        agentic_config.update({
            'workflow_id': params.get('workflow_id', ''),
            'workflow_session_id': params.get('session_id', ''),
            'workflow_step': params.get('step_id', ''),
        })
    # Prefer the launched workflow session id whenever present so artifact tools work
    # even if parent_agentic_config carried a stale empty workflow_session_id.
    launched_session_id = str(params.get('session_id') or '').strip()
    if launched_session_id:
        agentic_config['workflow_session_id'] = launched_session_id
    return agentic_config


def _build_subagent_plan(
    ctx: SubAgentContext,
    db: Optional['SubAgentDB'],
    *,
    tools: List[Any],
    tool_prompt_appendices: Dict[str, List[str]],
    resume: bool = False,
) -> AgentRunPlan:
    builder = PromptBuilder.for_role(AgentRole.SUBAGENT)
    add_standard_system_sections(
        builder,
        bool(tools),
        use_memory=False,
        current_query=ctx.objective,
        show_tool_status=False,
        tool_prompt_appendices=tool_prompt_appendices,
    )
    builder.system(
        'subagent_role', 'SubAgent Role', (
            'You are an autonomous SubAgent. Complete the task objective using only the '
            'available tools. You may not spawn, create, or delegate to other agents.\n'
            'You cannot interact with the user and must never ask the user a question or '
            'request clarification. Use the authoritative task context as provided. If a '
            'required input is genuinely absent, report that as a task failure rather than '
            'phrasing it as a question.\n'
            'Use the selected user-visible language for progress and the final summary. '
            'Artifact content must follow the language required by the task objective or '
            'the output slot contract; do not translate an artifact when its required '
            'format specifies another language.'
        ),
        'platform.subagent',
        priority=20,
    )

    display_params = {
        key: value for key, value in ctx.params.items()
        if key not in _STRUCTURED_PARAM_KEYS and value not in (None, '', [], {})
    }
    builder.runtime(
        'subagent_parameters', 'Task Parameters',
        '\n'.join(f'- {key}: {value}' for key, value in display_params.items()),
        'task.params', priority=10, content_kind='reference',
    )

    # Inject artifact context: workflow session reads from slot revisions with sort_order;
    # ordinary SubAgent reads from sub_agent_artifacts of prior succeeded steps.
    session_id: str = ctx.params.get('session_id', '')
    if session_id or ctx.input_slots:
        artifact_section = _build_artifact_context_section(ctx, db) if db else []
        if artifact_section:
            builder.runtime(
                'subagent_artifacts', 'Existing Artifacts', '\n'.join(artifact_section),
                'database.artifacts',
                priority=20,
                content_kind='reference',
            )
        elif ctx.input_slots:
            builder.runtime(
                'subagent_input_slots', 'Input Slots', ', '.join(ctx.input_slots),
                'task.slots',
                priority=20,
                content_kind='reference',
            )
    # Inject intent/constraints from the workflow session so SubAgent respects user preferences.
    if db:
        intent_lines = _build_intent_context_section(ctx.params)
        if intent_lines:
            builder.runtime(
                'subagent_intent', 'Effective Execution Intent',
                '\n'.join(intent_lines).strip(), 'database.intent',
                priority=30,
                authoritative=True,
                content_kind='instruction',
            )
    # Inject user attachment context so the SubAgent knows which files were uploaded.
    history_files_per_turn = _history_files_per_turn(ctx.params)
    attachment_section = render_attachment_content(
        normalize_attachments(history_files_per_turn),
        role=AgentRole.SUBAGENT,
        skip_pdf=True,
    )
    file_catalog = ''
    attachment_context = _attachment_context(ctx.params)
    conversation_id = str(
        getattr(ctx, 'conversation_id', '')
        or ctx.params.get('conversation_id')
        or attachment_context.get('conversation_id')
        or ''
    ).strip()
    user_id = str(attachment_context.get('user_id') or '').strip()
    if conversation_id:
        try:
            from lazymind.chat.engine.tools.local_file.store import (
                FileResourceStore,
                render_file_resource_catalog,
            )
            from lazymind.chat.engine.tools.local_file.workspace import chat_agent_workspace
            store = FileResourceStore(chat_agent_workspace(user_id or '0', conversation_id))
            file_catalog = render_file_resource_catalog(store)
        except Exception:
            file_catalog = ''
    if file_catalog:
        attachment_section = (
            f'{file_catalog}\n\n{attachment_section}' if attachment_section else file_catalog
        )
    builder.runtime(
        'subagent_attachments', 'User Attachments', attachment_section,
        'request.attachments', priority=40, content_kind='reference',
    )
    # Translate partial_indices (internal 0-based list_index) into sort_order guidance.
    # This tells the AI exactly which display position(s) to overwrite instead of append.
    partial_indices: Dict[str, List[int]] = ctx.params.get('partial_indices') or {}
    if partial_indices and session_id:
        sort_order_hints = _build_partial_sort_order_hints(partial_indices)
        if sort_order_hints:
            builder.runtime(
                'subagent_partial_retry', 'Partial Retry', sort_order_hints, 'task.retry',
                priority=50,
                authoritative=True,
                content_kind='instruction',
            )
    publisher_owned_outputs = _publisher_owns_outputs(ctx)
    if ctx.params.get('required_output_artifact_keys') is not None:
        required_keys = _coerce_str_list(ctx.params.get('required_output_artifact_keys'))
    elif str(ctx.agent_type or '') == 'workflow_step':
        required_keys = []
    else:
        required_keys = list(ctx.output_slots)
    output_lines = []
    if publisher_owned_outputs:
        output_lines.append(
            'The declared output slots are publisher-owned. The Workflow package tool '
            'writes them at the correct list_index and revision automatically. Do not call '
            'save_artifacts, patch_artifact, or any generic artifact write for these slots. '
            'After the package publisher tool succeeds, stop and return a short summary.'
        )
    elif required_keys:
        output_lines.append(
            'Required output artifacts: '
            + ', '.join(required_keys)
            + '. Save every required key before finishing. When there are multiple outputs, '
            'use one save_artifacts call containing every output entry.'
        )
    else:
        output_lines.append(
            'No output artifact is unconditionally required. Save only artifacts requested by '
            'the objective or step prompt, and never save placeholder content.'
        )
    optional_keys = [k for k in ctx.output_slots if k not in required_keys]
    if optional_keys and not publisher_owned_outputs:
        output_lines.append(
            'Optional output artifact keys: ' + ', '.join(optional_keys)
        )
    if not publisher_owned_outputs:
        output_lines.append(
            '## Exact save_artifacts call shape\n'
            'Use this exact JSON structure:\n'
            '{"artifacts":[{"key":"<declared output key>","value":"<actual content>",'
            '"content_type":"text","caption":"<optional label>"}]}\n'
            'The payload field MUST be named value. Never use content, data, body, or text '
            'as a replacement for value. key and value are required inside EVERY artifacts item.\n'
            'For multiple outputs, put all entries in the same artifacts array. Do not make a '
            'small test/placeholder save before saving the real output.'
        )
        output_lines.append(
            '## Overwrite vs. Append for list slots\n'
            'Each save_artifacts entry has an optional sort_order parameter (1-based):\n'
            '- Omit sort_order → append a new item at the end of the list.\n'
            '- Pass sort_order=N → overwrite the item currently at display position N.\n'
            'sort_order is NOT a page number or a desired append position. During a normal full '
            'run that creates page 1, page 2, page 3, OMIT sort_order on all three entries.\n'
            'If the objective says the user wants to replace a specific item '
            '(e.g. "重新收集第二张图", "replace item 3", "redo position N"), '
            'you MUST pass sort_order=N. Omitting it will append a new item instead of replacing.'
        )
    output_lines.append(
        ('After the publisher tool succeeds, ' if publisher_owned_outputs
         else 'After all required artifacts are saved, ')
        + 'write a final summary that contains the actual results and key findings — not only '
        'a reference to the artifacts. '
        'For example, if you searched for information, include the information itself. '
        'The summary must be self-contained and directly usable by the caller without '
        'opening any artifact.'
    )
    builder.runtime(
        'subagent_output_contract', 'Output Contract', '\n'.join(output_lines), 'task.slots',
        priority=60,
        authoritative=True,
        content_kind='instruction',
    )
    input_content = (
        'Continue the task from the execution history using the refreshed context above.'
        if resume else ctx.objective
    )
    builder.input(
        content=input_content,
        source='task.objective',
    )
    history = []
    return AgentRunPlan(
        role=AgentRole.SUBAGENT,
        prompt=builder.build(),
        history=history,
        tools=tools,
        force_summarize_context=ctx.objective,
        execution_options=AgentExecutionOptions(
            extra_stop_condition=make_cancel_stop_condition(),
            max_retries=max(1, int(_cfg['agentic_expanded_max_rounds']) - 1),
        ),
    )


def _truncate_tool_result(ctx: SubAgentContext, result: Any, tool_name: str) -> str:
    """Truncate a large tool result for the LLM.

    If the serialised result exceeds LARGE_TOOL_RESULT_THRESHOLD the full
    content is written to the workspace filesystem and the LLM receives a
    compact notice with the file path and size so it can reference the file
    in subsequent tool calls or reasoning.
    """
    text = result if isinstance(result, str) else json.dumps(result, ensure_ascii=False, default=str)
    encoded = text.encode('utf-8', errors='replace')
    if len(encoded) <= LARGE_TOOL_RESULT_THRESHOLD:
        return text
    try:
        abs_path = ctx.write_large_content(text, hint=tool_name or 'tool_result')
        rel_path = os.path.relpath(abs_path, ctx.workspace_path) if ctx.workspace_path else abs_path
        size_kb = len(encoded) / 1024
        return (
            f'[Large result offloaded to file — {size_kb:.1f} KB]\n'
            f'File path (relative to workspace): {rel_path}\n'
            f'Use this path to reference the content in subsequent reasoning or tool calls.'
        )
    except Exception as exc:
        LOG.warning('[SubAgent] failed to offload large tool result for %s: %s', tool_name, exc)
        # Fallback: truncate with a notice.
        limit = LARGE_TOOL_RESULT_THRESHOLD
        truncated = text[:limit]
        return truncated + f'\n... [truncated — original {len(encoded) // 1024} KB]'


def _commit_prompt_only_text_output(
    ctx: SubAgentContext,
    required_output_keys: List[str],
    saved_keys: set[str],
    final_result: Any,
) -> bool:
    """Commit the final text for a prompt-only Workflow step.

    A model may satisfy a one-output prompt step by returning the requested text
    directly instead of calling ``save_artifacts``.  When the Workflow declares no
    script tools and exactly one required output, that final text is the step's
    unambiguous material value, so persist it deterministically at the execution
    boundary.  Tool-backed and multi-output steps still require explicit artifact
    calls and keep the strict completeness check below.
    """
    if str(ctx.agent_type or '') != 'workflow_step':
        return False
    if _coerce_str_list((ctx.params or {}).get('legacy_tools')):
        return False
    missing = [key for key in required_output_keys if key not in saved_keys]
    if len(missing) != 1:
        return False
    content = str(final_result or '').strip()
    if not content:
        return False
    key = missing[0]
    seq = ctx.next_artifact_seq(key)
    value = {'text': content}
    ctx.record_local_artifact(key, 'text', value, seq)
    ctx.emit({
        'type': 'artifact', 'slot': key, 'content_type': 'text',
        'seq': seq, 'value': value,
    })
    LOG.info('[SubAgent] committed prompt-only output key=%r for task=%s', key, ctx.task_id)
    return True


def _persist_step(ctx: SubAgentContext, seq: int, event: Dict[str, Any]) -> None:
    tag = event.get('tag')
    if tag == 'tool_calls':
        tool_calls = []
        for tc in event.get('tool_calls', []) or []:
            if not isinstance(tc, dict):
                continue
            tool_calls.append({
                'id': tc.get('id', ''),
                'name': tc.get('name') or (tc.get('function') or {}).get('name', ''),
                'args': tc.get('args') or (tc.get('function') or {}).get('arguments', {}),
            })
        ctx.db.append_step(ctx.task_id, seq, 'assistant', {'text': '', 'tool_calls': tool_calls})
    elif tag == 'tool_results':
        results = []
        for tr in event.get('tool_results', []) or []:
            if not isinstance(tr, dict):
                continue
            raw_result = tr.get('result', tr.get('content', ''))
            tool_name = tr.get('name', '')
            results.append({
                'tool_call_id': tr.get('id', ''),
                'name': tool_name,
                'result': _truncate_tool_result(ctx, raw_result, tool_name),
            })
        ctx.db.append_step(ctx.task_id, seq, 'tool', {'tool_results': results})


async def run_subagent_stream(
    task_id: str,
    db_dsn: str = '',
    resume: bool = False,
    model_config: Optional[Dict[str, Any]] = None,
    tool_config: Optional[Dict[str, Any]] = None,
    agent_type: Optional[str] = None,
    tools: Optional[List[str]] = None,
    task_spec: Optional[Dict[str, Any]] = None,
    initial_steps: Optional[List[Dict[str, Any]]] = None,
):
    """Async generator yielding Task SSE lines.

    Events: task_start / progress / text / think / artifact / done / error.
    text and think frames come from AgentEventFrameTranslator (same as ChatAgent),
    giving a unified LLM output representation across both agent types.
    """
    start_time = time.time()
    db: Optional[SubAgentDB] = None
    emitted: List[Dict[str, Any]] = []
    stream_events: asyncio.Queue[Dict[str, Any]] = asyncio.Queue()
    loop = asyncio.get_running_loop()
    source_state: Dict[str, Any] = {}
    reset_citation_state(source_state)
    last_sources_snapshot = '[]'

    def _sources_event() -> Optional[Dict[str, Any]]:
        nonlocal last_sources_snapshot
        sources = materialize_source_views(source_state)
        snapshot = json.dumps(sources, ensure_ascii=False, sort_keys=True, default=str)
        if snapshot == last_sources_snapshot:
            return None
        last_sources_snapshot = snapshot
        return {'type': 'sources', 'task_id': task_id, 'sources': sources}

    def _emit(ev: Dict[str, Any]) -> None:
        if ev.get('type') in DRAFT_STREAM_EVENT_TYPES:
            try:
                loop.call_soon_threadsafe(stream_events.put_nowait, dict(ev))
            except RuntimeError as exc:
                LOG.warning('[SubAgent] failed to enqueue Draft stream event: %s', exc)
            return
        emitted.append(ev)

    def _sse(ev: Dict[str, Any]) -> str:
        return 'data: ' + json.dumps(ev, ensure_ascii=False, default=str) + '\n\n'

    try:
        db = (MemorySubAgentStore(task_spec, initial_steps)
              if task_spec is not None else SubAgentDB(db_dsn))
        task = db.load_task(task_id)
        if not task:
            yield _sse({'type': 'error', 'status': 'failed', 'message': f'task {task_id} not found'})
            yield 'data: [DONE]\n\n'
            return

        # Go persists an accepted task before launching this request. A user stop
        # may race with the launch and mark that pending task interrupted first.
        # Treat the persisted terminal state as authoritative and never revive the
        # task by emitting task_start after it has already been cancelled.
        if str(task.get('status') or '') in {'interrupted', 'canceled'}:
            yield _sse({
                'type': 'done',
                'task_id': task_id,
                'status': 'interrupted',
                'summary': str(task.get('summary') or 'stopped by user'),
            })
            yield 'data: [DONE]\n\n'
            return

        output_keys = _coerce_str_list(task.get('output_slots'))
        input_keys = _coerce_str_list(task.get('input_slots'))
        params = _coerce_dict(task.get('params'))
        register_existing_sources(source_state, _coerce_source_list(task.get('sources')))
        last_sources_snapshot = json.dumps(
            materialize_source_views(source_state),
            ensure_ascii=False,
            sort_keys=True,
            default=str,
        )
        # SubAgents collect searched sources for their Task Center card only.
        # Tool results remain citation-free so the model does not emit body references.
        effective_agent_type = str(task.get('agent_type') or agent_type or '')
        if params.get('required_output_artifact_keys') is not None:
            required_output_keys = _coerce_str_list(params.get('required_output_artifact_keys'))
        elif effective_agent_type == 'workflow_step':
            # Do not treat every declared output as mandatory when Go omits empty lists.
            required_output_keys = []
        else:
            required_output_keys = output_keys

        ctx = SubAgentContext(
            task_id=task_id,
            conversation_id=str(task.get('conversation_id') or ''),
            agent_type=str(task.get('agent_type') or ''),
            objective=str(task.get('objective') or ''),
            params=params,
            workspace_path=str(task.get('workspace_path') or ''),
            input_slots=input_keys,
            output_slots=output_keys,
            db=db,
            emit=_emit,
        )
        ctx.ensure_workspace()

        # For workflow_step tasks: remove {{slot}} placeholders from the objective
        # (artifact context is now injected as a summary section in _objective_prompt instead).
        # The public Host Attempt contains the immutable tool declaration.
        if effective_agent_type == 'workflow_step':
            # Strip any remaining {{slot}} placeholders so they don't confuse the LLM.
            ctx.objective = re.sub(r'\{\{[^}]+\}\}', '', ctx.objective).strip()
            if not tools:
                tools = _resolve_workflow_step_tools(params)

        sid = task_id
        lazyllm.globals._init_sid(sid=sid)
        lazyllm.locals._init_sid(sid=sid)
        lazyllm.set_trace_context({
            'trace_id': params.get('trace_id') or None,
            'parent_span_id': params.get('parent_span_id') or None,
            'session_id': (
                params.get('session_id') or params.get('chat_session_id')
                or lazyllm.get_trace_context().session_id
            ),
            'sampled': True, 'request_tags': ['subagent'],
            'module_trace': {
                'by_class': {
                    'FunctionCall': False, 'ToolManager': False,
                    'Pipeline': False, 'Diverter': False,
                },
                'by_name': {
                    '_build_history': False, '_post_action': False, '_safe_call': False,
                },
            }
        })
        inject_model_config(model_config)
        inject_tool_config(tool_config)
        set_context(ctx)

        agentic_config = _build_agentic_config(task, params, effective_agent_type)
        agentic_config['citation_state'] = source_state
        agentic_config['citation_mode'] = 'collect_only'
        lazyllm.globals['agentic_config'] = agentic_config
        # Materialize session bucket before Parallel-based tools (e.g. kb_search).
        _ = lazyllm.globals._data

        yield _sse({'type': 'task_start', 'task_id': task_id})

        llm = AutoModel(model='llm')
        runtime_tools = _resolve_runtime_tools(tools, params)
        # Workflow steps often need find_user_attachment even when the current synthetic
        # chat turn has no files; keep the tools available and let the tool report empty.
        attachment_configs = (
            [*USER_ATTACHMENT_TOOL_CONFIGS, ATTACHMENT_EDIT_TOOL_CONFIG]
            if (
                agentic_config.get('files')
                or agentic_config.get('history_files_per_turn')
                or effective_agent_type == 'workflow_step'
            )
            else []
        )
        subagent_tools_all = _build_subagent_tools(
            runtime_tools,
            attachment_configs,
            include_artifact_writes=not _publisher_owns_outputs(ctx),
        )
        runtime_configs = _tool_configs_for_runtime_tools(runtime_tools)
        plan = _build_subagent_plan(
            ctx,
            db,
            tools=subagent_tools_all,
            tool_prompt_appendices=collect_system_prompt_appendices(
                runtime_configs + attachment_configs,
            ),
            resume=resume,
        )

        step_seq = db.max_step_seq(task_id) + 1 if resume else 0
        resume_history = _rebuild_history_from_steps(db, task_id) if resume else None
        if resume:
            objective_message = (
                PromptBuilder.for_role(AgentRole.SUBAGENT)
                .input(content=ctx.objective, source='task.objective')
                .build()
                .current_input
            )
            plan.history = [{'role': 'user', 'content': objective_message}, *(resume_history or [])]
        progress = 5
        yield _sse({'type': 'progress', 'task_id': task_id, 'progress': progress,
                    'current_phase': '恢复执行...' if resume else '开始执行...'})

        # translator unifies text/think output with ChatAgent frame semantics.
        translator = AgentEventFrameTranslator(query=ctx.objective)
        final_result: Any = None
        # Accumulate streaming text/think chunks; flush to DB when a tool step follows or at end.
        _pending_text: str = ''
        _pending_think: str = ''

        executor = AgentExecutor()
        merged_events = merge_agent_and_stream_events(
            executor.stream(llm, plan), stream_events,
        )
        async for source, merged_payload in merged_events:
            if source == 'stream':
                stream_event = dict(merged_payload)
                stream_event['task_id'] = task_id
                yield _sse(stream_event)
                continue

            kind, payload = merged_payload
            if kind == 'event':
                item = payload
                tag = item.get('tag')
                # Persist tool steps for resume / breakpoint recovery.
                if tag in ('tool_calls', 'tool_results'):
                    # Flush accumulated text/think as a single step before tool call.
                    if _pending_think:
                        ctx.db.append_step(task_id, step_seq, 'think', {'content': _pending_think})
                        step_seq += 1
                        _pending_think = ''
                    if _pending_text:
                        ctx.db.append_step(task_id, step_seq, 'text', {'content': _pending_text})
                        step_seq += 1
                        _pending_text = ''
                    _persist_step(ctx, step_seq, item)
                    step_seq += 1
                    # Forward tool steps as SSE events so the frontend can render them.
                    if tag == 'tool_calls':
                        calls = [
                            {
                                'id': tc.get('id', ''),
                                'name': tc.get('name') or (tc.get('function') or {}).get('name', ''),
                                'args': tc.get('args') or (tc.get('function') or {}).get('arguments', {}),
                            }
                            for tc in (item.get('tool_calls') or [])
                            if isinstance(tc, dict)
                        ]
                        if calls:
                            yield _sse({'type': 'tool_calls', 'task_id': task_id, 'tool_calls': calls})
                    elif tag == 'tool_results':
                        results = [
                            {
                                'id': tr.get('id', ''),
                                'name': tr.get('name', ''),
                                'result': str(tr.get('result', tr.get('content', '')))[:2000],
                            }
                            for tr in (item.get('tool_results') or [])
                            if isinstance(tr, dict)
                        ]
                        if results:
                            yield _sse({'type': 'tool_results', 'task_id': task_id, 'tool_results': results})
                        source_event = _sources_event()
                        if source_event is not None:
                            yield _sse(source_event)
                    # Drain artifact events emitted synchronously by tools.
                    while emitted:
                        ev = emitted.pop(0)
                        ev['task_id'] = task_id
                        yield _sse(ev)
                    if tag == 'tool_results' and progress < 90:
                        progress = min(90, progress + 15)
                        yield _sse({'type': 'progress', 'task_id': task_id, 'progress': progress,
                                    'current_phase': '执行中...'})
                # Translate all events (text/think/tool_calls/tool_results) via shared translator.
                for frame in translator.feed(item):
                    ev_type = 'think' if frame.get('think') else 'text'
                    yield _sse({'type': ev_type, 'task_id': task_id,
                                'think': frame.get('think'), 'text': frame.get('text')})
                    if ev_type == 'think':
                        _pending_think += frame.get('think') or ''
                    else:
                        _pending_text += frame.get('text') or ''
            else:  # 'final' -- AgentExecutor propagates future exceptions before yielding this.
                final_result = payload
                # Flush any remaining accumulated text/think as the final step.
                if _pending_think:
                    ctx.db.append_step(task_id, step_seq, 'think', {'content': _pending_think})
                    step_seq += 1
                    _pending_think = ''
                if _pending_text:
                    ctx.db.append_step(task_id, step_seq, 'text', {'content': _pending_text})
                    step_seq += 1
                    _pending_text = ''

        # Drain remaining artifact events.
        while emitted:
            ev = emitted.pop(0)
            ev['task_id'] = task_id
            yield _sse(ev)

        # Flush any buffered text/think from translator (e.g. citation scanning remainder).
        for frame in translator.finish(final_result):
            ev_type = 'think' if frame.get('think') else 'text'
            yield _sse({'type': ev_type, 'task_id': task_id,
                        'think': frame.get('think'), 'text': frame.get('text')})

        source_event = _sources_event()
        if source_event is not None:
            yield _sse(source_event)

        # Flush required drafts before checking graph material guarantees.
        if effective_agent_type == 'workflow_step' and required_output_keys:
            _auto_flush_drafts(ctx, db)

        # Completeness check: every required output key must have at least one artifact.
        saved = set(ctx.saved_keys())
        if _commit_prompt_only_text_output(
            ctx, required_output_keys, saved, final_result,
        ):
            while emitted:
                ev = emitted.pop(0)
                ev['task_id'] = task_id
                yield _sse(ev)
            saved = set(ctx.saved_keys())
        missing = [k for k in required_output_keys if k not in saved]
        if missing:
            if effective_agent_type == 'workflow_step':
                cost = round(time.time() - start_time, 3)
                message = f'缺少必需产出素材: {", ".join(missing)}'
                yield _sse({'type': 'error', 'task_id': task_id, 'status': 'failed',
                            'summary': message, 'message': message, 'cost': cost})
                yield 'data: [DONE]\n\n'
                return
            steps = db.load_steps(task_id)
            is_ok, eval_summary = _evaluate_completion(
                llm=llm,
                objective=ctx.objective,
                steps=steps,
                saved_keys=list(saved),
                missing_keys=missing,
                force_result=final_result,
                ctx=ctx,
            )
            cost = round(time.time() - start_time, 3)
            if is_ok:
                _auto_flush_drafts(ctx, db)
                while emitted:
                    ev = emitted.pop(0)
                    ev['task_id'] = task_id
                    yield _sse(ev)
                yield _sse({'type': 'done', 'task_id': task_id, 'status': 'succeeded',
                            'summary': eval_summary, 'cost': cost})
            else:
                yield _sse({'type': 'error', 'task_id': task_id, 'status': 'failed',
                            'summary': eval_summary,
                            'message': f'缺少 artifact: {", ".join(missing)}。{eval_summary}'})
            yield 'data: [DONE]\n\n'
            return

        summary = _result_summary(final_result, required_output_keys)
        cost = round(time.time() - start_time, 3)
        # Auto-flush any pending drafts before emitting done.
        _auto_flush_drafts(ctx, db)
        while emitted:
            ev = emitted.pop(0)
            ev['task_id'] = task_id
            yield _sse(ev)
        yield _sse({'type': 'done', 'task_id': task_id, 'status': 'succeeded',
                    'summary': summary, 'cost': cost})
        yield 'data: [DONE]\n\n'
    except Exception as exc:  # noqa: BLE001
        LOG.exception('[SubAgent] run failed')
        source_event = _sources_event()
        if source_event is not None:
            yield _sse(source_event)
        exc_summary = str(exc)
        if db is not None:
            try:
                steps = db.load_steps(task_id)
                trace = _steps_to_trace(steps)
                exc_summary = f'异常：{exc}\n执行路径：\n{trace}'
            except Exception:
                pass
        yield _sse({'type': 'error', 'task_id': task_id, 'status': 'failed',
                    'summary': exc_summary, 'message': exc_summary})
        yield 'data: [DONE]\n\n'
    finally:
        try:
            from lazyllm.common.queue import FileSystemQueue
            FileSystemQueue(klass='cancel').clear()
        except Exception:
            pass
        if db is not None:
            db.dispose()


def _auto_flush_drafts(ctx: 'SubAgentContext', db: 'SubAgentDB') -> None:
    """Commit any pending draft files as new artifact revisions before the step ends.

    This is a safety net: if the model called patch_artifact but forgot to call
    save_artifacts, the edits are not lost — they are committed here at step boundary.
    Only drafts for required keys or keys already saved in this run are flushed.
    """
    from . import tools as subagent_tools
    required = set(_coerce_str_list((ctx.params or {}).get('required_output_artifact_keys')))
    saved = set(ctx.saved_keys())
    for base_key, list_index, original_type, content in ctx.list_pending_drafts():
        if required:
            if base_key not in required and base_key not in saved:
                ctx.delete_draft(base_key, list_index)
                LOG.info(
                    '[SubAgent] discarded optional draft key=%r for task=%s',
                    base_key, ctx.task_id,
                )
                continue
        elif base_key not in saved:
            ctx.delete_draft(base_key, list_index)
            LOG.info(
                '[SubAgent] discarded draft for unsaved key=%r for task=%s',
                base_key, ctx.task_id,
            )
            continue
        try:
            sort_order = (list_index + 1) if list_index is not None else None
            subagent_tools._save_artifact(
                base_key, content, content_type=original_type, sort_order=sort_order,
            )
            ctx.delete_draft(base_key, list_index)
            LOG.info('[SubAgent] auto-flushed draft key=%r for task=%s', base_key, ctx.task_id)
        except Exception as exc:
            LOG.warning('[SubAgent] auto-flush draft key=%r failed: %s', base_key, exc)


def _coerce_str_list(value: Any) -> List[str]:
    if isinstance(value, list):
        return [str(v) for v in value if str(v).strip()]
    if isinstance(value, (bytes, bytearray)):
        value = value.decode('utf-8')
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except ValueError:
            return []
        if isinstance(parsed, list):
            return [str(v) for v in parsed if str(v).strip()]
    return []


def _coerce_dict(value: Any) -> Dict[str, Any]:
    if isinstance(value, dict):
        return value
    if isinstance(value, (bytes, bytearray)):
        value = value.decode('utf-8')
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except ValueError:
            return {}
        if isinstance(parsed, dict):
            return parsed
    return {}


def _coerce_source_list(value: Any) -> List[Dict[str, Any]]:
    if isinstance(value, list):
        return [item for item in value if isinstance(item, dict)]
    if isinstance(value, (bytes, bytearray)):
        value = value.decode('utf-8')
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except ValueError:
            return []
        if isinstance(parsed, list):
            return [item for item in parsed if isinstance(item, dict)]
    return []


def _result_summary(result: Any, output_keys: List[str]) -> str:
    if isinstance(result, str) and result.strip():
        return result.strip()
    if output_keys:
        return f'已完成，产出：{", ".join(output_keys)}'
    return '已完成'


def _steps_to_trace(steps: List[Dict[str, Any]]) -> str:
    """Convert persisted steps into a compact execution trace string for LLM review."""
    lines: List[str] = []
    for s in steps:
        role = s.get('role', '')
        content = s.get('content') or {}
        if role == 'assistant':
            calls = content.get('tool_calls') or []
            names = ', '.join(tc.get('name', '?') for tc in calls) if calls else '（无工具调用）'
            lines.append(f'[assistant] called: {names}')
        elif role == 'tool':
            results = content.get('tool_results') or []
            for r in results:
                name = r.get('name', '?')
                res = str(r.get('result', ''))[:300]
                lines.append(f'[tool:{name}] {res}')
    return '\n'.join(lines) if lines else '（无步骤记录）'


def _evaluate_completion(
    llm: Any,
    objective: str,
    steps: List[Dict[str, Any]],
    saved_keys: List[str],
    missing_keys: List[str],
    force_result: Any,
    ctx: Optional[Any] = None,
) -> tuple:
    """Ask the LLM to judge whether the SubAgent substantively completed the objective.

    Returns (is_succeeded: bool, summary: str).
    The summary must contain actual findings/results, not references to artifacts.

    If the LLM judges YES and ctx is provided, the final output is auto-saved as a
    text artifact for each missing key so the task is not penalised for a missing
    save_artifacts call when the content is clearly present in the final output.
    """
    trace = _steps_to_trace(steps)
    force_text = str(force_result or '').strip()
    saved_str = ', '.join(saved_keys) if saved_keys else '（无）'
    missing_str = ', '.join(missing_keys) if missing_keys else '（无）'

    prompt_lines = [
        'You are reviewing the execution of an autonomous SubAgent that stopped without '
        'calling save_artifacts for all required output keys.',
        '',
        f'Original objective: {objective}',
        f'Required artifact keys: {missing_str or saved_str}',
        f'Actually saved artifact keys: {saved_str}',
        f'Missing artifact keys: {missing_str}',
        '',
        'Execution trace (tool calls and results):',
        trace,
    ]
    if force_text:
        prompt_lines += ['', f'Agent final output: {force_text[:2000]}']
    prompt_lines += [
        '',
        'Evaluation rules:',
        '- Answer YES if the agent gathered and delivered the information needed to satisfy '
        'the objective, even if it forgot to call save_artifacts. The final output text counts '
        'as evidence of completion.',
        '- Answer NO only if the agent clearly failed to obtain the required information '
        '(e.g. all tool calls errored out, or the output is empty / irrelevant).',
        '',
        'Based on the above, answer TWO things:',
        '1. Did the SubAgent substantively achieve the objective? Reply YES or NO on the first line.',
        '2. Write a self-contained summary of what was actually accomplished (include key findings, '
        'data, or results inline — not references to artifacts). '
        'If nothing useful was accomplished, briefly explain what went wrong.',
    ]
    eval_prompt = '\n'.join(prompt_lines)

    try:
        summarize_llm = llm.share(stream=False)
        resp = summarize_llm(eval_prompt)
        text = resp if isinstance(resp, str) else (
            resp.get('content', '') if isinstance(resp, dict) else ''
        )
        text = (text or '').strip()
        first_line = text.split('\n')[0].strip().upper()
        is_succeeded = first_line.startswith('YES')
        rest = text[len(text.split('\n')[0]):].strip() if '\n' in text else text
        summary = rest if rest else text

        # Auto-save final output as text artifacts for each missing key when the
        # LLM judges the task as succeeded. This recovers from models that forget
        # to call save_artifacts but include the results in their final reply.
        if is_succeeded and ctx is not None and force_text and missing_keys:
            content = summary if summary else force_text
            _image_keys = frozenset({
                'generated_image_url', 'enhanced_image_url', 'material_image',
            })
            for key in missing_keys:
                if key in _image_keys:
                    continue
                try:
                    seq = ctx.next_artifact_seq(key)
                    ctx.record_local_artifact(key, 'text', {'text': content}, seq)
                    ctx.emit({'type': 'artifact', 'slot': key,
                              'content_type': 'text', 'seq': seq, 'value': {'text': content}})
                    LOG.info(f'[SubAgent] auto-saved missing artifact key={key!r} for task={ctx.task_id}')
                except Exception as save_err:
                    LOG.warning(f'[SubAgent] auto-save artifact key={key!r} failed: {save_err}')

        return is_succeeded, summary
    except Exception as e:
        LOG.warning(f'[SubAgent] _evaluate_completion LLM call failed: {e}')
        return False, f'执行中断，已完成步骤数：{len(steps)}，缺少产出：{missing_str}'


def _rebuild_history_from_steps(db: SubAgentDB, task_id: str) -> List[Dict[str, Any]]:
    """Rebuild LLM chat history from persisted steps for resume.

    Validates tool_call_id pairing: every assistant tool_call must have a matching tool result.
    A tool step whose result has no preceding assistant tool_call id (orphan) is discarded, and
    replay stops at the last complete assistant boundary.

    Also validates that every tool_call's function.arguments is valid JSON.  If any arguments
    field is malformed (e.g. persisted from a truncated stream), the offending assistant message
    and everything after it are dropped so the model never receives corrupt history.
    """
    steps = db.load_steps(task_id)
    history: List[Dict[str, Any]] = []
    pending_ids: set = set()
    for step in steps:
        role = step.get('role')
        content = step.get('content') or {}
        if role == 'assistant':
            tool_calls = content.get('tool_calls') or []
            # Validate function.arguments JSON before appending.
            for tc in tool_calls:
                args = (tc.get('function') or {}).get('arguments') or tc.get('args')
                if args and isinstance(args, str):
                    try:
                        json.loads(args)
                    except (ValueError, TypeError):
                        # Corrupt arguments: stop replay at the last clean boundary.
                        LOG.warning(
                            f'[SubAgent] resume: dropping corrupt tool_call '
                            f'(task={task_id}, name={(tc.get("function") or {}).get("name")})'
                        )
                        return history
            pending_ids = {tc.get('id') for tc in tool_calls if tc.get('id')}
            history.append({
                'role': 'assistant',
                'content': content.get('text', ''),
                'tool_calls': tool_calls,
            })
        elif role == 'tool':
            results = content.get('tool_results') or []
            valid = [r for r in results if r.get('tool_call_id') in pending_ids]
            if not valid:
                # Orphan tool results: drop and stop replay at the last complete boundary.
                if history and history[-1].get('role') == 'assistant':
                    history.pop()
                break
            for r in valid:
                history.append({
                    'role': 'tool',
                    'tool_call_id': r.get('tool_call_id'),
                    'name': r.get('name', ''),
                    'content': str(r.get('result', '')),
                })
            pending_ids = set()
    return history
