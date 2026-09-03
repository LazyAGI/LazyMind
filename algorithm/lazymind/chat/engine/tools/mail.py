"""Connected mailbox tools for NetEase, Tencent, and Gmail accounts."""

from __future__ import annotations

import email
import imaplib
import json
import mimetypes
import os
import re
import smtplib
import socket
import ssl
import uuid
from datetime import datetime, timedelta, timezone
from email.header import decode_header, make_header
from email.message import EmailMessage
from email.utils import formatdate
from typing import Any, NoReturn

import lazyllm
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.base import _write_agent_data

from lazymind.chat.config import CHAT_ATTACHMENT_EXTENSIONS
from lazymind.chat.engine.attachment_reader import parse_attachment_content
from lazymind.chat.engine.tools.local_file.workspace import (
    chat_agent_workspace,
    _resolve_workspace_path,
)


_MAIL_PROVIDERS = {'gmailimap', 'qqmail', 'qqexmail', 'netease163', 'neteaseqiye'}
_IMAP_ENDPOINTS = {
    'netease163': {
        'imap_id': True,
        'by_domain': {
            '163.com': {'imap_host': 'imap.163.com', 'smtp_host': 'smtp.163.com'},
            '126.com': {'imap_host': 'imap.126.com', 'smtp_host': 'smtp.126.com'},
            'yeah.net': {'imap_host': 'imap.yeah.net', 'smtp_host': 'smtp.yeah.net'},
            'vip.163.com': {'imap_host': 'imap.vip.163.com', 'smtp_host': 'smtp.vip.163.com'},
            '188.com': {'imap_host': 'imap.188.com', 'smtp_host': 'smtp.188.com'},
        },
    },
    'qqmail': {
        'by_domain': {
            'qq.com': {'imap_host': 'imap.qq.com', 'smtp_host': 'smtp.qq.com'},
            'foxmail.com': {'imap_host': 'imap.qq.com', 'smtp_host': 'smtp.qq.com'},
        },
    },
    'qqexmail': {'imap_host': 'imap.exmail.qq.com', 'smtp_host': 'smtp.exmail.qq.com'},
    'neteaseqiye': {
        'imap_id': True,
        'imap_host': 'imap.qiye.163.com',
        'smtp_host': 'smtp.qiye.163.com',
    },
    # IMAP + Google app password, not Gmail OAuth. App passwords skip Google Cloud
    # OAuth client / consent-screen setup and are the more user-friendly connect path.
    'gmailimap': {'imap_host': 'imap.gmail.com', 'smtp_host': 'smtp.gmail.com'},
}
_REAUTH_PATH = '/cloud-documents/mail'
_EMAIL_RE = re.compile(r'[^,\s;]+@[^,\s;]+')
_COMMON_ATTACHMENT_EXTS = set(CHAT_ATTACHMENT_EXTENSIONS) | {
    '.zip', '.rar', '.7z', '.xlsx', '.xls', '.csv', '.ppt', '.odt', '.rtf',
}


def _agentic_config() -> dict[str, Any]:
    config = lazyllm.globals.get('agentic_config')
    return config if isinstance(config, dict) else {}


def _fail(message: str) -> NoReturn:
    raise ToolExecutionError(message)


def _draft_revision(draft: dict[str, Any]) -> int:
    try:
        value = int(draft.get('revision') or 1)
    except (TypeError, ValueError):
        value = 1
    return value if value > 0 else 1


def _confirm_revision() -> int:
    raw = _agentic_config().get('mail_draft_confirm_revision')
    try:
        return int(raw)
    except (TypeError, ValueError):
        return 0


def _is_uncertain_delivery_error(orig: BaseException) -> bool:
    if isinstance(orig, smtplib.SMTPServerDisconnected):
        return True
    if isinstance(orig, (TimeoutError, socket.timeout, ConnectionError, ssl.SSLError, socket.gaierror)):
        return True
    if isinstance(orig, OSError) and not isinstance(orig, smtplib.SMTPException):
        return True
    return False


def _raise_send_error(orig: BaseException, *, data_submitted: bool) -> NoReturn:
    detail = str(orig).strip() or orig.__class__.__name__
    if data_submitted and _is_uncertain_delivery_error(orig):
        err = ToolExecutionError(
            'Mail delivery is unknown: DATA may already have been accepted, but the '
            f'server acknowledgement was not received ({detail}). Do not retry until '
            'you confirm the recipient did not receive it; retrying can send a duplicate.'
        )
        err.delivery_unknown = True
        raise err from orig
    _fail(f'Failed to send the email: {detail}')


