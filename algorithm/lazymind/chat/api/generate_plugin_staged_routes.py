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
import importlib.util
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

# ---------------------------------------------------------------------------
# Phase-specific prompts
# ---------------------------------------------------------------------------

_SKELETON_SYSTEM = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Your task is Phase 1 of a 3-phase plugin generation: produce a plugin.yaml SKELETON.\n\n'
    'The skeleton must include:\n'
    '  - id, name, description, when_to_use\n'
    '  - slots list (each slot: id, label, type, cardinality)\n'
    '  - steps list (each step: id, label) — list of step IDs only, NO execution details\n'
    '  - tool_scripts — ONLY include this section when the plugin genuinely needs custom Python\n'
    '    tools (external API calls, local data processing, complex computation).\n'
    '    DO NOT add tool_scripts for pure LLM reasoning/writing workflows.\n\n'
    'Do NOT include state machine logic or execution prompts — those come in Phase 2.\n\n'
    'Return ONLY a JSON object:\n'
    '  {{"plugin_yaml": "<full plugin.yaml content as YAML string>"}}\n\n'
    'Follow the format specification below.\n\n'
    '=== Plugin Format Specification ===\n'
    '{spec}\n'
    '=== End of Specification ==='
)

_STATE_MACHINE_SYSTEM = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Your task is Phase 2 of a 3-phase plugin generation: produce a complete state.yml.\n\n'
    'The state.yml must include:\n'
    '  - initial: __start__\n'
    '  - transitions (dict): __start__ key holds the entry transitions list; other keys hold per-step transitions\n'
    '  - steps (dict, each step needs: prompt, and optionally inputs/outputs/tools/route/skipif)\n\n'
    'The plugin.yaml skeleton from Phase 1 is provided below as context.\n'
    'Your step IDs in state.yml MUST match the steps in plugin.yaml exactly.\n\n'
    'Return ONLY a JSON object:\n'
    '  {{"state_yaml": "<full state.yml content as YAML string>"}}\n\n'
    'Follow the format specification below.\n\n'
    '=== Plugin Format Specification ===\n'
    '{spec}\n'
    '=== End of Specification ===\n\n'
    '=== Plugin Skeleton (plugin.yaml from Phase 1) ===\n'
    '{plugin_yaml}\n'
    '=== End of Skeleton ==='
)

_SCENARIO_SCRIPTS_SYSTEM = (
    'You are a LazyMind plugin authoring assistant.\n'
    'Your task is Phase 3 of a 3-phase plugin generation:\n'
    '  1. Write scenario.md — a user-facing guide describing what this plugin does,\n'
    '     its workflow steps, and usage notes.\n'
    '  2. Optionally write Python scripts ONLY when strictly necessary (see rules below).\n\n'
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
    'When in doubt, set scripts to {}.\n\n'
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
    'The complete plugin.yaml and state.yml are provided below as context.\n\n'
    'Return ONLY a JSON object:\n'
    '  {{"scenario_md": "<scenario.md content as Markdown string>",\n'
    '    "scripts": {{"scripts/tools.py": "<python code>"}}}}\n'
    '- scripts: set to {{}} when no custom tool scripts are needed.\n'
    '- If you do write a script, only implement functions declared in '
    'plugin.yaml tool_scripts[].functions.\n\n'
    'Follow the scenario.md format specification below.\n\n'
    '=== Plugin Format Specification ===\n'
    '{spec}\n'
    '=== End of Specification ===\n\n'
    '=== plugin.yaml ===\n'
    '{plugin_yaml}\n'
    '=== state.yml ===\n'
    '{state_yaml}\n'
    '=== End of Context ==='
)

# ---------------------------------------------------------------------------
# Required field sets per phase
# ---------------------------------------------------------------------------

