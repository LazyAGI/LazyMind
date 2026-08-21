from __future__ import annotations

import json
import time
from typing import Any, AsyncIterator, Tuple
import uuid
from typing import Any, AsyncIterator, Optional, Tuple

import lazyllm
import lazyllm.module.stream_helper as _sh
import lazyllm.tools.agent as _agent_mod
from lazymind.config import config as _cfg
from lazymind.chat.engine.tools.infra import CitationResultMiddleware

from .models import AgentRole, AgentRunPlan
from .tool_limit_control import tool_limit_decision_coordinator


_EXPANDED_BUDGET_TOOLS = {
    'advance_step',
    'advance_step_and_hand_off',
    'create_workflow_draft',
    'create_subagent',
}
_MAX_TOOL_LOG_CHARS = 800
_RESULT_LOG_KEYS = (
    'target', 'display_name', 'kind', 'file_id', 'offset', 'end_line',
    'total_lines', 'eof', 'next_offset', 'limit', 'pattern', 'total',
    'truncated', 'status', 'filename',
)


def _requires_expanded_budget(tool_name: str) -> bool:
    """Return whether invoking this tool starts workflow or SubAgent work."""
    return tool_name in _EXPANDED_BUDGET_TOOLS or tool_name.startswith('trigger_')


def _tool_call_session_id() -> str:
    cfg = lazyllm.globals.get('agentic_config') or {}
    if isinstance(cfg, dict) and cfg.get('session_id'):
        return str(cfg['session_id'])
    try:
        return str(getattr(lazyllm.globals, '_sid', '') or '')
    except Exception:
        return ''


def _compact_json(value: Any, limit: int = _MAX_TOOL_LOG_CHARS) -> str:
    try:
        text = json.dumps(value, ensure_ascii=False, default=str)
    except Exception:
        text = str(value)
    if len(text) > limit:
        return text[:limit] + f'...<{len(text) - limit} more chars>'
    return text


def _parse_tool_arguments(function: dict[str, Any]) -> Any:
    arguments = function.get('arguments', {})
    if isinstance(arguments, str):
        try:
            return json.loads(arguments)
        except Exception:
            return arguments
    return arguments


def _summarize_tool_result(result: Any) -> dict[str, Any]:
    summary: dict[str, Any] = {}
    if not isinstance(result, dict):
        summary['result_type'] = type(result).__name__
        return summary
    if 'ok' in result:
        summary['ok'] = result.get('ok')
    msg = result.get('msg')
    if msg:
        summary['msg'] = str(msg)[:240]
    value = result.get('value') if 'value' in result else result
    if not isinstance(value, dict):
        return summary
    if 'success' in value:
        summary['success'] = value.get('success')
    error = value.get('error')
    if isinstance(error, dict) and error.get('reason'):
        summary['error'] = str(error.get('reason'))[:240]
    payload = value.get('result') if isinstance(value.get('result'), dict) else value
    if not isinstance(payload, dict):
        return summary
    for key in _RESULT_LOG_KEYS:
        if key in payload and payload[key] is not None:
            summary[key] = payload[key]
    matches = payload.get('matches')
    if isinstance(matches, list):
        summary['match_count'] = len(matches)
    footer = payload.get('footer')
    if isinstance(footer, str) and footer.strip():
        summary['footer'] = footer.strip()[:240]
    return summary


def _format_log_fields(fields: dict[str, Any]) -> str:
    parts: list[str] = []
    for key, value in fields.items():
        if value is None:
            continue
        if isinstance(value, (dict, list)):
            rendered = _compact_json(value, 240)
        else:
            rendered = str(value)
        parts.append(f'[{key}={rendered}]')
    return ' '.join(parts)


def _log_tool_call(event: str, name: str, **fields: Any) -> None:
    extras = _format_log_fields(fields)
    suffix = f' {extras}' if extras else ''
    lazyllm.LOG.info(
        f'[ToolCall] [sid={_tool_call_session_id()}] [event={event}] [name={name}]{suffix}'
    )


