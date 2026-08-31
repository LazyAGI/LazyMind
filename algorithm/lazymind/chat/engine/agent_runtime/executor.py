from __future__ import annotations

import types
import uuid
from typing import Any, AsyncIterator, Optional, Tuple

import lazyllm
import lazyllm.module.stream_helper as _sh
import lazyllm.tools.agent as _agent_mod

from lazymind.chat.engine.tools.infra import CitationResultMiddleware
from lazymind.config import config as _cfg

from .context_estimator import estimate_non_history_tokens
from .models import AgentRole, AgentRunPlan
from .pruner import estimate_history_tokens, make_history_compactor
from .telemetry import (
    append_event,
    make_runtime_observer,
    sid,
    telemetry_enabled,
)
from .tool_call_guard import ToolCallGuard
from .tool_limit_control import tool_limit_decision_coordinator


def _sanitize_tools(tools: list[Any]) -> list[Any]:
    """Drop invalid tool entries (e.g. partially-imported modules) before ReactAgent."""
    cleaned: list[Any] = []
    for tool in tools:
        if isinstance(tool, types.ModuleType):
            lazyllm.LOG.error(
                '[AgentExecutor] dropping invalid tool module '
                f'name={getattr(tool, "__name__", None)} file={getattr(tool, "__file__", None)}'
            )
            continue
        if isinstance(tool, dict):
            children = tool.get('tools')
            if isinstance(children, list):
                kept = []
                for child in children:
                    if isinstance(child, types.ModuleType):
                        lazyllm.LOG.error(
                            '[AgentExecutor] dropping invalid ToolGroup child module '
                            f'group={tool.get("name")} name={getattr(child, "__name__", None)} '
                            f'file={getattr(child, "__file__", None)}'
                        )
                        continue
                    kept.append(child)
                tool = {**tool, 'tools': kept}
        cleaned.append(tool)
    return cleaned


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
        from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs

        options = plan.execution_options
        keep_full_turns = options.keep_full_turns
        if keep_full_turns is None:
            keep_full_turns = int(_cfg['agentic_keep_full_turns'])
        history_compactor = options.history_compactor
        if not _cfg['context_compression_enabled']:
            history_compactor = None
        elif history_compactor is None:
            history_compactor = make_history_compactor(
                max_input_tokens=options.max_input_tokens,
                llm_config=options.llm_config,
                keep_recent=keep_full_turns,
                trigger='mid_turn',
                llm=llm,
                workspace=options.workspace,
            )
        run_id = uuid.uuid4().hex[:12]
        observer = (
            make_runtime_observer(
                role=getattr(plan.role, 'value', str(plan.role)),
                run_id=run_id,
            )
            if telemetry_enabled() else None
        )
        kwargs = {
            'stream': True,
            'max_retries': options.max_retries or _cfg['max_retries'],
            'enable_builtin_tools': bool(_cfg['trusted_local_mode']),
            'force_summarize': True,
            'force_summarize_context': plan.force_summarize_context,
            'on_max_retries': (
                tool_limit_decision_coordinator.on_max_retries
                if plan.role == AgentRole.CHAT else None
            ),
        }
        optional = {
            'skills': options.skills,
            'workspace': options.workspace,
            'keep_full_turns': keep_full_turns,
            'history_compactor': history_compactor,
            'fs': options.fs,
            'skills_dir': options.skills_dir,
            'extra_stop_condition': options.extra_stop_condition,
            'runtime_observer': observer,
        }
        kwargs.update({key: value for key, value in optional.items() if value is not None})
        tools = _sanitize_tools(_deduplicate_tools(plan.tools))
        ensure_lazyllm_tool_docs(tools)
        agent = _agent_mod.ReactAgent(
            llm=llm,
            tools=tools,
            prompt=plan.prompt.system_prompt,
            **kwargs,
        )
        agent._tools_manager = ToolCallGuard(
            CitationResultMiddleware(agent._tools_manager),
            expanded_round_limit=max(2, int(_cfg['agentic_expanded_max_rounds'])),
            cancel_check=options.extra_stop_condition,
        )
        agent._agent_lab_run_id = run_id
        # Restore lazy Toolkit activation before the streaming helper takes over.
        # Relying only on ReactAgent._pre_process makes restoration dependent on
        # llm_chat_history surviving the helper/framework call path.
        agent._prepare_tool_context(plan.prompt.current_input, plan.history)
        prefix = agent._model_facing_prefix()
        estimated = (
            estimate_non_history_tokens(prefix, plan.prompt.current_input)
            + estimate_history_tokens(plan.history or [])
        )
        if telemetry_enabled():
            append_event(
                'run_prepare',
                role=getattr(plan.role, 'value', str(plan.role)),
                compression_enabled=bool(_cfg['context_compression_enabled']),
                history_len=len(plan.history or []),
                estimated_tokens=estimated,
                sid=sid(),
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
        run_id = getattr(agent, '_agent_lab_run_id', '')
        if telemetry_enabled():
            append_event(
                'run_start',
                role=getattr(plan.role, 'value', str(plan.role)),
                run_id=run_id,
                history_len=len(history or []),
                estimated_tokens=estimate_history_tokens(history or []),
                input_preview=(plan.prompt.current_input or '')[:240],
                sid=sid(),
            )
        helper = _sh.StreamCallHelper(agent, init_sid=False)
        kwargs = {'llm_chat_history': history} if history is not None else {}
        finished_model_calls: set[str] = set()
        failed = False
        try:
            async for item in helper.astream(plan.prompt.current_input, **kwargs):
                self._record_finished_model_call(item, finished_model_calls)
                yield 'event', item
            try:
                result = helper.future.result()
            except Exception as exc:
                failed = True
                terminal = self._find_model_terminal(exc)
                model_call_id = str((terminal or {}).get('model_call_id') or '')
                if terminal and model_call_id not in finished_model_calls:
                    yield 'event', {
                        'tag': 'runtime_event',
                        'runtime_event': {
                            'schema_version': 1,
                            'event_id': uuid.uuid4().hex,
                            'type': 'model_call_finished',
                            'data': terminal,
                        },
                    }
                lazyllm.LOG.exception(
                    f'[AgentExecutor] agent future raised: {type(exc).__name__}: {exc}'
                )
                raise
            yield 'final', result
        finally:
            if telemetry_enabled():
                append_event(
                    'run_end',
                    role=getattr(plan.role, 'value', str(plan.role)),
                    run_id=run_id,
                    ok=not failed,
                    sid=sid(),
                )

    @staticmethod
    def _record_finished_model_call(item: Any, seen: set[str]) -> None:
        if not isinstance(item, dict) or item.get('tag') != 'runtime_event': return
        event = item.get('runtime_event')
        if not isinstance(event, dict) or event.get('type') != 'model_call_finished': return
        data = event.get('data')
        if isinstance(data, dict) and data.get('model_call_id'):
            seen.add(str(data['model_call_id']))

    @staticmethod
    def _find_model_terminal(exc: Exception) -> Optional[dict[str, Any]]:
        seen = set()
        while exc is not None and id(exc) not in seen:
            seen.add(id(exc))
            terminal = getattr(exc, 'terminal', None)
            if terminal is not None:
                public_dict = getattr(terminal, 'public_dict', None)
                return public_dict() if callable(public_dict) else terminal
            exc = exc.__cause__ or exc.__context__
        return None

    def run(self, llm: Any, plan: AgentRunPlan) -> Any:
        """Run a one-shot agent while preserving ReactAgent's synchronous API."""
        agent = self.create_agent(llm, plan)
        return agent(plan.prompt.current_input)
