from __future__ import annotations

import json
from typing import Any

import lazyllm


def emit_agent_event(event_type: str, **payload: Any) -> None:
    event = {'tag': event_type, **payload}
    lazyllm.FileSystemQueue().enqueue(
        json.dumps(event, ensure_ascii=False, default=str),
    )
