from __future__ import annotations

from typing import Any, Callable, Optional

from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor
from lazymind.chat.engine.agent_runtime.projection_state import projection_fingerprint

from .tools import PreferenceOrganizerGate


def make_preference_organizer_compactor(
    gate: PreferenceOrganizerGate,
    *,
    llm_config: Optional[dict[str, Any]] = None,
    llm: Any = None,
    base_compactor: Optional[Callable[..., Any]] = None,
) -> Callable[..., tuple[list[dict[str, Any]], list[dict[str, Any]]]]:
    base = base_compactor or make_history_compactor(llm_config=llm_config, llm=llm)

    def _compact(
        history: list[dict[str, Any]],
        keep_full_turns: Optional[int] = None,
        **kwargs: Any,
    ) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
        prior, current = base(
            history,
            keep_full_turns,
            **kwargs,
        )
        runtime_state = kwargs.get('runtime_state')
        entries = runtime_state.get('entries') if isinstance(runtime_state, dict) else []
        compressed = any(
            isinstance(entry, dict) and entry.get('kind') in {'compacted', 'spilled', 'summary'}
            for entry in (entries or [])
        )
        if gate.phase != 'apply' or not gate.plan_markdown or not compressed:
            return list(prior), list(current)

        marker = f'<preference_organizer_plan hash="{gate.plan_hash}" pass="{gate.pass_number}">'
        visible = [*prior, *current]
        if any(marker in str(message.get('content') or '') for message in visible):
            return list(prior), list(current)
        authoritative = {
            'role': 'user',
            'content': (
                f'{marker}\n'
                'This exact Gate Plan is the only allowed action set. Do not add new actions.\n\n'
                f'{gate.plan_markdown}\n'
                '</preference_organizer_plan>'
            ),
        }
        projected = list(current)
        projected.append(authoritative)
        if isinstance(runtime_state, dict):
            runtime_state['preference_organizer_plan_injection_fingerprint'] = (
                projection_fingerprint(entries or [])
            )
            runtime_state['preference_organizer_plan_injection_hash'] = gate.plan_hash
        return list(prior), projected

    return _compact
