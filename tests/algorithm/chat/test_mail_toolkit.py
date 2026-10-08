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
from lazymind.chat.engine.tools.conversation_workspace import chat_agent_workspace
from lazymind.chat.engine.tools.mail import (
    MailToolkit,
    _IMAPBackend,
    _apply_confirm_patch,
    _attachments_from_bodystructure,
    _build_message,
    _named_mime_parts,
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
    _plain_error_text,
    _resolve_imap_endpoint,
    _resolve_search_folders,
    _save_draft,
    _split_mail_ref,
    _write_outgoing_attachments,
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
        second = MailToolkit().send_draft('draft_once')
    assert second == first
    assert first['body'] == 'body'
    assert first['message_id'] == 'm1'
    assert first['requires_confirmation'] is False
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


def test_compose_accepts_inline_artifact_workspace_path(mail_auth, monkeypatch):
    from lazymind.chat.engine.tools import chat_artifact

    monkeypatch.setattr(chat_artifact, '_write_agent_data', lambda *_args, **_kwargs: None)
    artifact = chat_artifact.save_chat_artifact('spring.txt', '春天来了')

    preview = MailToolkit().compose_draft(
        to='a@b.com',
        subject='chat artifact',
        body='body',
        attachment_paths=artifact['workspace_path'],
    )

    assert preview['attachments'] == ['spring.txt']
    assert Path(preview['attachment_paths'][0]).read_text(encoding='utf-8') == '春天来了'


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


def test_send_reset_while_submitting_data_is_delivery_unknown(mail_auth):
    draft = {
        'draft_id': 'draft_unk_during_data',
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
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = draft['draft_id']
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1

    class ResetDuringDataSMTP(_FakeSMTP):
        def send(self, payload):
            if bytes(payload).endswith(b'.\r\n'):
                raise ConnectionResetError('Connection reset while awaiting DATA acknowledgement')
            return None

    with patch('lazymind.chat.engine.tools.mail.smtplib.SMTP_SSL', ResetDuringDataSMTP):
        with pytest.raises(ToolExecutionError, match='delivery is unknown'):
            MailToolkit().send_draft(draft['draft_id'])
    assert _load_draft(draft['draft_id'])['status'] == 'delivery_unknown'


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
    assert saved['error_code'] == 'recipient_rejected'
    assert "b'user unknown'" not in saved['last_error']
    assert 'user unknown' in saved['last_error']


def test_send_classifies_nonexistent_recipient_reported_after_data(mail_auth):
    draft = {
        'draft_id': 'draft_nonexistent',
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
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_nonexistent'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1

    class RefuseAfterDataSMTP(_FakeSMTP):
        def sendmail(self, from_addr, to_addrs, msg, *args, **kwargs):
            raise smtplib.SMTPDataError(
                550,
                b'The recipient may contain a non-existent account, please check the recipient address.',
            )

    with patch('lazymind.chat.engine.tools.mail.smtplib.SMTP_SSL', RefuseAfterDataSMTP):
        with pytest.raises(ToolExecutionError):
            MailToolkit().send_draft('draft_nonexistent')

    saved = _load_draft('draft_nonexistent')
    assert saved['status'] == 'failed'
    assert saved['error_code'] == 'recipient_rejected'
    assert "b'The recipient" not in saved['last_error']


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


@pytest.mark.parametrize('context_key', ['_core_workspace_context', 'parent_agentic_config'])
def test_bound_workspace_keeps_internal_files_scoped_in_legacy_mode(mail_auth, tmp_path, monkeypatch, context_key):
    from lazymind.chat.engine.tools import conversation_workspace as workspace
    context = {'workspace_id': 'bound'}
    lazyllm.globals['agentic_config'][context_key] = (
        {'_core_workspace_context': context} if context_key == 'parent_agentic_config' else context)
    monkeypatch.setattr(workspace, '_cfg', {'agentic_workspace': str(tmp_path), 'trusted_local_mode': True})
    internal = workspace.chat_agent_workspace('u1', 'c1')
    assert workspace._resolve_workspace_path('allowed.txt', 'u1', 'c1')[1] == os.path.join(internal, 'allowed.txt')
    outside = tmp_path / 'outside.txt'
    outside.write_text('private')
    with pytest.raises(ToolExecutionError, match='workspace'):
        MailToolkit().compose_draft(to='a@b.com', subject='blocked', body='body', attachment_paths=str(outside))


def test_send_rechecks_persisted_attachment_before_read_or_delivery(mail_auth, tmp_path):
    from lazymind.chat.engine.tools import mail
    lazyllm.globals['agentic_config']['_core_workspace_context'] = {'workspace_id': 'bound'}
    outside = tmp_path / 'outside.txt'
    outside.write_text('private')
    draft = {'draft_id': 'old_draft', 'status': 'draft', 'revision': 1,
             'attachment_paths': [str(outside)], 'mailbox': 'user@qq.com'}
    _save_draft(draft)
    lazyllm.globals['agentic_config'].update(mail_draft_confirm_id='old_draft', mail_draft_confirm_revision=1)
    with patch.object(mail, '_build_message') as build, patch.object(mail, '_backend') as backend:
        with pytest.raises(ToolExecutionError, match='workspace'):
            MailToolkit().send_draft('old_draft')
    build.assert_not_called()
    backend.assert_not_called()


@pytest.mark.parametrize('target', ['.mail_drafts', '.mail_drafts/linked.json'])
def test_mail_draft_outputs_reject_existing_external_symlinks(mail_auth, tmp_path, target):
    from pathlib import Path
    lazyllm.globals['agentic_config']['_core_workspace_context'] = {'workspace_id': 'bound'}
    root = Path(chat_agent_workspace('u1', 'c1'))
    link = root / target
    link.parent.mkdir(parents=True, exist_ok=True)
    outside = tmp_path / 'outside'
    if '.' not in link.name or link.name == '.mail_drafts':
        outside.mkdir()
    else:
        outside.write_text('unchanged')
    link.symlink_to(outside, target_is_directory=outside.is_dir())
    with pytest.raises(ToolExecutionError, match='workspace'):
        _save_draft({'draft_id': 'linked'})
    assert list(outside.iterdir()) == [] if outside.is_dir() else outside.read_text() == 'unchanged'


@pytest.mark.parametrize('target', ['mail_attachments', 'mail_attachments_file'])
def test_mail_attachment_outputs_reject_existing_external_symlinks(mail_auth, tmp_path, target):
    from pathlib import Path
    from lazymind.chat.engine.tools import mail

    lazyllm.globals['agentic_config']['_core_workspace_context'] = {'workspace_id': 'bound'}
    root = Path(chat_agent_workspace('u1', 'c1'))
    outside = tmp_path / 'outside'
    if target == 'mail_attachments':
        link = root / 'mail_attachments'
        link.parent.mkdir(parents=True, exist_ok=True)
        outside.mkdir()
    else:
        cred = mail._lookup_accounts('qqmail')[0]
        link = Path(mail._incoming_attachment_path(cred, '1', 'linked.txt'))
        link.parent.mkdir(parents=True, exist_ok=True)
        outside.write_text('unchanged')
    link.symlink_to(outside, target_is_directory=outside.is_dir())

    with patch.object(
        mail._IMAPBackend,
        'read_attachments',
        return_value={'files': {'linked.txt': b'new'}, 'parts': [], 'transfer': False},
    ):
        with pytest.raises(ToolExecutionError, match='workspace'):
            MailToolkit().read_attachment('1', 'linked.txt')
    assert list(outside.iterdir()) == [] if outside.is_dir() else outside.read_text() == 'unchanged'


@pytest.mark.parametrize('missing', ['user_id', 'conversation_id'])
def test_mail_internal_drafts_require_full_identity(mail_auth, missing):
    from lazymind.chat.engine.tools.mail import _draft_dir
    lazyllm.globals['agentic_config'].pop(missing)
    with pytest.raises(ToolExecutionError, match='user_id.*conversation_id'):
        _draft_dir()


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
            'lazymind.chat.engine.tools.file_resources.resolver.parse_attachment_content',
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
        assert result['files']['a.pdf'] == b'pdf-bytes'
        assert result['files']['b.zip'] == b'zip-bytes'
        assert [part['attachment_id'] for part in result['parts']] == ['2', '3']
        assert result['files']['2'] == b'pdf-bytes'
        assert backend.read_attachment('INBOX::1', '2') == b'pdf-bytes'
        assert result['transfer'] is False
        assert backend.read_attachment('INBOX::1', 'b.zip') == b'zip-bytes'


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
            'lazymind.chat.engine.tools.file_resources.resolver.parse_attachment_content',
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


def test_read_attachment_reuses_cache_for_section_id(mail_auth):
    calls = {'n': 0}

    class FakeBackend:
        def read_attachments(self, message_id):
            calls['n'] += 1
            return {
                'files': {'2': b'%PDF-invoice', 'invoice.pdf': b'%PDF-invoice'},
                'parts': [{
                    'attachment_id': '2',
                    'filename': 'invoice.pdf',
                    'data': b'%PDF-invoice',
                }],
                'transfer': False,
            }

    with patch('lazymind.chat.engine.tools.mail._backend', return_value=FakeBackend()):
        with patch(
            'lazymind.chat.engine.tools.file_resources.resolver.parse_attachment_content',
            return_value='invoice text',
        ):
            first = MailToolkit().read_attachment('INBOX::9', '2')
            again = MailToolkit().read_attachment('INBOX::9', '2')
            by_name = MailToolkit().read_attachment('INBOX::9', 'invoice.pdf')

    assert calls['n'] == 1
    assert first['parse_status'] == 'parsed'
    assert again['path'] == first['path']
    assert by_name['path'] == first['path']
    assert os.path.basename(first['path']).startswith('2-')


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
    assert errors == []
    assert _load_draft('draft_race')['status'] == 'sent'


def test_read_requires_exact_mailbox_when_uids_could_collide(mail_auth):
    lazyllm.globals.config['dynamic_tool_auth'] = {'mail': _two_qq_accounts()}
    with pytest.raises(ToolExecutionError, match='ambiguous'):
        MailToolkit().read('INBOX::123')

    class FakeBackend:
        def __init__(self, cred):
            self.cred = cred

        def read(self, message_id):
            return {'id': message_id, 'body': self.cred['email']}

    with patch('lazymind.chat.engine.tools.mail._backend', side_effect=lambda cred: FakeBackend(cred)):
        result = MailToolkit().read('INBOX::123', mailbox='b@qq.com')
    assert result['mailbox'] == 'b@qq.com'
    assert result['body'] == 'b@qq.com'


def test_search_merges_accounts_then_respects_limit(mail_auth):
    lazyllm.globals.config['dynamic_tool_auth'] = {'mail': _two_qq_accounts()}

    class FakeBackend:
        def __init__(self, cred):
            self.cred = cred

        def search(self, **kwargs):
            email = self.cred['email']
            if email.startswith('a@'):
                items = [{'id': f'a{i}', 'date': '2020-01-01', 'subject': 'old'} for i in range(20)]
            else:
                items = [{'id': 'b-new', 'date': '2026-09-09', 'subject': 'new'}]
            return {'items': items}

    with patch('lazymind.chat.engine.tools.mail._backend', side_effect=lambda cred: FakeBackend(cred)):
        default = MailToolkit().search(keyword='x')
        limited = MailToolkit().search(keyword='x', limit=3)
    assert len(default['items']) == 21
    assert any(item['id'] == 'b-new' for item in default['items'])
    assert len(limited['items']) == 3
    assert limited['items'][0]['id'] == 'b-new'


class _WindowIMAP(_RecordingIMAP):
    def __init__(self, uids: list[int]):
        super().__init__()
        self._uids = uids

    def list(self, *args, **kwargs):
        return 'OK', [b'(\\HasNoChildren) "/" INBOX']

    def uid(self, command, *args):
        self.calls.append((str(command).upper(), args))
        if str(command).upper() == 'SEARCH':
            ceiling = None
            if 'UID' in args:
                spec = str(args[args.index('UID') + 1])
                if ':' in spec:
                    ceiling = int(spec.split(':', 1)[1])
            matched = [uid for uid in self._uids if ceiling is None or uid <= ceiling]
            payload = ' '.join(str(uid) for uid in matched).encode('ascii')
            return 'OK', [payload]
        header = (
            b'From: a@b.com\r\nTo: c@d.com\r\nSubject: hi\r\n'
            b'Date: Wed, 2 Sep 2026 12:00:00 +0000\r\nMessage-ID: <x@y>\r\n\r\n'
        )
        meta = args[0] if args else ''
        requested = [token for token in str(meta).split(',') if token.isdigit()]
        rows = []
        for uid in requested:
            rows.append((f'1 (UID {uid} BODY[HEADER.FIELDS] {{10}}'.encode('ascii'), header))
            rows.append(b')')
        return 'OK', rows


def test_imap_search_returns_requested_page_when_more_exist(mail_auth):
    imap = _WindowIMAP(list(range(1, 81)))
    with patch.object(_IMAPBackend, '_connect', return_value=imap):
        result = MailToolkit().search(mailbox='user@qq.com', folder='inbox', limit=40)
    assert result['requested'] == 40
    assert result['returned'] == 40
    assert result['has_more'] is True
    assert result['next_before'] == '2026-09-02'
    assert 'reason' not in result
    assert result['items'][0]['id'].endswith('::80')


def test_imap_search_reports_window_exhausted(mail_auth):
    imap = _WindowIMAP(list(range(398, 430)))
    with patch.object(_IMAPBackend, '_connect', return_value=imap):
        result = MailToolkit().search(mailbox='user@qq.com', folder='inbox', limit=40)
    assert result['requested'] == 40
    assert result['returned'] == 32
    assert result['has_more'] is False
    assert result['reason'] == 'imap_window_exhausted'
    assert result['oldest_date']
    assert 'next_before' not in result


def test_search_has_more_when_backend_reports_more(mail_auth):
    class FakeBackend:
        def search(self, **kwargs):
            return {
                'items': [{'id': f'i{i}', 'date': '2026-09-09'} for i in range(3)],
                'has_more': True,
            }

    with patch('lazymind.chat.engine.tools.mail._backend', return_value=FakeBackend()):
        result = MailToolkit().search(keyword='x', limit=3)
    assert len(result['items']) == 3
    assert result['has_more'] is True
    assert result['next_before'] == '2026-09-09'


def test_plain_error_text_unwraps_json_payloads():
    envelope = json.dumps({'ok': False, 'msg': {'message': 'SMTP authentication failed'}})
    assert _plain_error_text(envelope) == 'SMTP authentication failed'
    assert '{' not in _plain_error_text(envelope)


@pytest.mark.parametrize('value', [
    'ToolExecutionError: {"error": {"message": "Attachment unavailable"}}',
    {'result': {'ok': False, 'value': '{"detail": "Attachment unavailable"}'}},
    '[{"message": "Attachment unavailable"}]',
    '"{\\"message\\": \\"Attachment unavailable\\"}"',
])
def test_plain_error_text_unwraps_prefixed_and_nested_errors(value):
    assert _plain_error_text(value) == 'Attachment unavailable'


@pytest.mark.parametrize('status', ['sent', 'sending'])
def test_terminal_or_inflight_card_has_no_send_question(mail_auth, status):
    from lazymind.chat.engine.tools.mail import _emit_draft_card
    with patch('lazymind.chat.engine.tools.mail._write_agent_data') as emit:
        card = _emit_draft_card({'draft_id': 'receipt', 'status': status})
    assert card['requires_confirmation'] is False
    assert emit.call_args.kwargs['questions'] == []


def test_interrupted_send_requires_new_explicit_confirmation(mail_auth):
    _save_draft({'draft_id': 'uncertain', 'revision': 1, 'status': 'sending', 'to': ['a@b.com'],
                 'last_error': 'Previous partial delivery. Retry refused addresses.'})
    lazyllm.globals['agentic_config'].update(mail_draft_confirm_id='uncertain', mail_draft_confirm_revision=1)
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send', return_value={'id': 'resent'}) as send:
        with pytest.raises(ToolExecutionError, match='delivery|Delivery'):
            MailToolkit().send_draft('uncertain')
        with pytest.raises(ToolExecutionError, match='stale'):
            MailToolkit().send_draft('uncertain')
        send.assert_not_called()
        saved = _load_draft('uncertain')
        assert saved['status'] == 'delivery_unknown'
        assert saved['revision'] == 2
        assert 'unknown' in saved['last_error']
        lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = saved['revision']
        assert MailToolkit().send_draft('uncertain')['status'] == 'sent'
        send.assert_called_once()


def test_unknown_card_warns_and_update_preserves_unknown_delivery(mail_auth):
    from lazymind.chat.engine.tools.mail import _emit_draft_card
    draft = {'draft_id': 'unknown', 'revision': 2, 'status': 'delivery_unknown', 'to': ['a@b.com'],
             'last_error': 'Delivery unknown. Resending can send a duplicate.'}
    _save_draft(draft)
    with patch('lazymind.chat.engine.tools.mail._write_agent_data') as emit:
        card = _emit_draft_card(draft)
    assert card['requires_confirmation'] is True
    assert card['retryable'] is True
    assert card['delivery_unknown'] is True
    assert emit.call_args.kwargs['questions']
    updated = MailToolkit().update_draft('unknown', body='edited')
    assert updated['revision'] == 3
    assert updated['status'] == 'delivery_unknown'
    assert updated['last_error'] == draft['last_error']


@pytest.mark.parametrize('failure', ['attachment', 'build', 'recipients', 'smtp'])
def test_unknown_retry_failure_keeps_duplicate_warning_and_requires_new_confirmation(mail_auth, failure):
    from contextlib import nullcontext

    _save_draft({'draft_id': 'unknown_retry', 'revision': 2, 'status': 'delivery_unknown',
                 'to': ['a@b.com'], 'body': 'original', 'attachment_paths': [],
                 'last_error': 'Delivery unknown. Resending can send a duplicate.'})
    cfg = lazyllm.globals['agentic_config']
    cfg.update(mail_draft_confirm_id='unknown_retry', mail_draft_confirm_revision=2,
               mail_draft_patch={'body': 'edited'})
    if failure == 'attachment':
        cfg['mail_draft_patch']['attachments'] = [{'filename': 'bad.txt', 'content_base64': '@@@'}]
    elif failure == 'recipients':
        cfg['mail_draft_patch']['to'] = []
    build = patch('lazymind.chat.engine.tools.mail._build_message', side_effect=ValueError('Invalid header'))
    with patch(
        'lazymind.chat.engine.tools.mail._IMAPBackend.send',
        side_effect=ToolExecutionError('SMTP unavailable'),
    ) as send, patch('lazymind.chat.engine.tools.mail._write_agent_data') as emit:
        with build if failure == 'build' else nullcontext():
            with pytest.raises(ToolExecutionError):
                MailToolkit().send_draft('unknown_retry')
        card = emit.call_args.kwargs['mail_draft']
        assert card['revision'] == 3
        assert card['status'] == 'delivery_unknown'
        assert card['delivery_unknown'] is True
        assert 'duplicate' in card['last_error'].lower()
        assert card['body'] == 'edited'
        expected = {
            'attachment': 'base64', 'build': 'Invalid header',
            'recipients': 'No recipients', 'smtp': 'SMTP unavailable',
        }
        assert expected[failure] in card['last_error']
        with pytest.raises(ToolExecutionError, match='stale'):
            MailToolkit().send_draft('unknown_retry')
        assert send.call_count == (1 if failure == 'smtp' else 0)
        cfg.update(mail_draft_confirm_revision=card['revision'], mail_draft_patch={
            'to': ['a@b.com'], 'attachment_paths': [],
        })
        send.side_effect = None
        send.return_value = {'id': 'confirmed-retry'}
        assert MailToolkit().send_draft('unknown_retry')['status'] == 'sent'


@pytest.mark.parametrize('via_update', [False, True])
def test_partial_recipient_edits_never_resend_to_accepted_addresses(mail_auth, via_update):
    card = MailToolkit().compose_draft(['accepted@b.com', 'refused@b.com'], 'subject', 'body', cc=['acceptedcc@b.com'])
    cfg = lazyllm.globals['agentic_config']
    cfg.update(mail_draft_confirm_id=card['draft_id'], mail_draft_confirm_revision=card['revision'])
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send', side_effect=[
        {'partial_sent': True, 'accepted': ['accepted@b.com', 'acceptedcc@b.com'],
         'refused': [{'address': 'refused@b.com'}]},
        {'id': 'retry'},
    ]) as send:
        partial = MailToolkit().send_draft(card['draft_id'])
        edits = {'to': ['ACCEPTEDCC@b.com', 'refused@b.com', 'new@b.com'], 'cc': ['ACCEPTED@b.com']}
        if via_update:
            partial = MailToolkit().update_draft(card['draft_id'], **edits)
        else:
            cfg['mail_draft_patch'] = edits
        cfg['mail_draft_confirm_revision'] = partial['revision']
        receipt = MailToolkit().send_draft(card['draft_id'])
    retry = send.call_args.args[0]
    assert retry['To'] == 'refused@b.com, new@b.com'
    assert retry['Cc'] is None
    assert receipt['accepted_recipients'] == ['accepted@b.com', 'acceptedcc@b.com', 'refused@b.com', 'new@b.com']


@pytest.mark.parametrize('failure', ['missing', 'upload', 'build'])
def test_attachment_failure_preserves_edits_and_blocks_silent_retry(mail_auth, failure):
    card = MailToolkit().compose_draft('a@b.com', 'old', 'old')
    draft_id = card['draft_id']
    cfg = lazyllm.globals['agentic_config']
    cfg.update(mail_draft_confirm_id=draft_id, mail_draft_confirm_revision=1)
    cfg['mail_draft_patch'] = {'body': 'edited body', 'subject': 'edited subject'}
    if failure == 'missing':
        cfg['mail_draft_patch']['attachment_paths'] = ['missing.pdf']
    elif failure == 'upload':
        cfg['mail_draft_patch']['attachments'] = [{'filename': 'bad.txt', 'content_base64': '@@@'}]
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send') as send, patch(
        'lazymind.chat.engine.tools.mail._write_agent_data',
    ) as emit:
        build = patch('lazymind.chat.engine.tools.mail._build_message', side_effect=OSError('Attachment disappeared'))
        from contextlib import nullcontext
        with build if failure == 'build' else nullcontext():
            with pytest.raises(ToolExecutionError):
                MailToolkit().send_draft(draft_id)
        saved = _load_draft(draft_id)
        assert saved['body'] == 'edited body'
        assert saved['subject'] == 'edited subject'
        assert saved['status'] == 'failed'
        emitted = emit.call_args.kwargs['mail_draft']
        assert emitted['last_error']
        assert emitted['requires_confirmation'] is True
        assert emitted['body'] == 'edited body'
        if failure != 'build':
            cfg.pop('mail_draft_patch')
            with pytest.raises(ToolExecutionError):
                MailToolkit().send_draft(draft_id)
        send.assert_not_called()


