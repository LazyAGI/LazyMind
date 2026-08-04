"""Feature-flagged observation of shared Workflow policy decisions."""

import json
import logging
import os
from typing import Any, Mapping

from lazymind.workflow_policy import Decision, decide, shadow_trace


LOGGER = logging.getLogger(__name__)
SHADOW_FLAG = 'LAZYMIND_WORKFLOW_POLICY_SHADOW'
COMPARE_FLAG = 'LAZYMIND_WORKFLOW_POLICY_COMPARE'
TRACE_LIMIT = 100


def enabled(environ: Mapping[str, str] | None = None) -> bool:
    env = environ or os.environ
    shadow = env.get(SHADOW_FLAG, '').strip().lower() in {'1', 'true', 'yes', 'on'}
    compare = env.get(COMPARE_FLAG, '1').strip().lower() not in {'0', 'false', 'no', 'off'}
    return shadow or compare


def _legacy_decision(projection: Mapping[str, Any], profile: Mapping[str, Any]) -> Decision:
    """Normalize the still-authoritative LazyMind prompt policy for comparison."""
    ready = tuple(str(item) for item in projection.get('ready_steps', ()) if item)
    if not ready:
        return Decision('observe', 'get_workflow_state', None, reason_code='no_ready_steps')
    attempted = set(map(str, projection.get('attempted_steps', ())))
    target = str(projection.get('changed_succeeded_step') or ready[0])
    retrying = target in attempted or target in set(map(str, projection.get('failed_steps', ())))
    targets = ready if profile.get('parallel_ready_steps') and not retrying else (target,)
    intents = set(projection.get('intent_tokens') or ())
    approval = projection.get('approval_by_step') or {}
    wait = bool({'continuous', 'run_all', 'boundary'} & intents) or all(
        approval.get(item) == 'not_required' for item in targets
    )
    # The legacy prompt still exposes the historical ``hand_off`` spelling.
    # Normalize it to the versioned public protocol name before comparison so
    # shadow equivalence measures decisions, not a temporary Host adapter alias.
    tool = 'advance_step' if wait or not profile.get('handoff') else 'advance_step_and_hand_off'
    return Decision('advance', tool, targets[0], targets, 'legacy_prompt_policy')


def observe(
    projection: Mapping[str, Any],
    profile: Mapping[str, Any],
    sink: dict[str, Any],
    *,
    source: str,
) -> dict[str, Any] | None:
    """Compare and record one decision without changing the authoritative result."""
    if not enabled():
        return None
    legacy = _legacy_decision(projection, profile)
    shared = decide(projection, profile, projection.get('intent_tokens', ()))
    policy_value = os.environ.get('LAZYMIND_WORKFLOW_POLICY_V1', '1').strip().lower()
    authority = 'legacy' if policy_value in {'0', 'false', 'no', 'off'} else 'shared'
    trace = shadow_trace(
        legacy, shared, authority=authority, source=source, profile=profile.get('profile'),
    )
    metrics = sink.setdefault('workflow_policy_shadow_metrics', {
        'evaluated': 0, 'equivalent': 0, 'mismatch': 0,
    })
    metrics['evaluated'] += 1
    key = 'equivalent' if trace['equivalent'] else 'mismatch'
    metrics[key] += 1
    traces = sink.setdefault('workflow_policy_shadow_traces', [])
    traces.append(trace)
    del traces[:-TRACE_LIMIT]
    LOGGER.info('workflow_policy_shadow %s', json.dumps(trace, sort_keys=True))
    return trace


def equivalence_rate(metrics: Mapping[str, int]) -> float:
    evaluated = int(metrics.get('evaluated', 0))
    return int(metrics.get('equivalent', 0)) / evaluated if evaluated else 1.0
