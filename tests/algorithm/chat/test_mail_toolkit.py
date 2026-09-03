import json
import os
from unittest.mock import patch

import lazyllm
import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.local_file.workspace import chat_agent_workspace
from lazymind.chat.engine.tools.mail import (
    MailToolkit,
    _imap_date,
    _load_draft,
    _resolve_imap_endpoint,
    _save_draft,
)


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
        'mail_draft_confirm_revision': 0,
        'query': 'send mail',
    }
    yield
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    lazyllm.globals['agentic_config'] = {}


def test_mail_search_disconnected():
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    with pytest.raises(ToolExecutionError, match='No mailbox is enabled'):
        MailToolkit().search(keyword='invoice')


def test_mail_search_filters(mail_auth):
    toolkit = MailToolkit()
    with patch.object(toolkit, 'search', wraps=toolkit.search):
        with patch('lazymind.chat.engine.tools.mail._IMAPBackend.search', return_value={'items': []}):
            result = toolkit.search(keyword='合同', sender='a@b.com')
    assert result['items'] == []


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
    with pytest.raises(ToolExecutionError, match='confirms the preview card'):
        MailToolkit().send_draft('draft_abc', confirm=True)


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
        'revision': 1,
        'sent_at': '',
        'last_error': '',
    }
    _save_draft(draft)
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_ok'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1
    with patch(
        'lazymind.chat.engine.tools.mail._IMAPBackend.send',
        return_value={'id': 'm1', 'sent_at': '2026-09-01T00:00:00+00:00'},
    ):
        result = MailToolkit().send_draft('draft_ok', confirm=False)
    assert result['status'] == 'sent'
    assert result['sent_at']


def test_send_draft_is_idempotent_after_success(mail_auth):
    draft = {
        'draft_id': 'draft_once',
        'to': ['a@b.com'],
        'cc': [],
        'subject': 'hi',
        'body': 'body',
        'attachment_paths': [],
        'in_reply_to': '',
        'status': 'draft',
        'revision': 1,
        'sent_at': '',
        'last_error': '',
    }
    _save_draft(draft)
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_once'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1
    with patch(
        'lazymind.chat.engine.tools.mail._IMAPBackend.send',
        return_value={'id': 'm1', 'sent_at': '2026-09-01T00:00:00+00:00'},
    ) as send:
        first = MailToolkit().send_draft('draft_once')
        with pytest.raises(ToolExecutionError, match='already sent'):
            MailToolkit().send_draft('draft_once')
    assert first['status'] == 'sent'
    assert send.call_count == 1


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
            return {'items': [{'id': '1', 'subject': self.cred['email']}]}

    with patch('lazymind.chat.engine.tools.mail._backend', side_effect=lambda cred: FakeBackend(cred)):
        result = MailToolkit().search(keyword='invoice')
        filtered = MailToolkit().search(keyword='invoice', mailbox='b@163.com')

    assert {item['mailbox'] for item in result['items']} == {'a@qq.com', 'b@163.com'}
    assert filtered['mailboxes'] == ['b@163.com']
    assert filtered['items'][0]['mailbox'] == 'b@163.com'


def test_compose_accepts_string_attachment_path(mail_auth, tmp_path):
    workspace = chat_agent_workspace('u1', 'c1')
    os.makedirs(workspace, exist_ok=True)
    attachment = os.path.join(workspace, 'attachment_test.txt')
    with open(attachment, 'w', encoding='utf-8') as handle:
        handle.write('hello attachment')
    result = MailToolkit().compose_draft(
        to='a@b.com',
        subject='with file',
        body='body',
        attachment_paths=str(attachment),
    )
    assert result['attachments'] == ['attachment_test.txt']


def test_compose_rejects_path_outside_workspace(mail_auth, tmp_path):
    outside = tmp_path / 'outside.txt'
    outside.write_text('secret', encoding='utf-8')
    with pytest.raises(ToolExecutionError, match='workspace'):
        MailToolkit().compose_draft(
            to='a@b.com',
            subject='escape',
            body='body',
            attachment_paths=str(outside),
        )


def test_compose_rejects_missing_attachment(mail_auth):
    with pytest.raises(ToolExecutionError, match='Attachment file was not found'):
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


def test_update_draft_bumps_revision_and_rejects_stale_confirm(mail_auth):
    preview = MailToolkit().compose_draft(to='a@b.com', subject='v1', body='one')
    draft_id = preview['draft_id']
    assert preview['revision'] == 1
    updated = MailToolkit().update_draft(draft_id, body='two')
    assert updated['revision'] == 2
    assert updated['body'] == 'two'
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = draft_id
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1
    with pytest.raises(ToolExecutionError, match='stale'):
        MailToolkit().send_draft(draft_id)


class _FakeSMTP:
    def __init__(self, *args, **kwargs):
        pass

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def login(self, *args):
        return None

    def send(self, payload):
        return None


def test_send_oserror_marks_failed(mail_auth):
    draft = {
        'draft_id': 'draft_fail',
        'revision': 1,
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
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_fail'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1

    class BoomSMTP(_FakeSMTP):
        def __init__(self, *args, **kwargs):
            raise TimeoutError('timed out')

    with patch('lazymind.chat.engine.tools.mail.smtplib.SMTP_SSL', BoomSMTP):
        with pytest.raises(ToolExecutionError, match='Failed to send'):
            MailToolkit().send_draft('draft_fail')
    saved = _load_draft('draft_fail')
    assert saved['status'] == 'failed'


def test_send_reset_after_data_is_delivery_unknown(mail_auth):
    draft = {
        'draft_id': 'draft_unk',
        'revision': 1,
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
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_unk'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1

    class ResetSMTP(_FakeSMTP):
        def send_message(self, message):
            self.send(b'payload\r\n.\r\n')
            raise ConnectionResetError('Connection reset by peer')

    with patch('lazymind.chat.engine.tools.mail.smtplib.SMTP_SSL', ResetSMTP):
        with pytest.raises(ToolExecutionError, match='delivery is unknown'):
            MailToolkit().send_draft('draft_unk')
    saved = _load_draft('draft_unk')
    assert saved['status'] == 'delivery_unknown'


def test_imap_before_date_is_inclusive():
    assert _imap_date('2026-09-02') == '02-Sep-2026'
    assert _imap_date('2026-09-02', before=True) == '03-Sep-2026'
    assert _imap_date('2026-12-31', before=True) == '01-Jan-2027'
