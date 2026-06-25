"""DriverAgent — evaluates a completed plugin step and emits a natural-language assessment.

The DriverAgent is powered by the configured LLM and uses the plugin's driver.md prompt
as its system instruction.  Its output is a concise natural-language message that describes
whether the step result is acceptable and, if not, why.

The message is passed verbatim as a synthetic user turn to the ChatAgent, which then
decides autonomously how to proceed (advance to next step, retry, rewind, or complete
the plugin by calling advance_step with step_id='__end__').
"""
from __future__ import annotations

from typing import Any, Dict, Optional

import lazyllm
from lazyllm import LOG

from lazymind.chat.plugin import plugin_loader

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
    'Your entire response must be 1-2 plain sentences (max 60 words).\n'
    'No verdict codes (PASS/RETRY/DONE/FAIL), no tags, no preamble, no thinking.\n'
    'Just describe what happened and, if something is wrong, why.'
)


def _build_driver_prompt(plugin_id: str) -> str:
    driver_md = plugin_loader.get_driver(plugin_id)
    base = driver_md if driver_md is not None else _DEFAULT_DRIVER_PROMPT
    return base + _OUTPUT_CONSTRAINT


def evaluate_step(
    plugin_id: str,
    step_id: str,
    step_result: str,
    session_id: Optional[str] = None,
    user_files: Optional[list] = None,
) -> Dict[str, Any]:
    """Evaluate a completed plugin step and return a natural-language assessment message.

    Args:
        plugin_id: The plugin identifier.
        step_id: The completed step identifier.
        step_result: The step summary / artifact description to evaluate.
        session_id: Optional session ID for contextual evaluation.
        user_files: Optional list of user-uploaded file paths available for this step.

    Returns:
        dict with key: message (str) — a concise natural-language assessment.
    """
    import os as _os

    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        return {'message': f'Plugin {plugin_id!r} not found; cannot evaluate step.'}

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
        'Describe whether the step result is complete and acceptable.'
    )
    if user_files:
        file_list = ', '.join(_os.path.basename(f) for f in user_files)
        user_msg += f'\n\nUser-uploaded files available for this step: {file_list}'

    try:
        llm = lazyllm.AutoModel(model='llm')
        response = llm(user_msg, system_prompt=driver_prompt)
        cleaned = _clean_message(str(response or ''))
        if cleaned:
            return {'message': cleaned}
        return {'message': f"Step '{step_id}' completed (evaluation unavailable)."}
    except Exception as exc:
        LOG.warning('[DriverAgent] LLM call failed for plugin=%s step=%s: %s', plugin_id, step_id, exc)
        return {'message': f"Step '{step_id}' completed (evaluation unavailable)."}


def _clean_message(text: str) -> str:
    """Strip thinking tokens, tags, and excess whitespace from the LLM output."""
    import re
    # Remove <think>...</think> blocks
    text = re.sub(r'<think>.*?</think>', '', text, flags=re.DOTALL | re.IGNORECASE)
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
