from unittest.mock import Mock

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from lazymind.chat.api.mail_read_routes import router, require_env_input_service
from lazymind.chat.engine.tools.mail import _IMAPBackend


@pytest.mark.parametrize('html', [False, True])
def test_preview_verifies_identity_and_reads_text_without_side_effects(monkeypatch, html):
    backend = _IMAPBackend({'provider': 'qqmail', 'email': 'a@qq.com', 'secret': 'secret'})
    client = Mock()
    monkeypatch.setattr(backend, '_connect', lambda: client)
    monkeypatch.setattr(backend, '_list_structure_attachments', lambda *_: [])
    header = (b'Message-ID: <one@example.com>\r\nSubject: Example\r\nContent-Type: text/'
              + (b'html' if html else b'plain') + b'; charset=utf-8\r\n\r\n')
    body = b'<style>hidden</style><p>Hello &amp; welcome</p><script>evil()</script>' if html else b'Hello & welcome'
    calls = []

    def fetch(ref, spec, conn):
        calls.append(spec)
        return header if 'HEADER' in spec else body
    monkeypatch.setattr(backend, '_fetch_raw', fetch)
    result = backend.read_preview('INBOX::1', '<one@example.com>')
    assert result['body'].strip() == 'Hello & welcome'
    assert result['truncated'] is False
    assert all('BODY.PEEK' in call for call in calls)
    assert client.logout.called
    calls.clear()
    with pytest.raises(ValueError, match='mail_location_changed'):
        backend.read_preview('INBOX::1', '<other@example.com>')
    assert calls == ['(BODY.PEEK[HEADER])']


@pytest.mark.parametrize('size, truncated', [(20000, False), (20001, True)])
def test_preview_reports_transport_truncation(monkeypatch, size, truncated):
    backend = _IMAPBackend({'provider': 'qqmail', 'email': 'a@qq.com', 'secret': 'secret'})
    monkeypatch.setattr(backend, '_connect', Mock())
    monkeypatch.setattr(backend, '_list_structure_attachments', lambda *_: [])
    monkeypatch.setattr(backend, '_fetch_raw', lambda ref, spec, client:
                        b'Message-ID: <one@example.com>\r\n\r\n' if 'HEADER' in spec else b'x' * size)
    result = backend.read_preview('INBOX::1', '<one@example.com>')
    assert result['truncated'] is truncated
    assert len(result['body']) == 20000


def test_internal_endpoint_requires_auth_and_sanitizes_failures(monkeypatch):
    app = FastAPI()
    app.include_router(router)
    client = TestClient(app)
    payload = {'provider': 'qqmail', 'email': 'a@qq.com', 'secret': 'private',
               'message_ref': 'INBOX::1', 'message_id': '<one@example.com>'}
    assert client.post('/api/mail/read', json=payload).status_code == 401
    app.dependency_overrides[require_env_input_service] = lambda: None
    monkeypatch.setattr(_IMAPBackend, 'read_preview', Mock(side_effect=RuntimeError('private provider data')))
    response = client.post('/api/mail/read', json=payload)
    assert response.json() == {'error_code': 'mail_read_failed'}
    monkeypatch.setattr(_IMAPBackend, 'read_preview', Mock(side_effect=TimeoutError()))
    assert client.post('/api/mail/read', json=payload).json() == {'error_code': 'mail_timeout'}


def test_complete_plain_text_is_not_marked_truncated_by_a_long_html_alternative(monkeypatch):
    backend = _IMAPBackend({'provider': 'qqmail', 'email': 'a@qq.com', 'secret': 'secret'})
    monkeypatch.setattr(backend, '_connect', Mock())
    monkeypatch.setattr(backend, '_list_structure_attachments', lambda *_: [])
    header = b'Message-ID: <one@example.com>\r\nContent-Type: multipart/alternative; boundary=part\r\n\r\n'
    body = (b'--part\r\nContent-Type: text/plain\r\n\r\nComplete original\r\n'
            b'--part\r\nContent-Type: text/html\r\n\r\n' + b'x' * 20001)
    monkeypatch.setattr(backend, '_fetch_raw', lambda ref, spec, client: header if 'HEADER' in spec else body[:20001])
    result = backend.read_preview('INBOX::1', '<one@example.com>')
    assert result['body'].strip() == 'Complete original'
    assert result['truncated'] is False


def test_truncation_before_text_is_not_reported_as_an_empty_complete_email(monkeypatch):
    backend = _IMAPBackend({'provider': 'qqmail', 'email': 'a@qq.com', 'secret': 'secret'})
    monkeypatch.setattr(backend, '_connect', Mock())
    monkeypatch.setattr(backend, '_list_structure_attachments', lambda *_: [])
    header = b'Message-ID: <one@example.com>\r\nContent-Type: multipart/mixed; boundary=part\r\n\r\n'
    body = (b'--part\r\nContent-Type: application/octet-stream\r\n'
            b'Content-Disposition: attachment; filename=large.bin\r\n\r\n' + b'x' * 20001)
    monkeypatch.setattr(backend, '_fetch_raw', lambda ref, spec, client: header if 'HEADER' in spec else body[:20001])
    result = backend.read_preview('INBOX::1', '<one@example.com>')
    assert result['body'] == ''
    assert result['truncated'] is True
