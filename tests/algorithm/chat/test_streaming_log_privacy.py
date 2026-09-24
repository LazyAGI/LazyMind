import json

from lazymind.chat.service.utils import streaming


def test_frame_logging_omits_user_input_and_payload_but_preserves_stream(monkeypatch):
    logs = []
    monkeypatch.setattr(streaming.LOG, 'debug', lambda message: logs.append(message))
    frame = {'text': 'synthetic-frame-secret', 'tool_calls': []}
    emitted = streaming.log_and_emit_frame(frame, 0.5, 'TOKEN=synthetic-query-secret', 'test-session')
    assert json.loads(emitted)['data'] == frame
    assert 'test-session' in logs[0]
    assert 'query_length=' in logs[0]
    assert 'synthetic-query-secret' not in logs[0]
    assert 'synthetic-frame-secret' not in logs[0]
