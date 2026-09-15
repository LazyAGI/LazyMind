from lazymind.chat.engine.agent_runtime import telemetry


def _unexpected_call(*_args, **_kwargs):
    raise AssertionError('disabled telemetry must not process event payloads')


def test_disabled_tool_call_telemetry_does_not_process_arguments(monkeypatch):
    monkeypatch.setattr(telemetry, 'telemetry_enabled', lambda: False)
    monkeypatch.setattr(telemetry.json, 'loads', _unexpected_call)
    monkeypatch.setattr(telemetry, 'redact_session_env_arguments', _unexpected_call)
    monkeypatch.setattr(telemetry, 'classify_tool', _unexpected_call)
    monkeypatch.setattr(telemetry, '_size_bytes', _unexpected_call)
    monkeypatch.setattr(telemetry, '_preview', _unexpected_call)
    monkeypatch.setattr(telemetry, 'append_event', _unexpected_call)

    telemetry.emit_tool_call({
        'id': 'call-1',
        'function': {'name': 'search', 'arguments': '{"query":"secret"}'},
    })


def test_disabled_tool_result_telemetry_does_not_serialize_result(monkeypatch):
    monkeypatch.setattr(telemetry, 'telemetry_enabled', lambda: False)
    monkeypatch.setattr(telemetry, 'classify_tool', _unexpected_call)
    monkeypatch.setattr(telemetry, '_size_bytes', _unexpected_call)
    monkeypatch.setattr(telemetry, '_preview', _unexpected_call)
    monkeypatch.setattr(telemetry, 'append_event', _unexpected_call)

    telemetry.emit_tool_result(
        {'id': 'call-1', 'function': {'name': 'search'}},
        {'ok': True, 'value': {'secret': 'must not be serialized'}},
    )


def test_enabled_tool_result_telemetry_still_emits_event(monkeypatch):
    events = []
    monkeypatch.setattr(telemetry, 'telemetry_enabled', lambda: True)
    monkeypatch.setattr(
        telemetry,
        'append_event',
        lambda kind, **payload: events.append((kind, payload)),
    )

    telemetry.emit_tool_result(
        {'id': 'call-1', 'function': {'name': 'search'}},
        {'ok': True, 'value': ['result']},
    )

    assert len(events) == 1
    kind, payload = events[0]
    assert kind == 'tool_result'
    assert payload['tool_call_id'] == 'call-1'
    assert payload['ok'] is True
    assert payload['result_preview']
