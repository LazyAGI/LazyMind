from __future__ import annotations

import hashlib
from typing import Any, Optional

import lazyllm

from lazymind.chat.engine.agent_runtime.prompt_builder import PromptBuilder

try:
    from lazyllm.tools.agent.base import _write_agent_data
except Exception:  # pragma: no cover
    _write_agent_data = None

SIDECAR_KEYS = (
    'active_skills',
    'artifact_coords',
    'spill_paths',
    'citation_map',
)
GOAL_SIDECAR_KEYS = (
    'task_goal',
    'key_instructions',
    'hard_constraints',
)

_SKILL_TOOLS = frozenset({'get_skill', 'read_reference', 'read_skill_resource'})
_ARTIFACT_TOOLS = frozenset({
    'get_artifact',
    'save_artifacts',
    'patch_artifact',
    'find_artifact',
    'discard_draft',
    'list_artifacts',
    'validate_proposal_from_inputs',
})
_ARTIFACT_PREFIXES = (
    'product_writer_',
    'validate_and_allocate',
)


def classify_special_tool(tool_name: str) -> str:
    name = str(tool_name or '').strip()
    lowered = name.lower()
    if lowered in _SKILL_TOOLS:
        return 'skill'
    if lowered in _ARTIFACT_TOOLS or any(lowered.startswith(prefix) for prefix in _ARTIFACT_PREFIXES):
        return 'artifact'
    return ''


def content_sha256(text: str) -> str:
    return hashlib.sha256(text.encode('utf-8', errors='replace')).hexdigest()


def _agentic_config() -> dict[str, Any]:
    cfg = lazyllm.globals.get('agentic_config')
    if not isinstance(cfg, dict):
        cfg = {}
        lazyllm.globals['agentic_config'] = cfg
    return cfg


def emit_model_context_sidecar() -> None:
    if _write_agent_data is None:
        return
    try:
        fields = sidecar_fields()
        _write_agent_data('model_context_updated', version=1, **fields)
        cfg = _agentic_config()
        current = cfg.get('model_context')
        if not isinstance(current, dict):
            current = {}
        cfg['model_context'] = merge_model_context_sidecar(current, fields)
    except Exception:
        return


def active_skills_from_model_context(model_context: Any) -> list[dict[str, Any]]:
    if not isinstance(model_context, dict):
        return []
    raw = model_context.get('active_skills')
    if not isinstance(raw, list):
        return []
    skills = []
    for item in raw:
        if not isinstance(item, dict):
            continue
        name = str(item.get('name') or '').strip()
        if not name:
            continue
        skills.append({
            'name': name,
            'path': str(item.get('path') or ''),
            'hash': str(item.get('hash') or ''),
            'spill_path': str(item.get('spill_path') or ''),
        })
    return skills


def record_task_goals(
    *,
    task_goal: str = '',
    key_instructions: str = '',
    hard_constraints: str = '',
) -> dict[str, str]:
    cfg = _agentic_config()
    model_context = cfg.get('model_context')
    if not isinstance(model_context, dict):
        model_context = {}
        cfg['model_context'] = model_context
    stored = {
        'task_goal': str(cfg.get('task_goal') or model_context.get('task_goal') or '').strip(),
        'key_instructions': str(
            cfg.get('key_instructions') or model_context.get('key_instructions') or ''
        ).strip(),
        'hard_constraints': str(
            cfg.get('hard_constraints') or model_context.get('hard_constraints') or ''
        ).strip(),
    }
    incoming = {
        'task_goal': str(task_goal or '').strip(),
        'key_instructions': str(key_instructions or '').strip(),
        'hard_constraints': str(hard_constraints or '').strip(),
    }
    for key, value in incoming.items():
        if value and not stored[key]:
            stored[key] = value
    for key, value in stored.items():
        cfg[key] = value
        if value:
            model_context[key] = value
    emit_model_context_sidecar()
    return stored


