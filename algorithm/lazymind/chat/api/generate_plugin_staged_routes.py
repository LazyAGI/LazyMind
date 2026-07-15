"""Staged plugin generation API routes.

Three sequential endpoints for phased plugin generation:

  POST /api/chat/generate_plugin/skeleton
      Phase 1 — Generate plugin.yaml skeleton (metadata + slots + steps, no step prompts).
      Returns: plugin_yaml (skeleton), step_ids list.

  POST /api/chat/generate_plugin/state_machine
      Phase 2 — Generate full state.yml (transitions + steps[].prompt).
      Input: plugin_yaml from phase 1.
      Returns: state_yaml.

  POST /api/chat/generate_plugin/scenario_scripts
      Phase 3 — Generate scenario.md and optional scripts/*.py.
      Input: plugin_yaml + state_yaml from phases 1 and 2.
      Returns: scenario_md, scripts dict.
      Scripts are validated with SecurityVisitor (AST check) before being accepted.
"""
from __future__ import annotations

import ast
import copy
import hashlib
import importlib.util
import json
import logging
import sys
import tempfile
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field
import yaml

from lazymind.model_config import inject_model_config
from lazymind.chat.api.generate_plugin_routes import (
    _PLUGIN_FORMAT_SPEC,
    MAX_PATCH_RETRIES,
    _call_llm,
    _check_missing_fields,
    _validate_with_pluginspec,
    _extract_json,
)
from lazyllm.common.utils import SecurityVisitor

router = APIRouter()
logger = logging.getLogger(__name__)


def _tmpl(template: str, **kwargs: str) -> str:
    """Replace named placeholders ({__key__}) in a template string.

    Uses explicit sentinel-wrapped keys so that bare `{}` or `{json_example}`
    fragments inside the template are never mistaken for format placeholders.
    """
    result = template
    for key, value in kwargs.items():
        result = result.replace('{__' + key + '__}', value)
    return result


# ---------------------------------------------------------------------------
# Phase 0: Design Brief system prompt
# ---------------------------------------------------------------------------

_DESIGN_BRIEF_SYSTEM = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Your task is Phase 0: produce a concise design brief for a plugin.\n\n'
    'Output a Markdown document with EXACTLY these three sections:\n\n'
    '## Plugin Overview\n'
    '(One paragraph: what this plugin does and what problem it solves.)\n\n'
    '## Slots\n'
    '- `slot_id` (type, cardinality) — description; produced by which step, consumed by which steps\n'
    '  (type: text | image | file | json; cardinality: single | list)\n\n'
    '## Steps & Flow\n'
    '1. `step_id` — one-sentence responsibility\n'
    '   - Inputs: slot_id1, slot_id2 (material inputs only; omit this line when none)\n'
    '   - Outputs: slot_id3\n'
    '   - Next: → next_step_id (optional natural-language when hint)\n\n'
    'Rules:\n'
    '  - A slot is a durable material/artifact: either extra data the user must provide separately '
    '(an uploaded file, reference image, form field, dataset, etc.) or an output produced by a prior step.\n'
    '  - The user query, task description, intent, instructions, prompt text, and conversation context are NOT slots. '
    'Never create pseudo-slots such as user_query, search_query, request, topic, task_description, or instructions '
    'unless the product explicitly asks the user for that value as a separate editable/uploaded input.\n'
    '  - Every slot_id used in Steps must appear in the Slots section.\n'
    '  - Every non-external slot has exactly one producer; transformations create new slot IDs.\n'
    '  - Required inputs may use Boolean groups such as (A OR B) AND C.\n'
    '  - Use snake_case for all IDs.\n'
    '  - Keep it concise; this brief is injected as context into later phases.\n\n'
    'Return ONLY a JSON object: {{"design_brief": "<markdown string>"}}\n'
    'No explanation outside the JSON.'
)


_SKELETON_SYSTEM = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Your task is Phase 1 of a 3-phase plugin generation: produce a plugin.yaml SKELETON.\n\n'
    'The skeleton MUST include ALL of the following:\n'
    '  - id, name, description\n'
    '  - when_to_use — REQUIRED. Write in English. Use clear trigger conditions:\n'
    '    "ONLY call this tool when ... Do NOT trigger if ..."\n'
    '  - slots list (each slot: id, label, type, cardinality)\n'
    '    Mark genuine extra user/session-provided MATERIALS with external: true.\n'
    '    A material is either separately supplied data (file/image/form value/dataset) or a prior step output.\n'
    '    Do NOT model the user query, task description, intent, instructions, prompt text, '
    'or conversation context as slots.\n'
    '    Never invent query-like slots '
    '(user_query/search_query/request/topic/task_description/instructions) merely to pass '
    'the original request into a step; step prompts already operate with the task/conversation context.\n'
    '  - steps list (each step: id, label) — list of step IDs only, NO execution details\n'
    '  - ui block — REQUIRED. Must contain:\n'
    '      tabs: list of tab objects. Each tab must have id, label, layout, and slots.\n'
    '        Every tab MUST contain at least one slot; never emit `slots: []`.\n'
    '        Every user-visible slot MUST appear in exactly one tab; internal routing materials may be omitted.\n'
    '        Put user-provided inputs in an Input tab and generated/intermediate results in result tabs.\n'
    '        A syntactically valid but empty layout is invalid because the UI cannot display anything.\n'
    '      slots: map of slot_id → widget config, each with widgetType. Use these mappings:\n'
    '        text + single   → text-single\n'
    '        text + list     → text-list\n'
    '        image + single  → image-single\n'
    '        image + list    → image-gallery\n'
    '        file  + *       → file-card\n'
    '        json  + *       → json-block\n'
    '  - tool_scripts — ONLY include this section when the plugin genuinely needs custom Python\n'
    '    tools (external API calls, local data processing, complex computation).\n'
    '    DO NOT add tool_scripts for pure LLM reasoning/writing workflows.\n\n'
    'Do NOT include state machine logic or execution prompts — those come in Phase 2.\n\n'
    'Return ONLY a JSON object with the skeleton represented as a JSON object:\n'
    '  {{"plugin": {{"id": "...", "name": "...", "description": "..."}}}}\n'
    'Include every required field described above inside `plugin`. Do not return YAML and do not '
    'wrap the JSON in markdown fences.\n\n'
    'Follow the format specification below.\n\n'
    '=== Plugin Format Specification ===\n'
    '{__spec__}\n'
    '=== End of Specification ===\n\n'
    '{__design_brief_section__}'
)

_STATE_MACHINE_SYSTEM = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Your task is Phase 2 of a 3-phase plugin generation: produce a complete state.yml.\n\n'
    'The state.yml must include:\n'
    '  - initial: __start__\n'
    '  - transitions (dict): __start__ key holds the entry transitions list; other keys hold per-step transitions\n'
    '  - steps (dict, each step needs prompt; optionally inputs/'
    'outputs/tools/route/skip_if)\n\n'
    'MATERIAL RULES:\n'
    '  - Materials are durable artifacts only: genuine extra user-provided inputs or outputs of prior steps.\n'
    '  - The user query, task description, intent, instructions, prompt text, and conversation '
    'context are not materials '
    'and must not appear in inputs, outputs, or skip_if.\n'
    '  - Inputs use one ordered list: [{material: id, required: true|false, alternatives?: [{material: id}]}].\n'
    '  - alternatives is allowed only for required inputs; never emit bind_as.\n'
    '  - Outputs use [{material: id}] and are always required. Each non-external material has one producer.\n'
    '  - A producer must be a control ancestor of every consumer; never invent implicit control edges.\n'
    '  - A step cannot consume and produce the same material; transformations use a new ID.\n'
    'ROUTING RULES:\n'
    '  - Edge routing uses optional natural-language `when` hints evaluated by ChatAgent. '
    'Do not use material conditions on edges.\n'
    '  - skip_if is material-based and flat: one all(materials) or any(materials) group, with no nesting.\n'
    '  - Natural-language route hints make their targets candidates; they do not require an unconditional fallback.\n'
    '  - Keep the control graph acyclic; retry/rewind are runtime commands, not back edges.\n\n'
    'CRITICAL RULE — transitions.__start__ MUST always be present and non-empty.\n'
    'Example of the mandatory __start__ entry:\n'
    '  transitions:\n'
    '    __start__:\n'
    '      - to: <first_step_id>\n'
    '  Never omit transitions.__start__. It is required for the state machine to start.\n\n'
    'CRITICAL RULE — every step listed in plugin.yaml MUST have a transitions entry\n'
    '(even if it only leads to __end__).\n\n'
    'The plugin.yaml skeleton from Phase 1 is provided below as context.\n'
    'Your step IDs in state.yml MUST match the steps in plugin.yaml exactly.\n\n'
    'Return ONLY a JSON object:\n'
    '  {{"state_yaml": "<full state.yml content as YAML string>"}}\n\n'
    'Follow the format specification below.\n\n'
    '=== Plugin Format Specification ===\n'
    '{__spec__}\n'
    '=== End of Specification ===\n\n'
    '=== Plugin Skeleton (plugin.yaml from Phase 1) ===\n'
    '{__plugin_yaml__}\n'
    '=== End of Skeleton ===\n\n'
    '{__design_brief_section__}'
)

_SCENARIO_SCRIPTS_SYSTEM = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Your task is Phase 3 of a 3-phase plugin generation:\n'
    '  1. Write scenario.md — a structured guide for the plugin editor.\n'
    '  2. Optionally write Python scripts ONLY when strictly necessary (see rules below).\n\n'
    '=== scenario.md FORMAT (STRICTLY REQUIRED) ===\n'
    'The scenario.md MUST follow this exact Markdown structure (write in Chinese):\n\n'
    '## 场景描述\n\n'
    '<One or two paragraphs describing what this plugin does and when to use it.>\n\n'
    '## 工作流程\n\n'
    '### {step_id}（{step_label}）\n\n'
    '<One or two sentences describing what this step does.>\n\n'
    '(repeat for every step in the same order as steps[] in plugin.yaml)\n\n'
    '## 注意事项\n\n'
    '<Optional usage tips, constraints, or warnings. Omit this section if nothing to add.>\n\n'
    'RULES:\n'
    '  - Use the EXACT step ids and labels from plugin.yaml steps[].\n'
    '  - Every step MUST have a non-empty description. Do NOT write "（暂无描述）".\n'
    '  - Write all content in Chinese.\n'
    '  - Do NOT add extra top-level sections or change the section names.\n'
    '=== End of scenario.md FORMAT ===\n\n'
    '=== When to Write Scripts ===\n'
    'WRITE scripts only when the plugin needs capabilities the LLM alone cannot perform:\n'
    '  - Calling a specific external HTTP API (use httpx, not requests)\n'
    '  - Local data processing (image manipulation, PDF parsing, format conversion, etc.)\n'
    '  - Complex deterministic computation (math, parsing, encoding/decoding)\n\n'
    'DO NOT write scripts when:\n'
    '  - Steps only need save_artifact (always auto-injected, never requires a script)\n'
    '  - The workflow is pure LLM reasoning, writing, analysis, or summarization\n'
    '  - A web search could work without calling a specific private API\n'
    '  - The plugin is a text/content generation pipeline\n\n'
    'Default: DO NOT write scripts unless there is a clear, concrete need.\n'
    'When in doubt, set scripts to {{}}.\n\n'
    '=== Script Safety Rules (STRICTLY ENFORCED) ===\n'
    'Generated scripts are validated with an AST security checker. '
    'Violations will cause the generation to fail.\n\n'
    'FORBIDDEN — do not use any of the following:\n'
    '  - Built-ins: exec, eval, open, compile, getattr, setattr, __import__, globals, locals, vars\n'
    '  - Modules: pickle, subprocess, socket, shutil, requests, inspect, tempfile\n'
    '  - os calls: os.system, os.popen, os.remove, os.rmdir, os.unlink, os.rename, os.environ\n'
    '  - sys calls: sys.exit, sys.modules\n\n'
    'ALLOWED for HTTP requests: use httpx instead of requests\n'
    'ALLOWED standard library: json, re, math, base64, hashlib, urllib.parse, datetime, typing\n'
    '=== End of Rules ===\n\n'
    '=== Output Format ===\n'
    'Return ONLY a JSON object with exactly these two keys:\n'
    '  {"scenario_md": "<scenario.md content as Markdown string>",\n'
    '    "scripts": {"scripts/tools.py": "<python code>"}}\n'
    '- scripts: set to {} when no custom tool scripts are needed.\n'
    '- If you do write a script, only implement functions declared in '
    'plugin.yaml tool_scripts[].functions.\n'
    '- Do NOT wrap the JSON in markdown code fences. Output raw JSON only.\n\n'
    '{__design_brief_section__}'
    '=== plugin.yaml ===\n'
    '{__plugin_yaml__}\n'
    '=== state.yml ===\n'
    '{__state_yaml__}\n'
    '=== End of Context ==='
)

