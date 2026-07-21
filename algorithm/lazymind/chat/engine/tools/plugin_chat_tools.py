"""Workflow creation tool for use in chat conversations.

Provides create_plugin_draft: a three-step workflow that creates a plugin draft
via the Go core API and triggers AI generation.

Workflow (driven by create-plugin SKILL.md):
  Step 1 — LLM produces a natural-language plugin summary.
  Step 2 — ask_user confirms the plan (ask_user is a separate stop-tool).
  Step 3 — User confirms → call create_plugin_draft to create, pre-fill, and
            trigger async AI generation.
"""
from __future__ import annotations

from typing import Any, Dict, Optional

import lazyllm

from lazymind.chat.engine.tools.infra import tool_error, tool_success
from lazymind.chat.engine.tools.infra.core_api_client import post_core_api
from lazyllm.tools.agent.base import _write_agent_data


def _agentic_config() -> Dict[str, Any]:
    try:
        return lazyllm.globals['agentic_config'] or {}
    except Exception:
        return {}


def create_plugin_draft(
    name: str,
    description: str,
    slots: Optional[str] = None,
    steps: Optional[str] = None,
) -> Dict[str, Any]:
    """Create a new plugin draft and trigger AI generation.

    Call this tool ONLY after the user has explicitly confirmed the plugin plan
    (confirmed via ask_user with type='boolean').

    This tool performs three sequential Go core API calls:
      1. POST /plugin-drafts — creates a blank draft, obtains draft_id.
      2. POST /plugin-drafts/{id}:save — pre-writes the confirmed name and
         description so the AI generation uses them as constraints.
      3. POST /plugin-drafts/{id}:ai-generate — triggers the three-phase async
         generation job.

    After the tool returns, write a short confirmation message that includes
    a Markdown link to the plugin editor using the editor_url in the result.

    Args:
        name: Workflow display name (e.g. "合同审阅助手").
        description: One-paragraph description of what the plugin does.
            This becomes the AI generation seed. Should be detailed enough
            to capture inputs, outputs, and main steps.
        slots: Optional newline-separated list describing the artifact slots,
            e.g. "contract_file (file, single)\\nreview_result (text, single)".
            Passed verbatim into the description for the AI.
        steps: Optional newline-separated list of step names,
            e.g. "extract_clauses\\nanalyze_risks\\ngenerate_report".
            Passed verbatim into the description for the AI.

    Returns a dict with:
        draft_id    — UUID of the newly created draft.
        editor_url  — Relative URL to open the plugin editor, e.g. /plugin/<id>.
        status      — Always "generating" immediately after creation.
        name        — The plugin name.
    """
    name = (name or '').strip()
    description = (description or '').strip()
    if not name:
        return tool_error('name is required')
    if not description:
        return tool_error('description is required')

    # Build enriched description for AI generation
    full_description = description
    if slots and slots.strip():
        full_description += f'\n\nSlots:\n{slots.strip()}'
    if steps and steps.strip():
        full_description += f'\n\nSteps:\n{steps.strip()}'

    # Step 1: Create blank draft
    try:
        create_resp = post_core_api('/plugin-drafts', {'name': name})
    except RuntimeError as exc:
        return tool_error(f'Failed to create plugin draft: {exc}')

    draft_data = create_resp.get('response', {})
    # Unwrap data envelope
    if isinstance(draft_data, dict) and 'data' in draft_data:
        draft_data = draft_data['data']
    draft_id = (draft_data.get('id') or '').strip() if isinstance(draft_data, dict) else ''
    if not draft_id:
        return tool_error('Failed to create plugin draft: no draft_id returned')

    # Step 2: Pre-fill confirmed name and description
    try:
        import yaml  # noqa: PLC0415
        skeleton_yaml = yaml.dump({'id': '', 'name': name, 'description': description}, allow_unicode=True)
        post_core_api(f'/plugin-drafts/{draft_id}:save', {
            'plugin_yaml_content': skeleton_yaml,
            'version': 0,
        })
    except Exception as exc:  # noqa: BLE001
        # Pre-fill failure is non-fatal; generation can still proceed
        _write_agent_data({'plugin_draft_prefill_warning': str(exc)})

    # Step 3: Trigger async AI generation
    try:
        post_core_api(f'/plugin-drafts/{draft_id}:ai-generate', {'description': full_description})
    except RuntimeError as exc:
        return tool_error(f'Failed to trigger AI generation: {exc}')

    editor_url = f'/plugin/{draft_id}'

    # Emit plugin_draft_created SSE event so future frontend cards can pick it up
    _write_agent_data({
        'plugin_draft_created': {
            'draft_id': draft_id,
            'name': name,
            'editor_url': editor_url,
            'status': 'generating',
        },
    })

    return tool_success({
        'draft_id': draft_id,
        'editor_url': editor_url,
        'status': 'generating',
        'name': name,
    })