def test_build_message_rejects_disappeared_attachment(mail_auth):
    with pytest.raises((ToolExecutionError, OSError)):
        _build_message({'to': ['a@b.com'], 'attachment_paths': ['missing.pdf']}, 'user@qq.com')


def test_update_attachment_failure_keeps_revision_edits_and_allows_explicit_removal(mail_auth):
    original = MailToolkit().compose_draft('a@b.com', 'old', 'old')
    failed = MailToolkit().update_draft(original['draft_id'], body='edited', attachment_paths=['missing.pdf'])
    assert failed['status'] == 'failed'
    assert failed['body'] == 'edited'
    assert failed['revision'] == 2
    assert failed['attachments'] == ['missing.pdf']
    cfg = lazyllm.globals['agentic_config']
    cfg.update(mail_draft_confirm_id=failed['draft_id'], mail_draft_confirm_revision=1)
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send', return_value={'id': 'sent'}) as send:
        with pytest.raises(ToolExecutionError, match='stale'):
            MailToolkit().send_draft(failed['draft_id'])
        send.assert_not_called()
        cfg.update(mail_draft_confirm_revision=2, mail_draft_patch={'attachment_paths': []})
        receipt = MailToolkit().send_draft(failed['draft_id'])
    assert receipt['status'] == 'sent'
    assert receipt['body'] == 'edited'
    assert receipt['attachments'] == []