_REQUIRED_SKELETON_PLUGIN_TOP = ['id', 'name', 'description', 'steps', 'slots']
_REQUIRED_SKELETON_SLOT_FIELDS = ['id', 'label', 'type', 'cardinality']
_REQUIRED_SKELETON_STEP_FIELDS = ['id', 'label']


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
    return missing


_SKELETON_PATCH_TEMPLATE = (
    'The generated plugin.yaml skeleton has missing fields:\n'
    '{missing_fields}\n\n'
    'Current skeleton:\n{plugin_yaml}\n\n'
    'Return ONLY a JSON patch: {{"plugin": {{...}}}}\n'
    'Fix only the missing fields. No explanation.'
)

_STATE_MACHINE_PATCH_TEMPLATE = (
    'The generated state.yml has missing or invalid fields:\n'
    '{missing_fields}\n\n'
    'Current state.yml:\n{state_yaml}\n\n'
    'plugin.yaml (for reference):\n{plugin_yaml}\n\n'
    'Return ONLY a JSON patch: {{"state": {{...}}}}\n'
    'Fix only the missing fields. No explanation.'
)


def _patch_skeleton(
    plugin_dict: Dict[str, Any],
    missing: List[str],
    system_prompt: str,
) -> Dict[str, Any]:
    plugin_yaml_str = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    patch_prompt = _SKELETON_PATCH_TEMPLATE.format(
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


def _patch_state_machine(
    plugin_dict: Dict[str, Any],
    state_dict: Dict[str, Any],
    missing: List[str],
    system_prompt: str,
) -> Dict[str, Any]:
    plugin_yaml_str = yaml.dump(plugin_dict, allow_unicode=True, sort_keys=False)
    state_yaml_str = yaml.dump(state_dict, allow_unicode=True, sort_keys=False)
    patch_prompt = _STATE_MACHINE_PATCH_TEMPLATE.format(
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
    'File: {filename}\n'
    'Problem node ({node_label} "{node_name}"):\n'
    '```python\n{node_source}\n```\n'
    'Error: {error}\n\n'
    'Rules:\n'
    '  Allowed HTTP library: httpx (not requests).\n'
    '  Allowed stdlib: json, re, math, base64, hashlib, urllib.parse, datetime, typing, httpx.\n'
    '  Forbidden: exec, eval, open, compile, getattr, setattr, __import__, globals, locals, vars,\n'
    '             pickle, subprocess, socket, shutil, requests, inspect, tempfile,\n'
    '             os.system, os.popen, os.remove, os.rmdir, os.unlink, os.rename, os.environ,\n'
    '             sys.exit, sys.modules.\n\n'
    'Full current script for context:\n'
    '```python\n{full_source}\n```\n\n'
    'Return ONLY a JSON object with the corrected FULL script (do not omit other functions):\n'
    '{{"fixed_source": "<complete corrected Python source>"}}\n'
    'No explanation.'
)

_DRY_RUN_FIX_TEMPLATE = (
    'A Python script fails to import (dry-run import error).\n\n'
    'File: {filename}\n'
    'Error: {error}\n\n'
    'Full current script:\n'
    '```python\n{full_source}\n```\n\n'
    'Fix the script so it can be imported without errors.\n'
    'Return ONLY a JSON object with the corrected FULL script:\n'
    '{{"fixed_source": "<complete corrected Python source>"}}\n'
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
# Request / Response models
# ---------------------------------------------------------------------------

class _LLMConfigMixin(BaseModel):
    llm_config: Dict[str, Any] = Field(default_factory=dict)


class SkeletonRequest(_LLMConfigMixin):
    name: str
    description: Optional[str] = None
    skill_content: Optional[str] = None


class SkeletonResponse(BaseModel):
    plugin_yaml: str


class StateMachineRequest(_LLMConfigMixin):
    name: str
    plugin_yaml: str  # output from Phase 1


class StateMachineResponse(BaseModel):
    state_yaml: str


class ScenarioScriptsRequest(_LLMConfigMixin):
    name: str
    plugin_yaml: str   # output from Phase 1
    state_yaml: str    # output from Phase 2


class ScenarioScriptsResponse(BaseModel):
    scenario_md: str = ''
    scripts: Dict[str, str] = Field(default_factory=dict)


# ---------------------------------------------------------------------------
# Phase handlers
# ---------------------------------------------------------------------------

@router.post(
    '/api/chat/generate_plugin/skeleton',
    response_model=SkeletonResponse,
    summary='Phase 1: Generate plugin.yaml skeleton',
)
async def generate_skeleton(req: SkeletonRequest) -> SkeletonResponse:
    """Phase 1: generate plugin.yaml skeleton (slots + steps list, no state logic)."""
    inject_model_config(req.llm_config or {})

    system_prompt = _SKELETON_SYSTEM.format(spec=_PLUGIN_FORMAT_SPEC)
    if req.skill_content and req.skill_content.strip():
        user_prompt = (
            f'Plugin name: {req.name}\n\n'
            f'Convert the following skill content into a plugin skeleton:\n\n{req.skill_content}'
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

    plugin_yaml = data.get('plugin_yaml', '')
    if not plugin_yaml:
        raise HTTPException(status_code=500, detail='Phase 1: missing plugin_yaml in response')

    try:
        plugin_dict = yaml.safe_load(plugin_yaml) or {}
    except yaml.YAMLError as exc:
        raise HTTPException(status_code=500, detail=f'Phase 1 YAML parse error: {exc}') from exc

    # Validate skeleton fields + patch retry
    for attempt in range(MAX_PATCH_RETRIES):
        missing = _check_skeleton_missing(plugin_dict)
        if not missing:
            break
        logger.info('[staged/skeleton] attempt=%d missing: %s', attempt + 1, missing)
        plugin_dict = _patch_skeleton(plugin_dict, missing, system_prompt)
    else:
        missing = _check_skeleton_missing(plugin_dict)
        if missing:
            raise HTTPException(
                status_code=500,
                detail=f'Phase 1: missing fields after retries: {missing}',
            )

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

    system_prompt = _STATE_MACHINE_SYSTEM.format(
        spec=_PLUGIN_FORMAT_SPEC,
        plugin_yaml=req.plugin_yaml,
    )
    user_prompt = (
        f'Plugin name: {req.name}\n\n'
        'Generate the complete state.yml for this plugin, including transitions and step prompts.'
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

    # Validate state machine fields + patch retry
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
            raise HTTPException(
                status_code=500,
                detail=f'Phase 2: missing fields after retries: {missing}',
            )

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
            raise HTTPException(status_code=500, detail=f'Phase 2 PluginSpec validation failed: {err}')

    return StateMachineResponse(state_yaml=state_yaml)


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

    system_prompt = _SCENARIO_SCRIPTS_SYSTEM.format(
        spec=_PLUGIN_FORMAT_SPEC,
        plugin_yaml=req.plugin_yaml,
        state_yaml=req.state_yaml,
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
        raise HTTPException(status_code=500, detail=f'Phase 3 JSON parse error: {exc}') from exc

    scenario_md = data.get('scenario_md', '')
    scripts: Dict[str, str] = data.get('scripts') or {}

    if not scenario_md:
        logger.warning('[staged/scenario_scripts] scenario_md is empty, using fallback')
        scenario_md = f'# {req.name}\n\n## 场景描述\n\n该插件由 AI 自动生成。\n'

    # --- Node-level security check + dry-run import + fix retry loop ---
    safe_scripts: Dict[str, str] = {}
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
                fix_prompt = _DRY_RUN_FIX_TEMPLATE.format(
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
                fix_prompt = _NODE_FIX_TEMPLATE.format(
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
                fix_prompt = _DRY_RUN_FIX_TEMPLATE.format(
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

    return ScenarioScriptsResponse(scenario_md=scenario_md, scripts=safe_scripts)
