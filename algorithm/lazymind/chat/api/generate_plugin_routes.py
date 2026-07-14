"""Plugin generation API route.

Route:
    POST /api/chat/generate_plugin   Generate plugin.yaml + state.yml from a description or skill content.
"""
from __future__ import annotations

import json
import logging
import re
import tempfile
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field
import yaml
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
# Required field definitions — sync with docs/plugin-format.md when fields change.
# Each entry is a dot-separated path into the parsed dict (plugin.* or state.*).
# ---------------------------------------------------------------------------

# Top-level required fields
_REQUIRED_PLUGIN_TOP = ['id', 'name', 'description', 'steps', 'slots']
_REQUIRED_STATE_TOP = ['transitions', 'steps']

# Required per-item fields (checked dynamically)
_REQUIRED_SLOT_FIELDS = ['id', 'label', 'type', 'cardinality']
_REQUIRED_STEP_FIELDS = ['id', 'label']           # plugin.yaml steps entries
_REQUIRED_STATE_STEP_FIELDS = ['prompt']           # state.yml steps[*]
_REQUIRED_TRANSITION_ENTRY_FIELDS = ['to']         # each transition entry

MAX_PATCH_RETRIES = 2

# ---------------------------------------------------------------------------
# Prompts
# ---------------------------------------------------------------------------

_SYSTEM_PROMPT_TEMPLATE = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Generate a valid LazyMind plugin consisting of exactly two YAML files:\n'
    '  1. plugin.yaml — plugin metadata and slot definitions (list format)\n'
    '  2. state.yml   — state machine execution logic\n'
    '  3. scenario_md — scenario.md content (Markdown string)\n'
    '  4. scripts     — optional Python script files (dict mapping filename to content)\n\n'
    'Rules:\n'
    '- Follow the format specification below exactly.\n'
    '- plugin.yaml slots must be a list of objects with {id, type, ...}; NOT a map.\n'
    '- state.yml inputs are one ordered list: [{material, required, alternatives?}].\n'
    '- alternatives is allowed only when required is true and contains material references.\n'
    '- Do not emit bind_as; material IDs are globally unique.\n'
    '- Outputs use {material: slot_id} and are always required; each non-external material has exactly one producer.\n'
    '- Materials are durable artifacts only: extra data explicitly supplied separately by the user '
    '(file/image/form field/dataset) or outputs produced by prior steps.\n'
    '- Mark genuine extra user/session-provided materials with external: true in plugin.yaml slots.\n'
    '- The user query, task description, intent, instructions, prompt text, and conversation context are NOT materials. '
    'Never create pseudo-materials such as user_query, search_query, request, topic, '
    'task_description, or instructions.\n'
    '- Route decisions use an optional natural-language `when` hint for ChatAgent; '
    'never use a material expression on an edge.\n'
    '- skip_if is a flat material expression: all(materials) or any(materials), with no nesting.\n'
    '- The control graph must be a DAG. Natural-language routes do not require an unconditional fallback.\n'
    '- Return your response as a JSON object with these keys:\n'
    '    {"plugin_yaml": "...", "state_yaml": "...", "scenario_md": "...", "scripts": {}}\n'
    '- scripts is optional; omit it or set to {} when no custom tools are needed.\n'
    '- No extra explanation outside the JSON object.\n\n'
    '=== Plugin Format Specification ===\n'
    '{spec}\n'
    '=== End of Specification ==='
)

_USER_PROMPT_DESCRIPTION = 'Generate a plugin based on the following description:\n\n{description}'
_USER_PROMPT_SKILL = 'Convert the following skill content into a plugin:\n\n{skill_content}'

_PATCH_PROMPT_TEMPLATE = (
    'The previously generated plugin has the following missing or invalid fields:\n'
    '{missing_fields}\n\n'
    'Here is the current plugin state for context:\n'
    '--- plugin.yaml ---\n'
    '{plugin_yaml}\n'
    '--- state.yml ---\n'
    '{state_yaml}\n\n'
    'Return ONLY a JSON patch object containing the corrected/missing values.\n'
    'Do NOT regenerate fields that are already correct.\n'
    'The patch must follow this structure (include only the keys that need fixing):\n'
    '{{\n'
    '  "plugin": {{ ... }},\n'
    '  "state": {{ ... }}\n'
    '}}\n'
    'Example: {{"state": {{"steps": {{"step_one": {{"prompt": "..."}}}}}}}}\n'
    'Return only the JSON patch, no explanation.'
)

# ---------------------------------------------------------------------------
# Field validation helpers
# ---------------------------------------------------------------------------