class ToolCallGuard:
    """Stop selected tools from looping after failures without limiting successful work."""

    def __init__(
        self,
        manager: Any,
        failure_limits: dict[str, int] | None = None,
        expanded_round_limit: int | None = None,
        repeated_call_limit: int = 3,
        cancel_check: Any = None,
    ):
        self._manager = manager
        self._failure_limits = dict(failure_limits or {})
        self._failed_signatures: set[str] = set()
        self._consecutive_failures: dict[str, int] = {}
        self._expanded_round_limit = expanded_round_limit
        self._repeated_call_limit = max(2, int(repeated_call_limit))
        self._signature_calls: dict[str, int] = {}
        self._cancel_check = cancel_check

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

    @staticmethod
    def _loop_blocked(name: str, message: str) -> dict[str, Any]:
        return {
            'ok': False,
            'value': None,
            'msg': f'[Repeated Tool Call] {name}: {message}',
        }

    def __call__(self, tools: Any, verbose: bool = False) -> Any:
        if self._cancel_check is not None:
            self._cancel_check(None)
        tool_calls = [tools] if isinstance(tools, dict) else list(tools or [])
        results: list[Any] = [None] * len(tool_calls)
        pending: list[dict[str, Any]] = []
        pending_indices: list[int] = []
        pending_signatures: dict[str, int] = {}
        duplicate_indices: dict[int, int] = {}
        for index, tool_call in enumerate(tool_calls):
            function = tool_call.get('function') or {}
            name = str(function.get('name') or '')
            if _requires_expanded_budget(name):
                workspace = lazyllm.locals.get('_lazyllm_agent', {}).get('workspace')
                if (
                    isinstance(workspace, dict)
                    and self._expanded_round_limit is not None
                    and workspace.get('_react_round_limit') != self._expanded_round_limit
                ):
                    workspace['_react_round_limit'] = self._expanded_round_limit
                    lazyllm.LOG.info(
                        f'ChatAgent used tool={name}; automatically expanding '
                        f'tool round limit to {self._expanded_round_limit}.'
                    )
            signature = self._signature(tool_call)
            signature_calls = self._signature_calls.get(signature, 0) + 1
            self._signature_calls[signature] = signature_calls
            arguments = _parse_tool_arguments(function)
            if signature_calls > self._repeated_call_limit:
                results[index] = self._loop_blocked(
                    name,
                    f'the exact same call was already made {self._repeated_call_limit} times; '
                    'stop retrying it and synthesize from existing results or choose another tool.',
                )
                _log_tool_call(
                    'blocked', name, reason='repeated_call', args=arguments,
                )
                continue
            guarded = name in self._failure_limits
            if guarded and signature in self._failed_signatures:
                results[index] = self._blocked(
                    name, 'this exact call already failed; do not retry it with the same arguments.',
                )
                _log_tool_call(
                    'blocked', name, reason='repeated_failure', args=arguments,
                )
                continue
            if guarded and signature in pending_signatures:
                duplicate_indices[index] = pending_signatures[signature]
                _log_tool_call(
                    'merged', name, reason='duplicate_in_batch', args=arguments,
                )
                continue
            failures = self._consecutive_failures.get(name, 0)
            limit = self._failure_limits.get(name)
            if limit is not None and failures >= limit:
                results[index] = self._blocked(
                    name,
                    f'{failures} consecutive attempts failed. Stop changing parameters and use '
                    'another grounded source or explain that the evidence is unavailable.',
                )
                _log_tool_call(
                    'blocked', name, reason='consecutive_failures',
                    failures=failures, args=arguments,
                )
                continue
            pending.append(tool_call)
            pending_indices.append(index)
            if guarded:
                pending_signatures[signature] = index
        if pending:
            for tool_call in pending:
                function = tool_call.get('function') or {}
                _log_tool_call(
                    'start',
                    str(function.get('name') or ''),
                    args=_parse_tool_arguments(function),
                )
            started_at = time.perf_counter()
            pending_results = self._manager(pending, verbose=verbose)
            elapsed = time.perf_counter() - started_at
            for index, tool_call, result in zip(pending_indices, pending, pending_results):
                results[index] = result
                name = str((tool_call.get('function') or {}).get('name') or '')
                _log_tool_call(
                    'done', name, elapsed=f'{elapsed:.3f}s', **_summarize_tool_result(result),
                )
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
        from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs

        options = plan.execution_options
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
            'keep_full_turns': options.keep_full_turns,
            'fs': options.fs,
            'skills_dir': options.skills_dir,
            'extra_stop_condition': options.extra_stop_condition,
        }
        kwargs.update({key: value for key, value in optional.items() if value is not None})
        tools = _deduplicate_tools(plan.tools)
        ensure_lazyllm_tool_docs(tools)
        agent = _agent_mod.ReactAgent(
            llm=llm,
            tools=tools,
            prompt=plan.prompt.system_prompt,
            **kwargs,
        )
        agent._tools_manager = ToolCallGuard(
            CitationResultMiddleware(agent._tools_manager),
            options.tool_failure_limits,
            max(2, int(_cfg['agentic_expanded_max_rounds'])),
            cancel_check=options.extra_stop_condition,
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
        finished_model_calls: set[str] = set()
        async for item in helper.astream(plan.prompt.current_input, **kwargs):
            self._record_finished_model_call(item, finished_model_calls)
            yield 'event', item
        try:
            result = helper.future.result()
        except Exception as exc:
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
