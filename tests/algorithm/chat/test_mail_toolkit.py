import json
from unittest.mock import patch

import lazyllm
import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.mail import MailToolkit, _resolve_imap_endpoint, _save_draft


@pytest.fixture
def mail_auth(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_AGENTIC_WORKSPACE', str(tmp_path))
    lazyllm.globals.config['dynamic_tool_auth'] = {
        'mail': json.dumps({
            'provider': 'qqmail',
            'email': 'user@qq.com',
            'secret': 'auth-code',
            'status': 'ACTIVE',
        }),
    }
    lazyllm.globals['agentic_config'] = {
        'user_id': 'u1',
        'conversation_id': 'c1',
        'mail_draft_confirm_id': '',
        'query': 'send mail',
    }
    yield
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    lazyllm.globals['agentic_config'] = {}


def test_mail_search_disconnected():
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    with pytest.raises(ToolExecutionError, match='mail_disconnected'):
        MailToolkit().search(keyword='invoice')


def test_mail_search_filters(mail_auth):
    toolkit = MailToolkit()
    with patch.object(toolkit, 'search', wraps=toolkit.search):
        with patch('lazymind.chat.engine.tools.mail._IMAPBackend.search', return_value={'ok': True, 'items': []}):
            result = toolkit.search(keyword='合同', sender='a@b.com')
    assert result['ok'] is True


def test_send_requires_user_confirmation(mail_auth, tmp_path):
    draft = {
        'draft_id': 'draft_abc',
        'to': ['a@b.com'],
        'cc': [],
        'subject': 'hi',
        'body': 'body',
        'attachment_paths': [],
        'in_reply_to': '',
        'status': 'draft',
        'sent_at': '',
        'last_error': '',
    }
    _save_draft(draft)
    result = MailToolkit().send_draft('draft_abc', confirm=True)
    assert result['ok'] is False
    assert result['error_code'] == 'mail_confirm_required'
    assert result['status'] == 'draft'


def test_send_after_confirm(mail_auth):
    draft = {
        'draft_id': 'draft_ok',
        'to': ['a@b.com'],
        'cc': [],
        'subject': 'hi',
        'body': 'body',
        'attachment_paths': [],
        'in_reply_to': '',
        'status': 'draft',
        'sent_at': '',
        'last_error': '',
    }
    _save_draft(draft)
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_ok'
    with patch(
        'lazymind.chat.engine.tools.mail._IMAPBackend.send',
        return_value={'ok': True, 'id': 'm1', 'sent_at': '2026-09-01T00:00:00+00:00'},
    ):
        result = MailToolkit().send_draft('draft_ok', confirm=False)
    assert result['ok'] is True
    assert result['status'] == 'sent'
    assert result['sent_at']


def test_search_merges_enabled_mailboxes(mail_auth):
    lazyllm.globals.config['dynamic_tool_auth'] = {
        'mail': [
            json.dumps({
                'provider': 'qqmail',
                'email': 'a@qq.com',
                'secret': 'auth-a',
                'status': 'ACTIVE',
            }),
            json.dumps({
                'provider': 'netease163',
                'email': 'b@163.com',
                'secret': 'auth-b',
                'status': 'ACTIVE',
            }),
        ],
    }

    class FakeBackend:
        def __init__(self, cred):
            self.cred = cred

        def search(self, **kwargs):
            return {'ok': True, 'items': [{'id': '1', 'subject': self.cred['email']}]}

    with patch('lazymind.chat.engine.tools.mail._backend', side_effect=lambda cred: FakeBackend(cred)):
        result = MailToolkit().search(keyword='invoice')
        filtered = MailToolkit().search(keyword='invoice', mailbox='b@163.com')

    assert result['ok'] is True
    assert {item['mailbox'] for item in result['items']} == {'a@qq.com', 'b@163.com'}
    assert filtered['mailboxes'] == ['b@163.com']
    assert filtered['items'][0]['mailbox'] == 'b@163.com'


def test_compose_accepts_string_attachment_path(mail_auth, tmp_path):
    attachment = tmp_path / 'attachment_test.txt'
    attachment.write_text('hello attachment', encoding='utf-8')
    result = MailToolkit().compose_draft(
        to='a@b.com',
        subject='with file',
        body='body',
        attachment_paths=str(attachment),
    )
    assert result['ok'] is True
    assert result['attachments'] == ['attachment_test.txt']


def test_compose_rejects_missing_attachment(mail_auth):
    with pytest.raises(ToolExecutionError, match='mail_attachment_missing'):
        MailToolkit().compose_draft(
            to='a@b.com',
            subject='missing',
            body='body',
            attachment_paths='definitely_no_such_file_98765.txt',
        )


def test_imap_endpoint_routes_netease_and_gmail():
    netease = _resolve_imap_endpoint('netease163', 'name@yeah.net')
    assert netease['imap_host'] == 'imap.yeah.net'
    gmail_imap = _resolve_imap_endpoint('gmailimap', 'user@workspace.com')
    assert gmail_imap['imap_host'] == 'imap.gmail.com'
    exmail = _resolve_imap_endpoint('qqexmail', 'hr@acme.cn')
    assert exmail['imap_host'] == 'imap.exmail.qq.com'
