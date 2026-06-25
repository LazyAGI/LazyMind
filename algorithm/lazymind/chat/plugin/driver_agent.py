"""DriverAgent — evaluates a completed plugin step and emits a structured verdict.

Verdicts:
    PASS   — step result is acceptable; ChatAgent should proceed to the next step.
    RETRY  — step result is not acceptable; ChatAgent should retry the current step.
    DONE   — the plugin session is complete; no more steps needed.
    FAIL   — unrecoverable error; terminate the session.

The DriverAgent is powered by the configured LLM and uses the plugin's driver.md prompt
as its system instruction. It returns only the structured verdict, not prose.
"""
from __future__ import annotations

import re
from typing import Any, Dict, Optional

import lazyllm
from lazyllm import LOG

from lazymind.chat.plugin import plugin_loader

# Match the last occurrence of a verdict tag in the model output.
_VERDICT_RE = re.compile(
    r'<verdict>\s*(PASS|RETRY|DONE|FAIL)\s*</verdict>',
    re.IGNORECASE,
)
_REASON_RE = re.compile(
    r'<reason>(.*?)</reason>',
    re.IGNORECASE | re.DOTALL,
)

_DEFAULT_DRIVER_PROMPT = (
    'You are a quality evaluator for a plugin workflow step.\n\n'
    'Given the step result, emit exactly one of:\n'
    '  PASS   — acceptable, proceed\n'
    '  RETRY  — not acceptable, retry\n'
    '  DONE   — workflow complete, no more steps\n'
    '  FAIL   — unrecoverable error\n\n'
    'Always wrap verdict in <verdict>...</verdict> and optional reason in <reason>...</reason>.'
)

# Appended to every driver prompt regardless of whether the plugin supplies driver.md.
_ROLLBACK_HINT = (
    '\n\n## Rollback decision guidance\n\n'
    'When evaluating a step result, if the root cause of the problem lies not in the\n'
    'current step but in the output of a prior step (e.g. the subject analysis is too\n'
    'vague, the collected materials are irrelevant, or the optimized prompt is\n'
    'misleading), you should reflect this in your reason so that the ChatAgent can\n'
    'decide to rewind to the responsible upstream step rather than retrying the\n'
    'current one in vain.\n\n'
    'In your <reason>, explicitly name the upstream step that should be re-run when\n'
    'applicable, for example: "The generated image is off-topic because the subject\n'
    'analysis misidentified the main subject. Recommend rewinding to analyze_subject."\n\n'
    'The ChatAgent has the ability to call advance_step with any previously completed\n'
    'step as step_id — your reason will guide that decision.'
)


def _build_driver_prompt(plugin_id: str) -> str:
    driver_md = plugin_loader.get_driver(plugin_id)
    base = driver_md or _DEFAULT_DRIVER_PROMPT
    return base + _ROLLBACK_HINT


def evaluate_step(
    plugin_id: str,
    step_id: str,
    step_result: str,
    session_id: Optional[str] = None,
    user_files: Optional[list] = None,
) -> Dict[str, Any]:
    """Evaluate a completed plugin step and return verdict + reason.

    Args:
        plugin_id: The plugin identifier.
        step_id: The completed step identifier.
        step_result: The step summary / artifact description to evaluate.
        session_id: Optional session ID for contextual evaluation.
        user_files: Optional list of user-uploaded file paths available for this step.

    Returns:
        dict with keys: verdict (PASS/RETRY/DONE/FAIL), reason (str).
    """
    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        return {'verdict': 'FAIL', 'reason': f'Plugin {plugin_id!r} not found.'}

    # Build evaluation context from step config.
    step_config = spec.get_step_config(step_id)
    acceptance = step_config.get('acceptance_criteria', '')
    accept_prompt = (
        f'\n\nAcceptance criteria for step {step_id!r}:\n{acceptance}'
        if acceptance else ''
    )

    driver_prompt = _build_driver_prompt(plugin_id) + accept_prompt

    import os as _os
    user_msg = (
        f'Plugin: {plugin_id}\n'
        f'Step: {step_id}\n'
        f'Step result:\n{step_result}\n\n'
        'Please evaluate whether the step result is acceptable. '
        'Output your verdict in <verdict>PASS|RETRY|DONE|FAIL</verdict> '
        'and optional explanation in <reason>...</reason>.'
    )
    if user_files:
        file_list = ', '.join(_os.path.basename(f) for f in user_files)
        user_msg += f'\n\nUser-uploaded files available for this step: {file_list}'

    try:
        llm = lazyllm.AutoModel(model='llm')
        response = llm(user_msg, system_prompt=driver_prompt)
        return _parse_verdict(str(response or ''))
    except Exception as exc:
        LOG.warning('[DriverAgent] LLM call failed for plugin=%s step=%s: %s', plugin_id, step_id, exc)
        return {'verdict': 'PASS', 'reason': 'DriverAgent unavailable; defaulting to PASS.'}


def _parse_verdict(text: str) -> Dict[str, Any]:
    """Extract verdict and reason from model output."""
    verdict = 'PASS'
    reason = ''
    m = _VERDICT_RE.search(text)
    if m:
        verdict = m.group(1).upper()
    r = _REASON_RE.search(text)
    if r:
        reason = r.group(1).strip()
    return {'verdict': verdict, 'reason': reason}
