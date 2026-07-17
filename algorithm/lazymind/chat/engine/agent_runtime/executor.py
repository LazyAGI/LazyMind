from __future__ import annotations

from typing import Any, AsyncIterator, Tuple

import lazyllm
import lazyllm.module.stream_helper as _sh
import lazyllm.tools.agent as _agent_mod
from lazymind.config import config as _cfg

from .models import AgentRunPlan


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
            'max_retries': _cfg['max_retries'],
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
