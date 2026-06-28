"""DriverAgent — evaluates a completed plugin step and emits a natural-language assessment.

The DriverAgent is powered by the configured LLM and uses the plugin's driver.md prompt
as its system instruction.  Its output is a concise natural-language message that describes
whether the step result is acceptable and, if not, why.

The message is passed verbatim as a synthetic user turn to the ChatAgent, which then
decides autonomously how to proceed (advance to next step, retry, rewind, or complete
the plugin by calling advance_step with step_id='__end__').
"""
from __future__ import annotations

import re
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import AutoModel, LOG

from lazymind.chat.plugin import plugin_loader
from lazymind.model_config import inject_model_config

# Matches thinking blocks emitted by reasoning models (open/close tag variants).
_LT, _GT = chr(60), chr(62)
_THINK_BLOCK_RE = re.compile(
    rf'{_LT}(?:redacted_thinking|think){_GT}.*?'
    rf'(?:{_LT}/(?:redacted_thinking|think)\s*{_GT}|{_LT}/think{_GT})',
    re.DOTALL | re.IGNORECASE,
)


class DriverEvaluationError(Exception):
    """Raised when DriverAgent cannot produce a usable assessment for auto-mode advance."""


_DEFAULT_DRIVER_PROMPT = (
    'You are a quality evaluator for a plugin workflow step.\n\n'
    'Your task: assess whether the step result is complete and acceptable.\n\n'
    '## Output rules (STRICT)\n\n'
    '- Write 1-2 sentences of plain natural language.\n'
    '- If the step result is acceptable: briefly state what was completed and that it looks good.\n'
    '- If the step result is NOT acceptable: state what is missing or wrong, and the likely cause.\n'
    '- Do NOT output PASS/RETRY/DONE/FAIL or any other verdict codes.\n'
    '- Do NOT output bullet lists, headers, or analysis beyond the verdict sentence.\n'
    '- Do NOT output any thinking process or preamble.\n'
    '- Keep the message under 60 words.\n\n'
    'Good examples:\n'
    '  "subject_analysis artifact saved with 120 words covering subject, style, and lighting."\n'
    '  "optimized_prompt saved: 65-word English prompt with style modifiers."\n'
    '  "enhanced_image_url saved successfully. The pipeline is complete."\n'
    '  "No optimized_prompt artifact found in the step output; the prompt generation likely failed silently."\n'
    '  "The generated image is off-topic — the subject analysis may have misidentified the subject; '
    'consider re-running analyze_subject."\n'
)

# Appended after the plugin-supplied or default prompt to enforce concise output.
_OUTPUT_CONSTRAINT = (
    '\n\n## Output format constraint (MANDATORY)\n\n'
    'Your entire response is injected verbatim as the next simulated user message in the chat.\n'
    'Write 1-2 plain sentences only (max 60 words).\n'
    'No verdict codes (PASS/RETRY/DONE/FAIL), no tags, no preamble, no thinking.\n'
    'Just describe what happened and, if something is wrong, why.'
)


def _build_driver_prompt(plugin_id: str) -> str:
    driver_md = plugin_loader.get_driver(plugin_id)
    base = driver_md if driver_md is not None else _DEFAULT_DRIVER_PROMPT
    return base + _OUTPUT_CONSTRAINT


def _init_driver_sid(session_id: Optional[str], plugin_id: str, step_id: str) -> str:
    """Isolate DriverAgent globals per evaluation request."""
    sid = session_id or f'driver_{plugin_id}_{step_id}'
    lazyllm.globals._init_sid(sid=sid)
    lazyllm.locals._init_sid(sid=sid)
    return sid


def _init_driver_artifact_context(
    session_id: Optional[str],
    plugin_id: str,
    step_id: str,
) -> Any:
    """Set agentic_config and a minimal SubAgentContext so artifact tools can read from DB."""
    lazyllm.globals['agentic_config'] = {
        'plugin_id': plugin_id,
        'plugin_session_id': session_id or '',
        'plugin_step': step_id,
    }
    if not session_id:
        return None

    try:
        from lazymind.config import config as _cfg
        from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
        from lazymind.chat.engine.subagent.db import SubAgentDB

        dsn = str(_cfg['acl_db_dsn'] or '').strip()
        if not dsn:
            return None

        import tempfile
        db = SubAgentDB(dsn)
        ctx = SubAgentContext(
            task_id=f'driver_{session_id}_{step_id}',
            conversation_id='',
            agent_type='driver',
            objective='',
            params={'session_id': session_id, 'plugin_id': plugin_id, 'step_id': step_id},
            workspace_path=tempfile.mkdtemp(prefix='driver_'),
            input_artifact_keys=[],
            output_artifact_keys=[],
            db=db,
            emit=lambda _ev: None,
        )
        set_context(ctx)
        return db
    except Exception as exc:
        LOG.warning('[DriverAgent] failed to init artifact context: %s', exc)
        return None


