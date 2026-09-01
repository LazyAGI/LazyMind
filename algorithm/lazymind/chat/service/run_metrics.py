from __future__ import annotations

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
    if chosen is None:
        best_prompt = -1
        for record in records.values():
            if not isinstance(record, dict):
                continue
            adapted = adapt_provider_usage(record)
            prompt = adapted.get('input_tokens')
            if isinstance(prompt, int) and prompt > best_prompt:
                best_prompt = prompt
                chosen = record
    return chosen or {}


def snapshot_provider_usage(
    usage: Optional[dict[str, Any]] = None,
    usage_map: Optional[dict[str, Any]] = None,
    module_id: Optional[str] = None,
) -> dict[str, Any]:
    return adapt_provider_usage(choose_usage_record(usage, usage_map, module_id))


class RunMetricsTracker:
    def __init__(self, clock: Callable[[], float]) -> None:
        self._clock = clock
        self._started = clock()
        self._first_output_at: Optional[float] = None
        self._model_open_at: Optional[float] = self._started
        self._tool_open_at: Optional[float] = None
        self.model_steps = 0
        self.tool_steps = 0
        self.model_ms = 0.0
        self.tool_ms = 0.0

    def mark_output(self) -> None:
        if self._first_output_at is None:
            self._first_output_at = self._clock()

    def on_tool_calls(self, count: int) -> None:
        self.mark_output()
        self._close_model()
        if count > 0:
            self.tool_steps += count
            if self._tool_open_at is None:
                self._tool_open_at = self._clock()

    def on_tool_results(self, duration_ms: Optional[float] = None) -> None:
        if duration_ms is not None:
            self.tool_ms += max(0.0, float(duration_ms))
            self._tool_open_at = None
        else:
            self._close_tools()
        if self._model_open_at is None:
            self._model_open_at = self._clock()

    def on_model_call_finished(self) -> None:
        self.model_steps += 1
        if self._tool_open_at is None:
            self._close_model()
            self._model_open_at = self._clock()

    def finish(self) -> None:
        self._close_tools()
        self._close_model()

    def snapshot(
        self,
        *,
        usage: Optional[dict[str, Any]] = None,
        usage_map: Optional[dict[str, Any]] = None,
        module_id: Optional[str] = None,
        llm_config: Optional[dict[str, Any]] = None,
        turn_seq: Optional[int] = None,
        max_input_tokens: Optional[int] = None,
    ) -> dict[str, Any]:
        self.finish()
        wall_ms = max(0.0, (self._clock() - self._started) * 1000.0)
        if self.tool_ms > 0:
            self.model_ms = max(0.0, wall_ms - self.tool_ms)
        chosen = choose_usage_record(usage, usage_map, module_id)
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
        if self._first_output_at is not None:
            ttft_ms = max(0.0, (self._first_output_at - self._started) * 1000.0)
        tok_s = None
        if completion and self.model_ms > 0:
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
            'model_ms': round(self.model_ms),
            'tool_ms': round(self.tool_ms),
        }
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

    def _close_model(self) -> None:
        if self._model_open_at is None:
            return
        self.model_ms += max(0.0, (self._clock() - self._model_open_at) * 1000.0)
        self._model_open_at = None

    def _close_tools(self) -> None:
        if self._tool_open_at is None:
            return
        self.tool_ms += max(0.0, (self._clock() - self._tool_open_at) * 1000.0)
        self._tool_open_at = None