def _parse_credential(raw: Any) -> dict[str, str]:
    if not raw:
        return {}
    if isinstance(raw, dict):
        return {str(k): str(v) for k, v in raw.items() if v is not None}
    text = str(raw).strip()
    if text.startswith('{'):
        try:
            loaded = json.loads(text)
        except json.JSONDecodeError:
            return {}
        if isinstance(loaded, dict):
            return {str(k): str(v) for k, v in loaded.items() if v is not None}
    return {}


def _accounts() -> list[dict[str, str]]:
    auth = lazyllm.globals.config['dynamic_tool_auth'] or {}
    raw = auth.get('mail')
    chunks = raw if isinstance(raw, list) else ([raw] if raw else [])
    accounts: list[dict[str, str]] = []
    for chunk in chunks:
        cred = _parse_credential(chunk)
        provider = (cred.get('provider') or '').strip().lower()
        email_addr = (cred.get('email') or '').strip()
        secret = (cred.get('secret') or '').strip()
        if provider in _MAIL_PROVIDERS and email_addr and secret:
            cred['provider'] = provider
            cred['email'] = email_addr
            cred['secret'] = secret
            accounts.append(cred)
    return accounts


def _credential() -> dict[str, str]:
    accounts = _accounts()
    return accounts[0] if accounts else {}


def _require_accounts() -> list[dict[str, str]]:
    accounts = _accounts()
    if not accounts:
        _fail(
            'No mailbox is enabled for chat. Connect a supported mailbox in '
            '资源库 → 云文档 → 邮箱连接 and turn the switch on.'
        )
    valid: list[dict[str, str]] = []
    expired = False
    for cred in accounts:
        status = (cred.get('status') or 'ACTIVE').strip().upper()
        if status in {'REVOKED', 'DISCONNECTED'}:
            continue
        if status in {'EXPIRED', 'ERROR'}:
            expired = True
            continue
        valid.append(cred)
    if valid:
        return valid
    if expired:
        _fail(
            'Mailbox authorization is invalid. Re-authorize the connected account '
            'in 资源库 → 云文档 → 邮箱连接.'
        )
    _fail(
        'No mailbox is enabled for chat. Connect a supported mailbox in '
        '资源库 → 云文档 → 邮箱连接 and turn the switch on.'
    )


def _pick_account(mailbox: str = '') -> dict[str, str]:
    accounts = _require_accounts()
    key = str(mailbox or '').strip().lower()
    if not key:
        return accounts[0]
    for cred in accounts:
        candidates = {
            (cred.get('email') or '').strip().lower(),
            (cred.get('provider') or '').strip().lower(),
            (cred.get('connection_id') or '').strip().lower(),
        }
        if key in candidates:
            return cred
    _fail(
        f'Mailbox {mailbox} is not enabled. Pass mailbox as the email address or provider.'
    )


def _require_connection() -> dict[str, str]:
    return _pick_account()


def _tag_mailbox(payload: dict[str, Any], cred: dict[str, str]) -> dict[str, Any]:
    tagged = dict(payload)
    tagged['mailbox'] = cred.get('email') or ''
    tagged['provider'] = cred.get('provider') or ''
    items = []
    for item in tagged.get('items') or []:
        if isinstance(item, dict):
            row = dict(item)
            row['mailbox'] = tagged['mailbox']
            row['provider'] = tagged['provider']
            items.append(row)
        else:
            items.append(item)
    if 'items' in tagged:
        tagged['items'] = items
    return tagged


def _call_mailboxes(mailbox: str, runner):
    if str(mailbox or '').strip():
        cred = _pick_account(mailbox)
        return _tag_mailbox(runner(cred), cred)
    last_error: Exception | None = None
    for cred in _require_accounts():
        try:
            return _tag_mailbox(runner(cred), cred)
        except ToolExecutionError as orig:
            last_error = orig
            if 'was not found' in str(orig):
                continue
            raise
    if last_error is not None:
        raise last_error
    _fail('The requested email was not found.')


def _split_addresses(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, (list, tuple)):
        items = [str(item).strip() for item in value]
    else:
        items = _EMAIL_RE.findall(str(value))
    return [item for item in items if item]


def _coerce_path_list(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, (list, tuple, set)):
        items = [str(item).strip() for item in value]
    else:
        text = str(value).strip()
        if not text:
            return []
        if text.startswith('['):
            try:
                parsed = json.loads(text)
            except json.JSONDecodeError:
                parsed = None
            if isinstance(parsed, list):
                items = [str(item).strip() for item in parsed]
            else:
                items = [text]
        else:
            items = [text]
    return [item for item in items if item]