def test_compose_attachment_failure_emits_saved_full_card(mail_auth):
    with patch('lazymind.chat.engine.tools.mail._write_agent_data') as emit:
        with pytest.raises(ToolExecutionError, match='not found'):
            MailToolkit().compose_draft('a@b.com', 'subject', 'body', attachment_paths=['missing.pdf'])
    card = emit.call_args.kwargs['mail_draft']
    assert card['status'] == 'failed'
    assert card['attachments'] == ['missing.pdf']
    assert _load_draft(card['draft_id'])['body'] == 'body'


def test_upload_only_patch_preserves_existing_attachments(mail_auth):
    import base64
    path = Path(chat_agent_workspace('u1', 'c1')) / 'existing.txt'
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text('existing')
    draft = {'attachment_paths': [str(path)]}
    lazyllm.globals['agentic_config']['mail_draft_patch'] = {
        'attachments': [{'filename': 'new.txt', 'content_base64': base64.b64encode(b'new').decode()}],
    }
    _apply_confirm_patch(draft)
    assert [os.path.basename(item) for item in draft['attachment_paths']] == ['existing.txt', 'new.txt']


@pytest.mark.parametrize('failure', ['base64', 'empty', 'size', 'total', 'io', 'partial_write', 'malformed'])
def test_mixed_upload_failure_reports_only_unsaved_files(mail_auth, monkeypatch, failure):
    import base64
    import builtins
    from lazymind.chat.engine.tools import mail

    encoded = base64.b64encode(b'file').decode()
    files = [{'filename': name, 'content_base64': encoded} for name in ['saved.txt', 'failed.txt', 'later.txt']]
    if failure == 'base64':
        files[1]['content_base64'] = '@@@'
    elif failure == 'empty':
        files[1]['content_base64'] = ''
    elif failure == 'size':
        monkeypatch.setattr(mail, '_MAX_CARD_ATTACHMENT_BYTES', 5)
        files[1]['content_base64'] = base64.b64encode(b'too large').decode()
    elif failure == 'total':
        monkeypatch.setattr(mail, '_MAX_CARD_ATTACHMENT_TOTAL_BYTES', 5)
    elif failure == 'malformed':
        files[1] = 'not an upload'
    else:
        real_open = builtins.open

        class PartialWrite:
            def __init__(self, handle):
                self.handle = handle

            def __enter__(self):
                return self

            def __exit__(self, *args):
                self.handle.close()

            def write(self, data):
                self.handle.write(data[:1])
                raise OSError('Disk full')

        def failing_open(path, mode='r', *args, **kwargs):
            if os.path.basename(path) == 'failed.txt' and ('w' in mode or 'x' in mode):
                if failure == 'partial_write':
                    return PartialWrite(real_open(path, mode, *args, **kwargs))
                raise OSError('Disk full')
            return real_open(path, mode, *args, **kwargs)

        monkeypatch.setattr(builtins, 'open', failing_open)
    card = MailToolkit().compose_draft('a@b.com', 'subject', 'body')
    cfg = lazyllm.globals['agentic_config']
    cfg.update(mail_draft_confirm_id=card['draft_id'], mail_draft_confirm_revision=card['revision'],
               mail_draft_patch={'body': 'edited', 'attachments': files})
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send') as send, patch(
        'lazymind.chat.engine.tools.mail._write_agent_data',
    ) as emit:
        with pytest.raises(ToolExecutionError):
            MailToolkit().send_draft(card['draft_id'])
        failed = emit.call_args.kwargs['mail_draft']
        assert failed['body'] == 'edited'
        assert [os.path.basename(path) for path in failed['attachment_paths']] == ['saved.txt']
        assert Path(failed['attachment_paths'][0]).read_bytes() == b'file'
        assert not Path(failed['attachment_paths'][0]).with_name('failed.txt').exists()
        expected = ['attachment.bin' if failure == 'malformed' else 'failed.txt', 'later.txt']
        assert failed['pending_attachment_names'] == expected
        assert failed['attachments'] == ['saved.txt', *expected]
        assert _load_draft(card['draft_id'])['attachment_paths'] == failed['attachment_paths']
        cfg.pop('mail_draft_patch')
        cfg['mail_draft_confirm_revision'] = failed['revision']
        with pytest.raises(ToolExecutionError):
            MailToolkit().send_draft(card['draft_id'])
        send.assert_not_called()