def _build_llm(llm_config: Optional[Dict[str, Any]]) -> Any:
    """Build an LLM instance after injecting per-request model config (same as ChatAgent/SubAgent)."""
    inject_model_config(llm_config)
    return AutoModel(model='llm')


def evaluate_step(
    plugin_id: str,
    step_id: str,
    step_result: str,
    session_id: Optional[str] = None,
    user_files: Optional[List[str]] = None,
    llm_config: Optional[Dict[str, Any]] = None,
    plugin_artifacts_summary: Optional[str] = None,
) -> Dict[str, Any]:
    """Evaluate a completed plugin step and return a natural-language assessment message.

    Args:
        plugin_id: The plugin identifier.
        step_id: The completed step identifier.
        step_result: The step summary / artifact description to evaluate.
        session_id: Optional session ID for contextual evaluation.
        user_files: Optional list of user-uploaded file paths available for this step.
        llm_config: Optional LLM configuration dict (API key, model name, etc.) aligned
            with ChatAgent/SubAgent configs.
        plugin_artifacts_summary: Optional text summary of all artifacts produced so far
            in this plugin session, for richer quality assessment.

    Returns:
        dict with key: message (str) — a concise natural-language assessment.

    Raises:
        DriverEvaluationError: when the plugin is missing or the LLM cannot produce output.
    """
    import os as _os

    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        raise DriverEvaluationError(f'Plugin {plugin_id!r} not found; cannot evaluate step.')

    step_config = spec.get_step_config(step_id)
    acceptance = step_config.get('acceptance_criteria', '')
    accept_prompt = (
        f'\n\nAcceptance criteria for step {step_id!r}:\n{acceptance}'
        if acceptance else ''
    )

    driver_prompt = _build_driver_prompt(plugin_id) + accept_prompt

    user_msg = (
        f'Plugin: {plugin_id}\n'
        f'Step: {step_id}\n'
        f'Step result:\n{step_result}\n\n'
    )
    if plugin_artifacts_summary:
        user_msg += f'Session artifacts produced so far:\n{plugin_artifacts_summary}\n\n'
    user_msg += 'Describe whether the step result is complete and acceptable.'

    if user_files:
        file_list = ', '.join(_os.path.basename(f) for f in user_files)
        user_msg += f'\n\nUser-uploaded files available for this step: {file_list}'

    # Inject artifact read tools so DriverAgent can inspect produced artifacts.
    tools = []
    try:
        from lazymind.chat.engine.subagent.tools import find_artifact, get_artifact
        tools = [find_artifact, get_artifact]
    except Exception:
        pass

    driver_db = None
    try:
        _init_driver_sid(session_id, plugin_id, step_id)
        driver_db = _init_driver_artifact_context(session_id, plugin_id, step_id)
        llm = _build_llm(llm_config)
        if tools:
            response = llm(user_msg, system_prompt=driver_prompt, tools=tools)
        else:
            response = llm(user_msg, system_prompt=driver_prompt)
        cleaned = _clean_message(str(response or ''))
        if cleaned:
            return {'message': cleaned}
        raise DriverEvaluationError(
            f'DriverAgent returned empty assessment for plugin={plugin_id!r} step={step_id!r}.',
        )
    except DriverEvaluationError:
        raise
    except Exception as exc:
        LOG.warning('[DriverAgent] LLM call failed for plugin=%s step=%s: %s', plugin_id, step_id, exc)
        raise DriverEvaluationError(
            f'DriverAgent LLM call failed for plugin={plugin_id!r} step={step_id!r}: {exc}',
        ) from exc
    finally:
        if driver_db is not None:
            try:
                driver_db.dispose()
            except Exception:
                pass


def _clean_message(text: str) -> str:
    """Strip thinking tokens, tags, and excess whitespace from the LLM output."""
    text = _THINK_BLOCK_RE.sub('', text)
    # Remove any stray XML-style tags
    text = re.sub(r'<[^>]+>', '', text)
    text = text.strip()
    # Truncate at the 3rd sentence boundary as a safety net (keep up to 2 sentences)
    sentence_count = 0
    cutoff = len(text)
    for sep in ('。', '. ', '.\n'):
        pos = 0
        while True:
            idx = text.find(sep, pos)
            if idx < 0:
                break
            sentence_count += 1
            if sentence_count >= 2:
                cutoff = min(cutoff, idx + len(sep))
                break
            pos = idx + len(sep)
    text = text[:cutoff].strip()
    # Hard cap at 300 chars
    if len(text) > 300:
        text = text[:300].rstrip() + '...'
    return text