def _resolve_attachment_paths(attachment_paths: Any) -> list[str]:
    requested = _coerce_path_list(attachment_paths)
    if not requested:
        return []
    cfg = _agentic_config()
    user_id = str(cfg.get('user_id') or '0')
    conversation_id = str(cfg.get('conversation_id') or 'default')
    resolved: list[str] = []
    missing: list[str] = []
    for raw_path in requested:
        _, candidate = _resolve_workspace_path(raw_path, user_id, conversation_id)
        if os.path.isfile(candidate):
            resolved.append(candidate)
            continue
        missing.append(raw_path)
    if missing:
        _fail('Attachment file was not found: ' + ', '.join(missing))
    return resolved


def _decode_header_value(raw: Any) -> str:
    if raw is None:
        return ''
    try:
        return str(make_header(decode_header(str(raw))))
    except Exception:
        return str(raw)


def _iso(dt: datetime | None) -> str:
    if dt is None:
        return ''
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc).isoformat()


def _draft_dir() -> str:
    cfg = _agentic_config()
    root = chat_agent_workspace(str(cfg.get('user_id') or '0'), str(cfg.get('conversation_id') or 'default'))
    path = os.path.join(root, '.mail_drafts')
    os.makedirs(path, exist_ok=True)
    return path


def _draft_path(draft_id: str) -> str:
    safe = re.sub(r'[^A-Za-z0-9_-]', '', str(draft_id or ''))
    if not safe:
        raise ToolExecutionError('draft_id is required')
    return os.path.join(_draft_dir(), f'{safe}.json')


def _load_draft(draft_id: str) -> dict[str, Any]:
    path = _draft_path(draft_id)
    if not os.path.exists(path):
        _fail('Mail draft was not found.')
    with open(path, encoding='utf-8') as handle:
        return json.load(handle)


def _save_draft(draft: dict[str, Any]) -> dict[str, Any]:
    path = _draft_path(str(draft.get('draft_id') or ''))
    with open(path, 'w', encoding='utf-8') as handle:
        json.dump(draft, handle, ensure_ascii=False, indent=2)
    return draft


def _imap_date(value: str, *, before: bool = False) -> str:
    text = (value or '').strip()
    if not text:
        return ''
    try:
        dt = datetime.fromisoformat(text.replace('Z', '+00:00'))
    except ValueError:
        try:
            dt = datetime.strptime(text, '%Y-%m-%d')
        except ValueError:
            return ''
    if before:
        # IMAP BEFORE is exclusive of the given date. Advance one day so the
        # documented inclusive end date is actually included.
        dt = dt + timedelta(days=1)
    return dt.strftime('%d-%b-%Y')


def _imap_uid(message_id: str) -> str:
    uid = str(message_id or '').strip()
    if not uid.isdigit():
        _fail('The requested email was not found.')
    return uid


def _imap_payload(fetched: Any) -> bytes | None:
    if not fetched:
        return None
    for item in fetched:
        if isinstance(item, tuple) and len(item) >= 2 and item[1] is not None:
            raw = item[1]
            return raw if isinstance(raw, (bytes, bytearray)) else bytes(raw)
    return None


def _resolve_imap_endpoint(provider: str, email: str) -> dict[str, Any]:
    spec = _IMAP_ENDPOINTS.get((provider or '').strip().lower()) or {}
    domain = (email or '').rsplit('@', 1)[-1].lower()
    by_domain = spec.get('by_domain') or {}
    hosts = {}
    for suffix in sorted(by_domain, key=len, reverse=True):
        if domain == suffix or domain.endswith('.' + suffix):
            hosts = by_domain[suffix]
            break
    imap_host = hosts.get('imap_host') or spec.get('imap_host')
    smtp_host = hosts.get('smtp_host') or spec.get('smtp_host')
    if not imap_host or not smtp_host:
        _fail(f'Unsupported mailbox domain for {provider}.')
    return {
        'imap_host': imap_host,
        'imap_port': int(spec.get('imap_port') or 993),
        'smtp_host': smtp_host,
        'smtp_port': int(spec.get('smtp_port') or 465),
        'imap_id': bool(spec.get('imap_id')),
    }