def test_upload_only_retry_keeps_other_pending_files_until_explicit_removal(mail_auth):
    import base64
    from lazymind.chat.engine.tools.mail import _preview

    draft = {'attachment_paths': [], 'pending_attachment_names': ['one.txt', 'two.txt']}
    cfg = lazyllm.globals['agentic_config']
    cfg['mail_draft_patch'] = {
        'attachments': [{'filename': 'one.txt', 'content_base64': base64.b64encode(b'one').decode()}],
    }
    with pytest.raises(ToolExecutionError, match='Attachments'):
        _apply_confirm_patch(draft)
    assert _preview(draft)['attachments'] == ['one.txt', 'two.txt']
    assert draft['pending_attachment_names'] == ['two.txt']
    cfg['mail_draft_patch'] = {'attachment_paths': draft['attachment_paths'], 'attachments': []}
    _apply_confirm_patch(draft)
    assert draft['pending_attachment_names'] == []
    assert draft['attachment_error'] == ''


@pytest.mark.parametrize('uploads,names', [
    ([{'filename': f'{index}.txt', 'content_base64': 'eA=='} for index in range(6)],
     [f'{index}.txt' for index in range(6)]),
    ({'filename': 'wrong-shape.txt', 'content_base64': 'eA=='}, ['wrong-shape.txt']),
])
def test_batch_upload_rejection_preserves_all_pending_names(mail_auth, uploads, names):
    draft = {'attachment_paths': []}
    lazyllm.globals['agentic_config']['mail_draft_patch'] = {'attachments': uploads}
    with pytest.raises(ToolExecutionError):
        _apply_confirm_patch(draft)
    assert draft['attachment_paths'] == []
    assert draft['pending_attachment_names'] == names


