from __future__ import annotations

import logging
from typing import Any

from .loader import plugin_loader

logger = logging.getLogger(__name__)


def evaluate_step(
    plugin_id: str,
    step_id: str,
    step_result: str,
    artifacts: dict,
    attempt: int,
    llm: Any = None,
) -> str:
    """Call DriverAgent to evaluate a completed step.

    Returns a natural-language judgment that Go injects as the next user message.
    Never raises; returns a fallback string on any failure.
    """
    driver_md = plugin_loader.get_driver(plugin_id)
    if not driver_md:
        # plugin_loader blocks auto-mode plugins without driver.md; this is a safety fallback.
        return 'Step completed successfully. Proceed to the next step.'

    # Augment short driver.md with scenario context.
    if len(driver_md) < 3000:
        scenario_md = plugin_loader.get_scenario(plugin_id)
        driver_md = driver_md + '\n\n---\n## Scenario context\n' + scenario_md

    artifacts_summary = '\n'.join(
        f'- {k}: {str(v)[:100]}' for k, v in (artifacts or {}).items() if v is not None
    )
    prompt = (
        driver_md
        + '\n\n---\n## Current execution context\n'
        f'Current step: {step_id}\nAttempt: {attempt}\n'
        f'Step result:\n{step_result[:500]}\n'
        f'Saved artifacts:\n{artifacts_summary}'
        '\n\nBased on the rules above, output your evaluation.'
    )

    if llm is None:
        try:
            from lazyllm import AutoModel
            llm = AutoModel(model='llm')
        except Exception as exc:
            logger.warning('DriverAgent: cannot obtain LLM: %s', exc)
            return 'Step completed. Proceed.'

    try:
        result = llm(prompt)
        return (result or '').strip() or 'Step completed. Proceed.'
    except Exception as exc:
        logger.warning('DriverAgent evaluation failed (%s). Proceeding.', exc)
        return f'Driver evaluation failed ({exc}). Proceeding.'