# ---------------------------------------------------------------------------
# Required field sets per phase
# ---------------------------------------------------------------------------

_REQUIRED_SKELETON_PLUGIN_TOP = ['id', 'name', 'description', 'when_to_use', 'steps', 'slots']
_REQUIRED_SKELETON_SLOT_FIELDS = ['id', 'label', 'type', 'cardinality']
_REQUIRED_SKELETON_STEP_FIELDS = ['id', 'label']

# widgetType mappings expected by the frontend
_WIDGET_TYPE_DEFAULTS: Dict[str, str] = {
    ('text', 'single'): 'text-single',
    ('text', 'list'): 'text-list',
    ('image', 'single'): 'image-single',
    ('image', 'list'): 'image-gallery',
    ('file', 'single'): 'file-card',
    ('file', 'list'): 'file-card',
    ('json', 'single'): 'json-block',
    ('json', 'list'): 'json-block',
}


def _check_skeleton_missing(plugin_dict: Dict[str, Any]) -> List[str]:
    missing: List[str] = []
    for field in _REQUIRED_SKELETON_PLUGIN_TOP:
        val = plugin_dict.get(field)
        if val is None or val == '' or val == [] or val == {}:
            missing.append(f'plugin.{field}')
    slots = plugin_dict.get('slots')
    if isinstance(slots, list):
        for i, slot in enumerate(slots):
            if not isinstance(slot, dict):
                missing.append(f'plugin.slots[{i}] (must be a dict)')
                continue
            for f in _REQUIRED_SKELETON_SLOT_FIELDS:
                if not slot.get(f):
                    missing.append(f'plugin.slots[{i}].{f}')
    steps = plugin_dict.get('steps')
    if isinstance(steps, list):
        for i, step in enumerate(steps):
            if not isinstance(step, dict):
                missing.append(f'plugin.steps[{i}] (must be a dict)')
                continue
            for f in _REQUIRED_SKELETON_STEP_FIELDS:
                if not step.get(f):
                    missing.append(f'plugin.steps[{i}].{f}')
    # Blocking: ui.tabs must be present for the frontend to render the layout
    ui = plugin_dict.get('ui')
    if not isinstance(ui, dict) or not ui.get('tabs'):
        missing.append('plugin.ui.tabs (ui tabs block missing, frontend cannot render layout)')
    elif isinstance(ui.get('tabs'), list):
        placed: set[str] = set()
        for i, tab in enumerate(ui['tabs']):
            if not isinstance(tab, dict):
                missing.append(f'plugin.ui.tabs[{i}] (must be a dict)')
                continue
            tab_slots = tab.get('slots')
            if not isinstance(tab_slots, list) or not tab_slots:
                missing.append(f'plugin.ui.tabs[{i}].slots (must contain at least one declared slot)')
                continue
            for raw_ref in tab_slots:
                slot_id = raw_ref.get('id') if isinstance(raw_ref, dict) else raw_ref
                if slot_id:
                    placed.add(str(slot_id))
        declared = {
            str(slot.get('id')) for slot in (slots or [])
            if isinstance(slot, dict) and slot.get('id') and slot.get('exposed') is True
        }
        for slot_id in sorted(declared - placed):
            missing.append(f'plugin.ui.tabs slot coverage (declared slot {slot_id!r} is not placed in any tab)')
    return missing


_SKELETON_PATCH_TEMPLATE = (
    'The generated plugin.yaml skeleton has missing fields:\n'
    '{__missing_fields__}\n\n'
    'Current skeleton:\n{__plugin_yaml__}\n\n'
    'Return ONLY a JSON patch: {"plugin": {...}}\n'
    'Fix only the missing fields. No explanation.'
)

_SKELETON_YAML_REPAIR_TEMPLATE = (
    'The plugin skeleton below was returned as YAML, but it is invalid and could not be parsed.\n'
    'Parse its intended structure and return the complete skeleton as a JSON object.\n'
    'Preserve all values and quote-sensitive text exactly; do not add or remove workflow behavior.\n\n'
    'YAML parser error:\n{__yaml_error__}\n\n'
    'Invalid skeleton:\n{__plugin_yaml__}\n\n'
    'Return ONLY: {"plugin": {<complete plugin skeleton>}}'
)

_STATE_MACHINE_PATCH_TEMPLATE = (
    'The generated state.yml has missing or invalid fields:\n'
    '{__missing_fields__}\n\n'
    'Current state.yml:\n{__state_yaml__}\n\n'
    'plugin.yaml (for reference):\n{__plugin_yaml__}\n\n'
    'Return the COMPLETE FIXED state.yml (not a partial patch) as:\n'
    '{"state_yaml": "<complete corrected state.yml as YAML string>"}\n'
    'Fix ALL listed issues. Ensure transitions.__start__ is present with a valid "to" field.\n'
    'No explanation.'
)


def _patch_skeleton(
    plugin_dict: Dict[str, Any],
    missing: List[str],
    system_prompt: str,
) -> Dict[str, Any]:
    plugin_yaml_str = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    patch_prompt = _tmpl(
        _SKELETON_PATCH_TEMPLATE,
        missing_fields='\n'.join(f'  - {f}' for f in missing),
        plugin_yaml=plugin_yaml_str,
    )
    raw = _call_llm(f'{system_prompt}\n\n{patch_prompt}')
    try:
        patch = _extract_json(raw)
    except ValueError as exc:
        logger.warning('[staged] skeleton patch parse failed: %s', exc)
        return plugin_dict
    if 'plugin' in patch and isinstance(patch['plugin'], dict):
        from lazymind.chat.api.generate_plugin_routes import _deep_merge  # noqa: PLC0415
        plugin_dict = _deep_merge(plugin_dict, patch['plugin'])
    return plugin_dict


def _plugin_dict_from_skeleton_response(
    data: Dict[str, Any],
    system_prompt: str,
) -> Dict[str, Any]:
    """Accept the object contract and repair legacy invalid embedded YAML once."""
    plugin = data.get('plugin')
    if isinstance(plugin, dict):
        return plugin

    plugin_yaml = data.get('plugin_yaml', '')
    if not isinstance(plugin_yaml, str) or not plugin_yaml.strip():
        raise HTTPException(status_code=500, detail='Phase 1: missing plugin object in response')
    try:
        parsed = yaml.safe_load(plugin_yaml) or {}
    except yaml.YAMLError as exc:
        repair_prompt = _tmpl(
            _SKELETON_YAML_REPAIR_TEMPLATE,
            yaml_error=str(exc),
            plugin_yaml=plugin_yaml,
        )
        raw = _call_llm(f'{system_prompt}\n\n{repair_prompt}')
        try:
            repaired = _extract_json(raw)
        except ValueError as repair_exc:
            raise HTTPException(
                status_code=500,
                detail=f'Phase 1 skeleton repair JSON parse error: {repair_exc}',
            ) from repair_exc
        parsed = repaired.get('plugin')
        if not isinstance(parsed, dict):
            raise HTTPException(
                status_code=500,
                detail=f'Phase 1 YAML parse error: {exc}; repair response missing plugin object',
            ) from exc
    if not isinstance(parsed, dict):
        raise HTTPException(status_code=500, detail='Phase 1: plugin skeleton must be an object')
    return parsed


def _patch_state_machine(
    plugin_dict: Dict[str, Any],
    state_dict: Dict[str, Any],
    missing: List[str],
    system_prompt: str,
) -> Dict[str, Any]:
    plugin_yaml_str = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    state_yaml_str = yaml.dump(state_dict, allow_unicode=True, sort_keys=False)
    patch_prompt = _tmpl(
        _STATE_MACHINE_PATCH_TEMPLATE,
        missing_fields='\n'.join(f'  - {f}' for f in missing),
        state_yaml=state_yaml_str,
        plugin_yaml=plugin_yaml_str,
    )
    raw = _call_llm(f'{system_prompt}\n\n{patch_prompt}')
    try:
        patch = _extract_json(raw)
    except ValueError as exc:
        logger.warning('[staged] state patch parse failed: %s', exc)
        return state_dict
    # Prefer full replacement via state_yaml (avoids deep_merge issues with nested lists).
    if 'state_yaml' in patch:
        try:
            fixed = yaml.safe_load(patch['state_yaml']) or {}
            if isinstance(fixed, dict) and fixed:
                return fixed
        except yaml.YAMLError as exc:
            logger.warning('[staged] state patch YAML parse failed: %s', exc)
    # Fallback: legacy patch dict merge (kept for backward compat)
    if 'state' in patch and isinstance(patch['state'], dict):
        from lazymind.chat.api.generate_plugin_routes import _deep_merge  # noqa: PLC0415
        state_dict = _deep_merge(state_dict, patch['state'])
    return state_dict


# ---------------------------------------------------------------------------
# Script safety validation — function-level AST check + dry-run import
# ---------------------------------------------------------------------------

# Mapping from AST node type to a human-readable label for fix prompts.
_NODE_LABEL: Dict[type, str] = {
    ast.FunctionDef: 'function',
    ast.AsyncFunctionDef: 'async function',
    ast.Import: 'import statement',
    ast.ImportFrom: 'import statement',
    ast.Assign: 'module-level statement',
    ast.Expr: 'module-level statement',
    ast.ClassDef: 'class definition',
}


def _node_source(node: ast.AST, source: str) -> str:
    """Extract the source text of a single AST node."""
    try:
        seg = ast.get_source_segment(source, node)
        if seg:
            return seg
    except Exception:
        pass
    return ast.unparse(node)


def _node_name(node: ast.AST) -> str:
    """Return a display name for an AST node (function name, imported module, etc.)."""
    if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
        return node.name
    if isinstance(node, ast.Import):
        return ', '.join(a.name for a in node.names)
    if isinstance(node, ast.ImportFrom):
        return f'{node.module or "?"}'
    return ast.unparse(node)[:60]


def _check_node_security(node: ast.AST) -> Optional[str]:
    """Run SecurityVisitor on a single AST node subtree.

    Returns an error string if the node violates security rules, None otherwise.
    """
    try:
        SecurityVisitor().visit(node)
        return None
    except ValueError as exc:
        return str(exc)