def test_upload_retry_receipt_retains_saved_files_and_replaces_pending_name(mail_auth):
    card = MailToolkit().compose_draft('a@b.com', 'subject', 'body')
    cfg = lazyllm.globals['agentic_config']
    cfg.update(mail_draft_confirm_id=card['draft_id'], mail_draft_confirm_revision=card['revision'],
               mail_draft_patch={'attachments': [
                   {'filename': 'saved.txt', 'content_base64': 'eA=='},
                   {'filename': 'retry.txt', 'content_base64': '@@@'},
               ]})
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send') as blocked_send:
        with pytest.raises(ToolExecutionError):
            MailToolkit().send_draft(card['draft_id'])
        blocked_send.assert_not_called()
    failed = _load_draft(card['draft_id'])
    cfg.update(mail_draft_confirm_revision=failed['revision'], mail_draft_patch={
        'attachments': [{'filename': 'retry.txt', 'content_base64': 'eQ=='}],
    })
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send', return_value={'id': 'complete'}) as send:
        receipt = MailToolkit().send_draft(card['draft_id'])
    assert receipt['status'] == 'sent'
    assert receipt['attachments'] == ['saved.txt', 'retry.txt']
    assert receipt['pending_attachment_names'] == []
    assert len(receipt['attachment_paths']) == 2
    assert [part.get_payload(decode=True) for part in send.call_args.args[0].iter_attachments()] == [b'x', b'y']


