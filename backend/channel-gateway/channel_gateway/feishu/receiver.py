import multiprocessing
import queue
import threading
import time
import uuid
from collections.abc import Callable
from dataclasses import asdict

from channel_gateway.feishu.domain import (
    FeishuAppCredentials,
    FeishuInboundAction,
    FeishuInboundMenu,
    FeishuInboundMessage,
    FeishuRuntimeError,
)
from channel_gateway.feishu.sdk import LarkChannelClient


_ACK_TIMEOUT_SECONDS = 10
_ACTION_ACK_TIMEOUT_SECONDS = 2.0


def _await_persistence_ack(
    acknowledgements,
    stop_event,
    acknowledgement_id: str,
    timeout_seconds: float = _ACK_TIMEOUT_SECONDS,
):
    deadline = time.monotonic() + timeout_seconds
    while not stop_event.is_set():
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise FeishuRuntimeError(
                'Gateway persistence acknowledgement timed out'
            )
        try:
            ack_id, succeeded, error, response = acknowledgements.get(
                timeout=min(0.5, remaining)
            )
        except queue.Empty:
            continue
        if ack_id != acknowledgement_id:
            # Late acknowledgements belong to an event that already timed out.
            continue
        if not succeeded:
            raise FeishuRuntimeError(
                str(error or 'Gateway persistence failed')
            )
        return response
    raise FeishuRuntimeError(
        'Feishu receiver stopped before message persistence'
    )


def _receiver_process_main(
    credentials_payload: dict[str, str],
    events,
    acknowledgements,
    stop_event,
) -> None:
    credentials = FeishuAppCredentials(
        **credentials_payload
    )

    delivery_lock = threading.Lock()

    def on_message(message: FeishuInboundMessage) -> None:
        acknowledgement_id = uuid.uuid4().hex
        with delivery_lock:
            events.put(
                (
                    'message',
                    {
                        'acknowledgement_id': acknowledgement_id,
                        'message': asdict(message),
                    },
                )
            )
            _await_persistence_ack(
                acknowledgements,
                stop_event,
                acknowledgement_id,
            )

    def on_action(action: FeishuInboundAction):
        acknowledgement_id = uuid.uuid4().hex
        with delivery_lock:
            events.put(
                (
                    'action',
                    {
                        'acknowledgement_id': acknowledgement_id,
                        'action': asdict(action),
                    },
                )
            )
            return _await_persistence_ack(
                acknowledgements,
                stop_event,
                acknowledgement_id,
                timeout_seconds=_ACTION_ACK_TIMEOUT_SECONDS,
            )

    def on_menu(menu: FeishuInboundMenu) -> None:
        acknowledgement_id = uuid.uuid4().hex
        with delivery_lock:
            events.put(
                (
                    'menu',
                    {
                        'acknowledgement_id': acknowledgement_id,
                        'menu': asdict(menu),
                    },
                )
            )
            _await_persistence_ack(
                acknowledgements,
                stop_event,
                acknowledgement_id,
            )

    client = LarkChannelClient(
        credentials,
        on_message,
        on_action=on_action,
        on_menu=on_menu,
    )

    def watch() -> None:
        ready_reported = False
        last_state = ''
        while not stop_event.wait(0.2):
            state = client.connection_state()
            if state != last_state:
                events.put(('state', state))
                last_state = state
            if client.is_ready() and not ready_reported:
                events.put(('ready', 'connected'))
                ready_reported = True
        client.stop()

    watcher = threading.Thread(
        target=watch,
        name='feishu-process-stop',
        daemon=True,
    )
    watcher.start()
    try:
        client.start_blocking()
    except Exception as exc:
        if not stop_event.is_set():
            events.put(('error', str(exc)))
    finally:
        stop_event.set()
        client.stop()
        watcher.join(timeout=2)
        events.put(('stopped', 'stopped'))


