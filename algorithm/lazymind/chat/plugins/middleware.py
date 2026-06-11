from __future__ import annotations

import asyncio
import logging
from typing import Any, AsyncIterator, Dict, List, Optional

import lazyllm

from .loader import plugin_loader
from .config import get_db_session_factory, load_execution_path, load_plugin_info
from .manager import build_advance_step_tool, build_all_plugin_tools
from lazymind.chat.engine.prompts.guidance import PLUGIN_ACTIVE_GUIDANCE

logger = logging.getLogger(__name__)


class PluginMiddleware:
    """Encapsulates all plugin-related injection logic for handle_chat.

    Responsibilities:
    - Unpack plugin_context and write plugin fields into agentic_config.
    - Create and wire the shared plugin_event_queue.
    - Build the extra tool list (advance_step or all cold-start triggers).
    - Render plugin guidance text for system prompt (plugin_prompt str).

    handle_chat only needs to:
      1. Await PluginMiddleware.create(plugin_context, agentic_config)
      2. Append mw.extra_tools to the agent tool list.
      3. Pass mw.plugin_prompt to build_system_prompt.
      4. Call `async for ev in mw.iter_pending_events()` before and after the agent run.
    """

    def __init__(
        self,
        plugin_context: Optional[Dict[str, Any]],
        agentic_config: Dict[str, Any],
        plugin_id: str,
        plugin_step: str,
        execution_path: list,
    ) -> None:
        """Internal constructor — use PluginMiddleware.create() from async contexts."""
        pctx = plugin_context or {}
        plugin_session_id = pctx.get('plugin_session_id', '')

        # Shared event queue — a plain list so tool functions in a separate
        # asyncio task can append to the same object (lazyllm.globals is
        # task-local and cannot be shared across asyncio tasks).
        event_queue: list = []

        agentic_config.update({
            'plugin_id': plugin_id,
            'plugin_session_id': plugin_session_id,
            'plugin_step': plugin_step,
            'db_session_factory': get_db_session_factory(),
            'plugin_event_queue': event_queue,
        })

        # Keep lazyllm.globals pointing at the same list object for any legacy readers.
        lazyllm.globals['plugin_event_queue'] = event_queue

        self._queue = event_queue
        self.extra_tools = self._build_tools(plugin_id, plugin_step, agentic_config)

        self.plugin_prompt = self._render_plugin_prompt(
            plugin_id, plugin_step, execution_path,
        )
        # True when cold-start trigger tools are present but no session is active.
        # build_system_prompt appends PLUGIN_TOOLS_GUIDANCE in this case without
        # skipping the standard tool guidance sections.
        self.has_plugin_tools = bool(not plugin_id and self.extra_tools)

    @classmethod
    async def create(
        cls,
        plugin_context: Optional[Dict[str, Any]],
        agentic_config: Dict[str, Any],
    ) -> 'PluginMiddleware':
        """Async factory — offloads all blocking DB lookups to a thread pool so the
        asyncio event loop is not stalled during SQLAlchemy queries."""
        pctx = plugin_context or {}
        plugin_session_id = pctx.get('plugin_session_id', '')
        loop = asyncio.get_event_loop()
        plugin_id, plugin_step = await loop.run_in_executor(
            None, cls._resolve_plugin_info, plugin_session_id,
        )
        execution_path = await loop.run_in_executor(
            None, cls._load_execution_path_static, plugin_id, plugin_session_id,
        )
        return cls(plugin_context, agentic_config, plugin_id, plugin_step, execution_path)

    # ------------------------------------------------------------------
    # Private helpers
    # ------------------------------------------------------------------

    def _build_tools(self, plugin_id: str, plugin_step: str, agentic_config: dict | None = None) -> List[Any]:
        if plugin_id:
            return build_advance_step_tool(plugin_id, plugin_step, agentic_config)
        return build_all_plugin_tools()

    @staticmethod
    def _resolve_plugin_info(plugin_session_id: str) -> tuple[str, str]:
        """Query plugin_id and current_step_id from DB.

        Returns ('', '') when session_id is empty (cold-start / no active session).
        Logs a warning when the DB query fails.
        """
        if not plugin_session_id:
            logger.info('[PluginMiddleware._resolve_plugin_info] empty session_id → cold-start')
            return '', ''
        info = load_plugin_info(plugin_session_id)
        plugin_id = info.get('plugin_id', '')
        step_id = info.get('step_id', '')
        logger.info(
            '[PluginMiddleware._resolve_plugin_info] session=%r → plugin_id=%r step_id=%r',
            plugin_session_id, plugin_id, step_id,
        )
        if not plugin_id:
            logger.warning(
                '[PluginMiddleware] Could not resolve plugin_id for session %r '
                '(session may not exist yet)',
                plugin_session_id,
            )
        return plugin_id, step_id

    @staticmethod
    def _load_execution_path_static(plugin_id: str, plugin_session_id: str) -> list:
        """Load the execution path from DB. Returns [] on failure.
        Static so it can be called directly via run_in_executor without a bound self."""
        if not plugin_id or not plugin_session_id:
            return []
        try:
            return load_execution_path(plugin_session_id)
        except Exception as exc:
            logger.warning(
                '[Plugin %s] Failed to load execution_path from DB (%s)',
                plugin_id, exc,
            )
            return []

    def _render_plugin_prompt(
        self,
        plugin_id: str,
        plugin_step: str,
        execution_path: list,
    ) -> str:
        """Render PLUGIN_ACTIVE_GUIDANCE for an active session. Returns '' otherwise."""
        if not plugin_id or not plugin_loader.is_loaded(plugin_id):
            return ''
        if not execution_path:
            logger.warning(
                '[Plugin %s] execution_path is empty; ChatAgent will have no history context',
                plugin_id,
            )

        sm = plugin_loader.get_state_machine(plugin_id)
        scenario = plugin_loader.get_scenario(plugin_id)
        reachable_steps = sm.get_reachable_steps(plugin_step)

        parts = [PLUGIN_ACTIVE_GUIDANCE]
        parts.append('\n## Scenario\n' + scenario.strip())

        if reachable_steps:
            steps_str = ', '.join(f'`{s}`' for s in reachable_steps)
            parts.append(f'\n## Available steps\n{steps_str}')

        if plugin_step:
            parts.append(f'\n## Current step\n{plugin_step}')

        if execution_path:
            lines = []
            for entry in execution_path:
                step_id = entry.get('step_id', '')
                status = entry.get('status', '')
                summary = entry.get('summary', '')
                if summary:
                    lines.append(f'- {step_id} ({status}): {summary}')
                else:
                    lines.append(f'- {step_id} ({status})')
            parts.append('\n## Execution path (chronological)\n' + '\n'.join(lines))

        return '\n'.join(parts)

    # ------------------------------------------------------------------
    # Async event drain — call before and after agent execution
    # ------------------------------------------------------------------

    async def iter_pending_events(self) -> AsyncIterator[Dict[str, Any]]:
        """Drain the shared event queue and yield each event dict."""
        pending = list(self._queue)
        del self._queue[:]
        for ev in pending:
            yield ev


class _NoopPluginMiddleware:
    """Fallback used when the plugin package is unavailable."""

    def __init__(
        self,
        plugin_context: Any = None,
        agentic_config: Any = None,
    ) -> None:
        self.extra_tools: List[Any] = []
        self.plugin_prompt: str = ''
        self.has_plugin_tools: bool = False

    async def iter_pending_events(self) -> AsyncIterator[Dict[str, Any]]:
        return
        yield  # make this an async generator