def test_partial_send_accumulates_accepted_recipients_across_retries(mail_auth):
    card = MailToolkit().compose_draft(['a@b.com', 'b@b.com', 'c@b.com'], 'subject', 'body')
    cfg = lazyllm.globals['agentic_config']
    cfg.update(mail_draft_confirm_id=card['draft_id'], mail_draft_confirm_revision=1)
    replies = [
        {'partial_sent': True, 'accepted': ['a@b.com'], 'refused': [{'address': 'b@b.com'}, {'address': 'c@b.com'}]},
        {'partial_sent': True, 'accepted': ['b@b.com'], 'refused': [{'address': 'c@b.com'}]},
        {'id': 'complete'},
    ]
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send', side_effect=replies) as send:
        for _ in replies:
            receipt = MailToolkit().send_draft(card['draft_id'])
            cfg['mail_draft_confirm_revision'] = receipt['revision']
    assert [call.args[0]['To'] for call in send.call_args_list] == [
        'a@b.com, b@b.com, c@b.com', 'b@b.com, c@b.com', 'c@b.com',
    ]
    assert receipt['accepted_recipients'] == ['a@b.com', 'b@b.com', 'c@b.com']
    assert receipt['status'] == 'sent'


def test_bodystructure_keeps_duplicate_filenames():
    raw = (
        '1 (UID 12 BODYSTRUCTURE (('
        '"TEXT" "PLAIN" ("CHARSET" "UTF-8") NIL NIL "7BIT" 12 1)'
        '("APPLICATION" "PDF" ("NAME" "a.pdf") NIL NIL "BASE64" 100 NIL '
        '("ATTACHMENT" ("FILENAME" "a.pdf")) NIL)'
        '("APPLICATION" "PDF" ("NAME" "a.pdf") NIL NIL "BASE64" 80 NIL '
        '("ATTACHMENT" ("FILENAME" "a.pdf")) NIL) "MIXED"))'
    )
    items = _attachments_from_bodystructure(raw)
    assert [row['attachment_id'] for row in items] == ['2', '3']
    assert [row['filename'] for row in items] == ['a.pdf', 'a.pdf']


