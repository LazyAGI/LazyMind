from __future__ import annotations

import json
import http.client
import socket
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import lazyllm
import pytest
from lazyllm.module.llms.onlinemodule.base.model_request import ModelCallControl, model_response, response_frames

from lazymind.chat.engine.agent_runtime.cancellation import (
    UserCancelledError,
    make_cancel_stop_condition,
    request_cancel,
)


def test_user_cancel_signal_raises_domain_cancellation(monkeypatch):
    from lazyllm.common import queue as queue_module

    class FakeQueue:
        def __init__(self, *, klass):
            assert klass == 'cancel'

        def dequeue(self):
            return [json.dumps({'tag': 'cancel'})]

    monkeypatch.setattr(queue_module, 'FileSystemQueue', FakeQueue)

    with pytest.raises(UserCancelledError, match='stopped by user'):
        make_cancel_stop_condition()(None)


def test_non_user_queue_failure_does_not_cancel(monkeypatch):
    from lazyllm.common import queue as queue_module

    class BrokenQueue:
        def __init__(self, *, klass):
            assert klass == 'cancel'

        def dequeue(self):
            raise RuntimeError('state unavailable')

    monkeypatch.setattr(queue_module, 'FileSystemQueue', BrokenQueue)

    assert make_cancel_stop_condition()(None) is False


@pytest.mark.parametrize('phase', ['headers', 'body'])
@pytest.mark.parametrize('stream', [False, True])
def test_workflow_cancel_interrupts_blocked_model_read(monkeypatch, phase, stream):
    # Windows shutdown does not reliably wake a timed socket read. Cancellation
    # must make progress even when cross-thread shutdown cannot interrupt it.
    monkeypatch.setattr(socket.socket, 'shutdown', lambda *_args: None)
    entered_read, reached_server, release, finished = (threading.Event() for _ in range(4))
    outcomes, requests = [], []
    sid = 'cancel-model-' + uuid.uuid4().hex
    read_method = '_read_status' if phase == 'headers' else 'read1'
    original_read = getattr(http.client.HTTPResponse, read_method)

    def observed_read(response, *args, **kwargs):
        entered_read.set()
        return original_read(response, *args, **kwargs)

    monkeypatch.setattr(http.client.HTTPResponse, read_method, observed_read)

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            self.rfile.read(int(self.headers['Content-Length']))
            requests.append(self.path)
            reached_server.set()
            if phase == 'headers' and len(requests) == 1:
                release.wait(3)
            self.send_response(200)
            self.send_header('Content-Length', '2')
            self.end_headers()
            self.wfile.flush()
            if phase == 'body' and len(requests) == 1:
                release.wait(3)
            try:
                self.wfile.write(b'ok')
            except OSError:
                pass  # Cancellation closes this connection before the response.

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    serving = threading.Thread(target=lambda: server.serve_forever(poll_interval=0.01), daemon=True)
    serving.start()
    url = f'http://127.0.0.1:{server.server_port}/model'

    def execute():
        with lazyllm.globals._bind_sid(sid):
            check = make_cancel_stop_condition()
            control = ModelCallControl(cancel_check=lambda: check(None))
            try:
                with model_response(url, json={}, stream=True, timeout=5, control=control) as response:
                    list(response_frames(response, stream, control))
            except (Exception, UserCancelledError) as exc:
                outcomes.append(exc)
            finally:
                finished.set()

    worker = threading.Thread(target=execute, daemon=True)
    worker.start()
    try:
        assert reached_server.wait(2)
        assert entered_read.wait(2)
        request_cancel(sid)
        assert finished.wait(1), 'cancel waited for the model response or network timeout'
        assert len(outcomes) == 1 and isinstance(outcomes[0], UserCancelledError), outcomes
        assert requests == ['/model'], 'cancellation must not retry the model request'
        # A later execution in the same session must not inherit cancellation.
        release.set()
        with lazyllm.globals._bind_sid(sid):
            check = make_cancel_stop_condition()
            control = ModelCallControl(cancel_check=lambda: check(None))
            with model_response(url, json={}, stream=True, timeout=2, control=control) as response:
                assert list(response_frames(response, False, control)) == ['ok']
    finally:
        release.set()
        worker.join(5)
        server.shutdown()
        server.server_close()
        serving.join(2)
    assert not worker.is_alive()