def _check_missing_fields(
    plugin_dict: Dict[str, Any],
    state_dict: Dict[str, Any],
) -> List[str]:
    """Return a list of dot-path strings for missing or invalid required fields."""
    missing: List[str] = []

    # --- plugin.yaml top-level ---
    for field in _REQUIRED_PLUGIN_TOP:
        val = plugin_dict.get(field)
        if val is None or val == '' or val == [] or val == {}:
            missing.append(f'plugin.{field}')

    # --- plugin.yaml slots items ---
    slots = plugin_dict.get('slots')
    if isinstance(slots, list):
        for i, slot in enumerate(slots):
            if not isinstance(slot, dict):
                missing.append(f'plugin.slots[{i}] (must be a dict)')
                continue
            for f in _REQUIRED_SLOT_FIELDS:
                if not slot.get(f):
                    missing.append(f'plugin.slots[{i}].{f}')

    # --- plugin.yaml steps items ---
    steps = plugin_dict.get('steps')
    if isinstance(steps, list):
        for i, step in enumerate(steps):
            if not isinstance(step, dict):
                missing.append(f'plugin.steps[{i}] (must be a dict)')
                continue
            for f in _REQUIRED_STEP_FIELDS:
                if not step.get(f):
                    missing.append(f'plugin.steps[{i}].{f}')

    # --- state.yml top-level ---
    for field in _REQUIRED_STATE_TOP:
        val = state_dict.get(field)
        if val is None or val == '' or val == [] or val == {}:
            missing.append(f'state.{field}')

    # --- state.yml transitions entries ---
    transitions = state_dict.get('transitions')
    if isinstance(transitions, dict):
        for src, edges in transitions.items():
            if not isinstance(edges, list):
                missing.append(f'state.transitions.{src} (must be a list)')
                continue
            for i, edge in enumerate(edges):
                if not isinstance(edge, dict) or not edge.get('to'):
                    missing.append(f'state.transitions.{src}[{i}].to')

    # --- state.yml steps[*].prompt ---
    state_steps = state_dict.get('steps')
    if isinstance(state_steps, dict):
        for step_id, step_cfg in state_steps.items():
            if not isinstance(step_cfg, dict):
                missing.append(f'state.steps.{step_id} (must be a dict)')
                continue
            for f in _REQUIRED_STATE_STEP_FIELDS:
                if not step_cfg.get(f):
                    missing.append(f'state.steps.{step_id}.{f}')

    return missing


def _deep_merge(base: Dict[str, Any], patch: Dict[str, Any]) -> Dict[str, Any]:
    """Recursively merge patch into base, returning a new dict."""
    result = dict(base)
    for key, patch_val in patch.items():
        base_val = result.get(key)
        if isinstance(base_val, dict) and isinstance(patch_val, dict):
            result[key] = _deep_merge(base_val, patch_val)
        else:
            result[key] = patch_val
    return result


# ---------------------------------------------------------------------------
# LLM helpers
# ---------------------------------------------------------------------------


def _call_llm(prompt: str) -> str:
    module = lazyllm.AutoModel(model='llm')
    return module(prompt)


def _extract_json(raw: str) -> Dict[str, Any]:
    """Extract the first complete JSON object from a raw LLM response string.

    Uses json.JSONDecoder.raw_decode to find the first well-formed object rather
    than a greedy regex that can fail when the LLM wraps output in markdown fences
    or when script code contains bare braces outside the JSON.
    """
    # Strip common markdown code-fence wrappers that LLMs sometimes add.
    text = raw.strip()
    for fence in ('```json', '```JSON', '```'):
        if text.startswith(fence):
            text = text[len(fence):]
            if text.endswith('```'):
                text = text[:-3]
            text = text.strip()
            break

    decoder = json.JSONDecoder()
    # Scan from the first '{' character so we skip any preamble text.
    idx = text.find('{')
    if idx == -1:
        raise ValueError(f'No JSON object found in LLM response: {raw[:300]}')
    try:
        obj, _ = decoder.raw_decode(text, idx)
        if isinstance(obj, dict):
            return obj
        raise ValueError(f'Parsed JSON is not a dict: {type(obj)}')
    except json.JSONDecodeError as exc:
        # Fallback: try the original greedy regex as last resort.
        match = re.search(r'\{[\s\S]*\}', text)
        if match:
            try:
                return json.loads(match.group(0))
            except json.JSONDecodeError:
                pass
        raise ValueError(f'Invalid JSON in LLM response: {exc}') from exc