def test_card_upload_rejects_too_many_files(mail_auth):
    import base64
    items = [
        {'filename': f'{index}.txt', 'content_base64': base64.b64encode(b'x').decode('ascii')}
        for index in range(6)
    ]
    with pytest.raises(ToolExecutionError, match='At most 5'):
        _write_outgoing_attachments(items)


def test_card_upload_uses_realtime_safe_ten_megabyte_limit(mail_auth):
    import base64
    content = b'x' * (10 * 1024 * 1024 + 1)
    with pytest.raises(ToolExecutionError, match='10MB'):
        _write_outgoing_attachments([{
            'filename': 'too-large.bin',
            'content_base64': base64.b64encode(content).decode('ascii'),
        }])


def test_card_upload_rejects_malformed_base64(mail_auth):
    with pytest.raises(ToolExecutionError, match='not valid base64'):
        _write_outgoing_attachments([{'filename': 'bad.txt', 'content_base64': '@@@'}])


def test_partial_send_retries_only_refused_recipients(mail_auth):
    draft = {
        'draft_id': 'draft_partial',
        'revision': 1,
        'to': ['ok@b.com', 'bad@b.com'],
        'cc': [],
        'subject': 'hi',
        'body': 'body',
        'attachment_paths': [],
        'in_reply_to': '',
        'status': 'draft',
        'mailbox': 'user@qq.com',
        'provider': 'qqmail',
        'sent_at': '',
        'last_error': '',
    }
    _save_draft(draft)
    lazyllm.globals['agentic_config']['mail_draft_confirm_id'] = 'draft_partial'
    lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = 1
    seen: list[list[str]] = []

    class PartialSMTP(_FakeSMTP):
        def sendmail(self, from_addr, to_addrs, msg, *args, **kwargs):
            self.send(b'payload\r\n.\r\n')
            seen.append(list(to_addrs))
            if 'ok@b.com' in to_addrs:
                return {'bad@b.com': (550, b'user unknown')}
            return {}

    with patch('lazymind.chat.engine.tools.mail.smtplib.SMTP_SSL', PartialSMTP):
        first = MailToolkit().send_draft('draft_partial')
        assert first['status'] == 'partial_sent'
        saved = _load_draft('draft_partial')
        assert saved['pending_recipients'] == ['bad@b.com']
        with pytest.raises(ToolExecutionError, match='stale'):
            MailToolkit().send_draft('draft_partial')
        assert len(seen) == 1
        lazyllm.globals['agentic_config']['mail_draft_confirm_revision'] = first['revision']
        second = MailToolkit().send_draft('draft_partial')
    assert second['status'] == 'sent'
    assert seen[0] == ['ok@b.com', 'bad@b.com']
    assert seen[1] == ['bad@b.com']


