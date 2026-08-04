"""Deterministic, host-neutral Workflow v1 decision policy.

This module is deliberately free of LazyMind runtime imports so the same cases can
be evaluated by other hosts. The shared Skill policy is authoritative by default;
Host adapters may compare it with the bounded legacy rollback implementation.
"""

from dataclasses import asdict, dataclass
from typing import Any, Mapping, Sequence


POLICY_VERSION = 'workflow.policy.v1'


@dataclass(frozen=True)
class Decision:
    action: str
    tool: str | None
    target: str | None
    targets: tuple[str, ...] = ()
    reason_code: str = 'unspecified'


def _step_id(value: Any) -> str:
    if isinstance(value, Mapping):
        return str(value.get('step_id') or value.get('id') or '')
    return str(value or '')


def _ready_steps(projection: Mapping[str, Any]) -> tuple[str, ...]:
    return tuple(filter(None, (_step_id(item) for item in projection.get('ready_steps', ()))))


def _intent_tokens(user_intent: Any) -> set[str]:
    if isinstance(user_intent, str):
        return {user_intent.strip().lower()} if user_intent.strip() else set()
    if isinstance(user_intent, Mapping):
        return {
            str(key).lower() for key, enabled in user_intent.items() if enabled
        }
    if isinstance(user_intent, Sequence):
        return {str(item).strip().lower() for item in user_intent if str(item).strip()}
    return set()


def _advance_tool(profile: Mapping[str, Any], *, wait: bool) -> str:
    tools = tuple(profile.get('advance_tools') or ('advance_step',))
    if wait or not profile.get('handoff'):
        return 'advance_step' if 'advance_step' in tools else str(tools[0])
    if 'advance_step_and_hand_off' in tools:
        return 'advance_step_and_hand_off'
    return 'advance_step_and_hand_off'


def decide(
    projection: Mapping[str, Any],
    profile: Mapping[str, Any],
    user_intent: Any = '',
) -> Decision:
    """Return the shared policy decision for a normalized Runtime projection."""
    intents = _intent_tokens(user_intent)
    missing = tuple(str(item) for item in projection.get('missing_inputs', ()) if item)
    if missing:
        return Decision('request_input', None, None, reason_code='missing_inputs')
    if projection.get('status') == 'stopped':
        if 'resume' in intents:
            return Decision('resume', 'resume_workflow', None, reason_code='resume_requested')
        return Decision('observe', 'get_workflow_state', None, reason_code='session_stopped')

    ready = _ready_steps(projection)
    if not ready:
        return Decision('observe', 'get_workflow_state', None, reason_code='no_ready_steps')

    attempted = set(map(str, projection.get('attempted_steps', ())))
    changed = str(projection.get('changed_succeeded_step') or '')
    target = changed if changed and changed in ready else ready[0]
    retrying = target in attempted or target in set(map(str, projection.get('failed_steps', ())))

    parallel = bool(profile.get('parallel_ready_steps')) and not retrying
    selected = ready if parallel else (target,)
    continuous = bool({'continuous', 'run_all', 'boundary'} & intents)
    approval = projection.get('approval_by_step') or {}
    approval_free = all(approval.get(item) == 'not_required' for item in selected)
    wait = continuous or approval_free
    tool = _advance_tool(profile, wait=wait)
    reason = (
        'retry_or_rewind_target' if retrying or changed
        else 'continuous_execution' if continuous
        else 'approval_not_required' if approval_free
        else 'ready_frontier'
    )
    return Decision('advance', tool, selected[0], selected, reason)


def shadow_trace(legacy: Decision, shared: Decision, *, authority: str = 'legacy',
                 **context: Any) -> dict[str, Any]:
    """Build a structured, non-authoritative comparison trace."""
    dimensions = {
        'action': legacy.action == shared.action,
        'tool': legacy.tool == shared.tool,
        'target': legacy.target == shared.target,
        'targets': legacy.targets == shared.targets,
    }
    return {
        'schema_version': 'workflow.shadow-trace.v1',
        'policy_version': POLICY_VERSION,
        'authority': authority,
        'legacy': asdict(legacy),
        'shared': asdict(shared),
        'dimensions': dimensions,
        'equivalent': all(dimensions.values()),
        'context': context,
    }
