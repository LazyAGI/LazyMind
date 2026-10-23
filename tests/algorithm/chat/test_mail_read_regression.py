"""Regression coverage for IMAP section ordering and directory identity."""
from types import SimpleNamespace

import pytest
from lazyllm.tools.agent import ToolExecutionDisposition

from lazymind.chat.engine.tools import mail
from lazymind.directory.runtime import DirectoryRuntime


class SectionIMAP:
    def __init__(self, body=b'Original body', content_type='text/plain', fail_partial=False):
        self.header = (f'Message-ID: <one@example.com>\r\nSubject: Original subject\r\n'
                       f'From: sender@example.com\r\nTo: reader@example.com\r\n'
                       f'Content-Type: {content_type}\r\n\r\n').encode()
        self.body = body
        self.fail_partial = fail_partial
        self.readonly = []
        self.logged_out = False
        self.fetches = []

    def select(self, folder, readonly=False):
        self.readonly.append(readonly)
        return 'OK', [b'1']

    def uid(self, command, uid, spec):
        assert command == 'FETCH'
        self.fetches.append(spec)
        if 'HEADER' in spec and 'TEXT' in spec:
            # Gmail can return the requested sections in the opposite order.
            return 'OK', [(b'1 (BODY[TEXT] {20000}', self.body[:20000]),
                          (b' BODY[HEADER] {200}', self.header), b')']
        if self.fail_partial and 'BODY.PEEK' in spec:
            return 'NO', [b'partial fetch unavailable']
        if 'HEADER' in spec:
            return 'OK', [(b'1 (BODY[HEADER] {200}', self.header), b')']
        if 'TEXT' in spec:
            return 'OK', [(b'1 (BODY[TEXT]<0> {20001}', self.body[:20001]), b')']
        assert spec == '(RFC822)'
        return 'OK', [(b'1 (RFC822 {500}', self.header + self.body), b')']

    def logout(self):
        self.logged_out = True


def backend(monkeypatch, client):
    value = mail._IMAPBackend({'provider': 'gmailimap', 'email': 'reader@example.com', 'secret': 'unused'})
    monkeypatch.setattr(value, '_connect', lambda: client)
    monkeypatch.setattr(value, '_list_structure_attachments', lambda *_: [])
    return value


def candidate(tool, result):
    record = SimpleNamespace(tool_name=tool, result=result, arguments={}, validated_arguments={},
                             call_id='read-1', disposition=ToolExecutionDisposition.EXECUTED, reason='')
    return DirectoryRuntime('user', 'conversation', 'history', 'run', None).normalize(record)['candidates'][0]


@pytest.mark.parametrize('body,content_type,expected', [
    (b'Original body', 'text/plain', 'Original body'),
    (b'<p>Original body</p>', 'text/html', 'Original body'),
    (b'--part\r\nContent-Type: text/plain\r\n\r\nOriginal body\r\n'
     b'--part\r\nContent-Type: application/octet-stream\r\n'
     b'Content-Disposition: attachment; filename=example.txt\r\n\r\nattachment\r\n--part--\r\n',
     'multipart/mixed; boundary=part', 'Original body'),
])
def test_read_preserves_headers_and_search_identity_when_sections_can_be_reversed(
    monkeypatch, body, content_type, expected,
):
    client = SectionIMAP(body, content_type)
    result = backend(monkeypatch, client).read('INBOX::42')
    assert (result['subject'], result['from'], result['thread_id']) == (
        'Original subject', 'sender@example.com', '<one@example.com>')
    assert result['body'].strip() == expected
    assert result['truncated'] is False
    if 'mixed' in content_type:
        assert result['attachments'][0]['filename'] == 'example.txt'
    result['mailbox'] = 'reader@example.com'
    searched = candidate('mail_search', {'mailbox': result['mailbox'], 'items': [
        {'id': 'INBOX::42', 'subject': result['subject'], 'thread_id': '<one@example.com>'}]})
    read = candidate('mail_read', result)
    assert tuple(read[k] for k in ('source', 'namespace', 'resource_id')) == tuple(
        searched[k] for k in ('source', 'namespace', 'resource_id'))
    assert read['read_state'] == 'read'
    assert client.readonly and all(client.readonly)
    assert client.logged_out


@pytest.mark.parametrize('size,truncated', [(20000, False), (20001, True)])
def test_read_reports_transport_boundary(monkeypatch, size, truncated):
    result = backend(monkeypatch, SectionIMAP(b'x' * size)).read('INBOX::42')
    assert len(result['body']) == 20000
    assert result['truncated'] is truncated


def test_clipped_html_does_not_truncate_complete_plain_alternative(monkeypatch):
    body = (b'--part\r\nContent-Type: text/plain\r\n\r\nComplete original\r\n'
            b'--part\r\nContent-Type: text/html\r\n\r\n' + b'x' * 20001)
    result = backend(monkeypatch, SectionIMAP(body, 'multipart/alternative; boundary=part')).read('INBOX::42')
    assert result['body'].strip() == 'Complete original'
    assert result['truncated'] is False


@pytest.mark.parametrize('body,fail_partial', [(b'', False), (b'Original body', True)])
def test_full_message_fallback_preserves_identity_and_connection_ownership(monkeypatch, body, fail_partial):
    client = SectionIMAP(body, fail_partial=fail_partial)
    result = backend(monkeypatch, client)._read_message('INBOX::42', client)
    assert result['thread_id'] == '<one@example.com>'
    assert result['body'] == body.decode()
    assert '(RFC822)' in client.fetches
    assert not client.logged_out
    assert all(client.readonly)


def test_thread_uses_corrected_reader_and_closes_shared_connection(monkeypatch):
    client = SectionIMAP()
    value = backend(monkeypatch, client)
    monkeypatch.setattr(mail, '_resolve_search_folders', lambda *_: ['INBOX'])
    monkeypatch.setattr(value, '_search_thread_uids', lambda *_: ['INBOX::42'])
    result = value.read_thread('<one@example.com>')
    assert result['messages'][0]['subject'] == 'Original subject'
    assert result['messages'][0]['thread_id'] == '<one@example.com>'
    assert client.logged_out


def test_failed_full_fallback_raises_and_closes_connection(monkeypatch):
    client = SectionIMAP()
    monkeypatch.setattr(client, 'uid', lambda *_: ('NO', [b'fetch unavailable']))
    with pytest.raises(mail.ToolExecutionError):
        backend(monkeypatch, client).read('INBOX::42')
    assert client.logged_out