@pytest.mark.parametrize('unknown', [False, True])
def test_send_attempt_consumes_confirmation_on_failure(mail_auth, unknown):
    card = MailToolkit().compose_draft('a@b.com', 'subject', 'body')
    cfg = lazyllm.globals['agentic_config']
    cfg.update(mail_draft_confirm_id=card['draft_id'], mail_draft_confirm_revision=card['revision'])
    error = ToolExecutionError('Delivery unknown' if unknown else 'SMTP unavailable')
    error.delivery_unknown = unknown
    with patch('lazymind.chat.engine.tools.mail._IMAPBackend.send', side_effect=error) as send:
        with pytest.raises(ToolExecutionError):
            MailToolkit().send_draft(card['draft_id'])
        failed = _load_draft(card['draft_id'])
        assert failed['revision'] > card['revision']
        with pytest.raises(ToolExecutionError, match='unknown|stale'):
            MailToolkit().send_draft(card['draft_id'])
        assert send.call_count == 1
        cfg['mail_draft_confirm_revision'] = failed['revision']
        send.side_effect = None
        send.return_value = {'id': 'retry'}
        assert MailToolkit().send_draft(card['draft_id'])['status'] == 'sent'
        assert send.call_count == 2


def test_confirm_patch_clears_pending_recipients_when_to_or_cc_changes(mail_auth):
    draft = {
        'to': ['ok@b.com', 'bad@b.com'],
        'cc': ['cc@b.com'],
        'pending_recipients': ['bad@b.com'],
        'subject': 'hi',
        'body': 'body',
        'attachment_paths': [],
    }
    lazyllm.globals['agentic_config']['mail_draft_patch'] = {
        'to': 'ok@b.com, bad@b.com',
        'cc': 'cc@b.com',
        'subject': 'hi',
    }
    _apply_confirm_patch(draft)
    assert draft['pending_recipients'] == ['bad@b.com']

    lazyllm.globals['agentic_config']['mail_draft_patch'] = {'to': 'new@b.com'}
    _apply_confirm_patch(draft)
    assert draft['to'] == ['new@b.com']
    assert draft['pending_recipients'] == []

    draft['pending_recipients'] = ['bad@b.com']
    lazyllm.globals['agentic_config']['mail_draft_patch'] = {'cc': 'other@b.com'}
    _apply_confirm_patch(draft)
    assert draft['cc'] == ['other@b.com']
    assert draft['pending_recipients'] == []

    draft['pending_recipients'] = ['bad@b.com']
    lazyllm.globals['agentic_config']['mail_draft_patch'] = {'subject': 'new subject'}
    _apply_confirm_patch(draft)
    assert draft['pending_recipients'] == ['bad@b.com']


def test_named_mime_parts_use_bodystructure_section_ids():
    from email.message import EmailMessage

    msg = EmailMessage()
    msg.set_content('body')
    msg.add_attachment(b'pdf-bytes', maintype='application', subtype='pdf', filename='a.pdf')
    msg.add_attachment(b'zip-bytes', maintype='application', subtype='zip', filename='b.zip')
    parts = _named_mime_parts(msg)
    assert [section for section, _name, _part, _payload in parts] == ['2', '3']
    raw = (
        '1 (UID 12 BODYSTRUCTURE (('
        '"TEXT" "PLAIN" ("CHARSET" "UTF-8") NIL NIL "7BIT" 12 1)'
        '("APPLICATION" "PDF" ("NAME" "a.pdf") NIL NIL "BASE64" 100 NIL '
        '("ATTACHMENT" ("FILENAME" "a.pdf")) NIL)'
        '("APPLICATION" "ZIP" ("NAME" "b.zip") NIL NIL "BASE64" 80 NIL '
        '("ATTACHMENT" ("FILENAME" "b.zip")) NIL) "MIXED"))'
    )
    items = _attachments_from_bodystructure(raw)
    assert [row['attachment_id'] for row in items] == ['2', '3']


def test_build_message_keeps_refused_cc_as_cc():
    message = _build_message({
        'to': ['ok@b.com'],
        'cc': ['cc@b.com', 'badcc@b.com'],
        'pending_recipients': ['badcc@b.com'],
        'subject': 'hi',
        'body': 'body',
        'attachment_paths': [],
    }, 'user@qq.com')
    assert (message['To'] or '') == ''
    assert (message['Cc'] or '') == 'badcc@b.com'


def test_build_message_excludes_accepted_recipients_after_to_edit():
    message = _build_message({
        'to': ['ok@b.com', 'new@b.com'],
        'cc': [],
        'pending_recipients': [],
        'accepted_recipients': ['ok@b.com'],
        'subject': 'hi',
        'body': 'body',
        'attachment_paths': [],
    }, 'user@qq.com')
    assert (message['To'] or '') == 'new@b.com'
