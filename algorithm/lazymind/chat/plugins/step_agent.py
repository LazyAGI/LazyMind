from __future__ import annotations

import os
import shutil
from typing import Any

import lazyllm


# ---- Builtin tool implementations ----

def save_step_artifact(artifact_id: str, value: Any) -> str:
    """Persist the final output of this step. Call when the task is complete.

    value: text/URL pass as string; binary file pass as local path
    (framework copies to step_workspace automatically).
    """
    workspace = lazyllm.globals.get('agentic_config', {}).get('step_workspace', '')
    stored_value = _maybe_copy_to_workspace(value, artifact_id, workspace)
    _queue = lazyllm.globals.get('agentic_config', {}).get('plugin_event_queue') \
        or lazyllm.globals.get('plugin_event_queue', [])
    _queue.append(
        {'type': 'artifact', 'artifact_id': artifact_id, 'value': stored_value}
    )
    return f'Artifact {artifact_id!r} saved.'


def save_step_checkpoint(data: dict) -> str:
    """Persist intermediate progress. Call every ~10 items or at major milestones.

    data keys: completed_count (int), total_count (int),
               partial_results (list), phase_note (str)
    """
    workspace = lazyllm.globals.get('agentic_config', {}).get('step_workspace', '')
    stored_data = _normalize_checkpoint_data(data, workspace)
    _queue = lazyllm.globals.get('agentic_config', {}).get('plugin_event_queue') \
        or lazyllm.globals.get('plugin_event_queue', [])
    _queue.append(
        {'type': 'checkpoint', 'value': stored_data}
    )
    count = data.get('completed_count', 0)
    total = data.get('total_count', 0)
    return f'Checkpoint: {count}/{total} done.'


def get_checkpoint_details(item_range: str) -> list:
    """Lazily fetch specific past partial_results. item_range e.g. "0-9"."""
    checkpoint = lazyllm.globals.get('agentic_config', {}).get('step_checkpoint', {})
    partial = checkpoint.get('partial_results', [])
    try:
        start_str, end_str = item_range.split('-')
        start, end = int(start_str), int(end_str)
        return partial[start:end + 1]
    except Exception:
        return partial


# ---- Workspace helpers ----

def _maybe_copy_to_workspace(value: Any, artifact_id: str, workspace: str) -> Any:
    """If value is a local file path outside workspace, copy it in and return new path."""
    if not isinstance(value, str):
        return value
    if not workspace:
        return value
    if not os.path.isfile(value):
        return value
    dest_dir = workspace
    os.makedirs(dest_dir, exist_ok=True)
    filename = os.path.basename(value)
    dest = os.path.join(dest_dir, f'{artifact_id}_{filename}')
    if os.path.abspath(value) == os.path.abspath(dest):
        return dest
    shutil.copy2(value, dest)
    return dest


def _normalize_checkpoint_data(data: dict, workspace: str) -> dict:
    """Copy any file paths in partial_results into workspace."""
    result = {
        'completed_count': data.get('completed_count', 0),
        'total_count': data.get('total_count', 0),
        'phase_note': data.get('phase_note', ''),
    }
    partial = data.get('partial_results', [])
    if workspace and partial:
        normalized = []
        for item in partial:
            if isinstance(item, str) and os.path.isfile(item):
                normalized.append(_maybe_copy_to_workspace(item, 'partial', workspace))
            else:
                normalized.append(item)
        result['partial_results'] = normalized
    else:
        result['partial_results'] = partial
    return result


# ---- Prompt rendering ----

def _render_step_prompt(step_config: dict, artifacts: dict,
                        checkpoint: dict | None) -> str:
    prompt_template = step_config.get('prompt', '')
    # Replace {{artifact_id}} template variables.
    for key, val in (artifacts or {}).items():
        placeholder = '{{' + key + '}}'
        prompt_template = prompt_template.replace(placeholder, str(val) if val is not None else '')

    # Replace any remaining placeholders with empty string.
    import re
    prompt_template = re.sub(r'\{\{[a-zA-Z_][a-zA-Z0-9_]*\}\}', '', prompt_template)

    parts = [prompt_template.rstrip()]

    if checkpoint:
        completed = checkpoint.get('completed_count', 0)
        total = checkpoint.get('total_count', 0)
        phase_note = checkpoint.get('phase_note', '')
        if completed or total or phase_note:
            resume_block = (
                '\n\n## Resuming from checkpoint\n'
                f'Progress: {completed}/{total} items completed.\n'
            )
            if phase_note:
                resume_block += f'Phase note: {phase_note}\n'
            resume_block += (
                'Use get_checkpoint_details("0-9") to retrieve previous partial results if needed.\n'
                'Continue from where you left off.'
            )
            parts.append(resume_block)

    builtin_instructions = (
        '\n\n## Built-in tools (always available)\n'
        '- save_step_artifact(artifact_id, value): call when the step is fully complete to save the output.\n'
        '- save_step_checkpoint(data): call periodically to save progress'
        ' (completed_count, total_count, phase_note, partial_results).\n'
        '- get_checkpoint_details(item_range): fetch prior partial_results by range e.g. "0-9".'
    )
    parts.append(builtin_instructions)

    return '\n'.join(parts)


def _resolve_step_tools(step_config: dict, default_tools: list,
                        builtin_tools: list) -> list:
    declared = step_config.get('tools', [])
    if not declared:
        # Empty list means inherit all default tools.
        return default_tools + builtin_tools

    # Filter default_tools by name.
    tool_names = set(declared)
    selected = [t for t in default_tools if getattr(t, '__name__', '') in tool_names]
    return selected + builtin_tools


def _build_builtin_tools() -> list:
    return [save_step_artifact, save_step_checkpoint, get_checkpoint_details]


# ---- StepAgent factory ----

def create_step_agent(
    step_config: dict,
    artifacts: dict,
    checkpoint: dict | None,
    default_tools: list,
    llm: Any,
    step_exec_id: str = '',
) -> Any:
    """Build and return a ReactAgent configured for this step execution."""
    prompt = _render_step_prompt(step_config, artifacts or {}, checkpoint or {})
    builtin_tools = _build_builtin_tools()
    tools = _resolve_step_tools(step_config, default_tools, builtin_tools)

    from lazymind.config import config as _cfg
    agent = lazyllm.tools.agent.ReactAgent(
        llm=llm,
        tools=tools,
        max_retries=_cfg.get('max_retries', 20),
        stream=True,
        prompt=prompt,
        enable_builtin_tools=False,
        force_summarize=True,
        force_summarize_context='',
    )
    return agent