def _parse_initial_response(raw: str) -> Tuple[
    str, str, str, Dict[str, str],
    Dict[str, Any], Dict[str, Any],
]:
    """Parse the initial LLM response.

    Returns:
        plugin_yaml, state_yaml, scenario_md, scripts,
        plugin_dict, state_dict
    """
    data = _extract_json(raw)
    plugin_yaml = data.get('plugin_yaml', '')
    state_yaml = data.get('state_yaml', '')
    scenario_md = data.get('scenario_md', '')
    scripts: Dict[str, str] = data.get('scripts') or {}

    if not plugin_yaml or not state_yaml:
        raise ValueError(f'LLM response missing plugin_yaml or state_yaml: {list(data.keys())}')

    try:
        plugin_dict = yaml.safe_load(plugin_yaml) or {}
    except yaml.YAMLError as exc:
        raise ValueError(f'plugin_yaml is not valid YAML: {exc}') from exc

    try:
        state_dict = yaml.safe_load(state_yaml) or {}
    except yaml.YAMLError as exc:
        raise ValueError(f'state_yaml is not valid YAML: {exc}') from exc

    return plugin_yaml, state_yaml, scenario_md, scripts, plugin_dict, state_dict


def _apply_patch(
    plugin_dict: Dict[str, Any],
    state_dict: Dict[str, Any],
    missing: List[str],
    system_prompt: str,
) -> Tuple[Dict[str, Any], Dict[str, Any]]:
    """Ask the LLM for a JSON patch covering the missing fields, then merge it."""
    plugin_yaml_current = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    state_yaml_current = yaml.dump(state_dict, allow_unicode=True, sort_keys=False)

    patch_prompt = _PATCH_PROMPT_TEMPLATE.format(
        missing_fields='\n'.join(f'  - {f}' for f in missing),
        plugin_yaml=plugin_yaml_current,
        state_yaml=state_yaml_current,
    )
    full_prompt = f'{system_prompt}\n\n{patch_prompt}'

    raw_patch = _call_llm(full_prompt)
    try:
        patch = _extract_json(raw_patch)
    except ValueError as exc:
        logger.warning('Failed to parse patch response: %s', exc)
        return plugin_dict, state_dict

    if 'plugin' in patch and isinstance(patch['plugin'], dict):
        plugin_dict = _deep_merge(plugin_dict, patch['plugin'])
    if 'state' in patch and isinstance(patch['state'], dict):
        state_dict = _deep_merge(state_dict, patch['state'])

    return plugin_dict, state_dict


# ---------------------------------------------------------------------------
# PluginSpec validation (reuses plugin_loader._validate logic)
# ---------------------------------------------------------------------------


def _validate_with_pluginspec(plugin_yaml: str, state_yaml: str) -> Optional[str]:
    """Write YAML to a temp dir and construct a PluginSpec to run _validate().

    Returns None on success, or an error message string on failure.
    """
    try:
        from lazymind.chat.plugin.plugin_loader import PluginSpec  # noqa: PLC0415
    except ImportError:
        logger.warning('PluginSpec not available; skipping runtime validation')
        return None

    with tempfile.TemporaryDirectory() as tmpdir:
        plugin_dir = Path(tmpdir) / '_gen_plugin'
        scenario_dir = plugin_dir / 'scenario'
        scenario_dir.mkdir(parents=True)

        (plugin_dir / 'plugin.yaml').write_text(plugin_yaml, encoding='utf-8')
        (scenario_dir / 'state.yml').write_text(state_yaml, encoding='utf-8')
        # scenario.md is required by PluginSpec; provide a minimal placeholder
        (scenario_dir / 'scenario.md').write_text('# placeholder', encoding='utf-8')

        try:
            PluginSpec(plugin_id='_gen_plugin', plugin_dir=plugin_dir)
            return None
        except (ValueError, KeyError, FileNotFoundError) as exc:
            return str(exc)
        except Exception as exc:  # noqa: BLE001
            return f'Unexpected validation error: {exc}'


# ---------------------------------------------------------------------------
# Main generation logic
# ---------------------------------------------------------------------------


def _build_prompt(name: str, description: str, skill_content: str) -> Tuple[str, str]:
    spec = _PLUGIN_FORMAT_SPEC
    system = _SYSTEM_PROMPT_TEMPLATE.format(spec=spec)
    if skill_content.strip():
        user = _USER_PROMPT_SKILL.format(skill_content=skill_content)
    else:
        user = _USER_PROMPT_DESCRIPTION.format(description=description or name)
    user = f'Plugin name: {name}\n\n{user}'
    return system, user