class _IMAPBackend:
    def __init__(self, cred: dict[str, str]) -> None:
        self.provider = cred['provider']
        self.email = cred['email']
        self.secret = cred['secret']
        self.endpoint = _resolve_imap_endpoint(self.provider, self.email)

    def _connect(self) -> imaplib.IMAP4_SSL:
        client = imaplib.IMAP4_SSL(self.endpoint['imap_host'], self.endpoint['imap_port'])
        if self.endpoint.get('imap_id'):
            try:
                client.xatom('ID', '("name" "LazyMind" "version" "1.0")')
            except Exception:
                pass
        try:
            status, _ = client.login(self.email, self.secret)
        except imaplib.IMAP4.error as orig:
            client.logout()
            raise ToolExecutionError(
                'Mailbox authorization expired. Re-authorize the mailbox in 资源库 → 云文档 → 邮箱连接.'
            ) from orig
        if status != 'OK':
            client.logout()
            _fail('Mailbox authorization expired. Re-authorize the mailbox in 资源库 → 云文档 → 邮箱连接.')
        return client

    def search(self, **filters: str) -> dict[str, Any]:
        client = self._connect()
        try:
            client.select('INBOX', readonly=True)
            criteria = ['ALL']
            if filters.get('sender'):
                criteria.extend(['FROM', filters['sender']])
            if filters.get('recipient'):
                criteria.extend(['TO', filters['recipient']])
            if filters.get('subject'):
                criteria.extend(['SUBJECT', filters['subject']])
            if filters.get('keyword'):
                criteria.extend(['TEXT', filters['keyword']])
            since = _imap_date(filters.get('after', ''))
            before = _imap_date(filters.get('before', ''), before=True)
            if since:
                criteria.extend(['SINCE', since])
            if before:
                criteria.extend(['BEFORE', before])
            status, data = client.uid('SEARCH', *criteria)
            if status != 'OK':
                return {'provider': self.provider, 'mailbox': self.email, 'items': []}
            ids = (data[0] or b'').split()[-20:]
            items = []
            for uid in reversed(ids):
                status, fetched = client.uid('FETCH', uid, '(RFC822.HEADER)')
                raw = _imap_payload(fetched)
                if status != 'OK' or raw is None:
                    continue
                msg = email.message_from_bytes(raw)
                items.append({
                    'id': uid.decode('ascii'),
                    'thread_id': _decode_header_value(msg.get('Message-ID') or uid.decode('ascii')),
                    'from': _decode_header_value(msg.get('From')),
                    'to': _decode_header_value(msg.get('To')),
                    'subject': _decode_header_value(msg.get('Subject')),
                    'date': _decode_header_value(msg.get('Date')),
                    'snippet': '',
                })
            return {'provider': self.provider, 'mailbox': self.email, 'items': items}
        finally:
            try:
                client.logout()
            except Exception:
                pass

    def _fetch_message(self, message_id: str) -> email.message.Message:
        uid = _imap_uid(message_id)
        client = self._connect()
        try:
            client.select('INBOX', readonly=True)
            status, fetched = client.uid('FETCH', uid, '(RFC822)')
            raw = _imap_payload(fetched)
            if status != 'OK' or raw is None:
                _fail('The requested email was not found.')
            return email.message_from_bytes(raw)
        finally:
            try:
                client.logout()
            except Exception:
                pass

    def read(self, message_id: str) -> dict[str, Any]:
        msg = self._fetch_message(message_id)
        attachments = []
        body_parts = []
        for part in msg.walk():
            filename = part.get_filename()
            if filename:
                decoded = _decode_header_value(filename)
                attachments.append({
                    'attachment_id': decoded,
                    'filename': decoded,
                    'mime_type': part.get_content_type(),
                    'size': len(part.get_payload(decode=True) or b''),
                })
                continue
            if part.get_filename():
                continue
            payload = part.get_payload(decode=True) or b''
            charset = part.get_content_charset() or 'utf-8'
            text = payload.decode(charset, errors='replace')
            if part.get_content_type() == 'text/plain':
                body_parts.append(text)
            elif part.get_content_type() == 'text/html' and not body_parts:
                body_parts.append(re.sub(r'<[^>]+>', ' ', text))
        return {
            'id': message_id,
            'thread_id': _decode_header_value(msg.get('Message-ID') or message_id),
            'from': _decode_header_value(msg.get('From')),
            'to': _decode_header_value(msg.get('To')),
            'cc': _decode_header_value(msg.get('Cc')),
            'subject': _decode_header_value(msg.get('Subject')),
            'date': _decode_header_value(msg.get('Date')),
            'body': '\n'.join(body_parts)[:20000],
            'attachments': attachments,
            'cite': f'email:{message_id}',
        }

    def read_thread(self, thread_id: str) -> dict[str, Any]:
        needle = (thread_id or '').strip()
        client = self._connect()
        try:
            client.select('INBOX', readonly=True)
            status, data = client.uid('SEARCH', 'ALL')
            ids = (data[0] or b'').split() if status == 'OK' else []
            matched: list[str] = []
            for uid in reversed(ids[-40:]):
                status, fetched = client.uid(
                    'FETCH',
                    uid,
                    '(BODY.PEEK[HEADER.FIELDS (MESSAGE-ID IN-REPLY-TO REFERENCES)])',
                )
                raw = _imap_payload(fetched)
                if status != 'OK' or raw is None:
                    continue
                headers = email.message_from_bytes(raw)
                blob = ' '.join([
                    _decode_header_value(headers.get('Message-ID')),
                    _decode_header_value(headers.get('In-Reply-To')),
                    _decode_header_value(headers.get('References')),
                ])
                if needle and (needle in blob or needle.strip('<>') in blob):
                    matched.append(uid.decode('ascii'))
            messages = [self.read(mid) for mid in reversed(matched[-20:])]
            if not messages and needle.isdigit():
                messages = [self.read(needle)]
            return {'thread_id': thread_id, 'messages': messages}
        finally:
            try:
                client.logout()
            except Exception:
                pass

    def read_attachment(self, message_id: str, attachment_id: str) -> bytes:
        msg = self._fetch_message(message_id)
        wanted = (attachment_id or '').strip()
        for part in msg.walk():
            filename = part.get_filename() or ''
            if filename == wanted or _decode_header_value(filename) == wanted:
                payload = part.get_payload(decode=True)
                if payload is None:
                    break
                return payload
        _fail('Failed to read the email attachment.')

    def send(self, message: EmailMessage) -> dict[str, Any]:
        data_submitted = False
        try:
            with smtplib.SMTP_SSL(
                self.endpoint['smtp_host'],
                self.endpoint['smtp_port'],
                timeout=30,
            ) as smtp:
                smtp.login(self.email, self.secret)
                orig_send = smtp.send

                def tracked_send(payload):
                    orig_send(payload)
                    blob = (
                        payload if isinstance(payload, (bytes, bytearray))
                        else str(payload).encode('utf-8', 'replace')
                    )
                    if blob.endswith(b'.\r\n') or blob.endswith(b'.\n'):
                        nonlocal data_submitted
                        data_submitted = True

                smtp.send = tracked_send
                smtp.send_message(message)
        except smtplib.SMTPAuthenticationError as orig:
            raise ToolExecutionError(
                'Mailbox authorization expired. Re-authorize the mailbox in 资源库 → 云文档 → 邮箱连接.'
            ) from orig
        except ToolExecutionError:
            raise
        except (smtplib.SMTPException, OSError) as orig:
            _raise_send_error(orig, data_submitted=data_submitted)
        return {'id': message.get('Message-ID') or '', 'sent_at': _iso(datetime.now(timezone.utc))}


