from __future__ import annotations

import logging
from typing import Any, AsyncIterator, Dict, List, Optional

import lazyllm

from .loader import plugin_loader
from .config import get_db_session_factory
from .manager import build_advance_step_tool, build_all_plugin_tools

logger = logging.getLogger(__name__)


class PluginMiddleware:
    """Encapsulates all plugin-related injection logic for handle_chat.

    Responsibilities:
    - Unpack plugin_context and write plugin fields into agentic_config.
    - Create and wire the shared plugin_event_queue.
    - Build the extra tool list (advance_step or all cold-start triggers).
    - Inject plugin fields into environment_context for system prompt rendering.

    handle_chat only needs to:
      1. Construct PluginMiddleware(plugin_context, agentic_config, environment_context)
      2. Append mw.extra_tools to the agent tool list.
      3. Use mw.environment_context as the updated environment_context.
      4. Call `async for ev in mw.iter_pending_events()` before and after the agent run.
    """

    def __init__(
        self,
        plugin_context: Optional[Dict[str, Any]],
        agentic_config: Dict[str, Any],
        environment_context: Optional[Dict[str, Any]],
    ) -> None:
        pctx = plugin_context or {}
        plugin_id = pctx.get('plugin_id', '')
        plugin_step = pctx.get('step', '')
        plugin_session_id = pctx.get('plugin_session_id', '')
        steps_context = pctx.get('steps_context', [])

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
        self.extra_tools = self._build_tools(plugin_id, plugin_step)
        self.environment_context = self._build_env_context(
            environment_context, plugin_id, plugin_step, steps_context,
        )

    # ------------------------------------------------------------------
    # Private helpers
    # ------------------------------------------------------------------

    def _build_tools(self, plugin_id: str, plugin_step: str) -> List[Any]:
        if plugin_id:
            return build_advance_step_tool(plugin_id, plugin_step)
        return build_all_plugin_tools()

    def _build_env_context(
        self,
        base: Optional[Dict[str, Any]],
        plugin_id: str,
        plugin_step: str,
        steps_context: list,
    ) -> Dict[str, Any]:
        ctx = dict(base or {})

        if self.extra_tools:
            ctx['_has_plugins'] = True

        if plugin_id and plugin_loader.is_loaded(plugin_id):
            ctx['_plugin_scenario'] = plugin_loader.get_scenario(plugin_id)
            ctx['_plugin_step'] = plugin_step
            sm = plugin_loader.get_state_machine(plugin_id)
            ctx['_plugin_reachable_steps'] = sm.get_reachable_steps(plugin_step)
            if steps_context:
                ctx['_steps_context'] = steps_context

        return ctx

    # ------------------------------------------------------------------
    # Async event drain — call before and after agent execution
    # ------------------------------------------------------------------

    async def iter_pending_events(self) -> AsyncIterator[Dict[str, Any]]:
        """Drain the shared event queue and yield each event dict."""
        pending = list(self._queue)
        del self._queue[:]
        for ev in pending:
            yield ev
