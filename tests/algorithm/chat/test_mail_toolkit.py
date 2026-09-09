import json
import os
import smtplib
import threading
import time
from pathlib import Path
from unittest.mock import patch

import lazyllm
import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazyllm.tools.tool_config_inject import TOOL_AUTH_REGISTRY
from lazymind.chat.engine.tool_auth import inject_tool_config
from lazymind.chat.engine.tools.local_file.workspace import chat_agent_workspace
from lazymind.chat.engine.tools.mail import (
    MailToolkit,
    _IMAPBackend,
    _apply_confirm_patch,
    _display_mail_date,
    _encode_imap_utf7,
    _extract_transfer_links,
    _find_account,
    _imap_date,
    _imap_search_args,
    _incoming_attachment_path,
    _load_draft,
    _lookup_accounts,
    _mailbox_role,
    _resolve_imap_endpoint,
    _resolve_search_folders,
    _save_draft,
    _split_mail_ref,
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


def test_compose_asks_for_mailbox_when_multiple_accounts_and_none_named(mail_auth):
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
    preview = MailToolkit().compose_draft(to='to@b.com', subject='hi', body='body')
    assert preview['status'] == 'needs_mailbox'
    assert preview['mailbox'] == ''
    assert {row['email'] for row in preview['mailboxes']} == {'a@qq.com', 'b@163.com'}
    lazyllm.globals['agentic_config']['mail_mailbox_confirm'] = 'b@163.com'
    lazyllm.globals['agentic_config']['mail_mailbox_confirm_draft_id'] = preview['draft_id']
    updated = MailToolkit().update_draft(preview['draft_id'])
    assert updated['status'] == 'draft'
    assert updated['mailbox'] == 'b@163.com'
    named = MailToolkit().compose_draft(
        to='to@b.com',
        subject='named',
        body='body',
        mailbox='a@qq.com',
    )
    assert named['status'] == 'draft'
    assert named['mailbox'] == 'a@qq.com'


def test_compose_uses_the_only_enabled_mailbox_without_a_picker(mail_auth):
    preview = MailToolkit().compose_draft(to='to@b.com', subject='hi', body='body')
    assert preview['status'] == 'draft'
    assert preview['mailbox'] == 'user@qq.com'


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

    def sendmail(self, from_addr, to_addrs, msg, *args, **kwargs):
        self.send(b'payload\r\n.\r\n')
        return {}


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
        def sendmail(self, from_addr, to_addrs, msg, *args, **kwargs):
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


class _RecordingIMAP:
    def __init__(self):
        self.calls: list[tuple[str, tuple]] = []
        self.selected: list[str] = []

    def list(self, *args, **kwargs):
        return 'OK', [
            b'(\\HasNoChildren) "/" INBOX',
            b'(\\Sent) "/" "Sent"',
            b'(\\Drafts) "/" Drafts',
            b'(\\Trash) "/" Trash',
        ]

    def select(self, mailbox='INBOX', readonly=False):
        self.selected.append(str(mailbox).strip('"'))
        return 'OK', []

    def uid(self, command, *args):
        encoding = getattr(self, '_encoding', 'ascii')
        for arg in args:
            if isinstance(arg, str):
                arg.encode(encoding)
        self.calls.append((str(command).upper(), args))
        if str(command).upper() == 'SEARCH':
            return 'OK', [b'101 102']
        header = (
            b'From: a@b.com\r\nTo: c@d.com\r\nSubject: hi\r\n'
            b'Date: Wed, 2 Sep 2026 12:00:00 +0000\r\nMessage-ID: <x@y>\r\n\r\nbody'
        )
        return 'OK', [(b'1 (UID 102 RFC822 {n}', header), b')']

    def search(self, *args, **kwargs):
        raise AssertionError('IMAP sequence SEARCH must not be used')

    def fetch(self, *args, **kwargs):
        raise AssertionError('IMAP sequence FETCH must not be used')

    def logout(self):
        return 'OK', []


def test_imap_search_and_read_use_uid(mail_auth):
    imap = _RecordingIMAP()
    with patch.object(_IMAPBackend, '_connect', return_value=imap):
        result = MailToolkit().search(keyword='hi', mailbox='user@qq.com')
        assert result['items'][0]['id'].endswith('::102')
        assert _split_mail_ref(result['items'][0]['id'])[1] == '102'
        folders = {item['folder'] for item in result['items']}
        assert folders >= {'INBOX', 'Sent', 'Drafts', 'Trash'}
        MailToolkit().read(result['items'][0]['id'], mailbox='user@qq.com')
        MailToolkit().read_thread('<x@y>', mailbox='user@qq.com')
    commands = [command for command, _args in imap.calls]
    assert commands.count('SEARCH') >= 2
    assert commands.count('FETCH') >= 2
    assert 'Sent' in imap.selected


def test_mail_date_uses_user_timezone(mail_auth):
    lazyllm.globals['agentic_config']['environment_context'] = {
        'time': {'timezone': 'Asia/Shanghai'},
    }
    assert _display_mail_date('Wed, 2 Sep 2026 12:00:00 +0000').startswith('2026-09-02T20:00:00')


def test_send_rejects_refused_recipients(mail_auth):
    draft = {
        'draft_id': 'draft_bad',
        'revision': 1,
        'to': ['nobody@invalid.example'],
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
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_bad'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1

    class RefuseSMTP(_FakeSMTP):
        def sendmail(self, from_addr, to_addrs, msg, *args, **kwargs):
            raise smtplib.SMTPRecipientsRefused({
                'nobody@invalid.example': (550, b'user unknown'),
            })

    with patch('lazymind.chat.engine.tools.mail.smtplib.SMTP_SSL', RefuseSMTP):
        with pytest.raises(ToolExecutionError, match='rejected'):
            MailToolkit().send_draft('draft_bad')
    saved = _load_draft('draft_bad')
    assert saved['status'] == 'failed'


def test_send_applies_confirm_patch(mail_auth):
    draft = {
        'draft_id': 'draft_patch',
        'revision': 1,
        'to': ['old@b.com'],
        'cc': [],
        'subject': 'old',
        'body': 'old-body',
        'attachment_paths': [],
        'in_reply_to': '',
        'status': 'draft',
        'sent_at': '',
        'last_error': '',
    }
    _save_draft(draft)
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_patch'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1
    lazyllm.globals['agentic_config']['mail_draft_patch'] = {
        'to': 'new@b.com',
        'subject': 'new',
        'body': 'new-body',
    }
    with patch(
        'lazymind.chat.engine.tools.mail._IMAPBackend.send',
        return_value={'id': 'm1', 'sent_at': '2026-09-01T08:00:00+08:00'},
    ) as send:
        MailToolkit().send_draft('draft_patch')
    message = send.call_args[0][0]
    assert message['To'] == 'new@b.com'
    assert message['Subject'] == 'new'


def test_search_named_disabled_mailbox_is_final(mail_auth):
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.search') as search:
        result = MailToolkit().search(mailbox='missing@qq.com')
    search.assert_not_called()
    assert result['status'] == 'mailbox_not_enabled'
    assert result['requested'] == 'missing@qq.com'
    assert result['enabled_mailboxes'][0]['email'] == 'user@qq.com'
    assert 'Do not call MailToolkit_search' in result['message']


def test_send_empty_to_marks_failed_and_keeps_card(mail_auth):
    draft = {
        'draft_id': 'draft_empty',
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
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_empty'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1
    lazyllm.globals['agentic_config']['mail_draft_patch'] = {'to': ''}
    with pytest.raises(ToolExecutionError, match='To field is empty'):
        MailToolkit().send_draft('draft_empty')
    saved = _load_draft('draft_empty')
    assert saved['status'] == 'failed'
    assert 'No recipients' in saved['last_error']


def test_mail_toolkit_registers_only_mail_auth_name():
    MailToolkit()
    assert TOOL_AUTH_REGISTRY.get('mail') == 'dynamic_tool_auth'
    for name in ('gmailimap', 'qqmail', 'qqexmail', 'netease163', 'neteaseqiye'):
        assert name not in TOOL_AUTH_REGISTRY


def test_compose_accepts_conversation_upload_filename(mail_auth, tmp_path):
    chat_file = tmp_path / 'uploads' / 'report.pdf'
    chat_file.parent.mkdir()
    chat_file.write_bytes(b'%PDF-1.4')
    lazyllm.globals['agentic_config']['files'] = [str(chat_file)]
    result = MailToolkit().compose_draft(
        to='a@b.com',
        subject='chat file',
        body='body',
        attachment_paths='report.pdf',
    )
    assert result['attachments'] == ['report.pdf']


def test_confirm_patch_writes_card_upload_to_mail_outgoing(mail_auth):
    import base64

    draft = {
        'draft_id': 'draft_card_file',
        'to': ['a@b.com'],
        'cc': [],
        'subject': 'hi',
        'body': 'body',
        'attachment_paths': [],
        'status': 'draft',
        'revision': 1,
    }
    lazyllm.globals['agentic_config']['mail_draft_patch'] = {
        'attachment_paths': [],
        'attachments': [{
            'filename': 'card.txt',
            'content_base64': base64.b64encode(b'from card').decode('ascii'),
        }],
    }
    _apply_confirm_patch(draft)
    assert len(draft['attachment_paths']) == 1
    path = draft['attachment_paths'][0]
    assert os.path.basename(path) == 'card.txt'
    assert 'mail_outgoing' in path
    with open(path, 'rb') as handle:
        assert handle.read() == b'from card'


def test_extract_qq_transfer_station_links():
    html = (
        '<p>文件中转站</p>'
        '<a href="https://mail.qq.com/cgi-bin/ftnExs_download?k=abc">big.zip</a>'
    )
    links = _extract_transfer_links(html)
    assert links
    assert 'ftn' in links[0]['url']
    assert 'transfer station' in links[0]['note'].lower() or '中转站' in links[0]['note']


def test_inject_clears_stale_mail_auth_before_current_request(mail_auth):
    lazyllm.globals.config['dynamic_tool_auth'] = {
        'mail': json.dumps({'provider': 'qqmail', 'email': 'old@qq.com', 'secret': 'old'}),
        'bing': 'keep-me',
    }
    inject_tool_config({'bing': 'keep-me'})
    auth = lazyllm.globals.config['dynamic_tool_auth'] or {}
    assert 'mail' not in auth
    assert auth.get('bing') == 'keep-me'


def test_imap_search_args_quote_and_charset():
    assert _imap_search_args({'keyword': '合同'}) == [
        'CHARSET', 'UTF-8', 'ALL', 'TEXT', '"合同"',
    ]
    assert _imap_search_args({'keyword': 'hello world'}) == [
        'ALL', 'TEXT', '"hello world"',
    ]
    assert _imap_search_args({'subject': 'a "quoted" subject'}) == [
        'ALL', 'SUBJECT', '"a \\"quoted\\" subject"',
    ]


def test_imap_search_encodes_unicode_and_quoted_values(mail_auth):
    imap = _RecordingIMAP()
    with patch.object(_IMAPBackend, '_connect', return_value=imap):
        MailToolkit().search(keyword='合同', mailbox='user@qq.com')
        MailToolkit().search(keyword='hello world', mailbox='user@qq.com')
        MailToolkit().search(subject='a "quoted" subject', mailbox='user@qq.com')
    searches = [args for command, args in imap.calls if command == 'SEARCH']
    assert any('CHARSET' in args and '"合同"' in args for args in searches)
    assert any('"hello world"' in args for args in searches)
    assert any('"a \\"quoted\\" subject"' in args for args in searches)


def _two_qq_accounts():
    return [
        json.dumps({
            'provider': 'qqmail',
            'email': 'a@qq.com',
            'secret': 'auth-a',
            'status': 'ACTIVE',
        }),
        json.dumps({
            'provider': 'qqmail',
            'email': 'b@qq.com',
            'secret': 'auth-b',
            'status': 'ACTIVE',
        }),
    ]


def test_same_provider_mailbox_is_ambiguous_for_send(mail_auth):
    lazyllm.globals.config['dynamic_tool_auth'] = {'mail': _two_qq_accounts()}
    assert _find_account('qqmail') is None
    assert {cred['email'] for cred in _lookup_accounts('qqmail')} == {'a@qq.com', 'b@qq.com'}
    preview = MailToolkit().compose_draft(
        to='to@b.com',
        subject='hi',
        body='body',
        mailbox='qqmail',
    )
    assert preview['status'] == 'needs_mailbox'
    assert {row['email'] for row in preview['mailboxes']} == {'a@qq.com', 'b@qq.com'}
    named = MailToolkit().compose_draft(
        to='to@b.com',
        subject='named',
        body='body',
        mailbox='a@qq.com',
    )
    assert named['status'] == 'draft'
    assert named['mailbox'] == 'a@qq.com'


def test_search_same_provider_covers_every_account(mail_auth):
    lazyllm.globals.config['dynamic_tool_auth'] = {'mail': _two_qq_accounts()}

    class FakeBackend:
        def __init__(self, cred):
            self.cred = cred

        def search(self, **kwargs):
            return {'items': [{'id': '1', 'subject': self.cred['email']}]}

    with patch('lazymind.chat.engine.tools.mail._backend', side_effect=lambda cred: FakeBackend(cred)):
        result = MailToolkit().search(keyword='invoice', mailbox='qqmail')
    assert {item['mailbox'] for item in result['items']} == {'a@qq.com', 'b@qq.com'}
    assert set(result['mailboxes']) == {'a@qq.com', 'b@qq.com'}


def test_read_attachment_namespaces_same_filename(mail_auth):
    cred = {'email': 'user@qq.com', 'provider': 'qqmail', 'connection_id': ''}
    path_a = _incoming_attachment_path(cred, 'INBOX::1', '报价单.pdf')
    path_b = _incoming_attachment_path(cred, 'INBOX::2', '报价单.pdf')
    assert path_a != path_b
    assert os.path.basename(path_a) == '报价单.pdf'

    class FakeBackend:
        def read_attachments(self, message_id):
            payload = b'content-a' if message_id.endswith('1') else b'content-b'
            return {'files': {'报价单.pdf': payload}, 'transfer': False}

    with patch('lazymind.chat.engine.tools.mail._backend', return_value=FakeBackend()):
        with patch(
            'lazymind.chat.engine.tools.local_file.resolver.parse_attachment_content',
            return_value='parsed',
        ):
            first = MailToolkit().read_attachment('INBOX::1', '报价单.pdf')
            second = MailToolkit().read_attachment('INBOX::2', '报价单.pdf')
    assert first['path'] != second['path']
    assert first['parse_status'] == 'parsed'
    with open(first['path'], 'rb') as handle:
        assert handle.read() == b'content-a'
    with open(second['path'], 'rb') as handle:
        assert handle.read() == b'content-b'


def test_imap_read_attachments_walks_message_once():
    from email.message import EmailMessage

    msg = EmailMessage()
    msg['Subject'] = 'files'
    msg['From'] = 'a@b.com'
    msg.set_content('body')
    msg.add_attachment(b'pdf-bytes', maintype='application', subtype='pdf', filename='a.pdf')
    msg.add_attachment(b'zip-bytes', maintype='application', subtype='zip', filename='b.zip')

    backend = _IMAPBackend({
        'email': 'user@qq.com',
        'provider': 'qqmail',
        'secret': 'x',
        'connection_id': '',
    })
    with patch.object(backend, '_fetch_message', return_value=msg):
        result = backend.read_attachments('INBOX::1')
    assert result['files'] == {'a.pdf': b'pdf-bytes', 'b.zip': b'zip-bytes'}
    assert result['transfer'] is False
    with patch.object(backend, '_fetch_message', return_value=msg) as fetch:
        assert backend.read_attachment('INBOX::1', 'b.zip') == b'zip-bytes'
        fetch.assert_called_once()


def test_read_attachment_fetches_message_once_and_reuses_workspace(mail_auth):
    calls = {'n': 0}

    class FakeBackend:
        def read_attachments(self, message_id):
            calls['n'] += 1
            return {
                'files': {'invoice.pdf': b'%PDF-invoice', 'notes.zip': b'PK-zip'},
                'transfer': False,
            }

    parse_calls = []

    def fake_parse(path, priority=0):
        parse_calls.append(path)
        return 'invoice text'

    with patch('lazymind.chat.engine.tools.mail._backend', return_value=FakeBackend()):
        with patch(
            'lazymind.chat.engine.tools.local_file.resolver.parse_attachment_content',
            side_effect=fake_parse,
        ):
            pdf = MailToolkit().read_attachment('INBOX::9', 'invoice.pdf')
            zip_file = MailToolkit().read_attachment('INBOX::9', 'notes.zip')
            again = MailToolkit().read_attachment('INBOX::9', 'invoice.pdf')

    assert calls['n'] == 1
    assert pdf['parse_status'] == 'parsed'
    assert pdf['text'] == 'invoice text'
    assert zip_file['parse_status'] == 'unsupported'
    assert zip_file['text'] == ''
    assert 'not parsed' in zip_file['parse_note']
    assert os.path.isfile(zip_file['path'])
    assert again['text'] == 'invoice text'
    assert parse_calls == [pdf['path']]
    workspace = chat_agent_workspace('u1', 'c1')
    assert list(Path(workspace).glob('attachment-text-cache/*/parsed.txt'))


def test_imap_folder_fallback_decodes_modified_utf7(mail_auth):
    sent = _encode_imap_utf7('已发送')
    drafts = _encode_imap_utf7('草稿')
    trash = _encode_imap_utf7('已删除')
    assert sent != '已发送'
    assert _mailbox_role(sent, set()) == 'sent'
    assert _mailbox_role(drafts, set()) == 'drafts'
    assert _mailbox_role(trash, set()) == 'trash'

    class FakeList:
        def list(self):
            return 'OK', [
                b'(\\HasNoChildren) "/" INBOX',
                f'() "/" "{sent}"'.encode('ascii'),
                f'() "/" "{drafts}"'.encode('ascii'),
                f'() "/" "{trash}"'.encode('ascii'),
            ]

    folders = _resolve_search_folders(FakeList(), 'all')
    assert folders == ['INBOX', sent, drafts, trash]


def test_send_draft_is_idempotent_under_concurrency(mail_auth):
    draft = {
        'draft_id': 'draft_race',
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
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_race'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1
    send_count = {'n': 0}
    auth = lazyllm.globals.config['dynamic_tool_auth']
    cfg = lazyllm.globals['agentic_config']

    def _slow_send(self, message):
        send_count['n'] += 1
        time.sleep(0.2)
        return {'id': 'm1', 'sent_at': '2026-09-01T00:00:00+00:00'}

    errors: list[str] = []

    def _worker():
        lazyllm.globals.config['dynamic_tool_auth'] = auth
        lazyllm.globals['agentic_config'] = cfg
        try:
            MailToolkit().send_draft('draft_race')
        except ToolExecutionError as orig:
            errors.append(str(orig))

    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send', _slow_send):
        workers = [threading.Thread(target=_worker) for _ in range(2)]
        for worker in workers:
            worker.start()
        for worker in workers:
            worker.join()
    assert send_count['n'] == 1
    assert any('already sent' in item for item in errors)
    assert _load_draft('draft_race')['status'] == 'sent'
