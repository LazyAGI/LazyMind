"""Plugin generation API route.

Route:
    POST /api/chat/generate_plugin   Generate plugin.yaml + state.yml from a description or skill content.
"""
from __future__ import annotations

import json
import logging
import os
import re
from pathlib import Path
from typing import Any, Dict, Optional

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field
import lazyllm

from lazymind.model_config import inject_model_config

router = APIRouter()
logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Plugin format spec — loaded once at module load time.
# ---------------------------------------------------------------------------

def _load_plugin_format_spec() -> str:
    """Load docs/plugin-format.md from the repository root."""
    candidates = [
        Path(__file__).parent.parent.parent.parent.parent.parent / 'docs' / 'plugin-format.md',
        Path('/app/docs/plugin-format.md'),
    ]
    for path in candidates:
        if path.exists():
            return path.read_text(encoding='utf-8')
    logger.warning('plugin-format.md not found; generating without spec')
    return ''


_PLUGIN_FORMAT_SPEC: str = _load_plugin_format_spec()

# ---------------------------------------------------------------------------
# Prompts
# ---------------------------------------------------------------------------

_SYSTEM_PROMPT_TEMPLATE = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Generate a valid LazyMind plugin consisting of exactly two YAML files:\n'
    '  1. plugin.yaml — plugin metadata and slot definitions (list format)\n'
    '  2. state.yml   — state machine execution logic\n\n'
    'Rules:\n'
    '- Follow the format specification below exactly.\n'
    '- plugin.yaml slots must be a list of objects with {id, type, ...}; NOT a map.\n'
    '- state.yml steps must use {slot, required} objects for inputs/outputs; no top-level slots block.\n'
    '- Return your response as a JSON object: {"plugin_yaml": "...", "state_yaml": "..."}\n'
    '- No extra explanation outside the JSON object.\n\n'
    '=== Plugin Format Specification ===\n'
    '{spec}\n'
    '=== End of Specification ==='
)

_USER_PROMPT_DESCRIPTION = 'Generate a plugin based on the following description:\n\n{description}'
_USER_PROMPT_SKILL = 'Convert the following skill content into a plugin:\n\n{skill_content}'


class AutoModel:
    """Thin wrapper that mirrors the pattern used in lazymind.rewrite.base."""
    def __call__(self, prompt: str) -> str:
        module = lazyllm.AutoModel(model='llm')
        return module(prompt)


def _build_prompt(name: str, description: str, skill_content: str) -> tuple[str, str]:
    spec = _PLUGIN_FORMAT_SPEC
    system = _SYSTEM_PROMPT_TEMPLATE.format(spec=spec)
    if skill_content.strip():
        user = _USER_PROMPT_SKILL.format(skill_content=skill_content)
    else:
        user = _USER_PROMPT_DESCRIPTION.format(description=description or name)
    # Prepend plugin name hint
    user = f'Plugin name: {name}\n\n{user}'
    return system, user


def _parse_llm_response(raw: str) -> tuple[str, str]:
    """Extract plugin_yaml and state_yaml from the LLM response JSON."""
    # Try to extract a JSON object from the response
    json_match = re.search(r'\{[\s\S]*\}', raw)
    if not json_match:
        raise ValueError(f'No JSON found in LLM response: {raw[:200]}')
    try:
        data = json.loads(json_match.group(0))
    except json.JSONDecodeError as exc:
        raise ValueError(f'Invalid JSON in LLM response: {exc}') from exc
    plugin_yaml = data.get('plugin_yaml', '')
    state_yaml = data.get('state_yaml', '')
    if not plugin_yaml or not state_yaml:
        raise ValueError(f'LLM response missing plugin_yaml or state_yaml: {list(data.keys())}')
    return plugin_yaml, state_yaml


# ---------------------------------------------------------------------------
# Request / Response models
# ---------------------------------------------------------------------------

class GeneratePluginRequest(BaseModel):
    name: str = Field(..., description='Plugin display name')
    description: Optional[str] = Field(None, description='Natural-language description of the plugin goal')
    skill_content: Optional[str] = Field(None, description='Existing skill content to convert (mutually exclusive with description)')
    llm_config: Dict[str, Any] = Field(default_factory=dict, description='Per-request model config from core')


class GeneratePluginResponse(BaseModel):
    plugin_yaml: str
    state_yaml: str


# ---------------------------------------------------------------------------
# Route handler
# ---------------------------------------------------------------------------

@router.post(
    '/api/chat/generate_plugin',
    response_model=GeneratePluginResponse,
    summary='Generate plugin.yaml and state.yml from a description or skill content',
)
async def generate_plugin(req: GeneratePluginRequest) -> GeneratePluginResponse:
    """Synchronously generate a LazyMind plugin from a natural-language description or an existing skill.

    Called by the Go asyncjob worker (plugin_draft_generate).
    Returns plugin_yaml and state_yaml as plain strings.
    """
    inject_model_config(req.llm_config or {})

    session_id = f'generate_plugin_{req.name}'
    try:
        lazyllm.globals._init_sid(sid=session_id)
        lazyllm.locals._init_sid(sid=session_id)
    except Exception:
        pass

    system_prompt, user_prompt = _build_prompt(
        name=req.name,
        description=req.description or '',
        skill_content=req.skill_content or '',
    )
    full_prompt = f'{system_prompt}\n\n{user_prompt}'

    try:
        module = lazyllm.AutoModel(model='llm')
        raw = module(full_prompt)
    except Exception as exc:
        logger.exception('LLM call failed during generate_plugin')
        raise HTTPException(status_code=500, detail=f'LLM call failed: {exc}') from exc

    try:
        plugin_yaml, state_yaml = _parse_llm_response(raw)
    except ValueError as exc:
        logger.error('Failed to parse LLM response for generate_plugin: %s', exc)
        raise HTTPException(status_code=500, detail=f'Failed to parse LLM response: {exc}') from exc

    return GeneratePluginResponse(plugin_yaml=plugin_yaml, state_yaml=state_yaml)
