import base64
from pathlib import Path
from types import SimpleNamespace

import lazyllm

from lazymind.chat.engine.subagent import tools


def test_find_artifact_materializes_data_image_without_returning_payload(monkeypatch, tmp_path):
    image_bytes = b'\x89PNG\r\n\x1a\n' + (b'image-content' * 1000)
    data_url = 'data:image/png;base64,' + base64.b64encode(image_bytes).decode('ascii')
    monkeypatch.setattr(
        tools, 'get_context', lambda: SimpleNamespace(workspace_path=str(tmp_path)),
    )
    monkeypatch.setattr(
        tools,
        'get_artifact',
        lambda slot, sort_order=None: {
            'success': True,
            'result': {
                'status': 'ok',
                'artifacts': [{'value': {'text': data_url}}],
            },
        },
    )
    monkeypatch.setattr(
        tools,
        '_materialize_local_path',
        lambda path: path,
    )
    monkeypatch.setattr(
        tools,
        '_sign_static_file_url',
        lambda path: (_ for _ in ()).throw(AssertionError('data URL reached URL signer')),
    )

    old_config = lazyllm.globals.get('agentic_config')
    try:
        lazyllm.globals['agentic_config'] = {'workflow_session_id': 'session-1'}
        result = tools.find_artifact('images')
    finally:
        lazyllm.globals['agentic_config'] = old_config or {}

    assert result['success'] is True
    assert result['result']['status'] == 'ok'
    path = result['result']['path']
    assert path != data_url
    assert result['result']['url'] == path
    assert Path(path).read_bytes() == image_bytes
    assert len(str(result)) < 1000


def test_find_artifact_does_not_treat_plain_text_as_a_path(monkeypatch):
    monkeypatch.setattr(tools, 'get_context', lambda: object())
    monkeypatch.setattr(
        tools,
        'get_artifact',
        lambda slot, sort_order=None: {
            'success': True,
            'result': {
                'status': 'ok',
                'artifacts': [{'value': {'text': 'ordinary artifact content'}}],
            },
        },
    )

    old_config = lazyllm.globals.get('agentic_config')
    try:
        lazyllm.globals['agentic_config'] = {'workflow_session_id': 'session-1'}
        result = tools.find_artifact('report')
    finally:
        lazyllm.globals['agentic_config'] = old_config or {}

    assert result['success'] is True
    assert result['result']['status'] == 'error'
    assert 'no resolvable path' in result['result']['message']
