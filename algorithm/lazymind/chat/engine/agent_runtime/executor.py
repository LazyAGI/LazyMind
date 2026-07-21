from __future__ import annotations

import json
from typing import Any, AsyncIterator, Tuple

import lazyllm
import lazyllm.module.stream_helper as _sh
import lazyllm.tools.agent as _agent_mod
from lazymind.config import config as _cfg

from .models import AgentRunPlan


class ToolCallGuard:
    """Stop selected tools from looping after failures without limiting successful work."""

    def __init__(self, manager: Any, failure_limits: dict[str, int] | None = None):
        self._manager = manager
        self._failure_limits = dict(failure_limits or {})
        self._failed_signatures: set[str] = set()
        self._consecutive_failures: dict[str, int] = {}

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

    @staticmethod
    def _failed(result: Any) -> bool:
        if not isinstance(result, dict):
            return False
        if result.get('ok') is False:
            return True
        value = result.get('value')
        if isinstance(value, dict):
            if value.get('success') is False:
                return True
            payload = value.get('result')
            if isinstance(payload, dict):
                total = payload.get('total')
                succeeded = payload.get('succeeded')
                if isinstance(total, int) and total > 0 and succeeded == 0:
                    return True
        return False

    @staticmethod
    def _blocked(name: str, message: str) -> dict[str, Any]:
        return {
            'ok': False,
            'value': None,
            'msg': f'[Repeated Tool Failure] {name}: {message}',
        }

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
            guarded = name in self._failure_limits
            if guarded and signature in self._failed_signatures:
                results[index] = self._blocked(
                    name, 'this exact call already failed; do not retry it with the same arguments.',
                )
                lazyllm.LOG.info(f'[ToolCallGuard] blocked repeated failed call: {name}')
                continue
            if guarded and signature in pending_signatures:
                duplicate_indices[index] = pending_signatures[signature]
                lazyllm.LOG.info(f'[ToolCallGuard] merged duplicate tool call: {name}')
                continue
            failures = self._consecutive_failures.get(name, 0)
            limit = self._failure_limits.get(name)
            if limit is not None and failures >= limit:
                results[index] = self._blocked(
                    name,
                    f'{failures} consecutive attempts failed. Stop changing parameters and use '
                    'another grounded source or explain that the evidence is unavailable.',
                )
                continue
            pending.append(tool_call)
            pending_indices.append(index)
            if guarded:
                pending_signatures[signature] = index
        if pending:
            pending_results = self._manager(pending, verbose=verbose)
            for index, tool_call, result in zip(pending_indices, pending, pending_results):
                results[index] = result
                name = str((tool_call.get('function') or {}).get('name') or '')
                if name in self._failure_limits:
                    if self._failed(result):
                        self._consecutive_failures[name] = (
                            self._consecutive_failures.get(name, 0) + 1
                        )
                        self._failed_signatures.add(self._signature(tool_call))
                    else:
                        self._consecutive_failures[name] = 0
                        prefix = f'{name}:'
                        self._failed_signatures = {
                            item for item in self._failed_signatures if not item.startswith(prefix)
                        }
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
        agent._tools_manager = ToolCallGuard(
            agent._tools_manager, options.tool_failure_limits,
        )
        # Restore lazy Toolkit activation before the streaming helper takes over.
        # Relying only on ReactAgent._pre_process makes restoration dependent on
        # llm_chat_history surviving the helper/framework call path.
        agent._prepare_tool_context(plan.prompt.current_input, plan.history)
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