class ProcessLarkReceiverClient:
    """Isolates the SDK's module-global WebSocket loop per Feishu app."""

    def __init__(
        self,
        credentials: FeishuAppCredentials,
        on_message: Callable[[FeishuInboundMessage], None],
        on_action: Callable[
            [FeishuInboundAction],
            dict | None,
        ],
        on_menu: Callable[[FeishuInboundMenu], None],
    ):
        context = multiprocessing.get_context('spawn')
        self._events = context.Queue()
        self._acknowledgements = context.Queue()
        self._stop_event = context.Event()
        self._on_message = on_message
        self._on_action = on_action
        self._on_menu = on_menu
        self._process = context.Process(
            target=_receiver_process_main,
            args=(
                asdict(credentials),
                self._events,
                self._acknowledgements,
                self._stop_event,
            ),
            name=f'feishu-ws-{credentials.app_id[-8:]}',
            daemon=True,
        )
        self._state = 'idle'
        self._ready = False
        self._lock = threading.Lock()

    def start(self) -> None:
        self._process.start()
        terminal_error = ''
        while True:
            try:
                kind, payload = self._events.get(timeout=0.5)
            except queue.Empty:
                if not self._process.is_alive():
                    break
                continue
            if kind == 'message':
                acknowledgement_id = str(
                    payload['acknowledgement_id']
                )
                try:
                    self._on_message(
                        FeishuInboundMessage(
                            **payload['message']
                        )
                    )
                except Exception as exc:
                    self._acknowledgements.put(
                        (
                            acknowledgement_id,
                            False,
                            exc.__class__.__name__,
                            None,
                        )
                    )
                    terminal_error = str(exc)
                    break
                else:
                    self._acknowledgements.put(
                        (acknowledgement_id, True, '', None)
                    )
            elif kind == 'action':
                acknowledgement_id = str(
                    payload['acknowledgement_id']
                )
                try:
                    response = self._on_action(
                        FeishuInboundAction(
                            **payload['action']
                        )
                    )
                except Exception as exc:
                    self._acknowledgements.put(
                        (
                            acknowledgement_id,
                            False,
                            exc.__class__.__name__,
                            None,
                        )
                    )
                    terminal_error = str(exc)
                    break
                else:
                    self._acknowledgements.put(
                        (acknowledgement_id, True, '', response)
                    )
            elif kind == 'menu':
                acknowledgement_id = str(
                    payload['acknowledgement_id']
                )
                try:
                    self._on_menu(FeishuInboundMenu(**payload['menu']))
                except Exception as exc:
                    self._acknowledgements.put(
                        (
                            acknowledgement_id,
                            False,
                            exc.__class__.__name__,
                            None,
                        )
                    )
                    terminal_error = str(exc)
                    break
                else:
                    self._acknowledgements.put(
                        (acknowledgement_id, True, '', None)
                    )
            elif kind == 'ready':
                with self._lock:
                    self._ready = True
                    self._state = 'connected'
            elif kind == 'state':
                with self._lock:
                    self._state = str(payload)
            elif kind == 'error':
                terminal_error = str(payload)
                break
            elif kind == 'stopped':
                break
        self._stop_event.set()
        self._process.join(timeout=5)
        if terminal_error:
            raise FeishuRuntimeError(terminal_error)
        if (
            self._process.exitcode not in (0, None)
            and not self._ready
        ):
            raise FeishuRuntimeError(
                'Feishu receiver process stopped unexpectedly'
            )

    def stop(self) -> None:
        self._stop_event.set()
        if (
            self._process.pid is not None
            and self._process.is_alive()
        ):
            self._process.join(timeout=5)
        if self._process.is_alive():
            self._process.terminate()
            self._process.join(timeout=2)
        with self._lock:
            self._ready = False
            self._state = 'stopped'

    def is_ready(self) -> bool:
        with self._lock:
            return self._ready

    def connection_state(self) -> str:
        with self._lock:
            return self._state


class LarkChannelFactory:
    def create_receiver(
        self,
        credentials: FeishuAppCredentials,
        on_message: Callable[[FeishuInboundMessage], None],
        on_action: Callable[[FeishuInboundAction], dict | None],
        on_menu: Callable[[FeishuInboundMenu], None],
    ) -> ProcessLarkReceiverClient:
        return ProcessLarkReceiverClient(
            credentials,
            on_message,
            on_action,
            on_menu,
        )

    def create_sender(
        self,
        credentials: FeishuAppCredentials,
    ) -> LarkChannelClient:
        return LarkChannelClient(credentials)