def _scan_file(source: str) -> List[Tuple[ast.AST, str]]:
    """Parse source and return a list of (node, error) for every top-level node that fails.

    Checks each top-level statement individually so callers know exactly which node
    is at fault — even if multiple nodes in the same file are problematic.
    """
    tree = ast.parse(source)  # SyntaxError propagates to caller
    violations: List[Tuple[ast.AST, str]] = []
    for node in ast.iter_child_nodes(tree):
        err = _check_node_security(node)
        if err:
            violations.append((node, err))
    return violations


def _dry_run_import(filename: str, source: str) -> Optional[str]:
    """Import the script in a temporary directory without calling any functions.

    Returns None on success, or an error string on failure.
    This catches missing imports, top-level runtime errors, and NameErrors.
    """
    with tempfile.TemporaryDirectory(prefix='lazymind_plugin_dryrun_') as tmpdir:
        script_path = Path(tmpdir) / Path(filename).name
        script_path.write_text(source, encoding='utf-8')
        module_name = f'_plugin_dryrun_{script_path.stem}'
        spec = importlib.util.spec_from_file_location(module_name, script_path)
        if spec is None or spec.loader is None:
            return f'Cannot create module spec for {filename}'
        module = importlib.util.module_from_spec(spec)
        sys.modules[module_name] = module
        try:
            spec.loader.exec_module(module)  # type: ignore[union-attr]
            return None
        except Exception as exc:
            return f'dry-run import error: {exc}'
        finally:
            sys.modules.pop(module_name, None)


_NODE_FIX_TEMPLATE = (
    'A Python script has a security or runtime problem in one of its top-level nodes.\n\n'
    'File: {__filename__}\n'
    'Problem node ({__node_label__} "{__node_name__}"):\n'
    '```python\n{__node_source__}\n```\n'
    'Error: {__error__}\n\n'
    'Rules:\n'
    '- Slots are durable materials only: separately supplied user data or prior-step outputs. Never add a slot for '
    'the user query, task description, intent, instructions, prompt text, or conversation context.\n'
    '- If an existing query-like pseudo-slot has no real producer, remove its plugin.yaml slot definition and all '
    'state.yml material references; do NOT "fix" it by marking it external.\n'
    '  Allowed HTTP library: httpx (not requests).\n'
    '  Allowed stdlib: json, re, math, base64, hashlib, urllib.parse, datetime, typing, httpx.\n'
    '  Forbidden: exec, eval, open, compile, getattr, setattr, __import__, globals, locals, vars,\n'
    '             pickle, subprocess, socket, shutil, requests, inspect, tempfile,\n'
    '             os.system, os.popen, os.remove, os.rmdir, os.unlink, os.rename, os.environ,\n'
    '             sys.exit, sys.modules.\n\n'
    'Full current script for context:\n'
    '```python\n{__full_source__}\n```\n\n'
    'Return ONLY a JSON object with the corrected FULL script (do not omit other functions):\n'
    '{"fixed_source": "<complete corrected Python source>"}\n'
    'No explanation.'
)

_DRY_RUN_FIX_TEMPLATE = (
    'A Python script fails to import (dry-run import error).\n\n'
    'File: {__filename__}\n'
    'Error: {__error__}\n\n'
    'Full current script:\n'
    '```python\n{__full_source__}\n```\n\n'
    'Fix the script so it can be imported without errors.\n'
    'Return ONLY a JSON object with the corrected FULL script:\n'
    '{"fixed_source": "<complete corrected Python source>"}\n'
    'No explanation.'
)


def _ask_fix(system_prompt: str, fix_prompt: str) -> Optional[str]:
    """Call the LLM with a fix prompt and extract the fixed_source field."""
    raw = _call_llm(f'{system_prompt}\n\n{fix_prompt}')
    try:
        data = _extract_json(raw)
        return data.get('fixed_source') or None
    except ValueError:
        return None


# ---------------------------------------------------------------------------
# Design brief section helper
# ---------------------------------------------------------------------------

def _design_brief_section(design_brief: Optional[str]) -> str:
    """Return a formatted design brief block for injection into system prompts.

    Returns an empty string when no brief is available (graceful fallback for
    old drafts or Phase 0 failures).
    """
    if not design_brief or not design_brief.strip():
        return ''
    return (
        '=== Design Brief (authoritative reference) ===\n'
        f'{design_brief.strip()}\n'
        '=== End of Design Brief ===\n'
        'The slots[], steps[], and step inputs/outputs MUST exactly match this brief.\n\n'
    )


# ---------------------------------------------------------------------------
# Slot reference validation & repair
# ---------------------------------------------------------------------------

def _validate_slot_references(
    plugin_dict: Dict[str, Any],
    state_dict: Dict[str, Any],
) -> List[str]:
    """Check that every slot id referenced in state.yml is defined in plugin.yaml.

    Returns a list of error strings, empty when all references are valid.
    """
    defined_slots: set = {
        s.get('id')
        for s in (plugin_dict.get('slots') or [])
        if isinstance(s, dict) and s.get('id')
    }
    errors: List[str] = []

    def expression_refs(value: Any) -> List[str]:
        if not isinstance(value, dict):
            return []
        refs: List[str] = []
        material = value.get('material')
        if isinstance(material, str) and material:
            refs.append(material)
        for key in ('all', 'any'):
            children = value.get(key)
            if isinstance(children, list):
                for child in children:
                    refs.extend(expression_refs(child))
        return refs

    def material_ref(value: Any) -> Optional[str]:
        if isinstance(value, str):
            return value
        if isinstance(value, dict):
            raw = value.get('material') or value.get('slot') or value.get('id')
            return str(raw) if raw else None
        return None

    steps = state_dict.get('steps') or {}
    if not isinstance(steps, dict):
        return errors
    for step_id, step in steps.items():
        if not isinstance(step, dict):
            continue
        refs_by_path: Dict[str, List[str]] = {
            'skip_if': expression_refs(step.get('skip_if')),
        }
        for direction in ('inputs', 'outputs'):
            raw_refs = step.get(direction) or []
            refs_by_path[direction] = []
            if isinstance(raw_refs, list):
                for ref in raw_refs:
                    ref_id = material_ref(ref)
                    if ref_id:
                        refs_by_path[direction].append(ref_id)
                    if direction == 'inputs' and isinstance(ref, dict):
                        alternatives = ref.get('alternatives') or []
                        if isinstance(alternatives, list):
                            refs_by_path[direction].extend(
                                alternative_id
                                for alternative_id in (material_ref(value) for value in alternatives)
                                if alternative_id
                            )
        for path, refs in refs_by_path.items():
            for ref_id in refs:
                if ref_id not in defined_slots:
                    errors.append(
                        f"step '{step_id}' references undefined slot '{ref_id}' in {path}"
                    )
    return errors


_SLOT_REPAIR_SYSTEM = (
    'You are a plugin schema doctor. You receive a plugin.yaml (slots definition) and a '
    'state.yml (inputs/outputs/route/skip expressions) that have mismatched slot IDs.\n\n'
    'Your task: fix the mismatch. You may EITHER:\n'
    '  A) Add missing slot definitions to plugin.yaml slots[] when the state.yml references '
    'are semantically correct but the slot was simply never declared, OR\n'
    '  B) Fix slot references in state.yml steps inputs/outputs to use the IDs already '
    'declared in plugin.yaml slots[], when the state.yml used wrong / inconsistent IDs.\n'
    '  C) Do both, when appropriate.\n\n'
    'Rules:\n'
    '- Prefer renaming state.yml references to match existing plugin.yaml slot IDs whenever '
    'a clear semantic match exists (same concept, different name).\n'
    '- Only add new slot entries to plugin.yaml when there is genuinely new data being '
    'produced with no equivalent in the existing slots.\n'
    '- Do NOT remove any existing slot from plugin.yaml.\n'
    '- Do NOT change step names, transition targets, or prompts; only material references may change.\n'
    '- Preserve the single-producer rule. Never make two steps produce the same material, '
    'and never let a step consume and produce the same material.\n'
    '- Use canonical {material: id} references; do not introduce legacy {slot: id} inputs. '
    'Route `when` values are natural-language hints and must not be changed by slot repair.\n'
    '- Return ONLY valid JSON: {"plugin_yaml": "...", "state_yaml": "..."}\n'
    '- No markdown fences, no explanation outside the JSON.'
)


def _repair_slots_only(
    plugin_dict: Dict[str, Any],
    state_dict: Dict[str, Any],
    errors: List[str],
    llm_config: Dict[str, Any],
) -> tuple[Dict[str, Any], Dict[str, Any]]:
    """Use LLM to fix slot-reference mismatches between plugin.yaml and state.yml.

    The LLM decides whether to add missing slots to plugin.yaml, rename wrong
    references in state.yml, or both.  Returns (fixed_plugin_dict, fixed_state_dict).
    Falls back to the originals if the LLM response cannot be parsed.
    """
    inject_model_config(llm_config or {})

    plugin_yaml_str = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    state_yaml_str = yaml.dump(state_dict, allow_unicode=True, sort_keys=False)

    user_prompt = (
        'Slot reference errors found:\n'
        + '\n'.join(f'  - {e}' for e in errors)
        + f'\n\nCurrent plugin.yaml:\n{plugin_yaml_str}\n\n'
        f'Current state.yml:\n{state_yaml_str}\n\n'
        'Fix the slot mismatches and return the corrected plugin.yaml and state.yml as JSON.'
    )

    raw = _call_llm(f'{_SLOT_REPAIR_SYSTEM}\n\n{user_prompt}')
    try:
        data = _extract_json(raw)
    except ValueError as exc:
        logger.warning('[staged/slot_repair] LLM parse failed (%s), falling back to originals', exc)
        return plugin_dict, state_dict

    fixed_plugin_dict = plugin_dict
    fixed_state_dict = state_dict

    if new_plugin_yaml := data.get('plugin_yaml', ''):
        try:
            parsed = yaml.safe_load(new_plugin_yaml)
            if isinstance(parsed, dict):
                fixed_plugin_dict = parsed
        except yaml.YAMLError as exc:
            logger.warning('[staged/slot_repair] plugin_yaml parse failed: %s', exc)

    if new_state_yaml := data.get('state_yaml', ''):
        try:
            parsed = yaml.safe_load(new_state_yaml)
            if isinstance(parsed, dict):
                fixed_state_dict = parsed
        except yaml.YAMLError as exc:
            logger.warning('[staged/slot_repair] state_yaml parse failed: %s', exc)

    remaining = _validate_slot_references(fixed_plugin_dict, fixed_state_dict)
    logger.info(
        '[staged/slot_repair] after LLM repair: %d remaining errors (was %d)',
        len(remaining), len(errors),
    )
    return fixed_plugin_dict, fixed_state_dict


# ---------------------------------------------------------------------------
# Request / Response models
# ---------------------------------------------------------------------------

class _LLMConfigMixin(BaseModel):
    llm_config: Dict[str, Any] = Field(default_factory=dict)


class AnalyzeSkillRequest(_LLMConfigMixin):
    name: str
    skill_package: Dict[str, Any]


class AnalyzeSkillResponse(BaseModel):
    verdict: str
    verdict_code: str = ''
    message: str = ''
    candidates: List[Dict[str, Any]] = Field(default_factory=list)
    coverage: Dict[str, Any] = Field(default_factory=dict)
    tool_mappings: Dict[str, Any] = Field(default_factory=dict)
    scripts: Dict[str, Any] = Field(default_factory=dict)


class DesignBriefRequest(_LLMConfigMixin):
    name: str
    description: Optional[str] = None
    skill_content: Optional[str] = None
    skill_package: Optional[Dict[str, Any]] = None
    workflow_analysis: Optional[str] = None


class DesignBriefResponse(BaseModel):
    design_brief: str


