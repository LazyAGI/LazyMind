"""Load package-declared actions without embedding domain policy in Chat routing."""
from copy import deepcopy
from dataclasses import dataclass
import re
from typing import Any, Callable


@dataclass
class CompletedAction:
    tool: Callable
    routing: str


def _strings(value: Any) -> bool:
    return isinstance(value, list) and bool(value) and all(isinstance(x, str) and x.strip() for x in value)


def _bind_action(spec: dict, session_id: str, register: Callable) -> CompletedAction:
    definition = deepcopy(spec)

    def execute(instruction: str) -> dict:
        if not instruction.strip():
            raise ValueError('A complete edit instruction is required')
        from lazymind.chat.engine.tools.subagent_chat_tools import create_subagent
        params = deepcopy(definition.get('params') or {})
        for key in definition.get('session_params') or []:
            params[key] = session_id
        return create_subagent(
            agent_type=definition['agent_type'], title=definition['title'],
            objective=instruction, params=params,
            output_slots=definition['output_slots'], tools=definition['tools'],
        )

    execute.__name__ = definition['name']
    execute.__doc__ = definition['description']
    return CompletedAction(register(execute), definition['routing'])


def load_completed_actions(toolkit: Any, session_id: str, policy: dict,
                           *, register: Callable) -> list[CompletedAction]:
    """Bind declared SubAgent actions only to one effective selected artifact.

    Business instructions, tools, output contracts and routing live in the package.
    Session scope is supplied by the host and never accepted from model arguments.
    No declarations means no artifact lookup and no additional tools.
    """
    declarations = policy.get('completed_actions')
    if not isinstance(declarations, list) or not declarations:
        return []
    rows = toolkit.list_artifacts(session_id).get('artifacts') or []
    actions = []
    names = set()
    for spec in declarations:
        if not isinstance(spec, dict):
            continue
        required = ('name', 'requires_slot', 'description', 'agent_type', 'title', 'routing')
        if any(not isinstance(spec.get(key), str) or not spec[key].strip() for key in required):
            continue
        name = spec['name']
        if (not re.fullmatch(r'[a-z][a-z0-9_]*', name) or name in names
                or name in {'advance_step', 'advance_step_and_hand_off', 'get_ready_steps'}):
            continue
        if not _strings(spec.get('tools')) or not _strings(spec.get('output_slots')):
            continue
        if not isinstance(spec.get('params', {}), dict):
            continue
        bindings = spec.get('session_params', [])
        if not isinstance(bindings, list) or any(not isinstance(k, str) or not k for k in bindings):
            continue
        matches = [row for row in rows if isinstance(row, dict)
                   and row.get('slot') == spec['requires_slot']
                   and row.get('selected', True) and not row.get('deleted')
                   and row.get('validity', 'effective') == 'effective']
        if len(matches) != 1:
            continue
        actions.append(_bind_action(spec, session_id, register))
        names.add(name)
    return actions
