from __future__ import annotations

import math
from typing import Any, Callable, Optional

from lazymind.chat.service.usage_adapter import (
    adapt_provider_usage,
    adapt_usage_frame,
    combine_adapted_usage,
    usage_frames,
)


def _nonneg_int(value: Any) -> Optional[int]:
    try:
        parsed = int(value)
    except (TypeError, ValueError):
        return None
    return parsed if parsed >= 0 else None


def _nonneg_float(value: Any) -> Optional[float]:
    try:
        parsed = float(value)
    except (TypeError, ValueError):
        return None
    return parsed if math.isfinite(parsed) and parsed >= 0 else None


def model_name_from_llm_config(llm_config: Optional[dict[str, Any]]) -> Optional[str]:
    if not isinstance(llm_config, dict):
        return None
    for role in ('llm', 'chat', 'vlm'):
        role_cfg = llm_config.get(role)
        if not isinstance(role_cfg, dict):
            continue
        name = role_cfg.get('model') or role_cfg.get('model_name')
        if isinstance(name, str) and name.strip():
            return name.strip()
    return None


def choose_usage_record(
    usage: Optional[dict[str, Any]] = None,
    usage_map: Optional[dict[str, Any]] = None,
    module_id: Optional[str] = None,
) -> dict[str, Any]:
    chosen = usage if isinstance(usage, dict) else None
    records = usage_map if isinstance(usage_map, dict) else {}
    if chosen is None and module_id and isinstance(records.get(module_id), dict):
        chosen = records[module_id]
    if chosen is None and not module_id:
        candidates = [record for record in records.values() if isinstance(record, dict)]
        if len(candidates) == 1:
            chosen = candidates[0]
    return chosen or {}


def snapshot_provider_usage(
    usage: Optional[dict[str, Any]] = None,
    usage_map: Optional[dict[str, Any]] = None,
    module_id: Optional[str] = None,
) -> dict[str, Any]:
    return adapt_provider_usage(choose_usage_record(usage, usage_map, module_id))


class RunMetricsTracker:
    def __init__(self, clock: Callable[[], float], *, started_at: Optional[float] = None) -> None:
        self._clock = clock
        self._started = clock() if started_at is None else started_at
        self._first_output_at: Optional[float] = None
        self._first_model_started_at: Optional[float] = None
        self._measured_model_steps = 0
        self._tool_batches = 0
        self._measured_tool_batches = 0
        self.model_steps = 0
        self.tool_steps = 0
        self.model_ms = 0.0
        self.tool_ms = 0.0

    def mark_output(self) -> None:
        if self._first_output_at is None:
            self._first_output_at = self._clock()

    def on_tool_calls(self, count: int) -> None:
        self.mark_output()
        if count > 0:
            self.tool_steps += count
            self._tool_batches += 1

    def on_tool_results(self, duration_ms: Optional[float] = None) -> None:
        measured = _nonneg_float(duration_ms)
        if measured is not None:
            self.tool_ms += measured
            self._measured_tool_batches += 1

    def on_model_call_started(self) -> None:
        if self._first_model_started_at is None:
            self._first_model_started_at = self._clock()

    def on_model_call_finished(self, duration_ms: Optional[float] = None) -> None:
        self.model_steps += 1
        measured = _nonneg_float(duration_ms)
        if measured is not None:
            self.model_ms += measured
            self._measured_model_steps += 1

    def snapshot(
        self,
        *,
        usage: Optional[dict[str, Any]] = None,
        usage_map: Optional[dict[str, Any]] = None,
        module_id: Optional[str] = None,
        llm_config: Optional[dict[str, Any]] = None,
        turn_seq: Optional[int] = None,
        max_input_tokens: Optional[int] = None,
        model_events: Optional[list[dict[str, Any]]] = None,
    ) -> dict[str, Any]:
        wall_ms = max(0.0, (self._clock() - self._started) * 1000.0)
        event_usages = [
            data.get('usage') for event in (model_events or [])
            if isinstance(event, dict)
            and isinstance((data := event.get('data')), dict)
            and isinstance(data.get('usage'), dict)
        ]
        chosen = ({'provider_usages': event_usages} if event_usages
                  else choose_usage_record(usage, usage_map, module_id))
        frames = usage_frames(chosen)
        adapted_frames = [adapt_usage_frame(frame) for frame in frames]
        provider = combine_adapted_usage(adapted_frames)
        prompt = provider.get('input_tokens')
        completion = provider.get('output_tokens')
        cached = provider.get('cached_tokens')
        cache_input = provider.get('cache_input_tokens')
        cache_rate = None
        if cached is not None and cache_input:
            cache_rate = cached / cache_input
        elif cached is not None:
            cache_rate = 0.0
        ttft_ms = None
        if self._first_output_at is not None and self._first_model_started_at is not None:
            ttft_ms = max(0.0, (self._first_output_at - self._first_model_started_at) * 1000.0)
        model_duration_complete = (
            self.model_steps > 0 and self._measured_model_steps == self.model_steps
        )
        tok_s = None
        if completion and model_duration_complete and self.model_ms > 0:
            tok_s = completion / (self.model_ms / 1000.0)
        context_ratio = None
        window = _nonneg_int(max_input_tokens)
        last_input = None
        if adapted_frames:
            last_input = adapted_frames[-1].get('input_tokens')
        if last_input is None:
            last_input = prompt
        if last_input is not None and window:
            context_ratio = last_input / window
        metrics: dict[str, Any] = {
            'schema_version': 1,
            'steps': self.model_steps + self.tool_steps,
            'model_steps': self.model_steps,
            'tool_steps': self.tool_steps,
            'wall_ms': round(wall_ms),
        }
        if model_duration_complete:
            metrics['model_ms'] = round(self.model_ms)
        if self.tool_steps == 0:
            metrics['tool_ms'] = 0
        elif self._tool_batches > 0 and self._measured_tool_batches == self._tool_batches:
            metrics['tool_ms'] = round(self.tool_ms)
        if turn_seq is not None:
            parsed_turn = _nonneg_int(turn_seq)
            if parsed_turn is not None:
                metrics['turn_seq'] = parsed_turn
        model_name = model_name_from_llm_config(llm_config)
        if model_name:
            metrics['model'] = model_name
        for key in ('input_tokens', 'output_tokens', 'total_tokens', 'cached_tokens', 'reasoning_tokens'):
            if key in provider:
                metrics[key] = provider[key]
        if frames:
            metrics['provider_usages'] = frames
        if cache_rate is not None:
            metrics['cache_hit_rate'] = cache_rate
        if ttft_ms is not None:
            metrics['ttft_ms'] = round(ttft_ms)
        if tok_s is not None:
            metrics['tok_s'] = tok_s
        if window:
            metrics['max_input_tokens'] = window
        if context_ratio is not None:
            metrics['context_ratio'] = context_ratio
        return metrics
