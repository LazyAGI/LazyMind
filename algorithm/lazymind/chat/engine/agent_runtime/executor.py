from __future__ import annotations

import json
from typing import Any, AsyncIterator, Tuple

import lazyllm
import lazyllm.module.stream_helper as _sh
import lazyllm.tools.agent as _agent_mod
from lazymind.config import config as _cfg

from .models import AgentRunPlan


class ToolCallGuard:
    """Deduplicate identical calls and cap selected exploratory tools per agent run."""

    def __init__(self, manager: Any, limits: dict[str, int] | None = None):
        self._manager = manager
        self._limits = dict(limits or {})
        self._cache: dict[str, Any] = {}
        self._counts: dict[str, int] = {}

    def __getattr__(self, name: str) -> Any:
        return getattr(self._manager, name)

    @staticmethod
    def _signature(tool_call: dict[str, Any]) -> str:
        function = tool_call.get('function') or {}
        arguments = function.get('arguments', {})
        if isinstance(arguments, str):
            try:
                arguments = json.loads(arguments)
            except Exception:
                arguments = arguments.strip()
        try:
            normalized = json.dumps(
                arguments, ensure_ascii=False, sort_keys=True, separators=(',', ':'),
            )
        except (TypeError, ValueError):
            normalized = str(arguments)
        return f"{function.get('name', '')}:{normalized}"

    def __call__(self, tools: Any, verbose: bool = False) -> Any:
        tool_calls = [tools] if isinstance(tools, dict) else list(tools or [])
        results: list[Any] = [None] * len(tool_calls)
        pending: list[dict[str, Any]] = []
        pending_indices: list[int] = []
        pending_signatures: dict[str, int] = {}
        duplicate_indices: dict[int, int] = {}
        for index, tool_call in enumerate(tool_calls):
            function = tool_call.get('function') or {}
            name = str(function.get('name') or '')
            signature = self._signature(tool_call)
            guarded = name in self._limits
            if guarded and signature in self._cache:
                results[index] = self._cache[signature]
                lazyllm.LOG.info(f'[ToolCallGuard] reused duplicate tool call: {name}')
                continue
            if guarded and signature in pending_signatures:
                duplicate_indices[index] = pending_signatures[signature]
                lazyllm.LOG.info(f'[ToolCallGuard] merged duplicate tool call: {name}')
                continue
            count = self._counts.get(name, 0)
            limit = self._limits.get(name)
            if limit is not None and count >= limit:
                results[index] = {
                    'ok': False,
                    'value': None,
                    'msg': (
                        f'[Tool Call Limit] {name} has already been called {count} times in this '
                        'task. Do not call it again; use existing observations or explain that '
                        'the available evidence is insufficient.'
                    ),
                }
                continue
            if guarded:
                self._counts[name] = count + 1
            pending.append(tool_call)
            pending_indices.append(index)
            if guarded:
                pending_signatures[signature] = index
        if pending:
            pending_results = self._manager(pending, verbose=verbose)
            for index, tool_call, result in zip(pending_indices, pending, pending_results):
                results[index] = result
                name = str((tool_call.get('function') or {}).get('name') or '')
                if name in self._limits:
                    self._cache[self._signature(tool_call)] = result
        for duplicate_index, original_index in duplicate_indices.items():
            results[duplicate_index] = results[original_index]
        return results


def _tool_name(tool: Any) -> str:
    if isinstance(tool, tuple) and len(tool) == 2:
        return _tool_name(tool[0])
    if isinstance(tool, dict):
        return str(tool.get('name') or '')
    return str(getattr(tool, '__name__', '') or '') or tool.__class__.__name__


def _deduplicate_tools(tools: list[Any]) -> list[Any]:
    result, seen = [], set()
    for tool in tools:
        name = _tool_name(tool)
        if name and name in seen:
            continue
        if name:
            seen.add(name)
        result.append(tool)
    return result


class AgentExecutor:
    """Create and drive ReactAgent instances from a fully assembled run plan."""

    def create_agent(self, llm: Any, plan: AgentRunPlan) -> Any:
        options = plan.execution_options
        kwargs = {
            'stream': True,
            'max_retries': options.max_retries or _cfg['max_retries'],
            'enable_builtin_tools': False,
            'force_summarize': True,
            'force_summarize_context': plan.force_summarize_context,
        }
        optional = {
            'skills': options.skills,
            'workspace': options.workspace,
            'keep_full_turns': options.keep_full_turns,
            'fs': options.fs,
            'skills_dir': options.skills_dir,
            'extra_stop_condition': options.extra_stop_condition,
        }
        kwargs.update({key: value for key, value in optional.items() if value is not None})
        agent = _agent_mod.ReactAgent(
            llm=llm,
            tools=_deduplicate_tools(plan.tools),
            prompt=plan.prompt.system_prompt,
            **kwargs,
        )
        agent._tools_manager = ToolCallGuard(agent._tools_manager, options.tool_call_limits)
        agent.set_stop_tools(plan.stop_tools)
        return agent

    async def stream(
        self,
        llm: Any,
        plan: AgentRunPlan,
    ) -> AsyncIterator[Tuple[str, Any]]:
        agent = self.create_agent(llm, plan)
        async for item in self.stream_agent(agent, plan):
            yield item

    async def stream_agent(
        self,
        agent: Any,
        plan: AgentRunPlan,
    ) -> AsyncIterator[Tuple[str, Any]]:
        history = plan.history if plan.history else None
        helper = _sh.StreamCallHelper(agent, init_sid=False)
        kwargs = {'llm_chat_history': history} if history is not None else {}
        async for item in helper.astream(plan.prompt.current_input, **kwargs):
            yield 'event', item
        try:
            result = helper.future.result()
        except Exception as exc:
            lazyllm.LOG.exception(
                f'[AgentExecutor] agent future raised: {type(exc).__name__}: {exc}'
            )
            raise
        yield 'final', result

    def run(self, llm: Any, plan: AgentRunPlan) -> Any:
        """Run a one-shot agent while preserving ReactAgent's synchronous API."""
        agent = self.create_agent(llm, plan)
        return agent(plan.prompt.current_input)