def _generate_and_validate(
    system_prompt: str,
    user_prompt: str,
) -> Tuple[str, str, str, Dict[str, str]]:
    """Run the full generate → field-check → patch loop → PluginSpec validate.

    Returns (plugin_yaml, state_yaml, scenario_md, scripts).
    Raises ValueError if all retries fail.
    """
    full_prompt = f'{system_prompt}\n\n{user_prompt}'
    raw = _call_llm(full_prompt)

    plugin_yaml, state_yaml, scenario_md, scripts, plugin_dict, state_dict = _parse_initial_response(raw)

    # --- Layer 1: required field path check + patch retry loop ---
    for attempt in range(MAX_PATCH_RETRIES):
        missing = _check_missing_fields(plugin_dict, state_dict)
        if not missing:
            break
        logger.info(
            '[generate_plugin] attempt=%d missing fields: %s', attempt + 1, missing
        )
        plugin_dict, state_dict = _apply_patch(plugin_dict, state_dict, missing, system_prompt)
    else:
        # Final check after last retry
        missing = _check_missing_fields(plugin_dict, state_dict)
        if missing:
            raise ValueError(f'Missing required fields after {MAX_PATCH_RETRIES} patch retries: {missing}')

    # Rebuild YAML strings from (possibly patched) dicts
    plugin_yaml = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    state_yaml = yaml.dump(state_dict, allow_unicode=True, sort_keys=False)

    # --- Layer 2: PluginSpec runtime validation + patch retry ---
    for attempt in range(MAX_PATCH_RETRIES):
        error_msg = _validate_with_pluginspec(plugin_yaml, state_yaml)
        if error_msg is None:
            break
        logger.info(
            '[generate_plugin] PluginSpec validation error (attempt=%d): %s', attempt + 1, error_msg
        )
        # Build a pseudo missing-fields list from the error message so the same
        # patch mechanism can be reused.
        pseudo_missing = [f'PluginSpec validation error: {error_msg}']
        plugin_dict_patched, state_dict_patched = _apply_patch(
            plugin_dict, state_dict, pseudo_missing, system_prompt
        )
        plugin_yaml = yaml.dump(plugin_dict_patched, allow_unicode=True, sort_keys=False)
        state_yaml = yaml.dump(state_dict_patched, allow_unicode=True, sort_keys=False)
        plugin_dict, state_dict = plugin_dict_patched, state_dict_patched
    else:
        error_msg = _validate_with_pluginspec(plugin_yaml, state_yaml)
        if error_msg:
            raise ValueError(f'PluginSpec validation failed after {MAX_PATCH_RETRIES} retries: {error_msg}')

    return plugin_yaml, state_yaml, scenario_md, scripts


# ---------------------------------------------------------------------------
# Request / Response models
# ---------------------------------------------------------------------------

class GeneratePluginRequest(BaseModel):
    name: str = Field(..., description='Plugin display name')
    description: Optional[str] = Field(None, description='Natural-language description of the plugin goal')
    skill_content: Optional[str] = Field(None, description='Existing skill content to convert '
                                                           '(mutually exclusive with description)')
    llm_config: Dict[str, Any] = Field(default_factory=dict, description='Per-request model config from core')


class GeneratePluginResponse(BaseModel):
    plugin_yaml: str
    state_yaml: str
    scenario_md: str = ''
    scripts: Dict[str, str] = Field(default_factory=dict)


# ---------------------------------------------------------------------------
# Route handler
# ---------------------------------------------------------------------------

@router.post(
    '/api/chat/generate_plugin',
    response_model=GeneratePluginResponse,
    summary='Generate plugin.yaml, state.yml, scenario.md and optional scripts from a description or skill content',
)
async def generate_plugin(req: GeneratePluginRequest) -> GeneratePluginResponse:
    """Synchronously generate a LazyMind plugin from a natural-language description or an existing skill.

    Called by the Go asyncjob worker (plugin_draft_generate).
    Returns plugin_yaml, state_yaml, scenario_md, and scripts.
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

    try:
        plugin_yaml, state_yaml, scenario_md, scripts = _generate_and_validate(
            system_prompt, user_prompt
        )
    except ValueError as exc:
        logger.error('Plugin generation failed: %s', exc)
        raise HTTPException(status_code=500, detail=str(exc)) from exc
    except Exception as exc:
        logger.exception('Unexpected error during generate_plugin')
        raise HTTPException(status_code=500, detail=f'LLM call failed: {exc}') from exc

    return GeneratePluginResponse(
        plugin_yaml=plugin_yaml,
        state_yaml=state_yaml,
        scenario_md=scenario_md,
        scripts=scripts,
    )
