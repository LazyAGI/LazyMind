"""Execution budgets declared and pinned by a workflow package."""
from __future__ import annotations
import asyncio
from dataclasses import dataclass
from typing import Any, AsyncIterator


@dataclass(frozen=True)
class ExecutionPolicy:
    rounds: int
    timeout: int
    calls: dict[str, int]


def policy_for(params: dict[str, Any]) -> ExecutionPolicy | None:
    limits = (params.get('workflow_runtime') or {}).get('execution_limits') or {}
    value = limits.get(params.get('step_id'))
    if value is None:
        return None
    rounds, timeout = int(value['rounds']), int(value['timeout'])
    calls = dict(value.get('tool_calls') or {})
    if (
        not 1 <= rounds <= 64
        or not 1 <= timeout <= 3600
        or any(not isinstance(v, int) or not 1 <= v <= 100 for v in calls.values())
    ):
        raise ValueError('Invalid package execution limits')
    return ExecutionPolicy(rounds, timeout, calls)


async def bounded_frames(frames: AsyncIterator[str], params: dict[str, Any], stop) -> AsyncIterator[str]:
    policy = policy_for(params)
    try:
        if policy is None:
            async for frame in frames:
                yield frame
        else:
            async with asyncio.timeout(policy.timeout):
                async for frame in frames:
                    yield frame
    except TimeoutError as exc:
        stop()
        raise RuntimeError(f'WORKFLOW_STEP_TIMEOUT: {params.get("step_id")} exceeded its declared timeout') from exc
    finally:
        await frames.aclose()