class SkeletonRequest(_LLMConfigMixin):
    name: str
    description: Optional[str] = None
    skill_content: Optional[str] = None
    skill_package: Optional[Dict[str, Any]] = None
    workflow_analysis: Optional[str] = None
    design_brief: Optional[str] = None


class SkeletonResponse(BaseModel):
    plugin_yaml: str


class StateMachineRequest(_LLMConfigMixin):
    name: str
    plugin_yaml: str  # output from Phase 1
    design_brief: Optional[str] = None
    workflow_analysis: Optional[str] = None


class StateMachineResponse(BaseModel):
    state_yaml: str
    plugin_yaml: str = ''  # updated when slot repair was applied
    warnings: List[str] = []


class ScenarioScriptsRequest(_LLMConfigMixin):
    name: str
    plugin_yaml: str   # output from Phase 1
    state_yaml: str    # output from Phase 2
    design_brief: Optional[str] = None
    source_scripts: Dict[str, str] = Field(default_factory=dict)


class ScenarioScriptsResponse(BaseModel):
    scenario_md: str = ''
    scripts: Dict[str, str] = Field(default_factory=dict)
    warnings: List[str] = Field(default_factory=list)


# ---------------------------------------------------------------------------
# Phase handlers
# ---------------------------------------------------------------------------

def _skill_package_prompt(package: Optional[Dict[str, Any]], fallback: str) -> str:
    """Render a bounded, path-preserving package view; never hide omitted files."""
    if not package:
        return fallback
    files = package.get('files') or []
    header = (
        f"Skill revision: {package.get('revision_id', '')}\n"
        f"Tree hash: {package.get('tree_hash', '')}\n"
        f'Files in manifest: {len(files)}\n'
    )
    parts: List[str] = [header]
    omitted: List[str] = []
    budget = 120_000
    used = len(header)
    for item in files:
        path = str(item.get('path') or '')
        if item.get('binary'):
            parts.append(f'\n=== {path} (binary metadata only, {item.get("size", 0)} bytes) ===\n')
            continue
        content = str(item.get('content') or '')
        block = f'\n=== FILE: {path} ===\n{content}\n=== END FILE ===\n'
        if used + len(block) > budget:
            omitted.append(path)
            continue
        parts.append(block)
        used += len(block)
    if omitted:
        parts.append(
            '\n=== UNRESOLVED FILES (context budget; generation must not claim full coverage) ===\n'
            + '\n'.join(omitted)
        )
    return ''.join(parts)


def _script_inventory(package: Dict[str, Any]) -> Dict[str, Any]:
    report: Dict[str, Any] = {}
    for item in package.get('files') or []:
        path = str(item.get('path') or '')
        if not path.endswith('.py') or item.get('binary'):
            continue
        source = str(item.get('content') or '')
        try:
            tree = ast.parse(source)
        except SyntaxError as exc:
            report[path] = {
                'classification': 'unsupported',
                'functions': [],
                'reason': f'SyntaxError: {exc}',
                'sha256': hashlib.sha256(source.encode()).hexdigest(),
            }
            continue
        functions = [
            n.name for n in tree.body
            if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))
        ]
        violations = _scan_file(source)
        if violations:
            report[path] = {
                'classification': 'unsupported',
                'functions': functions,
                'reason': '; '.join(error for _, error in violations),
                'sha256': hashlib.sha256(source.encode()).hexdigest(),
            }
            continue
        has_main = 'main' in functions or any(
            isinstance(n, ast.If) and isinstance(n.test, ast.Compare)
            for n in tree.body
        )
        classification = 'wrappable_command' if has_main else ('importable_tool' if functions else 'supporting_script')
        transformed, wrapper = _wrap_command_source(path, source) if has_main else (source, '')
        exported = list(functions)
        if wrapper:
            exported.append(wrapper)
        report[path] = {
            'classification': classification,
            'functions': exported,
            'wrapper_function': wrapper,
            'reason': '',
            'sha256': hashlib.sha256(transformed.encode()).hexdigest(),
        }
    return report


def _wrap_command_source(filename: str, source: str) -> Tuple[str, str]:
    """Append an import-safe explicit-signature wrapper around main without executing it."""
    try:
        tree = ast.parse(source)
    except SyntaxError:
        return source, ''
    main = next(
        (node for node in tree.body
         if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == 'main'),
        None,
    )
    if main is None or main.args.vararg is not None or main.args.kwarg is not None:
        return source, ''
    stem = ''.join(ch if ch.isalnum() else '_' for ch in Path(filename).stem).strip('_') or 'script'
    wrapper_name = f'run_{stem}'
    positional = [*main.args.posonlyargs, *main.args.args]
    call = ast.Call(
        func=ast.Name(id='main', ctx=ast.Load()),
        args=[ast.Name(id=arg.arg, ctx=ast.Load()) for arg in positional],
        keywords=[
            ast.keyword(arg=arg.arg, value=ast.Name(id=arg.arg, ctx=ast.Load()))
            for arg in main.args.kwonlyargs
        ],
    )
    value: ast.expr = ast.Await(value=call) if isinstance(main, ast.AsyncFunctionDef) else call
    wrapper_cls = ast.AsyncFunctionDef if isinstance(main, ast.AsyncFunctionDef) else ast.FunctionDef
    wrapper = wrapper_cls(
        name=wrapper_name,
        args=copy.deepcopy(main.args),
        body=[ast.Return(value=value)],
        decorator_list=[],
        returns=copy.deepcopy(main.returns),
        type_comment=None,
    )
    ast.fix_missing_locations(wrapper)
    return source.rstrip() + '\n\n' + ast.unparse(wrapper) + '\n', wrapper_name


def _hierarchical_evidence(package: Dict[str, Any]) -> Tuple[str, List[str]]:
    """Extract workflow evidence in bounded batches; return unresolved paths explicitly."""
    chunks: List[Tuple[str, str]] = []
    for item in package.get('files') or []:
        path = str(item.get('path') or '')
        if item.get('binary'):
            continue
        content = str(item.get('content') or '')
        if path.endswith('.py'):
            try:
                tree = ast.parse(content)
                content = ast.dump(tree, annotate_fields=True, include_attributes=False)[:24_000]
            except SyntaxError:
                content = content[:24_000]
        for offset in range(0, len(content) or 1, 24_000):
            chunks.append((path, content[offset:offset + 24_000]))
    batches: List[List[Tuple[str, str]]] = []
    current: List[Tuple[str, str]] = []
    size = 0
    for chunk in chunks:
        if current and size + len(chunk[1]) > 90_000:
            batches.append(current)
            current = []
            size = 0
        current.append(chunk)
        size += len(chunk[1])
    if current:
        batches.append(current)
    summaries: List[str] = []
    unresolved = sorted({path for batch in batches[8:] for path, _ in batch})
    for batch in batches[:8]:
        material = '\n'.join(f'=== {path} ===\n{text}' for path, text in batch)
        raw = _call_llm(
            'Extract workflow evidence from these versioned skill chunks. A SKILL.md normally uses '
            'natural-language guidance rather than a manifest, program, or formal state machine. '
            'Capture both explicit actions and actions, ordering, inputs, outputs, branches, or '
            'completion criteria that are directly and reliably implied by the text. You may '
            'normalize those implications into workflow terms, but do not add behavior unsupported '
            'by the source. Return compact raw JSON with paths, goals, ordered actions, branches, '
            'inputs, outputs, constraints, tools and script roles.\n' + material
        )
        try:
            summaries.append(yaml.safe_dump(_extract_json(raw), allow_unicode=True, sort_keys=False))
        except ValueError:
            unresolved.extend(path for path, _ in batch)
    return '\n'.join(summaries), sorted(set(unresolved))


def _replacement_mappings(workflow_analysis: Optional[str]) -> List[Tuple[str, str, str]]:
    try:
        context = __import__('json').loads(workflow_analysis or '{}')
    except (TypeError, ValueError):
        return []
    raw = context.get('tool_mappings') or {}
    result: List[Tuple[str, str, str]] = []
    if isinstance(raw, dict):
        for key, value in raw.items():
            if not isinstance(value, dict):
                continue
            action = str(value.get('action') or value.get('status') or '')
            replacement = str(value.get('replacement') or value.get('framework_tool') or '')
            if action not in {'replace', 'replaced', 'framework_replaced'} or not replacement:
                continue
            result.append((
                str(value.get('source_tool') or key),
                replacement,
                str(value.get('source_script') or ''),
            ))
    return result


def _apply_tool_replacements(
    plugin: Dict[str, Any],
    state: Optional[Dict[str, Any]],
    workflow_analysis: Optional[str],
) -> None:
    mappings = _replacement_mappings(workflow_analysis)
    if mappings:
        plugin['required_framework_tools'] = sorted({replacement for _, replacement, _ in mappings})
    skipped_paths = {path for _, _, path in mappings if path}
    ignored_functions: set[str] = set()
    try:
        context = __import__('json').loads(workflow_analysis or '{}')
        for path, report in (context.get('scripts') or {}).items():
            if isinstance(report, dict) and report.get('classification') == 'unsupported':
                skipped_paths.add(str(path))
                ignored_functions.update(str(name) for name in (report.get('functions') or []))
    except (TypeError, ValueError):
        pass
    if skipped_paths and isinstance(plugin.get('tool_scripts'), list):
        plugin['tool_scripts'] = [
            entry for entry in plugin['tool_scripts']
            if not isinstance(entry, dict) or str(entry.get('path') or '') not in skipped_paths
        ]
    if state is None:
        return
    replacements = {source: target for source, target, _ in mappings}
    for config in (state.get('steps') or {}).values():
        if isinstance(config, dict) and isinstance(config.get('tools'), list):
            config['tools'] = list(dict.fromkeys(
                replacements.get(str(tool), str(tool)) for tool in config['tools']
                if str(tool) not in ignored_functions
            ))


