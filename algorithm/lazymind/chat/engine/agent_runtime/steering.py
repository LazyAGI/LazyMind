from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field, replace
import threading
import time
from types import MethodType
from typing import Deque

from .models import AgentRunPlan


@dataclass(frozen=True)
class SteeringInput:
    steer_id: str
    content: str
    accepted_at: float


@dataclass
class _ActiveRun:
    conversation_id: str
    session_id: str
    pending: Deque[SteeringInput] = field(default_factory=deque)
    known_ids: set[str] = field(default_factory=set)
    accepting: bool = True


class SteeringInterruptedError(BaseException):
    """Internal control flow, NOT cancellation or a provider failure.

    Only the chat continuation loop catches this signal. In particular it must
    bypass LazyLLM's Exception/CancelledError handlers, which discard unfinished
    workspaces. Actual user cancellation and model errors keep those handlers.
    """


def continue_in_workspace(
    plan: AgentRunPlan, items: list[SteeringInput], assistant_text: str,
) -> AgentRunPlan:
    """Append at a joined model-call boundary; never clone the working history.

    LazyLLM owns this list, including tool-call/result pairs. On normal completion
    it moves the list to ``history``; late guidance moves that same list back.
    A missing workspace is an error, not permission to silently lose context.
    """
    import lazyllm

    if not items:
        raise ValueError('continuation requires user guidance')
    state = lazyllm.locals['_lazyllm_agent']
    workspace = state.get('workspace')
    if workspace is None:
        if 'history' not in state:
            raise RuntimeError('steering workspace is unavailable')
        workspace = state['workspace'] = {'history': state.pop('history')}
    history = workspace['history']
    if assistant_text:
        history.append({'role': 'assistant', 'content': assistant_text})
    for item in items[:-1]:
        history.append({'role': 'user', 'content': render_steering_context([item])})
    guidance = render_steering_context(items[-1:])
    return replace(
        plan, history=history,
        prompt=replace(plan.prompt, current_input=guidance, input_content=guidance,
                       input_title='New user guidance'),
    )


class SteeringRegistry:
    """Process-local inbox for the algorithm worker that owns a chat run."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._runs: dict[str, _ActiveRun] = {}

    def register(self, run_id: str, conversation_id: str, session_id: str) -> None:
        with self._lock:
            existing = self._runs.get(run_id)
            if (
                existing is not None
                and existing.conversation_id == conversation_id
                and existing.session_id == session_id
            ):
                # Streaming wrappers may register before the inner agent is
                # constructed. Preserve guidance accepted during that window.
                return
            self._runs[run_id] = _ActiveRun(conversation_id, session_id)

    def accept(
        self, run_id: str, conversation_id: str, steer_id: str, content: str,
    ) -> tuple[bool, str]:
        with self._lock:
            active = self._runs.get(run_id)
            if active is None or not active.accepting or active.conversation_id != conversation_id:
                return False, 'response_not_active'
            if steer_id in active.known_ids:
                return True, ''
            active.known_ids.add(steer_id)
            active.pending.append(SteeringInput(steer_id, content, time.time()))
            return True, ''

    def drain_or_close(self, run_id: str) -> list[SteeringInput]:
        """The continuation loop's single consumer and atomic completion boundary."""
        with self._lock:
            active = self._runs.get(run_id)
            if active is None:
                return []
            items = list(active.pending)
            active.pending.clear()
            if not items:
                active.accepting = False
            return items

    def has_pending_for_session(self, session_id: str) -> bool:
        """Return whether an active run needs its current model stream interrupted."""
        with self._lock:
            return any(
                active.accepting and active.session_id == session_id and bool(active.pending)
                for active in self._runs.values()
            )

    def stop_accepting_for_session(self, conversation_id: str, session_id: str) -> bool:
        """Reject guidance as soon as cancellation starts for this exact session.

        The chat stream may take a short time to observe its cancellation signal.
        Closing the inbox first prevents guidance submitted in that window from
        being reported as accepted even though the cancelled run cannot consume it.
        """
        stopped = False
        with self._lock:
            for active in self._runs.values():
                if active.conversation_id != conversation_id or active.session_id != session_id:
                    continue
                active.accepting = False
                stopped = True
        return stopped

    def unregister(self, run_id: str) -> None:
        with self._lock:
            self._runs.pop(run_id, None)


steering_registry = SteeringRegistry()


def check_stream_interrupt() -> None:
    import lazyllm

    session_id = lazyllm.globals._sid
    if steering_registry.has_pending_for_session(session_id):
        raise SteeringInterruptedError(
            f'model stream interrupted by guidance for session {session_id}'
        )


def _install_output_interrupt_hook(target: object) -> bool:
    if getattr(target, '_lazymind_steering_interrupt_installed', False):
        return True
    original = getattr(target, '_stream_output', None)
    if not callable(original):
        return False

    def interruptible_stream_output(
        self: object,
        text: str,
        color: str | None = None,
        *,
        cls: str | None = 'text',
    ) -> str:
        check_stream_interrupt()
        return original(text, color, cls=cls)

    target._stream_output = MethodType(interruptible_stream_output, target)
    target._lazymind_steering_interrupt_installed = True
    return True


def install_stream_interrupt_hook(llm: object) -> bool:
    """Make streamed model output observe newly accepted guidance.

    A dynamic LazyLLM supplier can only be resolved inside the module's active
    configuration stack. In that case, wrap ``forward`` now and attach the
    output hook lazily after the stack has been entered. Static models can be
    hooked directly. The interrupt check itself is scoped by session ID.
    """
    from .model_fallback import FallbackLLM

    if isinstance(llm, FallbackLLM):
        # FallbackLLM streams through copied supplier views, not its inherited
        # _stream_output. Install on the owner of those calls instead.
        llm._steering_interrupt_check = check_stream_interrupt
        return True
    get_supplier = getattr(llm, '_get_supplier', None)
    if not callable(get_supplier):
        return _install_output_interrupt_hook(llm)
    if getattr(llm, '_lazymind_steering_router_installed', False):
        return True
    original_forward = getattr(llm, 'forward', None)
    if not callable(original_forward):
        return False

    def interruptible_forward(self: object, *args: object, **kwargs: object) -> object:
        supplier = self._get_supplier()
        _install_output_interrupt_hook(supplier)
        return original_forward(*args, **kwargs)

    llm.forward = MethodType(interruptible_forward, llm)
    llm._lazymind_steering_router_installed = True
    return True


def render_steering_context(items: list[SteeringInput]) -> str | None:
    if not items:
        return None
    updates = '\n'.join(f'{index}. {item.content}' for index, item in enumerate(items, 1))
    return (
        '### New user guidance\n\n'
        'The user supplied these updates while this task was running. Treat them as the newest '
        'requirements for all subsequent reasoning and actions. Do not undo completed side effects; '
        'adjust the remaining work at the next safe boundary. When this guidance already provides '
        'a concrete scope, constraint, correction, or choice, apply it immediately. Do not call '
        'ask_user merely to confirm or repeat requirements that the guidance has already made '
        'explicit. Keep ask_user available only when essential information is still genuinely '
        'missing or the guidance itself asks you to present a choice.\n\n'
        f'{updates}'
    )
