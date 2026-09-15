from __future__ import annotations

import json

from typing import Any, Callable, Optional

from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor

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
        if not gate.plan_hash or not compressed:
            return list(prior), list(current)
        marker = '<preference_organizer_cursor>'
        projected = [m for m in current if not str(m.get('content') or '').startswith(marker)]
        prior = [m for m in prior if not str(m.get('content') or '').startswith(marker)]
        projected.append(
            {'role': 'user', 'content': marker + json.dumps(gate.cursor()) + '</preference_organizer_cursor>'}
        )
        return list(prior), projected

    return _compact