@router.post('/api/chat/generate_plugin/analyze_skill', response_model=AnalyzeSkillResponse)
async def analyze_skill(req: AnalyzeSkillRequest) -> AnalyzeSkillResponse:
    inject_model_config(req.llm_config or {})
    evidence, deterministically_unresolved = _hierarchical_evidence(req.skill_package)
    package_prompt = _skill_package_prompt({**req.skill_package, 'files': [
        {**f, 'content': ''} for f in req.skill_package.get('files', [])
    ]}, '') + '\n=== HIERARCHICAL EVIDENCE ===\n' + evidence
    script_inventory = _script_inventory(req.skill_package)
    from lazymind.chat.service.component.tool_registry import get_all_tool_groups
    tool_catalog = get_all_tool_groups()
    prompt = (
        """You are a workflow suitability analyzer. Decide whether the versioned skill package
contains enough semantic evidence to derive a useful executable workflow. Judge the behavior the
Skill describes, not whether it already uses a workflow schema. SKILL.md files commonly omit a
manifest entry point, formal step numbering, explicit input/output declarations, code-like control
flow, tool-call syntax, and scripts. Absence of any of those is NOT a reason to reject the Skill.

Treat a workflow as generatable when its goal and a coherent execution path can be reliably inferred
from natural-language instructions. You may normalize prose phases, ordered guidance, decision
guidance, and implied inputs/outputs into steps and transitions. This is inference rather than
invention when a reasonable reader would derive substantially the same workflow. Prefer generatable
when there is one clear primary workflow. Use needs_confirmation only for a material choice between
independent workflows or for unresolved behavior indispensable to execution.

Reject only when no stable execution path can be derived without adding substantive behavior; for
example, the package is solely reference knowledge, style or safety rules, preferences, or an
unordered collection of unrelated tools. Never invent steps merely to satisfy a schema, but do not
demand that the source itself define the target schema. Do not cite missing manifests, scripts,
formal I/O, explicit tool invocations, or code-like control flow as deficiencies unless the Skill's
meaning is genuinely ambiguous without that information.
Unsafe scripts must be classified as ignored with a user-visible reason; do not fail the whole
analysis merely because such a script exists. Use needs_confirmation only when the ignored script
is indispensable to the selected workflow and has no safe framework replacement.
Return raw JSON with: verdict (generatable|needs_confirmation|rejected), verdict_code, message,
candidates (id,name,goal,inputs,outputs,steps,evidence_paths), coverage (files map to disposition),
tool_mappings, and scripts. Use needs_confirmation for a choice between TWO OR MORE independent
candidate workflows, or when one candidate still has an indispensable unresolved behavior. A single
coherent candidate with sufficient evidence is generatable and must not require a ceremonial choice.
Each tool_mappings value must use {action, source_tool, replacement, source_script, reason}; action is
replace only for a proven equivalent framework capability, otherwise preserve or confirmation_required.
Use rejected when no genuine workflow exists. Every manifest path must appear in coverage.
Framework tool equivalence rules: infrastructure capabilities such as KB search may replace a
different implementation when I/O semantics match. Provider-bound cloud products are equivalent
only when provider_id/product_id match; a generic A/B-backed web_search must not replace an
explicitly requested XX Search. Report every replacement and skipped script to the user.

Framework capability catalog:
"""
        + yaml.safe_dump(tool_catalog, allow_unicode=True, sort_keys=False)
        + """
Deterministic script inventory (authoritative):
"""
        + yaml.safe_dump(script_inventory, allow_unicode=True, sort_keys=False)
        + '\n'
        + package_prompt
    )
    raw = _call_llm(prompt)
    try:
        data = _extract_json(raw)
    except ValueError as exc:
        raise HTTPException(
            status_code=500,
            detail=f'analysis JSON parse error: {exc}',
        ) from exc
    verdict = str(data.get('verdict') or 'rejected')
    if verdict not in {'generatable', 'needs_confirmation', 'rejected'}:
        verdict = 'rejected'
    candidates = data.get('candidates') if isinstance(data.get('candidates'), list) else []
    tool_mappings = data.get('tool_mappings') if isinstance(data.get('tool_mappings'), dict) else {}
    confirmation_codes = {
        'indispensable_behavior_unresolved',
        'indispensable_script_unsupported',
        'indispensable_tool_unavailable',
    }
    has_required_confirmation = any(
        isinstance(mapping, dict) and mapping.get('action') == 'confirmation_required'
        for mapping in tool_mappings.values()
    )
    # Do not stop the generation merely to make the user select the only option.
    # A single candidate needs confirmation only when the analyzer identifies a
    # concrete indispensable unresolved dependency.
    if (
        verdict == 'needs_confirmation'
        and len(candidates) == 1
        and str(data.get('verdict_code') or '') not in confirmation_codes
        and not has_required_confirmation
    ):
        verdict = 'generatable'
        data['verdict_code'] = 'single_candidate_auto_selected'
    if verdict == 'generatable' and not candidates:
        verdict = 'rejected'
        data['verdict_code'] = 'workflow_evidence_missing'
    manifest_paths = [str(f.get('path') or '') for f in req.skill_package.get('files', [])]
    coverage = data.get('coverage') if isinstance(data.get('coverage'), dict) else {}
    covered = coverage.setdefault('files', {})
    for path in manifest_paths:
        if path and path not in covered:
            covered[path] = 'unresolved'
    for path in deterministically_unresolved:
        covered[path] = 'unresolved'
    # Coverage is a report, not a user-resolvable choice.  An unresolved ancillary
    # file must remain visible in the report, but presenting the sole workflow as
    # a confirmation button cannot resolve that file.  The analyzer has already
    # been instructed to request confirmation/reject when unresolved behavior is
    # indispensable, so do not overwrite a coherent generatable verdict here.
    catalog_by_name = {str(item.get('name') or ''): item for item in tool_catalog}
    for mapping in tool_mappings.values():
        if isinstance(mapping, dict) and mapping.get('action') == 'replace':
            target = catalog_by_name.get(str(mapping.get('replacement') or ''))
            mapping['available'] = bool(target and target.get('active'))
            if target:
                mapping['capability_id'] = target.get('capability_id')
                mapping['equivalence_scope'] = target.get('equivalence_scope')
    return AnalyzeSkillResponse(
        verdict=verdict, verdict_code=str(data.get('verdict_code') or ''),
        message=str(data.get('message') or ''), candidates=candidates,
        coverage=coverage,
        tool_mappings=tool_mappings,
        scripts=script_inventory,
    )


@router.post(
    '/api/chat/generate_plugin/design_brief',
    response_model=DesignBriefResponse,
    summary='Phase 0: Generate design brief (slots + steps + flow)',
)
async def generate_design_brief(req: DesignBriefRequest) -> DesignBriefResponse:
    """Phase 0: generate a Markdown design brief that defines slot IDs and step flow.

    This brief is injected into Phase 1/2/3 prompts as an authoritative reference
    so that slot IDs remain consistent across all generation phases.
    """
    inject_model_config(req.llm_config or {})

    package_prompt = _skill_package_prompt(req.skill_package, req.skill_content or '')
    if package_prompt.strip():
        workflow = req.workflow_analysis or ''
        user_prompt = (
            f'Plugin name: {req.name}\n\n'
            f'Convert the following versioned skill package into a design brief. '
            f'Do not invent behavior for unresolved files. '
            f'Confirmed workflow:\n{workflow}\n\n{package_prompt}'
        )
    else:
        user_prompt = (
            f'Plugin name: {req.name}\n\n'
            f'Generate a design brief based on the following description:\n\n'
            f'{req.description or req.name}'
        )

    raw = _call_llm(f'{_DESIGN_BRIEF_SYSTEM}\n\n{user_prompt}')
    try:
        data = _extract_json(raw)
    except ValueError as exc:
        raise HTTPException(status_code=500, detail=f'Phase 0 JSON parse error: {exc}') from exc

    brief = data.get('design_brief', '')
    if not brief:
        raise HTTPException(status_code=500, detail='Phase 0: missing design_brief in response')

    return DesignBriefResponse(design_brief=brief)


@router.post(
    '/api/chat/generate_plugin/skeleton',
    response_model=SkeletonResponse,
    summary='Phase 1: Generate plugin.yaml skeleton',
)
async def generate_skeleton(req: SkeletonRequest) -> SkeletonResponse:
    """Phase 1: generate plugin.yaml skeleton (slots + steps list, no state logic)."""
    inject_model_config(req.llm_config or {})

    system_prompt = _tmpl(
        _SKELETON_SYSTEM,
        spec=_PLUGIN_FORMAT_SPEC,
        design_brief_section=_design_brief_section(req.design_brief),
    )
    package_prompt = _skill_package_prompt(req.skill_package, req.skill_content or '')
    if package_prompt.strip():
        workflow = req.workflow_analysis or ''
        user_prompt = (
            f'Plugin name: {req.name}\n\n'
            f'Convert the following versioned skill package into a plugin skeleton. '
            f'Do not invent behavior for unresolved files. '
            f'Confirmed workflow:\n{workflow}\n\n{package_prompt}'
        )
    else:
        user_prompt = (
            f'Plugin name: {req.name}\n\n'
            f'Generate a plugin skeleton based on the following description:\n\n'
            f'{req.description or req.name}'
        )

    raw = _call_llm(f'{system_prompt}\n\n{user_prompt}')
    try:
        data = _extract_json(raw)
    except ValueError as exc:
        raise HTTPException(status_code=500, detail=f'Phase 1 JSON parse error: {exc}') from exc

    plugin_dict = _plugin_dict_from_skeleton_response(data, system_prompt)
    _apply_tool_replacements(plugin_dict, None, req.workflow_analysis)

    # Validate skeleton fields + patch retry
    for attempt in range(MAX_PATCH_RETRIES):
        missing = _check_skeleton_missing(plugin_dict)
        # Separate blocking errors from warn-level ui.tabs issues
        blocking = [m for m in missing if 'warn:' not in m]
        if not blocking:
            break
        logger.info('[staged/skeleton] attempt=%d missing: %s', attempt + 1, blocking)
        plugin_dict = _patch_skeleton(plugin_dict, blocking, system_prompt)
    else:
        missing = _check_skeleton_missing(plugin_dict)
        blocking = [m for m in missing if 'warn:' not in m]
        if blocking:
            raise HTTPException(
                status_code=500,
                detail=f'Phase 1: missing fields after retries: {blocking}',
            )

    # Auto-fill missing ui.slots[*].widgetType based on slot type+cardinality
    slots_list = plugin_dict.get('slots', [])
    if isinstance(slots_list, list):
        ui = plugin_dict.setdefault('ui', {})
        ui_slots = ui.setdefault('slots', {})
        for slot in slots_list:
            if not isinstance(slot, dict):
                continue
            slot_id = slot.get('id')
            slot_type = (slot.get('type') or '').lower()
            cardinality = (slot.get('cardinality') or 'single').lower()
            if slot_id and slot_id not in ui_slots:
                widget = _WIDGET_TYPE_DEFAULTS.get(
                    (slot_type, cardinality),
                    _WIDGET_TYPE_DEFAULTS.get((slot_type, 'single'), 'text-single'),
                )
                ui_slots[slot_id] = {'widgetType': widget}

        # Fallback: if ui.tabs is missing or empty, generate one tab per slot
        # in declaration order so the frontend always has something to render.
        if not ui.get('tabs'):
            auto_tabs = []
            for slot in slots_list:
                if not isinstance(slot, dict):
                    continue
                slot_id = slot.get('id')
                if not slot_id:
                    continue
                auto_tabs.append({
                    'id': f'tab_{slot_id}',
                    'label': slot.get('label') or slot_id,
                    'layout': 'vertical',
                    'slots': [{'id': slot_id}],
                })
            if auto_tabs:
                ui['tabs'] = auto_tabs
                logger.info('[staged/skeleton] auto-generated %d ui.tabs from slots', len(auto_tabs))

    plugin_yaml = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    return SkeletonResponse(plugin_yaml=plugin_yaml)