def _backend(cred: dict[str, str]):
    provider = (cred.get('provider') or '').strip().lower()
    if provider in _IMAP_ENDPOINTS:
        return _IMAPBackend(cred)
    _fail(
        'No mailbox is enabled for chat. Connect a supported mailbox in 资源库 → 云文档 → 邮箱连接.'
    )


def _build_message(draft: dict[str, Any], mailbox: str) -> EmailMessage:
    message = EmailMessage()
    message['From'] = mailbox
    message['To'] = ', '.join(draft.get('to') or [])
    if draft.get('cc'):
        message['Cc'] = ', '.join(draft['cc'])
    message['Subject'] = str(draft.get('subject') or '')
    message['Date'] = formatdate(localtime=True)
    if draft.get('in_reply_to'):
        message['In-Reply-To'] = str(draft['in_reply_to'])
        message['References'] = str(draft['in_reply_to'])
    message.set_content(str(draft.get('body') or ''))
    for path in draft.get('attachment_paths') or []:
        if not os.path.isfile(path):
            continue
        ctype, encoding = mimetypes.guess_type(path)
        if ctype is None or encoding is not None:
            ctype = 'application/octet-stream'
        maintype, subtype = ctype.split('/', 1)
        with open(path, 'rb') as handle:
            message.add_attachment(
                handle.read(),
                maintype=maintype,
                subtype=subtype,
                filename=os.path.basename(path),
            )
    return message


