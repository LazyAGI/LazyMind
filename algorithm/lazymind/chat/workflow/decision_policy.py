"""LazyMind Host binding for the authoritative shared Workflow policy."""

from __future__ import annotations

import os
from pathlib import Path
from typing import Mapping


POLICY_FLAG = 'LAZYMIND_WORKFLOW_POLICY_V1'
POLICY_VERSION = 'workflow.policy.v1'
legacy_policy_hits = 0


def shared_policy_enabled(environ: Mapping[str, str] | None = None) -> bool:
    """Default-on policy switch; false is the bounded rollback path."""
    value = (environ or os.environ).get(POLICY_FLAG, '1')
    return value.strip().lower() not in {'0', 'false', 'no', 'off'}


def record_legacy_policy_call() -> None:
    global legacy_policy_hits
    legacy_policy_hits += 1


def _skill_root() -> Path:
    return Path(__file__).resolve().parents[4] / 'skills' / 'workflow-agent-kit'


def shared_decision_prompt() -> str:
    """Load the canonical lifecycle policy directly from the shared Skill pack."""
    root = _skill_root()
    policy = (root / 'references' / 'decision-policy.md').read_text(encoding='utf-8')
    return (
        '## Shared Workflow Decision Policy [AUTHORITATIVE]\n\n'
        f'Policy version: {POLICY_VERSION}. Runtime projection is authoritative.\n\n'
        + policy
    )


def lazymind_host_prompt(workflow_mode: str) -> str:
    """Describe Host capabilities without restating shared lifecycle decisions."""
    driver = workflow_mode == 'auto'
    return (
        '\n\n## LazyMind Host Profile\n\n'
        '- This Host supports synchronous waiting and durable handoff.\n'
        '- `advance_step_and_hand_off` is the canonical handoff tool name.\n'
        f'- Driver is {"enabled" if driver else "disabled"}; this changes only turn orchestration.\n'
        '- A handoff ends the ChatAgent turn only after durable Supervisor ownership.\n'
        '- Driver, approval, stop-tool, and synthetic-turn behavior never change Runtime projection, '
        'Attempt operation, or Artifact lineage.\n'
    )