@router.post(
    '/api/chat/generate_plugin/state_machine',
    response_model=StateMachineResponse,
    summary='Phase 2: Generate state.yml from plugin.yaml skeleton',
)
async def generate_state_machine(req: StateMachineRequest) -> StateMachineResponse:
    """Phase 2: generate full state.yml using the plugin.yaml skeleton from Phase 1."""
    inject_model_config(req.llm_config or {})

    try:
        plugin_dict = yaml.safe_load(req.plugin_yaml) or {}
    except yaml.YAMLError as exc:
        raise HTTPException(status_code=400, detail=f'Invalid plugin_yaml: {exc}') from exc

    system_prompt = _tmpl(
        _STATE_MACHINE_SYSTEM,
        spec=_PLUGIN_FORMAT_SPEC,
        plugin_yaml=req.plugin_yaml,
        design_brief_section=_design_brief_section(req.design_brief),
    )
    user_prompt = (
        f'Plugin name: {req.name}\n\nSteps in order: '
        f'{", ".join(s.get("id", "") for s in plugin_dict.get("steps", []) if isinstance(s, dict)) or "(none)"}\n\n'
        'Generate the complete state.yml for this plugin, including transitions and step prompts.\n'
        'transitions.__start__[0].to MUST be set to the first step listed above.'
    )

    raw = _call_llm(f'{system_prompt}\n\n{user_prompt}')
    try:
        data = _extract_json(raw)
    except ValueError as exc:
        raise HTTPException(status_code=500, detail=f'Phase 2 JSON parse error: {exc}') from exc

    state_yaml = data.get('state_yaml', '')
    if not state_yaml:
        raise HTTPException(status_code=500, detail='Phase 2: missing state_yaml in response')

    try:
        state_dict = yaml.safe_load(state_yaml) or {}
    except yaml.YAMLError as exc:
        raise HTTPException(status_code=500, detail=f'Phase 2 YAML parse error: {exc}') from exc

    # Sanitize: remove reserved keywords from steps dict if LLM accidentally included them
    reserved_keys = {'__start__', '__end__'}
    state_steps = state_dict.get('steps')
    if isinstance(state_steps, dict):
        for key in reserved_keys:
            if key in state_steps:
                logger.warning('[staged/state_machine] removing reserved key "%s" from steps', key)
                del state_steps[key]

    # Validate state machine fields + patch retry
    field_warnings: List[str] = []
    for attempt in range(MAX_PATCH_RETRIES):
        # Reuse _check_missing_fields but only for state fields (pass empty plugin_dict)
        missing = _check_missing_fields({}, state_dict)
        # Filter to state.* only since plugin is already done
        state_missing = [m for m in missing if m.startswith('state.')]
        if not state_missing:
            break
        logger.info('[staged/state_machine] attempt=%d missing: %s', attempt + 1, state_missing)
        state_dict = _patch_state_machine(plugin_dict, state_dict, state_missing, system_prompt)
    else:
        missing = [m for m in _check_missing_fields({}, state_dict) if m.startswith('state.')]
        if missing:
            warn_msg = f'Phase 2: missing fields after retries: {missing}'
            logger.warning('[staged/state_machine] degraded: %s', warn_msg)
            field_warnings.append(warn_msg)

    # PluginSpec validation (both YAML together)
    state_yaml = yaml.dump(state_dict, allow_unicode=True, sort_keys=False)
    for attempt in range(MAX_PATCH_RETRIES):
        error_msg = _validate_with_pluginspec(req.plugin_yaml, state_yaml)
        if error_msg is None:
            break
        logger.info('[staged/state_machine] PluginSpec error (attempt=%d): %s', attempt + 1, error_msg)
        state_dict = _patch_state_machine(
            plugin_dict, state_dict,
            [f'PluginSpec validation error: {error_msg}'],
            system_prompt,
        )
        state_yaml = yaml.dump(state_dict, allow_unicode=True, sort_keys=False)
    else:
        err = _validate_with_pluginspec(req.plugin_yaml, state_yaml)
        if err:
            warn_msg = f'Phase 2 PluginSpec validation failed: {err}'
            logger.warning('[staged/state_machine] degraded: %s', warn_msg)
            field_warnings.append(warn_msg)

    # Slot reference validation + repair (max 1 attempt).
    # Run after PluginSpec so slot errors are as accurate as possible.
    slot_errors = _validate_slot_references(plugin_dict, state_dict)
    if slot_errors:
        logger.info('[staged/state_machine] slot errors: %s', slot_errors)
        plugin_dict, state_dict = _repair_slots_only(plugin_dict, state_dict, slot_errors, req.llm_config or {})
        # Re-validate; if still broken, log a warning (non-fatal).
        remaining_slot_errors = _validate_slot_references(plugin_dict, state_dict)
        if remaining_slot_errors:
            warn_msg = f'Phase 2 slot repair incomplete: {remaining_slot_errors}'
            logger.warning('[staged/state_machine] %s', warn_msg)
            field_warnings.append(warn_msg)

    _apply_tool_replacements(plugin_dict, state_dict, req.workflow_analysis)

    final_plugin_yaml = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    state_yaml = yaml.dump(state_dict, allow_unicode=True, sort_keys=False)
    return StateMachineResponse(state_yaml=state_yaml, plugin_yaml=final_plugin_yaml, warnings=field_warnings)


@router.post(
    '/api/chat/generate_plugin/scenario_scripts',
    response_model=ScenarioScriptsResponse,
    summary='Phase 3: Generate scenario.md and optional scripts',
)
async def generate_scenario_scripts(req: ScenarioScriptsRequest) -> ScenarioScriptsResponse:
    """Phase 3: generate scenario.md and optional script files.

    All generated scripts are validated with SecurityVisitor (AST check).
    If a script contains forbidden operations, the model is asked to fix only
    that file (up to MAX_PATCH_RETRIES times).  If it still fails, the script
    is dropped and a warning is logged rather than failing the whole request.
    """
    inject_model_config(req.llm_config or {})

    system_prompt = _tmpl(
        _SCENARIO_SCRIPTS_SYSTEM,
        plugin_yaml=req.plugin_yaml,
        state_yaml=req.state_yaml,
        design_brief_section=_design_brief_section(req.design_brief),
    )
    user_prompt = (
        f'Plugin name: {req.name}\n\n'
        'Write scenario.md for this plugin. '
        'Also implement any Python tool functions declared in tool_scripts.'
    )

    raw = _call_llm(f'{system_prompt}\n\n{user_prompt}')
    try:
        data = _extract_json(raw)
    except ValueError as exc:
        # First attempt failed. Retry once with an explicit reminder.
        logger.warning('[staged/scenario_scripts] first parse failed (%s), retrying...', exc)
        retry_prompt = (
            'IMPORTANT: Your previous response could not be parsed as JSON. '
            'Return ONLY a raw JSON object (no markdown fences, no extra text):\n'
            '  {"scenario_md": "...", "scripts": {}}\n\n'
            + user_prompt
        )
        raw2 = _call_llm(f'{system_prompt}\n\n{retry_prompt}')
        try:
            data = _extract_json(raw2)
        except ValueError as exc2:
            logger.warning('[staged/scenario_scripts] retry also failed (%s), using fallback', exc2)
            data = {}

    scenario_md = data.get('scenario_md', '')
    scripts: Dict[str, str] = data.get('scripts') or {}
    scripts.update({path: _wrap_command_source(path, source)[0] for path, source in req.source_scripts.items()})

    if not scenario_md:
        logger.warning('[staged/scenario_scripts] scenario_md is empty, using fallback')
        scenario_md = f'# {req.name}\n\n## 场景描述\n\n该插件由 AI 自动生成。\n'

    # --- Node-level security check + dry-run import + fix retry loop ---
    safe_scripts: Dict[str, str] = {}
    script_warnings: List[str] = []
    for filename, code in scripts.items():
        current_code = code
        dropped = False

        for attempt in range(MAX_PATCH_RETRIES + 1):
            # Step 1: syntax check
            try:
                ast.parse(current_code)
            except SyntaxError as exc:
                if attempt == MAX_PATCH_RETRIES:
                    logger.error('[staged] dropping %s: syntax error after retries: %s', filename, exc)
                    dropped = True
                    break
                fix_prompt = _tmpl(
                    _DRY_RUN_FIX_TEMPLATE,
                    filename=filename,
                    error=f'SyntaxError: {exc}',
                    full_source=current_code,
                )
                current_code = _ask_fix(system_prompt, fix_prompt) or current_code
                continue

            # Step 2: node-level security scan — find ALL violating nodes
            violations = _scan_file(current_code)
            if violations:
                if attempt == MAX_PATCH_RETRIES:
                    names = ', '.join(_node_name(n) for n, _ in violations)
                    logger.error('[staged] dropping %s: security violations after retries: %s', filename, names)
                    dropped = True
                    break
                # Report all violations in one fix prompt so the model fixes everything at once
                violation_lines = '\n'.join(
                    f'  - {_NODE_LABEL.get(type(n), type(n).__name__)} '
                    f'"{_node_name(n)}": {err}'
                    for n, err in violations
                )
                # Use the first violation's node source for the node_source field;
                # the full script is always included so the model has full context.
                first_node, first_err = violations[0]
                fix_prompt = _tmpl(
                    _NODE_FIX_TEMPLATE,
                    filename=filename,
                    node_label=_NODE_LABEL.get(type(first_node), type(first_node).__name__),
                    node_name=_node_name(first_node),
                    node_source=_node_source(first_node, current_code),
                    error=f'{len(violations)} violation(s):\n{violation_lines}',
                    full_source=current_code,
                )
                logger.warning(
                    '[staged] %s security violations (attempt=%d): %s',
                    filename, attempt + 1, violation_lines,
                )
                current_code = _ask_fix(system_prompt, fix_prompt) or current_code
                continue

            # Step 3: dry-run import
            dry_run_err = _dry_run_import(filename, current_code)
            if dry_run_err:
                if attempt == MAX_PATCH_RETRIES:
                    logger.error('[staged] dropping %s: dry-run failed after retries: %s', filename, dry_run_err)
                    dropped = True
                    break
                fix_prompt = _tmpl(
                    _DRY_RUN_FIX_TEMPLATE,
                    filename=filename,
                    error=dry_run_err,
                    full_source=current_code,
                )
                logger.warning(
                    '[staged] %s dry-run import failed (attempt=%d): %s',
                    filename, attempt + 1, dry_run_err,
                )
                current_code = _ask_fix(system_prompt, fix_prompt) or current_code
                continue

            # All checks passed
            break

        if not dropped:
            safe_scripts[filename] = current_code
        else:
            script_warnings.append(f'已忽略未通过安全校验的脚本: {filename}')

    return ScenarioScriptsResponse(scenario_md=scenario_md, scripts=safe_scripts, warnings=script_warnings)


# ---------------------------------------------------------------------------
# Plugin info polish endpoint
# ---------------------------------------------------------------------------

_POLISH_FIELD_INSTRUCTIONS: Dict[str, str] = {
    'description': (
        'Polish this plugin description to be concise and professional. '
        'Clearly describe what the plugin does in 1-2 sentences. '
        'Output the polished text only, no extra explanation.'
    ),
    'when_to_use': (
        'Polish this trigger condition. '
        'IMPORTANT: Output in English only. '
        'Use precise trigger language: start with "ONLY call this tool when ..." '
        'and optionally add "Do NOT trigger if ...". '
        'Output the polished text only, no extra explanation.'
    ),
    'overview': (
        'Polish this scene overview. Keep the original meaning, improve clarity and '
        'professional tone to fit a business scenario description. '
        'Output the polished text only, no extra explanation.'
    ),
    'notes': (
        'Polish these usage notes. Keep all original points, improve clarity and '
        'organize them in a logical order. '
        'Output the polished text only, no extra explanation.'
    ),
}

_POLISH_SYSTEM = (
    'You are a professional editor for AI plugin documentation. '
    'Follow the field-specific instructions precisely and output only the polished text.'
)


class PolishInfoRequest(_LLMConfigMixin):
    fields: Dict[str, str] = Field(default_factory=dict)
    target_fields: List[str]


class PolishInfoResponse(BaseModel):
    description: Optional[str] = None
    when_to_use: Optional[str] = None
    overview: Optional[str] = None
    notes: Optional[str] = None


@router.post(
    '/api/chat/generate_plugin/polish_info',
    response_model=PolishInfoResponse,
    summary='Polish plugin info fields with AI',
)
async def polish_plugin_info(req: PolishInfoRequest) -> PolishInfoResponse:
    """Polish one or more plugin info fields (description, when_to_use, overview, notes).

    Each field is polished independently using a field-specific prompt.
    Only fields listed in target_fields are processed and returned.
    """
    inject_model_config(req.llm_config or {})

    allowed_fields = set(_POLISH_FIELD_INSTRUCTIONS.keys())
    results: Dict[str, str] = {}

    for field in req.target_fields:
        if field not in allowed_fields:
            logger.warning('[polish_info] skipping unknown field: %s', field)
            continue
        original = (req.fields.get(field) or '').strip()
        if not original:
            logger.warning('[polish_info] skipping empty field: %s', field)
            continue

        instruction = _POLISH_FIELD_INSTRUCTIONS[field]
        prompt = (
            f'{_POLISH_SYSTEM}\n\n'
            f'{instruction}\n\n'
            f'Original text:\n{original}'
        )
        polished = _call_llm(prompt).strip()
        # Strip surrounding quotes that some models add
        if len(polished) >= 2 and polished[0] == polished[-1] and polished[0] in ('"', "'"):
            polished = polished[1:-1].strip()
        if polished:
            results[field] = polished
        else:
            logger.warning('[polish_info] LLM returned empty result for field: %s', field)
            results[field] = original

    return PolishInfoResponse(**results)


