import json
import os
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


class _RecordingIMAP:
    def __init__(self):
        self.calls: list[tuple[str, tuple]] = []

    def select(self, *args, **kwargs):
        return 'OK', []

    def uid(self, command, *args):
        self.calls.append((str(command).upper(), args))
        if str(command).upper() == 'SEARCH':
            return 'OK', [b'101 102']
        header = (
            b'From: a@b.com\r\nTo: c@d.com\r\nSubject: hi\r\n'
            b'Date: Wed, 2 Sep 2026\r\nMessage-ID: <x@y>\r\n\r\nbody'
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
        assert result['items'][0]['id'] == '102'
        MailToolkit().read('102', mailbox='user@qq.com')
        MailToolkit().read_thread('<x@y>', mailbox='user@qq.com')
    commands = [command for command, _args in imap.calls]
    assert commands.count('SEARCH') >= 2
    assert commands.count('FETCH') >= 2


def test_mail_toolkit_registers_only_mail_auth_name():
    MailToolkit()
    assert TOOL_AUTH_REGISTRY.get('mail') == 'dynamic_tool_auth'
    for name in ('gmailimap', 'qqmail', 'qqexmail', 'netease163', 'neteaseqiye'):
        assert name not in TOOL_AUTH_REGISTRY


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
    from lazymind.chat.engine.tools.local_file import workspace
    context = {'workspace_id': 'bound'}
    lazyllm.globals['agentic_config'][context_key] = (
        {'_core_workspace_context': context} if context_key == 'parent_agentic_config' else context)
    monkeypatch.setattr(workspace, '_cfg', {'agentic_workspace': str(tmp_path), 'trusted_local_mode': True})
    internal = workspace.chat_agent_workspace('u1', 'c1')
    assert workspace._file_tool_root(internal) == internal
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


@pytest.mark.parametrize('target', ['.mail_drafts', '.mail_drafts/linked.json', 'mail_attachments', 'mail_attachments/linked.txt'])
def test_mail_outputs_reject_existing_external_symlinks(mail_auth, tmp_path, target):
    from pathlib import Path
    from lazymind.chat.engine.tools import mail
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
    if target.startswith('.mail_drafts'):
        with pytest.raises(ToolExecutionError, match='workspace'):
            _save_draft({'draft_id': 'linked'})
    else:
        with patch.object(mail._IMAPBackend, 'read_attachment', return_value=b'new'):
            with pytest.raises(ToolExecutionError, match='workspace'):
                MailToolkit().read_attachment('message', 'linked.txt')
    assert list(outside.iterdir()) == [] if outside.is_dir() else outside.read_text() == 'unchanged'


@pytest.mark.parametrize('missing', ['user_id', 'conversation_id'])
def test_mail_internal_drafts_require_full_identity(mail_auth, missing):
    from lazymind.chat.engine.tools.mail import _draft_dir
    lazyllm.globals['agentic_config'].pop(missing)
    with pytest.raises(ToolExecutionError, match='user_id.*conversation_id'):
        _draft_dir()
