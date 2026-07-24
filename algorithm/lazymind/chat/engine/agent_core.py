"""Shared agent execution primitives for ChatAgent and SubAgent.

Both agents use the same ReactAgent + StreamCallHelper loop. This module
provides:

- ``build_react_agent`` -- ReactAgent factory that centralises shared
  constructor kwargs (stream, max_retries, enable_builtin_tools,
  force_summarize).  Optional ChatAgent-only kwargs (prompt, skills,
  workspace, etc.) are passed through only when not None, so SubAgent
  gets a clean minimal construction.

- ``drive_agent`` -- async generator that wraps StreamCallHelper.astream.
  Yields ``('event', item)`` for each streaming item and a single
  ``('final', result)`` tuple when the agent finishes.  The caller owns
  exception handling (semantics differ: ChatAgent raises, SubAgent emits
  an error SSE frame).
"""
from __future__ import annotations

from typing import Any, AsyncIterator, List, Optional, Tuple, Union

import lazyllm.module.stream_helper as _sh
import lazyllm.tools.agent as _agent_mod
from lazymind.config import config as _cfg


def build_react_agent(
    llm: Any,
    tools: List[Any],
    *,
    force_summarize_context: str = '',
    # ChatAgent-only optional kwargs -- omitted (None) for SubAgent
    prompt: Optional[str] = None,
    skills: Optional[Union[bool, str, List[str]]] = None,
    workspace: Optional[str] = None,
    keep_full_turns: Optional[int] = None,
    fs: Optional[Any] = None,
    skills_dir: Optional[str] = None,
    extra_stop_condition: Optional[Any] = None,
) -> Any:
    """Build a ReactAgent with shared defaults.

    Common kwargs (stream, max_retries, enable_builtin_tools, force_summarize)
    are always applied.  Optional kwargs are forwarded only when not None so
    that SubAgent gets a lean construction without ChatAgent-only concerns.
    """
    kwargs: dict[str, Any] = {
        'stream': True,
        'max_retries': _cfg['max_retries'],
        'enable_builtin_tools': False,
        'force_summarize': True,
        'force_summarize_context': force_summarize_context,
    }
    if prompt is not None:
        kwargs['prompt'] = prompt
    if skills is not None:
        kwargs['skills'] = skills
    if workspace is not None:
        kwargs['workspace'] = workspace
    if keep_full_turns is not None:
        kwargs['keep_full_turns'] = keep_full_turns
    if fs is not None:
        kwargs['fs'] = fs
    if skills_dir is not None:
        kwargs['skills_dir'] = skills_dir
    if extra_stop_condition is not None:
        kwargs['extra_stop_condition'] = extra_stop_condition

    return _agent_mod.ReactAgent(llm=llm, tools=tools, **kwargs)


async def drive_agent(
    agent: Any,
    query: str,
    *,
    history: Optional[List[Any]] = None,
) -> AsyncIterator[Tuple[str, Any]]:
    """Async generator that drives a ReactAgent via StreamCallHelper.

    Yields:
        ``('event', item)`` for every streaming item from ``helper.astream``.
        ``('final', result)`` once the agent completes (result from future).

    The caller is responsible for exception handling around the ``'final'``
    result, since ChatAgent and SubAgent handle errors differently.
    """
    helper = _sh.StreamCallHelper(agent, init_sid=False)
    kwargs: dict[str, Any] = {}
    if history is not None:
        kwargs['llm_chat_history'] = history

    async for item in helper.astream(query, **kwargs):
        yield ('event', item)

    # Resolve the future; let the caller decide what to do on exception.
    try:
        result = helper.future.result()
    except Exception as exc:
        import lazyllm as _lazyllm
        _lazyllm.LOG.exception(f'[drive_agent] agent future raised: {type(exc).__name__}: {exc}')
        raise
    yield ('final', result)