# ---------------------------------------------------------------------------
# State machine repair endpoint
# ---------------------------------------------------------------------------

_REPAIR_SYSTEM = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Your task is to repair a state.yml that has missing or invalid fields.\n\n'
    'You are given:\n'
    '  - The current plugin.yaml (for context: step IDs, slot IDs, etc.)\n'
    '  - The current state.yml (may be incomplete or have warnings)\n'
    '  - A list of known warnings/issues\n'
    '  - An optional user hint describing what to fix\n\n'
    'CRITICAL RULE — transitions.__start__ MUST always be present and non-empty.\n'
    'CRITICAL RULE — every step in plugin.yaml MUST have a transitions entry.\n'
    'CRITICAL RULE — every step in plugin.yaml MUST have a prompt in state.yml.\n\n'
    'MATERIAL SEMANTICS:\n'
    '  - A material/slot is durable data: either an extra input explicitly supplied separately by the user '
    '(file, image, form field, dataset, etc.) or an output of exactly one prior step.\n'
    '  - The user query, task description, intent, instructions, prompt text, and conversation '
    'context are NOT materials.\n'
    '  - Never solve a missing-producer error by marking a query-like pseudo-material external. '
    'Remove pseudo-slots such as '
    'user_query, search_query, request, topic, task_description, or instructions from plugin.yaml and remove their '
    'state.yml inputs/outputs/skip_if references. The original request remains available as task/conversation context.\n'
    '  - Every remaining slot must be either external: true because it is genuinely requested as separate user data, '
    'or produced by exactly one control-ancestor step.\n\n'
    'Return BOTH complete files as:\n'
    '  {"plugin_yaml": "<complete corrected plugin.yaml>", '
    '"state_yaml": "<complete corrected state.yml>"}\n\n'
    'Do NOT return a partial patch. Return both full YAML files.\n\n'
    '=== Plugin Format Specification ===\n'
    '{__spec__}\n'
    '=== End of Specification ===\n\n'
    '=== Current plugin.yaml ===\n'
    '{__plugin_yaml__}\n'
    '=== End of plugin.yaml ==='
)


class RepairRequest(_LLMConfigMixin):
    plugin_yaml: str
    state_yaml: str
    repair_hint: str = ''
    warnings: List[str] = Field(default_factory=list)
    diagnostics: List[Dict[str, Any]] = Field(default_factory=list)
    target: str = 'statemachine'  # 'statemachine' | 'ui' | 'scenario'
    scenario_md: str = ''
    scripts: Dict[str, str] = Field(default_factory=dict)


class RepairResponse(BaseModel):
    state_yaml: str
    plugin_yaml: str = ''  # populated when slot repair was applied
    remaining_warnings: List[str] = []
    scenario_md: str = ''
    scripts: Dict[str, str] = Field(default_factory=dict)