def pin_task_goals_into_builder(
    builder: PromptBuilder,
    model_context: Any,
    *,
    task_goal: str = '',
    key_instructions: str = '',
    hard_constraints: str = '',
) -> None:
    stored = record_task_goals(
        task_goal=str((model_context or {}).get('task_goal') or task_goal or '')
        if isinstance(model_context, dict) else task_goal,
        key_instructions=(
            str((model_context or {}).get('key_instructions') or key_instructions or '')
            if isinstance(model_context, dict) else key_instructions
        ),
        hard_constraints=(
            str((model_context or {}).get('hard_constraints') or hard_constraints or '')
            if isinstance(model_context, dict) else hard_constraints
        ),
    )
    if stored['task_goal']:
        builder.runtime(
            'task_goal',
            'Current Task Goal',
            stored['task_goal'],
            'runtime.task.pinned',
            priority=5,
            authoritative=True,
            content_kind='instruction',
        )
    if stored['key_instructions']:
        builder.runtime(
            'key_instructions',
            'Key Instructions',
            stored['key_instructions'],
            'runtime.task.pinned',
            priority=5,
            authoritative=True,
            content_kind='instruction',
        )
    if stored['hard_constraints']:
        builder.runtime(
            'hard_constraints',
            'Hard Constraints',
            stored['hard_constraints'],
            'runtime.task.pinned',
            priority=5,
            authoritative=True,
            content_kind='instruction',
        )


def citation_map_from_state(state: Any) -> list[dict[str, Any]]:
    if not isinstance(state, dict):
        return []
    refs = state.get('_citation_sources')
    if not isinstance(refs, dict):
        return []
    mapping = []
    for index, source in refs.items():
        if not isinstance(source, dict):
            continue
        mapping.append({
            'id': str(index),
            'url': str(source.get('url') or ''),
            'doc_id': str(source.get('doc_id') or source.get('document_id') or ''),
            'title': str(source.get('title') or source.get('name') or '')[:200],
        })
    return mapping


def collect_summary_runtime_state() -> dict[str, Any]:
    cfg = _agentic_config()
    workflow = cfg.get('workflow_runtime') if isinstance(cfg.get('workflow_runtime'), dict) else {}
    session_id = str(
        cfg.get('workflow_session_id')
        or workflow.get('session_id')
        or ''
    )
    step_id = str(
        workflow.get('step_id')
        or workflow.get('current_step')
        or cfg.get('workflow_step_id')
        or ''
    )
    coords = cfg.get('artifact_coords')
    if not isinstance(coords, list):
        coords = []
    spills = cfg.get('spill_paths')
    if not isinstance(spills, list):
        spills = []
    model_context = cfg.get('model_context') if isinstance(cfg.get('model_context'), dict) else {}
    return {
        'active_skills': list(cfg.get('active_skills') or []),
        'artifact_coords': coords,
        'spill_paths': spills,
        'citation_map': citation_map_from_state(cfg.get('citation_state')),
        'workflow_session_id': session_id,
        'workflow_step_id': step_id,
        'task_goal': str(cfg.get('task_goal') or model_context.get('task_goal') or ''),
        'key_instructions': str(
            cfg.get('key_instructions') or model_context.get('key_instructions') or ''
        ),
        'hard_constraints': str(
            cfg.get('hard_constraints') or model_context.get('hard_constraints') or ''
        ),
    }


def resolve_summary_profile(runtime: Optional[dict[str, Any]] = None) -> str:
    state = runtime if isinstance(runtime, dict) else collect_summary_runtime_state()
    if state.get('workflow_session_id'):
        return 'workflow'
    if state.get('active_skills'):
        return 'skill'
    if state.get('citation_map'):
        return 'rag'
    return 'default'


def sidecar_fields(runtime: Optional[dict[str, Any]] = None) -> dict[str, Any]:
    state = runtime if isinstance(runtime, dict) else collect_summary_runtime_state()
    fields = {key: state.get(key) or [] for key in SIDECAR_KEYS}
    for key in GOAL_SIDECAR_KEYS:
        fields[key] = str(state.get(key) or '')
    return fields


def merge_model_context_sidecar(target: dict[str, Any], sidecar: dict[str, Any]) -> dict[str, Any]:
    merged = dict(target)
    for key in SIDECAR_KEYS:
        if key in sidecar and sidecar[key] is not None:
            merged[key] = sidecar[key]
    for key in GOAL_SIDECAR_KEYS:
        if key in sidecar and sidecar[key]:
            merged[key] = sidecar[key]
    return merged