def _preview(draft: dict[str, Any]) -> dict[str, Any]:
    status = str(draft.get('status') or 'draft')
    return {
        'draft_id': draft.get('draft_id'),
        'revision': _draft_revision(draft),
        'mailbox': draft.get('mailbox') or '',
        'provider': draft.get('provider') or '',
        'to': draft.get('to') or [],
        'cc': draft.get('cc') or [],
        'subject': draft.get('subject') or '',
        'body': draft.get('body') or '',
        'attachments': [os.path.basename(path) for path in draft.get('attachment_paths') or []],
        'in_reply_to': draft.get('in_reply_to') or '',
        'status': status,
        'sent_at': draft.get('sent_at') or '',
        'last_error': draft.get('last_error') or '',
        'requires_confirmation': status not in {'sent'},
        'requires_reauth': bool(draft.get('requires_reauth')),
        'reauth_path': _REAUTH_PATH if draft.get('requires_reauth') else '',
        'delivery_unknown': status == 'delivery_unknown',
    }


def _emit_draft_card(draft: dict[str, Any]) -> dict[str, Any]:
    preview = _preview(draft)
    _write_agent_data(
        'ask_pending',
        ask_id=str(uuid.uuid4()),
        title='邮件发送预览',
        description='确认后才会发送。发送失败可重新发送；若投递结果未知，不要轻易重试以免重复发送。',
        questions=[{
            'text': '确认发送这封邮件？',
            'type': 'boolean',
            'choices': ['是', '否'],
        }],
        mail_draft=preview,
    )
    return preview