@router.post(
    '/api/chat/generate_plugin/repair',
    response_model=RepairResponse,
    summary='Repair an incomplete or invalid state.yml',
)
async def repair_state_machine(req: RepairRequest) -> RepairResponse:
    """Repair a state.yml that has missing fields or validation warnings.

    Accepts the current plugin.yaml and state.yml, plus an optional user hint.
    Returns a fully corrected state.yml.
    """
    logger.info(
        '[repair] START target=%r repair_hint=%r warnings=%s '
        'plugin_yaml_len=%d state_yaml_len=%d',
        req.target,
        (req.repair_hint or '')[:120],
        req.warnings,
        len(req.plugin_yaml),
        len(req.state_yaml),
    )
    inject_model_config(req.llm_config or {})

    try:
        plugin_dict = yaml.safe_load(req.plugin_yaml) or {}
    except yaml.YAMLError as exc:
        logger.error('[repair] invalid plugin_yaml: %s', exc)
        raise HTTPException(status_code=400, detail=f'Invalid plugin_yaml: {exc}') from exc

    try:
        state_dict = yaml.safe_load(req.state_yaml) or {}
    except yaml.YAMLError as exc:
        logger.error('[repair] invalid state_yaml: %s', exc)
        raise HTTPException(status_code=400, detail=f'Invalid state_yaml: {exc}') from exc

    # ── Script isolation / repair ─────────────────────────────────────────────
    if req.target == 'scripts':
        package = {'files': [{'path': path, 'content': source, 'binary': False} for path, source in req.scripts.items()]}
        inventory = _script_inventory(package)
        safe: Dict[str, str] = {}
        warnings: List[str] = []
        ignored_paths: set[str] = set()
        ignored_functions: set[str] = set()
        for path, source in req.scripts.items():
            report = inventory.get(path) or {}
            if report.get('classification') == 'unsupported':
                warnings.append(f'已忽略不安全脚本 {path}: {report.get("reason") or "未通过安全检查"}')
                ignored_paths.add(path)
                ignored_functions.update(str(name) for name in report.get('functions') or [])
                continue
            safe[path] = _wrap_command_source(path, source)[0]
        repaired_plugin, repaired_state = plugin_dict, state_dict
        if ignored_paths and isinstance(repaired_plugin.get('tool_scripts'), list):
            repaired_plugin['tool_scripts'] = [
                entry for entry in repaired_plugin['tool_scripts']
                if not isinstance(entry, dict) or str(entry.get('path') or '') not in ignored_paths
            ]
        for config in (repaired_state.get('steps') or {}).values():
            if isinstance(config, dict) and isinstance(config.get('tools'), list):
                config['tools'] = [tool for tool in config['tools'] if str(tool) not in ignored_functions]
        return RepairResponse(
            state_yaml=yaml.dump(repaired_state, allow_unicode=True, sort_keys=False),
            plugin_yaml=yaml.dump(repaired_plugin, allow_unicode=True, sort_keys=False),
            scenario_md=req.scenario_md,
            scripts=safe,
            remaining_warnings=warnings,
        )

    # ── Scenario / documentation repair ──────────────────────────────────────
    if req.target == 'scenario':
        scenario_system = (
            'You are a LazyMind plugin authoring assistant.\n'
            'Your task is to write or repair the scenario.md for a plugin.\n\n'
            'scenario.md is a structured guide for the plugin editor.\n\n'
            '=== scenario.md FORMAT (STRICTLY REQUIRED) ===\n'
            'The scenario.md MUST follow this exact Markdown structure (write in Chinese):\n\n'
            '## 场景描述\n\n'
            '<One or two paragraphs describing what this plugin does and when to use it.>\n\n'
            '## 工作流程\n\n'
            '### {step_id}（{step_label}）\n\n'
            '<One or two sentences describing what this step does.>\n\n'
            '(repeat for every step in the same order as steps[] in plugin.yaml)\n\n'
            '## 注意事项\n\n'
            '<Optional usage tips, constraints, or warnings. Omit this section if nothing to add.>\n\n'
            'RULES:\n'
            '  - Use the EXACT step ids and labels from plugin.yaml steps[].\n'
            '  - Every step MUST have a non-empty description. Do NOT write "（暂无描述）".\n'
            '  - Write all content in Chinese.\n'
            '  - Do NOT add extra top-level sections or change the section names.\n'
            '=== End of scenario.md FORMAT ===\n\n'
            'Return the COMPLETE scenario.md as:\n'
            '  {{"scenario_md": "<complete scenario.md content>"}}\n\n'
            f'=== Current plugin.yaml ===\n{req.plugin_yaml}\n=== End ===\n\n'
            f'=== Current state.yml ===\n{req.state_yaml}\n=== End ==='
        )
        hint_section = (f'User instruction: {req.repair_hint.strip()}\n\n'
                        if req.repair_hint and req.repair_hint.strip() else '')
        scenario_user = f'{hint_section}Write a complete scenario.md for this plugin.'
        raw = _call_llm(f'{scenario_system}\n\n{scenario_user}')
        try:
            data = _extract_json(raw)
        except ValueError as exc:
            logger.error('[repair/scenario] JSON parse error: %s | raw=%r', exc, raw[:300])
            raise HTTPException(status_code=500, detail=f'Scenario repair JSON parse error: {exc}') from exc
        scenario_md = data.get('scenario_md', '')
        if not scenario_md:
            logger.error('[repair/scenario] missing scenario_md in LLM response, keys=%s', list(data.keys()))
            raise HTTPException(status_code=500, detail='Scenario repair: missing scenario_md in response')
        logger.info('[repair/scenario] SUCCESS scenario_md_len=%d', len(scenario_md))
        return RepairResponse(state_yaml=scenario_md, remaining_warnings=[])

    # ── UI layout repair ─────────────────────────────────────────────────────
    if req.target == 'ui':
        ui_system = (
            'You are repairing the UI layout in a LazyMind plugin.yaml.\n'
            'Return the COMPLETE plugin.yaml, preserving all non-UI behavior and identifiers.\n'
            'Repair only the ui block and any missing widget configuration required by it.\n\n'
            'Hard requirements:\n'
            '- ui.tabs must be a non-empty list.\n'
            '- Every tab must have id, label, layout, and a non-empty slots list.\n'
            '- Every slot with exposed: true must appear in exactly one tab as {id: slot_id}; '
            'internal materials may be omitted.\n'
            '- Put user-provided inputs in an Input tab and generated/intermediate artifacts in result tabs.\n'
            '- ui.slots must define a compatible widgetType for every exposed slot.\n'
            '- Never return slots: [] and never invent slot ids.\n'
            '- Preserve steps, tool_scripts, metadata, and all other non-UI fields exactly.\n\n'
            'Return ONLY JSON: {"plugin_yaml": "<complete fixed plugin.yaml>"}.\n'
        )
        known_issues = '\n'.join(f'- {w}' for w in req.warnings)
        if req.diagnostics:
            known_issues += '\n' + json.dumps(req.diagnostics, ensure_ascii=False, indent=2)
        ui_user = (
            f'Known issues:\n{known_issues or "- Infer and fix all unusable UI layout issues."}\n\n'
            f'User instruction:\n{req.repair_hint or "Repair the UI layout."}\n\n'
            f'Current plugin.yaml:\n{req.plugin_yaml}\n\n'
            f'Current state.yml (use step inputs/outputs to group slots):\n{req.state_yaml}'
        )
        raw = _call_llm(f'{ui_system}\n{ui_user}')
        try:
            data = _extract_json(raw)
            fixed_plugin_yaml = str(data.get('plugin_yaml') or '')
            fixed_plugin = yaml.safe_load(fixed_plugin_yaml) or {}
        except (ValueError, yaml.YAMLError) as exc:
            raise HTTPException(status_code=500, detail=f'UI repair parse error: {exc}') from exc
        if not fixed_plugin_yaml or not isinstance(fixed_plugin, dict):
            raise HTTPException(status_code=500, detail='UI repair: missing complete plugin_yaml')

        remaining = [m for m in _check_skeleton_missing(fixed_plugin) if m.startswith('plugin.ui.')]
        for _ in range(MAX_PATCH_RETRIES):
            if not remaining:
                break
            fixed_plugin = _patch_skeleton(fixed_plugin, remaining, ui_system)
            remaining = [m for m in _check_skeleton_missing(fixed_plugin) if m.startswith('plugin.ui.')]
        fixed_plugin_yaml = yaml.dump(fixed_plugin, allow_unicode=True, sort_keys=False)
        logger.info('[repair/ui] SUCCESS plugin_yaml_len=%d remaining=%s', len(fixed_plugin_yaml), remaining)
        return RepairResponse(
            state_yaml=req.state_yaml,
            plugin_yaml=fixed_plugin_yaml,
            scenario_md=req.scenario_md,
            scripts=req.scripts,
            remaining_warnings=remaining,
        )

    # ── State machine / UI repair ─────────────────────────────────────────────
    system_prompt = _tmpl(
        _REPAIR_SYSTEM,
        spec=_PLUGIN_FORMAT_SPEC,
        plugin_yaml=req.plugin_yaml,
    )

    # Pre-validate slot references so we can inject them into the repair prompt.
    pre_slot_errors = _validate_slot_references(plugin_dict, state_dict)

    warnings_section = ''
    if req.warnings:
        warnings_section = 'Known issues to fix:\n' + '\n'.join(f'  - {w}' for w in req.warnings) + '\n\n'
    if req.diagnostics:
        warnings_section += (
            'Authoritative Go diagnostics (preserve code/path/details while fixing every error):\n'
            + json.dumps(req.diagnostics, ensure_ascii=False, indent=2)
            + '\n\n'
        )
    hint_section = ''
    if req.repair_hint and req.repair_hint.strip():
        hint_section = f'User instruction: {req.repair_hint.strip()}\n\n'
    slot_error_section = ''
    if pre_slot_errors:
        slot_error_section = (
            'SLOT REFERENCE ERRORS (must fix — use ONLY the slot ids defined in plugin.yaml slots[]):\n'
            + '\n'.join(f'  - {e}' for e in pre_slot_errors)
            + '\n\n'
        )

    user_prompt = (
        f'{warnings_section}'
        f'{slot_error_section}'
        f'{hint_section}'
        f'Current state.yml to repair:\n{req.state_yaml}\n\n'
        'Return the complete fixed state.yml.'
    )

    raw = _call_llm(f'{system_prompt}\n\n{user_prompt}')
    try:
        data = _extract_json(raw)
    except ValueError as exc:
        logger.error('[repair/statemachine] JSON parse error: %s | raw=%r', exc, raw[:300])
        raise HTTPException(status_code=500, detail=f'Repair JSON parse error: {exc}') from exc

    fixed_yaml = data.get('state_yaml', '')
    if not fixed_yaml:
        logger.error('[repair/statemachine] missing state_yaml in LLM response, keys=%s', list(data.keys()))
        raise HTTPException(status_code=500, detail='Repair: missing state_yaml in response')

    try:
        fixed_dict = yaml.safe_load(fixed_yaml) or {}
    except yaml.YAMLError as exc:
        logger.error('[repair/statemachine] YAML parse error on fixed output: %s | yaml=%r', exc, fixed_yaml[:300])
        raise HTTPException(status_code=500, detail=f'Repair YAML parse error: {exc}') from exc

    # State-machine repairs may need to remove pseudo-materials or correct a
    # slot's real provenance, which cannot be expressed in state.yml alone.
    repaired_plugin_yaml = ''
    candidate_plugin_yaml = data.get('plugin_yaml', '')
    if candidate_plugin_yaml:
        try:
            candidate_plugin_dict = yaml.safe_load(candidate_plugin_yaml) or {}
            if isinstance(candidate_plugin_dict, dict):
                plugin_dict = candidate_plugin_dict
                repaired_plugin_yaml = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
        except yaml.YAMLError as exc:
            logger.warning('[repair/statemachine] ignoring invalid plugin_yaml from repair: %s', exc)

    # Sanitize: remove reserved keywords from steps
    reserved_keys = {'__start__', '__end__'}
    state_steps = fixed_dict.get('steps')
    if isinstance(state_steps, dict):
        for key in reserved_keys:
            if key in state_steps:
                del state_steps[key]

    # Check remaining issues and report them (non-fatal)
    remaining = [m for m in _check_missing_fields({}, fixed_dict) if m.startswith('state.')]
    logger.info('[repair/statemachine] field check remaining=%s', remaining)

    # Slot check before structural/PluginSpec retries: only used to update remaining.
    # The definitive slot repair runs after all retries (see below) so that structural
    # or PluginSpec retries cannot re-introduce slot errors undetected.
    # Structural pre-check before PluginSpec: catch common LLM mistakes that produce
    # cryptic AttributeErrors inside PluginSpec (e.g. transitions generated as a list).
    fixed_yaml_out = yaml.dump(fixed_dict, allow_unicode=True, sort_keys=False)

    def _structural_check(state: dict) -> str:
        transitions = state.get('transitions')
        if transitions is not None and not isinstance(transitions, dict):
            return (
                f'transitions must be a YAML mapping (dict), '
                f'got {type(transitions).__name__}. '
                f'Each key is a step id and each value is a list of {{to, when?}} entries.'
            )
        steps = state.get('steps')
        if steps is not None and not isinstance(steps, dict):
            return (
                f'steps must be a YAML mapping (dict), '
                f'got {type(steps).__name__}.'
            )
        return ''

    struct_err = _structural_check(fixed_dict)
    if struct_err:
        logger.warning('[repair/statemachine] structural error, requesting LLM retry: %s', struct_err)
        retry_prompt = (
            f'{warnings_section}'
            f'STRUCTURAL ERROR in your previous output (must fix):\n  - {struct_err}\n\n'
            f'{hint_section}'
            f'Current (broken) state.yml:\n{fixed_yaml_out}\n\n'
            'Return the complete fixed state.yml.'
        )
        raw2 = _call_llm(f'{system_prompt}\n\n{retry_prompt}')
        try:
            data2 = _extract_json(raw2)
            candidate_plugin_yaml2 = data2.get('plugin_yaml', '')
            if candidate_plugin_yaml2:
                candidate_plugin_dict2 = yaml.safe_load(candidate_plugin_yaml2) or {}
                if isinstance(candidate_plugin_dict2, dict):
                    plugin_dict = candidate_plugin_dict2
                    repaired_plugin_yaml = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
            fixed_yaml2 = data2.get('state_yaml', '')
            if fixed_yaml2:
                fixed_dict2 = yaml.safe_load(fixed_yaml2) or {}
                if isinstance(fixed_dict2, dict):
                    fixed_dict = fixed_dict2
                    fixed_yaml_out = yaml.dump(fixed_dict, allow_unicode=True, sort_keys=False)
                    struct_err2 = _structural_check(fixed_dict)
                    if struct_err2:
                        logger.error('[repair/statemachine] structural error persists after retry: %s', struct_err2)
                        raise HTTPException(
                            status_code=422,
                            detail=f'Repair produced structurally invalid YAML: {struct_err2}',
                        )
                    logger.info('[repair/statemachine] structural retry succeeded')
        except HTTPException:
            raise
        except Exception as exc:
            logger.error('[repair/statemachine] structural retry parse failed: %s', exc)
            raise HTTPException(
                status_code=422,
                detail=f'Repair produced structurally invalid YAML: {struct_err}',
            ) from exc

    # Final full validation — must pass PluginSpec before we call this a success.
    # Failure here triggers one more LLM retry with the PluginSpec error injected.
    final_plugin_yaml_for_check = repaired_plugin_yaml if repaired_plugin_yaml else req.plugin_yaml
    pluginspec_err = _validate_with_pluginspec(final_plugin_yaml_for_check, fixed_yaml_out)
    if pluginspec_err:
        logger.warning('[repair/statemachine] PluginSpec failed, requesting LLM retry: %s', pluginspec_err)
        retry_prompt = (
            f'{warnings_section}'
            f'PLUGINSPEC VALIDATION ERROR in your previous output (must fix):\n  - {pluginspec_err}\n\n'
            f'{hint_section}'
            f'Current (invalid) state.yml:\n{fixed_yaml_out}\n\n'
            'Return the complete fixed state.yml.'
        )
        raw3 = _call_llm(f'{system_prompt}\n\n{retry_prompt}')
        try:
            data3 = _extract_json(raw3)
            candidate_plugin_yaml3 = data3.get('plugin_yaml', '')
            if candidate_plugin_yaml3:
                candidate_plugin_dict3 = yaml.safe_load(candidate_plugin_yaml3) or {}
                if isinstance(candidate_plugin_dict3, dict):
                    plugin_dict = candidate_plugin_dict3
                    repaired_plugin_yaml = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
                    final_plugin_yaml_for_check = repaired_plugin_yaml
            fixed_yaml3 = data3.get('state_yaml', '')
            if fixed_yaml3:
                fixed_dict3 = yaml.safe_load(fixed_yaml3) or {}
                if isinstance(fixed_dict3, dict):
                    fixed_dict = fixed_dict3
                    fixed_yaml_out = yaml.dump(fixed_dict, allow_unicode=True, sort_keys=False)
        except Exception as exc:
            logger.error('[repair/statemachine] PluginSpec retry parse failed: %s', exc)
        pluginspec_err2 = _validate_with_pluginspec(final_plugin_yaml_for_check, fixed_yaml_out)
        if pluginspec_err2:
            logger.warning('[repair/statemachine] PluginSpec still fails after retry: %s', pluginspec_err2)
            raise HTTPException(
                status_code=422,
                detail=f'Repair produced invalid YAML (PluginSpec): {pluginspec_err2}',
            )
        logger.info('[repair/statemachine] PluginSpec retry succeeded')

    # ── Final slot reference check + repair ──────────────────────────────────
    # Run this AFTER all structural/PluginSpec retries so that any retry that
    # regenerates state.yml cannot re-introduce slot errors undetected.
    # This is the single authoritative slot repair pass.
    # Also strip any stray 'slots' block from state.yml — slot definitions belong
    # exclusively in plugin.yaml; the frontend parser ignores them in state.yml but
    # they are confusing and can mask V8 validation errors if left in.
    fixed_dict.pop('slots', None)
    fixed_yaml_out = yaml.dump(fixed_dict, allow_unicode=True, sort_keys=False)
    slot_errors = _validate_slot_references(plugin_dict, fixed_dict)
    if slot_errors:
        logger.info('[repair/statemachine] slot errors on final state: %s', slot_errors)
        plugin_dict, fixed_dict = _repair_slots_only(plugin_dict, fixed_dict, slot_errors, req.llm_config or {})
        fixed_dict.pop('slots', None)
        fixed_yaml_out = yaml.dump(fixed_dict, allow_unicode=True, sort_keys=False)
        remaining_slot_errors = _validate_slot_references(plugin_dict, fixed_dict)
        if remaining_slot_errors:
            logger.warning('[repair/statemachine] slot repair incomplete: %s', remaining_slot_errors)
            remaining.extend([f'slot repair incomplete: {e}' for e in remaining_slot_errors])
        else:
            logger.info('[repair/statemachine] slot repair succeeded')
        repaired_plugin_yaml = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    elif pre_slot_errors:
        # Pre-existing slot errors were fixed by the LLM rewriting state.yml (good path).
        logger.info('[repair/statemachine] pre-existing slot errors resolved by state repair')

    logger.info(
        '[repair/statemachine] SUCCESS state_yaml_len=%d plugin_yaml_updated=%s remaining=%s',
        len(fixed_yaml_out),
        bool(repaired_plugin_yaml),
        remaining,
    )
    return RepairResponse(state_yaml=fixed_yaml_out, plugin_yaml=repaired_plugin_yaml, remaining_warnings=remaining)