class MailToolkit:
    """Search, read, cite, and send mail through enabled NetEase, Tencent, and Gmail accounts.

    Personal and enterprise mailboxes can be enabled together. Gmail connects via
    IMAP/SMTP with a Google app password, not OAuth; that path is more user-friendly
    because it does not require a Google Cloud OAuth client or consent screen.
    Search results are tagged with mailbox/provider. When more than one mailbox is
    enabled, pass mailbox (email address or provider name) to read, attach, compose,
    or send. Sending always requires the user to confirm the draft preview card.
    Change an existing unsent draft with update_draft instead of composing a new one.
    """

    __public_apis__ = [
        'search', 'read', 'read_thread', 'read_attachment',
        'compose_draft', 'update_draft', 'send_draft',
    ]
    __tool_auto_activate__ = [
        r'邮件|邮箱|inbox|gmail|163|126|yeah\.net|qq邮箱|企业邮|(?<!\w)email(?!\w)|(?<!\w)mail(?!\w)',
    ]

    def __key_source__(self) -> Any:
        cred = _credential()
        if not cred.get('secret'):
            return None
        return cred

    def search(
        self,
        keyword: str = '',
        sender: str = '',
        recipient: str = '',
        subject: str = '',
        after: str = '',
        before: str = '',
        mailbox: str = '',
    ) -> dict[str, Any]:
        """Search enabled mailboxes without building a local index.

        Args:
            keyword: Free-text query matched against message bodies when supported.
            sender: Filter by From address.
            recipient: Filter by To address.
            subject: Filter by subject.
            after: Inclusive start date, YYYY-MM-DD.
            before: Inclusive end date, YYYY-MM-DD.
            mailbox: Optional email or provider (netease163/qqmail/gmailimap). Empty searches all enabled mailboxes.
        """
        accounts = [_pick_account(mailbox)] if str(mailbox or '').strip() else _require_accounts()
        items: list[dict[str, Any]] = []
        errors: list[dict[str, Any]] = []
        kwargs = {
            'keyword': str(keyword or '').strip(),
            'sender': str(sender or '').strip(),
            'recipient': str(recipient or '').strip(),
            'subject': str(subject or '').strip(),
            'after': str(after or '').strip(),
            'before': str(before or '').strip(),
        }
        for cred in accounts:
            try:
                result = _tag_mailbox(_backend(cred).search(**kwargs), cred)
            except ToolExecutionError as orig:
                errors.append({
                    'mailbox': cred.get('email') or '',
                    'provider': cred.get('provider') or '',
                    'error': str(orig),
                })
                continue
            items.extend(item for item in (result.get('items') or []) if isinstance(item, dict))
        if not items and errors and len(errors) == len(accounts):
            _fail(errors[0]['error'])
        payload: dict[str, Any] = {
            'items': items,
            'mailboxes': [cred.get('email') or '' for cred in accounts],
        }
        if errors:
            payload['errors'] = errors
        return payload

    def read(self, message_id: str, mailbox: str = '') -> dict[str, Any]:
        """Read one email, including headers, body, and attachment metadata.

        Args:
            message_id: IMAP UID returned by search. Sequence numbers are not stable.
            mailbox: Optional email or provider. Required when the same id could exist in more than one mailbox.
        """
        if not str(message_id or '').strip():
            raise ToolExecutionError('message_id is required')
        return _call_mailboxes(mailbox, lambda cred: _backend(cred).read(str(message_id).strip()))

    def read_thread(self, thread_id: str, mailbox: str = '') -> dict[str, Any]:
        """Read a complete email conversation/thread.

        Args:
            thread_id: Gmail thread id or IMAP Message-ID.
            mailbox: Optional email or provider when multiple mailboxes are enabled.
        """
        if not str(thread_id or '').strip():
            raise ToolExecutionError('thread_id is required')
        return _call_mailboxes(mailbox, lambda cred: _backend(cred).read_thread(str(thread_id).strip()))

    def read_attachment(self, message_id: str, attachment_id: str, mailbox: str = '') -> dict[str, Any]:
        """Download a common email attachment into the conversation workspace.

        Args:
            message_id: Provider message id.
            attachment_id: Attachment id or filename from read().
            mailbox: Optional email or provider when multiple mailboxes are enabled.
        """
        if not str(message_id or '').strip() or not str(attachment_id or '').strip():
            raise ToolExecutionError('message_id and attachment_id are required')

        def _download(cred: dict[str, str]) -> dict[str, Any]:
            backend = _backend(cred)
            raw = backend.read_attachment(str(message_id).strip(), str(attachment_id).strip())
            filename = os.path.basename(_decode_header_value(str(attachment_id))) or 'attachment.bin'
            ext = os.path.splitext(filename)[1].lower()
            if ext and ext not in _COMMON_ATTACHMENT_EXTS:
                _fail(f'Attachment type {ext} is not supported.')
            cfg = _agentic_config()
            workspace = chat_agent_workspace(
                str(cfg.get('user_id') or '0'),
                str(cfg.get('conversation_id') or 'default'),
            )
            folder = os.path.join(workspace, 'mail_attachments')
            os.makedirs(folder, exist_ok=True)
            target = os.path.join(folder, filename)
            with open(target, 'wb') as handle:
                handle.write(raw)
            parsed = ''
            try:
                if ext in CHAT_ATTACHMENT_EXTENSIONS:
                    parsed = parse_attachment_content(target)[:20000]
            except Exception as orig:
                raise ToolExecutionError(f'Failed to read the email attachment: {orig}') from orig
            return {
                'path': target,
                'filename': filename,
                'size': len(raw),
                'text': parsed,
                'cite': f'email-attachment:{message_id}:{filename}',
            }

        return _call_mailboxes(mailbox, _download)

    def compose_draft(
        self,
        to: Any,
        subject: str,
        body: str,
        cc: Any = None,
        attachment_paths: Any = None,
        in_reply_to: str = '',
        mailbox: str = '',
    ) -> dict[str, Any]:
        """Create a new or reply mail draft. Never send from this method.

        To change recipients, subject, body, or attachments later, call update_draft
        with this draft_id. Do not compose a second draft for the same email.

        Args:
            to: Recipient email or list of recipients.
            subject: Mail subject.
            body: Plain-text body.
            cc: Optional CC addresses.
            attachment_paths: One workspace/artifact path, or a list of paths to attach.
                Paths must stay inside the current conversation workspace.
            in_reply_to: Optional original Message-ID when composing a reply.
            mailbox: Optional sending account (email or provider). Defaults to the first enabled mailbox.
        """
        cred = _pick_account(mailbox)
        recipients = _split_addresses(to)
        if not recipients:
            raise ToolExecutionError('at least one recipient is required')
        paths = _resolve_attachment_paths(attachment_paths)
        now = _iso(datetime.now(timezone.utc))
        draft = {
            'draft_id': f'draft_{uuid.uuid4().hex[:16]}',
            'revision': 1,
            'mailbox': cred['email'],
            'provider': cred['provider'],
            'to': recipients,
            'cc': _split_addresses(cc),
            'subject': str(subject or '').strip(),
            'body': str(body or ''),
            'attachment_paths': paths,
            'in_reply_to': str(in_reply_to or '').strip(),
            'status': 'draft',
            'sent_at': '',
            'last_error': '',
            'created_at': now,
            'updated_at': now,
        }
        _save_draft(draft)
        preview = _emit_draft_card(draft)
        return preview

    def update_draft(
        self,
        draft_id: str,
        to: Any = None,
        subject: Any = None,
        body: Any = None,
        cc: Any = None,
        attachment_paths: Any = None,
        in_reply_to: Any = None,
        mailbox: str = '',
    ) -> dict[str, Any]:
        """Update an existing unsent draft in place and bump its revision.

        Confirm send is bound to draft_id plus this revision. An older preview card
        cannot authorize a later revision.

        Args:
            draft_id: Draft id returned by compose_draft.
            to: Replace recipients when provided.
            subject: Replace subject when provided.
            body: Replace body when provided.
            cc: Replace CC addresses when provided.
            attachment_paths: Replace attachments when provided. Pass [] to clear.
                Paths must stay inside the current conversation workspace.
            in_reply_to: Replace reply Message-ID when provided.
            mailbox: Optional sending account (email or provider).
        """
        if not str(draft_id or '').strip():
            raise ToolExecutionError('draft_id is required')
        draft = _load_draft(draft_id)
        if str(draft.get('status') or '') == 'sent':
            _fail('Cannot update a draft that was already sent.')
        if str(mailbox or '').strip():
            cred = _pick_account(mailbox)
            draft['mailbox'] = cred['email']
            draft['provider'] = cred['provider']
        if to is not None:
            recipients = _split_addresses(to)
            if not recipients:
                raise ToolExecutionError('at least one recipient is required')
            draft['to'] = recipients
        if cc is not None:
            draft['cc'] = _split_addresses(cc)
        if subject is not None:
            draft['subject'] = str(subject).strip()
        if body is not None:
            draft['body'] = str(body)
        if attachment_paths is not None:
            draft['attachment_paths'] = _resolve_attachment_paths(attachment_paths)
        if in_reply_to is not None:
            draft['in_reply_to'] = str(in_reply_to or '').strip()
        draft['revision'] = _draft_revision(draft) + 1
        draft['status'] = 'draft'
        draft['last_error'] = ''
        draft['requires_reauth'] = False
        draft['updated_at'] = _iso(datetime.now(timezone.utc))
        _save_draft(draft)
        return _emit_draft_card(draft)

    def send_draft(self, draft_id: str, confirm: bool = False) -> dict[str, Any]:
        """Send a previously composed draft only after the user confirms the preview card.

        Args:
            draft_id: Draft id returned by compose_draft.
            confirm: Ignored. Send is authorized only by mail_draft_confirm_id and
                mail_draft_confirm_revision from the draft card for this revision.
        """
        draft = _load_draft(draft_id)
        if str(draft.get('status') or '') == 'sent':
            _fail('This draft was already sent.')
        cred = _pick_account(str(draft.get('mailbox') or draft.get('provider') or ''))
        confirm_id = str(_agentic_config().get('mail_draft_confirm_id') or '').strip()
        confirmed = confirm_id == str(draft_id).strip()
        if not confirmed:
            _fail(
                'Send blocked until the user confirms the preview card in this turn. '
                'Do not call ask_user for send authorization. Wait for mail_draft_confirm_id.'
            )
        expected_revision = _draft_revision(draft)
        if _confirm_revision() != expected_revision:
            _fail(
                f'This preview is stale. The draft is now revision {expected_revision}. '
                'Confirm the latest preview card; do not send from an older card.'
            )
        message = _build_message(draft, cred['email'])
        try:
            result = _backend(cred).send(message)
        except ToolExecutionError as orig:
            unknown = bool(getattr(orig, 'delivery_unknown', False))
            draft['status'] = 'delivery_unknown' if unknown else 'failed'
            draft['last_error'] = str(orig)
            draft['requires_reauth'] = 'Re-authorize' in str(orig)
            _save_draft(draft)
            _emit_draft_card(draft)
            raise
        sent_at = result.get('sent_at') or _iso(datetime.now(timezone.utc))
        draft['status'] = 'sent'
        draft['sent_at'] = sent_at
        draft['last_error'] = ''
        draft['provider_message_id'] = result.get('id') or ''
        _save_draft(draft)
        _emit_draft_card(draft)
        return {
            'status': 'sent',
            'draft_id': draft['draft_id'],
            'revision': _draft_revision(draft),
            'sent_at': sent_at,
            'message_id': result.get('id') or '',
            'mailbox': cred['email'],
        }
