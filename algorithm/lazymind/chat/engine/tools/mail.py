"""Connected mailbox tools for NetEase, Tencent, and Gmail accounts."""

from __future__ import annotations

import base64
import email
import hashlib
import imaplib
import json
import mimetypes
import os
import re
import smtplib
import socket
import ssl
import time
import uuid
from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
from email.header import decode_header, make_header
from email.message import EmailMessage
from email.utils import formatdate, getaddresses, parsedate_to_datetime
from typing import Any, Iterator, NoReturn
from zoneinfo import ZoneInfo

import lazyllm
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.agent.base import _write_agent_data
from lazyllm.tools.tool_config_inject import register_tool_auth

from lazymind.chat.config import CHAT_ATTACHMENT_EXTENSIONS
from lazymind.chat.engine.tools.local_file.resolver import (
    _materialize_document_text,
    resolve_attachment_path,
)
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
_MAX_CARD_ATTACHMENT_BYTES = 15 * 1024 * 1024
_MAX_CARD_ATTACHMENT_COUNT = 5
_MAX_CARD_ATTACHMENT_TOTAL_BYTES = 20 * 1024 * 1024
_IMAP_TIMEOUT_SECONDS = 20
_TRANSFER_URL_RE = re.compile(
    r'https?://[^\s"\'<>]+(?:'
    r'(?:mail\.)?qq\.com/cgi-bin/ftn'
    r'|ftn\.qq\.com'
    r'|weiyun\.com'
    r')[^\s"\'<>]*',
    re.I,
)
_TRANSFER_HINT_RE = re.compile(r'文件中转站|超大附件|通过中转站|weiyun|ftnExs', re.I)
_TRANSFER_NOTE = (
    'This file is in the mailbox file-transfer station (e.g. QQ 文件中转站) '
    'and cannot be downloaded over IMAP. Open the mailbox web UI to download it.'
)


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


def _enabled_accounts() -> list[dict[str, str]]:
    valid: list[dict[str, str]] = []
    for cred in _accounts():
        status = (cred.get('status') or 'ACTIVE').strip().upper()
        if status in {'REVOKED', 'DISCONNECTED', 'EXPIRED', 'ERROR'}:
            continue
        valid.append(cred)
    return valid


def _require_accounts() -> list[dict[str, str]]:
    accounts = _accounts()
    if not accounts:
        _fail(
            'No mailbox is enabled for chat. Connect a supported mailbox in '
            '资源库 → 云文档 → 邮箱连接 and turn the switch on.'
        )
    valid = _enabled_accounts()
    if valid:
        return valid
    expired = any(
        (cred.get('status') or 'ACTIVE').strip().upper() in {'EXPIRED', 'ERROR'}
        for cred in accounts
    )
    if expired:
        _fail(
            'Mailbox authorization is invalid. Re-authorize the connected account '
            'in 资源库 → 云文档 → 邮箱连接.'
        )
    _fail(
        'No mailbox is enabled for chat. Connect a supported mailbox in '
        '资源库 → 云文档 → 邮箱连接 and turn the switch on.'
    )


def _lookup_accounts(mailbox: str) -> list[dict[str, str]]:
    key = str(mailbox or '').strip().lower()
    if not key:
        return []
    exact: list[dict[str, str]] = []
    by_provider: list[dict[str, str]] = []
    for cred in _enabled_accounts():
        email_addr = (cred.get('email') or '').strip().lower()
        connection_id = (cred.get('connection_id') or '').strip().lower()
        provider = (cred.get('provider') or '').strip().lower()
        if key in {email_addr, connection_id}:
            exact.append(cred)
        elif key == provider:
            by_provider.append(cred)
    return exact or by_provider


def _find_account(mailbox: str) -> dict[str, str] | None:
    matches = _lookup_accounts(mailbox)
    if len(matches) == 1:
        return matches[0]
    return None


def _unavailable_mailbox(mailbox: str) -> dict[str, Any]:
    enabled = [
        {'email': cred.get('email') or '', 'provider': cred.get('provider') or ''}
        for cred in _enabled_accounts()
    ]
    emails = ', '.join(item['email'] or item['provider'] for item in enabled) or '(none)'
    return {
        'status': 'mailbox_not_enabled',
        'requested': str(mailbox or '').strip(),
        'enabled_mailboxes': enabled,
        'items': [],
        'message': (
            f'Requested mailbox {mailbox!r} is not connected or not enabled for chat. '
            f'Enabled mailboxes: {emails}. '
            'Stop now. Do not call MailToolkit_search, read, or send_draft again for this '
            'user request, and do not omit mailbox to search other accounts. '
            'Tell the user to connect and enable this mailbox in 资源库 → 云文档 → 邮箱连接.'
        ),
    }


def _ambiguous_mailbox(mailbox: str, matches: list[dict[str, str]]) -> dict[str, Any]:
    enabled = [
        {'email': cred.get('email') or '', 'provider': cred.get('provider') or ''}
        for cred in matches
    ]
    emails = ', '.join(item['email'] or item['provider'] for item in enabled) or '(none)'
    return {
        'status': 'mailbox_ambiguous',
        'requested': str(mailbox or '').strip(),
        'enabled_mailboxes': enabled,
        'items': [],
        'message': (
            f'Requested mailbox {mailbox!r} matches more than one connected account: {emails}. '
            'Pass the exact email address. Do not pick the first account of this provider.'
        ),
    }


def _pick_account(mailbox: str = '') -> dict[str, str]:
    accounts = _require_accounts()
    key = str(mailbox or '').strip()
    if not key:
        return accounts[0]
    matches = _lookup_accounts(key)
    if len(matches) == 1:
        return matches[0]
    if matches:
        _fail(_ambiguous_mailbox(key, matches)['message'])
    _fail(_unavailable_mailbox(key)['message'])


def _mailbox_choice_rows(accounts: list[dict[str, str]] | None = None) -> list[dict[str, str]]:
    rows: list[dict[str, str]] = []
    for cred in accounts if accounts is not None else _enabled_accounts():
        email_addr = (cred.get('email') or '').strip()
        if not email_addr:
            continue
        rows.append({
            'email': email_addr,
            'provider': (cred.get('provider') or '').strip(),
        })
    return rows


def _confirmed_mailbox(draft_id: str = '') -> str:
    cfg = _agentic_config()
    mailbox = str(cfg.get('mail_mailbox_confirm') or '').strip()
    if not mailbox:
        return ''
    bound = str(cfg.get('mail_mailbox_confirm_draft_id') or '').strip()
    if bound and draft_id and bound != str(draft_id).strip():
        return ''
    return mailbox


def _resolve_sending_account(
    mailbox: str = '',
    *,
    draft_id: str = '',
    draft_mailbox: str = '',
) -> dict[str, str] | None:
    accounts = _require_accounts()
    requested = (
        str(mailbox or '').strip()
        or str(draft_mailbox or '').strip()
        or _confirmed_mailbox(draft_id)
    )
    if requested:
        matches = _lookup_accounts(requested)
        if len(matches) == 1:
            return matches[0]
        if not matches:
            _fail(_unavailable_mailbox(requested)['message'])
        return None
    if len(accounts) == 1:
        return accounts[0]
    return None


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
    key = str(mailbox or '').strip()
    if key:
        matches = _lookup_accounts(key)
        if not matches:
            _fail(_unavailable_mailbox(key)['message'])
        accounts = matches
    else:
        accounts = _require_accounts()
    if len(accounts) != 1:
        emails = ', '.join(
            str(cred.get('email') or '') for cred in accounts if cred.get('email')
        )
        _fail(
            'This email id is ambiguous across multiple mailboxes. '
            f'Pass mailbox as the exact address ({emails}).'
        )
    return _tag_mailbox(runner(accounts[0]), accounts[0])


def _split_addresses(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, (list, tuple)):
        items = [str(item).strip() for item in value]
    else:
        items = _EMAIL_RE.findall(str(value))
    return [item for item in items if item]


def _address_set(value: Any) -> set[str]:
    return {addr.lower() for addr in _split_addresses(value)}


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


def _mail_workspace() -> str:
    cfg = _agentic_config()
    return chat_agent_workspace(
        str(cfg.get('user_id') or '0'),
        str(cfg.get('conversation_id') or 'default'),
    )


def _outgoing_dir() -> str:
    path = os.path.join(_mail_workspace(), 'mail_outgoing')
    os.makedirs(path, exist_ok=True)
    return path


def _cached_incoming_attachment(cred: dict[str, str], message_id: str, wanted: str) -> tuple[str, str]:
    filename = os.path.basename(_decode_header_value(wanted)) or ''
    probe = _incoming_attachment_path(cred, message_id, filename or 'attachment.bin')
    folder = os.path.dirname(probe)
    if not os.path.isdir(folder):
        return '', filename or 'attachment.bin'
    names = os.listdir(folder)
    candidates: list[str] = []
    for name in names:
        if name == wanted or (filename and name == filename):
            candidates.append(name)
        elif wanted and name.startswith(f'{wanted}-'):
            candidates.append(name)
        elif filename and name.endswith(f'-{filename}'):
            candidates.append(name)
    for name in candidates:
        path = os.path.join(folder, name)
        if not os.path.isfile(path):
            continue
        display = filename or name
        if wanted and name.startswith(f'{wanted}-'):
            display = name[len(wanted) + 1:] or display
        elif filename and name.endswith(f'-{filename}'):
            display = filename
        return path, display
    return '', filename or 'attachment.bin'


def _incoming_attachment_path(cred: dict[str, str], message_id: str, filename: str) -> str:
    mailbox_key = hashlib.sha256(
        f"{cred.get('email') or ''}|{cred.get('connection_id') or ''}|{cred.get('provider') or ''}".encode()
    ).hexdigest()[:12]
    message_key = hashlib.sha256(str(message_id or '').encode()).hexdigest()[:12]
    safe_name = os.path.basename(str(filename or '').strip()) or 'attachment.bin'
    folder = os.path.join(_mail_workspace(), 'mail_attachments', mailbox_key, message_key)
    os.makedirs(folder, exist_ok=True)
    return os.path.join(folder, safe_name)


def _attachment_ext(filename: str) -> str:
    return os.path.splitext(os.path.basename(str(filename or '').strip()))[1].lower()


def _is_common_attachment(filename: str) -> bool:
    ext = _attachment_ext(filename)
    return not ext or ext in _COMMON_ATTACHMENT_EXTS


def _parsed_mail_attachment(target: str, filename: str, message_id: str) -> dict[str, Any]:
    ext = _attachment_ext(filename)
    parsed = ''
    parse_status = 'unsupported'
    parse_note = (
        f'Attachment type {ext or "(none)"} was downloaded but is not parsed. '
        'The file is available at path; convert it or open it locally if you need the content.'
    )
    if ext in CHAT_ATTACHMENT_EXTENSIONS:
        parse_note = ''
        try:
            parsed_path = _materialize_document_text(target, _mail_workspace())
            with open(parsed_path, encoding='utf-8') as handle:
                parsed = handle.read()[:20000]
        except Exception as orig:
            raise ToolExecutionError(f'Failed to read the email attachment: {orig}') from orig
        parse_status = 'parsed' if str(parsed).strip() else 'empty'
        if parse_status == 'empty':
            parse_note = 'The attachment was parsed but no text content was extracted.'
    payload = {
        'path': target,
        'filename': filename,
        'size': os.path.getsize(target),
        'text': parsed,
        'parse_status': parse_status,
        'cite': f'email-attachment:{message_id}:{filename}',
    }
    if parse_note:
        payload['parse_note'] = parse_note
    return payload


def _unique_outgoing_path(filename: str) -> str:
    folder = _outgoing_dir()
    base = os.path.basename(str(filename or '').strip()) or 'attachment.bin'
    stem, ext = os.path.splitext(base)
    candidate = os.path.join(folder, base)
    index = 1
    while os.path.exists(candidate):
        candidate = os.path.join(folder, f'{stem}_{index}{ext}')
        index += 1
    return candidate


def _find_workspace_basename(workspace: str, name: str) -> str:
    basename = os.path.basename(name)
    if not basename:
        return ''
    for folder in (
        workspace,
        os.path.join(workspace, 'mail_outgoing'),
        os.path.join(workspace, 'mail_attachments'),
    ):
        candidate = os.path.join(folder, basename)
        if os.path.isfile(candidate):
            return os.path.realpath(candidate)
    return ''


def _resolve_one_attachment(raw_path: str, existing_paths: list[str] | None = None) -> str:
    raw = str(raw_path or '').strip()
    if not raw:
        return ''
    for previous in existing_paths or []:
        if not previous:
            continue
        if previous == raw or os.path.basename(previous) == raw or os.path.basename(previous) == os.path.basename(raw):
            if os.path.isfile(previous):
                return previous
    cfg = _agentic_config()
    user_id = str(cfg.get('user_id') or '0')
    conversation_id = str(cfg.get('conversation_id') or 'default')
    workspace = chat_agent_workspace(user_id, conversation_id)
    workspace_error: ToolExecutionError | None = None
    try:
        _, candidate = _resolve_workspace_path(raw, user_id, conversation_id)
        if os.path.isfile(candidate):
            return candidate
    except ToolExecutionError as orig:
        workspace_error = orig
    found = _find_workspace_basename(workspace, raw)
    if found:
        return found
    chat_path, _error = resolve_attachment_path(os.path.basename(raw), prefer_newest=True)
    if chat_path and os.path.isfile(chat_path):
        if os.path.isabs(raw) and os.path.realpath(raw) != os.path.realpath(chat_path):
            if workspace_error is not None:
                raise workspace_error
            return ''
        return chat_path
    if workspace_error is not None and os.path.isabs(raw):
        raise workspace_error
    return ''


def _resolve_attachment_paths(
    attachment_paths: Any,
    *,
    existing_paths: list[str] | None = None,
) -> list[str]:
    requested = _coerce_path_list(attachment_paths)
    if not requested:
        return []
    resolved: list[str] = []
    missing: list[str] = []
    for raw_path in requested:
        candidate = _resolve_one_attachment(raw_path, existing_paths)
        if candidate:
            resolved.append(candidate)
            continue
        missing.append(raw_path)
    if missing:
        _fail('Attachment file was not found: ' + ', '.join(missing))
    return resolved


def _write_outgoing_attachments(items: Any) -> list[str]:
    if not items:
        return []
    if not isinstance(items, (list, tuple)):
        _fail('attachments must be a list of uploaded files.')
    if len(items) > _MAX_CARD_ATTACHMENT_COUNT:
        _fail(
            f'At most {_MAX_CARD_ATTACHMENT_COUNT} card-uploaded attachments are allowed.'
        )
    written: list[str] = []
    total = 0
    for item in items:
        if not isinstance(item, dict):
            _fail('Each uploaded mail attachment must be an object with filename and content_base64.')
        filename = os.path.basename(str(item.get('filename') or '').strip()) or 'attachment.bin'
        raw_b64 = str(item.get('content_base64') or '').strip()
        if not raw_b64:
            _fail(f'Uploaded mail attachment {filename} is empty.')
        try:
            padded = raw_b64 + '=' * ((4 - len(raw_b64) % 4) % 4)
            data = base64.b64decode(padded, validate=True)
        except Exception as orig:
            raise ToolExecutionError(f'Uploaded mail attachment {filename} is not valid base64.') from orig
        if len(data) > _MAX_CARD_ATTACHMENT_BYTES:
            _fail(
                f'Uploaded mail attachment {filename} exceeds '
                f'{_MAX_CARD_ATTACHMENT_BYTES // (1024 * 1024)}MB.'
            )
        total += len(data)
        if total > _MAX_CARD_ATTACHMENT_TOTAL_BYTES:
            _fail(
                'Card-uploaded attachments exceed '
                f'{_MAX_CARD_ATTACHMENT_TOTAL_BYTES // (1024 * 1024)}MB in total.'
            )
        target = _unique_outgoing_path(filename)
        with open(target, 'wb') as handle:
            handle.write(data)
        written.append(target)
    return written


def _extract_transfer_links(text: str) -> list[dict[str, Any]]:
    source = str(text or '')
    items: list[dict[str, Any]] = []
    seen: set[str] = set()
    for match in _TRANSFER_URL_RE.finditer(source):
        url = match.group(0).rstrip(').,]"\'')
        if not url or url in seen:
            continue
        seen.add(url)
        items.append({
            'kind': 'transfer_station',
            'url': url,
            'note': _TRANSFER_NOTE,
        })
    if not items and _TRANSFER_HINT_RE.search(source):
        items.append({
            'kind': 'transfer_station',
            'url': '',
            'note': _TRANSFER_NOTE,
        })
    return items


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


@contextmanager
def _locked_draft(draft_id: str) -> Iterator[None]:
    lock_path = _draft_path(draft_id) + '.lock'
    fd = os.open(lock_path, os.O_CREAT | os.O_RDWR, 0o600)
    try:
        if os.name == 'nt':
            import msvcrt
            while True:
                try:
                    msvcrt.locking(fd, msvcrt.LK_LOCK, 1)
                    break
                except OSError:
                    time.sleep(0.05)
        else:
            import fcntl
            fcntl.flock(fd, fcntl.LOCK_EX)
        yield
    finally:
        if os.name == 'nt':
            import msvcrt
            try:
                os.lseek(fd, 0, os.SEEK_SET)
                msvcrt.locking(fd, msvcrt.LK_UNLCK, 1)
            except OSError:
                pass
        else:
            import fcntl
            fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)


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


def _quote_imap_string(value: str) -> str:
    escaped = str(value).replace('\\', '\\\\').replace('"', '\\"')
    return f'"{escaped}"'


def _imap_search_args(filters: dict[str, str]) -> list[str]:
    fields: list[tuple[str, str]] = []
    for key, atom in (
        ('sender', 'FROM'),
        ('recipient', 'TO'),
        ('subject', 'SUBJECT'),
        ('keyword', 'TEXT'),
    ):
        value = str(filters.get(key) or '').strip()
        if value:
            fields.append((atom, value))
    since = _imap_date(str(filters.get('after') or ''))
    before = _imap_date(str(filters.get('before') or ''), before=True)
    needs_charset = any(not value.isascii() for _atom, value in fields)
    args: list[str] = []
    if needs_charset:
        args.extend(['CHARSET', 'UTF-8'])
    args.append('ALL')
    for atom, value in fields:
        args.extend([atom, _quote_imap_string(value)])
    if since:
        args.extend(['SINCE', since])
    if before:
        args.extend(['BEFORE', before])
    return args


def _decode_imap_utf7(name: str) -> str:
    text = str(name or '')
    if '&' not in text:
        return text
    parts: list[str] = []
    index = 0
    while index < len(text):
        amp = text.find('&', index)
        if amp < 0:
            parts.append(text[index:])
            break
        parts.append(text[index:amp])
        dash = text.find('-', amp + 1)
        if dash < 0:
            parts.append(text[amp:])
            break
        chunk = text[amp + 1:dash]
        if chunk == '':
            parts.append('&')
        else:
            padded = chunk.replace(',', '/')
            padded += '=' * ((4 - len(padded) % 4) % 4)
            try:
                parts.append(base64.b64decode(padded).decode('utf-16-be'))
            except Exception:
                parts.append(text[amp:dash + 1])
        index = dash + 1
    return ''.join(parts)


def _encode_imap_utf7(text: str) -> str:
    out: list[str] = []
    buf: list[str] = []

    def flush() -> None:
        if not buf:
            return
        raw = ''.join(buf).encode('utf-16-be')
        token = base64.b64encode(raw).decode('ascii').rstrip('=').replace('/', ',')
        out.append(f'&{token}-')
        buf.clear()

    for char in str(text or ''):
        code = ord(char)
        if char == '&':
            flush()
            out.append('&-')
        elif 0x20 <= code <= 0x7E:
            flush()
            out.append(char)
        else:
            buf.append(char)
    flush()
    return ''.join(out)


def _imap_uid(message_id: str) -> str:
    uid = str(message_id or '').strip()
    if not uid.isdigit():
        _fail('The requested email was not found.')
    return uid


def _split_mail_ref(message_id: str) -> tuple[str, str]:
    text = str(message_id or '').strip()
    if '::' in text:
        folder, uid = text.rsplit('::', 1)
        return (folder.strip() or 'INBOX'), _imap_uid(uid)
    return 'INBOX', _imap_uid(text)


def _mail_ref(folder: str, uid: str) -> str:
    return f'{folder}::{uid}'


def _user_timezone() -> ZoneInfo | None:
    env = _agentic_config().get('environment_context')
    if not isinstance(env, dict):
        return None
    time_info = env.get('time')
    if not isinstance(time_info, dict):
        return None
    name = str(time_info.get('timezone') or '').strip()
    if not name:
        return None
    try:
        return ZoneInfo(name)
    except Exception:
        return None


def _display_mail_date(raw: Any) -> str:
    text = _decode_header_value(raw)
    if not text:
        return ''
    try:
        dt = parsedate_to_datetime(text)
    except (TypeError, ValueError, IndexError):
        return text
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    tzinfo = _user_timezone()
    try:
        dt = dt.astimezone(tzinfo) if tzinfo is not None else dt.astimezone()
    except Exception:
        dt = dt.astimezone(timezone.utc)
    return dt.isoformat()


def _quote_mailbox(name: str) -> str:
    text = (name or '').strip() or 'INBOX'
    if text.upper() == 'INBOX':
        return 'INBOX'
    escaped = text.replace('\\', '\\\\').replace('"', '\\"')
    return f'"{escaped}"'


def _select_mailbox(client, folder: str, *, readonly: bool = True) -> bool:
    name = (folder or 'INBOX').strip() or 'INBOX'
    candidates = [name]
    quoted = _quote_mailbox(name)
    if quoted not in candidates:
        candidates.append(quoted)
    for candidate in candidates:
        try:
            status, _ = client.select(candidate, readonly=readonly)
        except Exception:
            continue
        if status == 'OK':
            return True
    return False


def _parse_imap_list_line(line: Any) -> tuple[str, set[str]] | None:
    raw = line[-1] if isinstance(line, tuple) else line
    if isinstance(raw, bytes):
        text = raw.decode('utf-8', 'replace')
    else:
        text = str(raw or '')
    match = re.match(
        r'\((?P<flags>[^)]*)\)\s+(?P<delim>NIL|"(?:\\.|[^"])*")\s+(?P<name>.+)$',
        text.strip(),
    )
    if not match:
        return None
    flags = {
        flag.strip('\\').lower()
        for flag in match.group('flags').split()
        if flag.strip()
    }
    name = match.group('name').strip()
    if len(name) >= 2 and name[0] == '"' and name[-1] == '"':
        name = name[1:-1].replace('\\"', '"').replace('\\\\', '\\')
    if not name:
        return None
    return name, flags


def _mailbox_role(name: str, flags: set[str]) -> str | None:
    if 'noselect' in flags:
        return None
    display = _decode_imap_utf7(name)
    lowered = display.lower().replace('[gmail]/', '').strip()
    if 'all' in flags or 'all mail' in lowered or lowered.endswith('/all'):
        return None
    if 'inbox' in flags or display.upper() == 'INBOX' or name.upper() == 'INBOX':
        return 'inbox'
    if 'sent' in flags:
        return 'sent'
    if 'drafts' in flags:
        return 'drafts'
    if 'trash' in flags:
        return 'trash'
    if 'junk' in flags:
        return 'junk'
    hints = (
        ('inbox', ('inbox',)),
        ('sent', ('sent', '已发送', '已傳送')),
        ('drafts', ('draft', '草稿')),
        ('trash', ('trash', 'deleted', '已删除', '已刪除', 'bin')),
        ('junk', ('junk', 'spam', '垃圾')),
    )
    for role, needles in hints:
        if any(needle in lowered for needle in needles):
            return role
    return None


def _list_mailboxes(client) -> list[tuple[str, set[str]]]:
    try:
        status, data = client.list()
    except Exception:
        return [('INBOX', {'inbox'})]
    if status != 'OK':
        return [('INBOX', {'inbox'})]
    mailboxes: list[tuple[str, set[str]]] = []
    for line in data or []:
        parsed = _parse_imap_list_line(line)
        if parsed is not None:
            mailboxes.append(parsed)
    if not mailboxes:
        mailboxes.append(('INBOX', {'inbox'}))
    return mailboxes


def _resolve_search_folders(client, folder_filter: str) -> list[str]:
    listed = _list_mailboxes(client)
    by_role: dict[str, str] = {}
    for name, flags in listed:
        role = _mailbox_role(name, flags)
        if role and role not in by_role:
            by_role[role] = name
    if 'inbox' not in by_role:
        by_role['inbox'] = 'INBOX'
    wanted = str(folder_filter or 'all').strip().lower()
    if wanted in {'', 'all'}:
        return [by_role[role] for role in ('inbox', 'sent', 'drafts', 'trash', 'junk') if role in by_role]
    if wanted in by_role:
        return [by_role[wanted]]
    for name, _flags in listed:
        display = _decode_imap_utf7(name)
        if name.lower() == wanted or name == folder_filter or display.lower() == wanted:
            return [name]
    return [by_role['inbox']]


def _draft_patch() -> dict[str, Any]:
    raw = _agentic_config().get('mail_draft_patch')
    return dict(raw) if isinstance(raw, dict) else {}


def _apply_confirm_patch(draft: dict[str, Any]) -> dict[str, Any]:
    patch = _draft_patch()
    if not patch:
        return draft
    if 'to' in patch:
        new_to = _split_addresses(patch.get('to'))
        if _address_set(new_to) != _address_set(draft.get('to')):
            draft['pending_recipients'] = []
        draft['to'] = new_to
    if 'cc' in patch:
        new_cc = _split_addresses(patch.get('cc'))
        if _address_set(new_cc) != _address_set(draft.get('cc')):
            draft['pending_recipients'] = []
        draft['cc'] = new_cc
    if 'subject' in patch:
        draft['subject'] = str(patch.get('subject') or '').strip()
    if 'body' in patch:
        draft['body'] = str(patch.get('body') or '')
    if 'attachment_paths' in patch or 'attachments' in patch:
        existing = [str(path) for path in (draft.get('attachment_paths') or []) if str(path).strip()]
        paths = _resolve_attachment_paths(patch.get('attachment_paths'), existing_paths=existing)
        paths.extend(_write_outgoing_attachments(patch.get('attachments')))
        draft['attachment_paths'] = paths
    return draft


def _imap_fetch_text(fetched: Any) -> str:
    parts: list[str] = []
    for item in fetched or []:
        if isinstance(item, tuple):
            for value in item:
                if isinstance(value, (bytes, bytearray)):
                    parts.append(value.decode('utf-8', 'replace'))
                elif value is not None:
                    parts.append(str(value))
        elif isinstance(item, (bytes, bytearray)):
            parts.append(item.decode('utf-8', 'replace'))
        elif item is not None:
            parts.append(str(item))
    return ' '.join(parts)


def _parse_imap_sexp(text: str) -> Any:
    token_re = re.compile(r'\s+|("(?:\\.|[^"\\])*")|(\()|(\))|(NIL)|([^\s()]+)', re.I)
    stack: list[list[Any]] = [[]]
    for match in token_re.finditer(str(text or '')):
        quoted, openp, closep, nilv, atom = match.groups()
        if quoted is not None:
            stack[-1].append(quoted[1:-1].replace('\\"', '"'))
            continue
        if openp:
            stack.append([])
            continue
        if closep:
            if len(stack) == 1:
                continue
            node = stack.pop()
            stack[-1].append(node)
            continue
        if nilv:
            stack[-1].append(None)
            continue
        if atom is None:
            continue
        if re.fullmatch(r'-?\d+', atom):
            stack[-1].append(int(atom))
        else:
            stack[-1].append(atom)
    data = stack[0]
    return data[0] if len(data) == 1 else data


def _sexp_string(value: Any) -> str:
    if value is None:
        return ''
    return str(value).strip()


def _attachment_from_body_part(node: list[Any], section: str) -> dict[str, Any] | None:
    if not node or not isinstance(node[0], str):
        return None
    params = node[2] if len(node) > 2 and isinstance(node[2], list) else []
    names = {str(params[idx]).lower(): _sexp_string(params[idx + 1])
             for idx in range(0, len(params) - 1, 2) if isinstance(params[idx], str)}
    filename = names.get('name') or names.get('filename') or ''
    size = 0
    if len(node) > 6 and isinstance(node[6], int):
        size = node[6]
    disposition = node[8] if len(node) > 8 else None
    disp_name = ''
    disp_kind = ''
    if isinstance(disposition, list) and disposition:
        disp_kind = _sexp_string(disposition[0]).upper()
        disp_params = disposition[1] if len(disposition) > 1 and isinstance(disposition[1], list) else []
        disp_map = {str(disp_params[idx]).lower(): _sexp_string(disp_params[idx + 1])
                    for idx in range(0, len(disp_params) - 1, 2) if isinstance(disp_params[idx], str)}
        disp_name = disp_map.get('filename') or ''
    filename = filename or disp_name
    if not filename and disp_kind != 'ATTACHMENT':
        return None
    filename = _decode_header_value(filename) or f'part-{section}'
    subtype = _sexp_string(node[1] if len(node) > 1 else '').lower()
    maintype = _sexp_string(node[0]).lower()
    return {
        'attachment_id': section,
        'filename': filename,
        'mime_type': f'{maintype}/{subtype}' if subtype else maintype,
        'size': size,
    }


def _walk_bodystructure(node: Any, prefix: str = '') -> list[dict[str, Any]]:
    if not isinstance(node, list) or not node:
        return []
    children = [item for item in node if isinstance(item, list)]
    if children and isinstance(node[0], list):
        found: list[dict[str, Any]] = []
        for index, child in enumerate(children, start=1):
            section = str(index) if not prefix else f'{prefix}.{index}'
            found.extend(_walk_bodystructure(child, section))
        return found
    part = _attachment_from_body_part(node, prefix or '1')
    return [part] if part else []


def _attachments_from_bodystructure(raw: str) -> list[dict[str, Any]]:
    match = re.search(r'BODYSTRUCTURE\s+(\(.*\))', str(raw or ''), re.I | re.S)
    blob = match.group(1) if match else str(raw or '').strip()
    if not blob:
        return []
    try:
        parsed = _parse_imap_sexp(blob)
    except Exception:
        return []
    return _walk_bodystructure(parsed)


def _mime_section_parts(
    msg: email.message.Message,
    prefix: str = '',
) -> list[tuple[str, email.message.Message]]:
    """Walk MIME parts using IMAP BODYSTRUCTURE section numbers."""
    if msg.is_multipart():
        found: list[tuple[str, email.message.Message]] = []
        children = msg.get_payload()
        if not isinstance(children, list):
            return found
        for index, child in enumerate(children, start=1):
            if not isinstance(child, email.message.Message):
                continue
            section = str(index) if not prefix else f'{prefix}.{index}'
            found.extend(_mime_section_parts(child, section))
        return found
    return [(prefix or '1', msg)]


def _named_mime_parts(msg: email.message.Message) -> list[tuple[str, str, email.message.Message, bytes]]:
    parts: list[tuple[str, str, email.message.Message, bytes]] = []
    for section, part in _mime_section_parts(msg):
        filename = part.get_filename() or ''
        if not filename:
            continue
        payload = part.get_payload(decode=True)
        if payload is None:
            continue
        parts.append((section, _decode_header_value(filename), part, payload))
    return parts


def _imap_payload(fetched: Any) -> bytes | None:
    if not fetched:
        return None
    chunks: list[bytes] = []
    for item in fetched:
        if isinstance(item, tuple) and len(item) >= 2 and item[1] is not None:
            raw = item[1]
            chunks.append(raw if isinstance(raw, (bytes, bytearray)) else bytes(raw))
    if not chunks:
        return None
    if len(chunks) == 1:
        return chunks[0]
    return chunks[0].rstrip(b'\r\n') + b'\r\n\r\n' + b''.join(chunks[1:])


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
        try:
            client = imaplib.IMAP4_SSL(
                self.endpoint['imap_host'],
                self.endpoint['imap_port'],
                timeout=_IMAP_TIMEOUT_SECONDS,
            )
        except OSError as orig:
            raise ToolExecutionError(f'Failed to connect to the mailbox: {orig}') from orig
        sock = getattr(client, 'sock', None)
        if sock is not None:
            sock.settimeout(_IMAP_TIMEOUT_SECONDS)
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
            criteria = _imap_search_args(filters)
            if any(isinstance(item, str) and not item.isascii() for item in criteria):
                client._encoding = 'utf-8'
            folders = _resolve_search_folders(client, filters.get('folder', ''))
            items = []
            for folder in folders:
                if not _select_mailbox(client, folder, readonly=True):
                    continue
                status, data = client.uid('SEARCH', *criteria)
                if status != 'OK':
                    continue
                ids = (data[0] or b'').split()[-20:]
                for uid in reversed(ids):
                    status, fetched = client.uid(
                        'FETCH',
                        uid,
                        '(BODY.PEEK[HEADER.FIELDS (FROM TO SUBJECT DATE MESSAGE-ID)])',
                    )
                    raw = _imap_payload(fetched)
                    if status != 'OK' or raw is None:
                        continue
                    msg = email.message_from_bytes(raw)
                    token = uid.decode('ascii')
                    items.append({
                        'id': _mail_ref(folder, token),
                        'folder': folder,
                        'thread_id': _decode_header_value(msg.get('Message-ID') or token),
                        'from': _decode_header_value(msg.get('From')),
                        'to': _decode_header_value(msg.get('To')),
                        'subject': _decode_header_value(msg.get('Subject')),
                        'date': _display_mail_date(msg.get('Date')),
                        'snippet': '',
                    })
            items.sort(key=lambda row: str(row.get('date') or ''), reverse=True)
            return {
                'provider': self.provider,
                'mailbox': self.email,
                'folders': folders,
                'items': items[:20],
            }
        finally:
            try:
                client.logout()
            except Exception:
                pass

    def _fetch_raw(self, message_id: str, spec: str, client=None, *, required: bool = True) -> bytes:
        folder, uid = _split_mail_ref(message_id)
        own = client is None
        if own:
            client = self._connect()
        try:
            if not _select_mailbox(client, folder, readonly=True):
                if required:
                    _fail('The requested email was not found.')
                return b''
            status, fetched = client.uid('FETCH', uid, spec)
            raw = _imap_payload(fetched)
            if status != 'OK' or raw is None:
                if required:
                    _fail('The requested email was not found.')
                return b''
            return raw
        finally:
            if own:
                try:
                    client.logout()
                except Exception:
                    pass

    def _fetch_message(self, message_id: str, client=None) -> email.message.Message:
        return email.message_from_bytes(self._fetch_raw(message_id, '(RFC822)', client))

    def _list_structure_attachments(self, message_id: str, client) -> list[dict[str, Any]]:
        folder, uid = _split_mail_ref(message_id)
        if not _select_mailbox(client, folder, readonly=True):
            return []
        status, fetched = client.uid('FETCH', uid, '(BODYSTRUCTURE)')
        if status != 'OK':
            return []
        return _attachments_from_bodystructure(_imap_fetch_text(fetched))

    def _read_message(self, message_id: str, client=None) -> dict[str, Any]:
        own = client is None
        if own:
            client = self._connect()
        try:
            attachments = self._list_structure_attachments(message_id, client)
            raw = self._fetch_raw(
                message_id,
                '(BODY.PEEK[HEADER] BODY.PEEK[TEXT]<0.20000>)',
                client,
                required=False,
            )
            if not raw:
                raw = self._fetch_raw(message_id, '(RFC822)', client)
            parsed = self._read_parsed_message(message_id, raw, client=client)
            if attachments:
                parsed['attachments'] = attachments
            return parsed
        finally:
            if own:
                try:
                    client.logout()
                except Exception:
                    pass

    def _read_parsed_message(
        self,
        message_id: str,
        raw: bytes,
        *,
        retry_full: bool = True,
        client=None,
    ) -> dict[str, Any]:
        msg = email.message_from_bytes(raw)
        attachments = []
        body_parts = []
        html_parts = []
        seen_parts: set[int] = set()
        for section, filename, part, _payload in _named_mime_parts(msg):
            seen_parts.add(id(part))
            disposition = str(part.get('Content-Disposition') or '')
            match = re.search(r'size\s*=\s*(\d+)', disposition, re.I)
            attachments.append({
                'attachment_id': section,
                'filename': filename,
                'mime_type': part.get_content_type(),
                'size': int(match.group(1)) if match else 0,
            })
        for part in msg.walk():
            if id(part) in seen_parts:
                continue
            filename = part.get_filename()
            if filename:
                continue
            payload = part.get_payload(decode=True) or b''
            charset = part.get_content_charset() or 'utf-8'
            text = payload.decode(charset, errors='replace')
            if part.get_content_type() == 'text/plain':
                body_parts.append(text)
            elif part.get_content_type() == 'text/html':
                html_parts.append(text)
                if not body_parts:
                    body_parts.append(re.sub(r'<[^>]+>', ' ', text))
        transfer_links = []
        for html in html_parts:
            transfer_links.extend(_extract_transfer_links(html))
        if not transfer_links:
            transfer_links = _extract_transfer_links('\n'.join(body_parts))
        hinted = bool(_TRANSFER_HINT_RE.search('\n'.join(html_parts + body_parts)))
        if retry_full and hinted and not any(item.get('url') for item in transfer_links):
            full = self._fetch_raw(message_id, '(RFC822)', client, required=False)
            if full and full != raw:
                return self._read_parsed_message(message_id, full, retry_full=False)
        return self._message_payload(message_id, msg, body_parts, attachments, transfer_links)

    def _message_payload(
        self,
        message_id: str,
        msg: email.message.Message,
        body_parts: list[str],
        attachments: list[dict[str, Any]],
        transfer_links: list[dict[str, Any]],
    ) -> dict[str, Any]:
        seen: set[str] = set()
        unique_links: list[dict[str, Any]] = []
        for item in transfer_links:
            key = str(item.get('url') or item.get('note') or '')
            if key in seen:
                continue
            seen.add(key)
            unique_links.append(item)
        return {
            'id': message_id,
            'thread_id': _decode_header_value(msg.get('Message-ID') or message_id),
            'from': _decode_header_value(msg.get('From')),
            'to': _decode_header_value(msg.get('To')),
            'cc': _decode_header_value(msg.get('Cc')),
            'subject': _decode_header_value(msg.get('Subject')),
            'date': _display_mail_date(msg.get('Date')),
            'folder': _split_mail_ref(message_id)[0],
            'body': '\n'.join(body_parts)[:20000],
            'attachments': attachments,
            'transfer_links': unique_links,
            'cite': f'email:{message_id}',
        }

    def read(self, message_id: str) -> dict[str, Any]:
        return self._read_message(message_id)

    def _search_thread_uids(self, client, folder: str, needle: str) -> list[str]:
        tokens = {needle, needle.strip('<>')}
        found: list[str] = []
        seen: set[str] = set()
        for header in ('Message-ID', 'In-Reply-To', 'References'):
            for token in tokens:
                if not token:
                    continue
                status, data = client.uid('SEARCH', 'HEADER', header, token)
                if status != 'OK':
                    continue
                for uid in (data[0] or b'').split():
                    ref = _mail_ref(folder, uid.decode('ascii'))
                    if ref not in seen:
                        seen.add(ref)
                        found.append(ref)
        return found

    def read_thread(self, thread_id: str) -> dict[str, Any]:
        needle = (thread_id or '').strip()
        client = self._connect()
        try:
            folders = _resolve_search_folders(client, 'all')
            matched: list[str] = []
            for folder in folders:
                if not _select_mailbox(client, folder, readonly=True):
                    continue
                matched.extend(self._search_thread_uids(client, folder, needle))
            messages = [
                self._read_message(mid, client) for mid in reversed(matched[-20:])
            ]
            if not messages and needle.isdigit():
                messages = [self._read_message(_mail_ref('INBOX', needle), client)]
            elif not messages and '::' in needle:
                messages = [self._read_message(needle, client)]
            return {'thread_id': thread_id, 'messages': messages}
        finally:
            try:
                client.logout()
            except Exception:
                pass

    def read_attachments(self, message_id: str) -> dict[str, Any]:
        """Fetch the message once and return every named MIME attachment payload."""
        msg = self._fetch_message(message_id)
        parts: list[dict[str, Any]] = []
        files: dict[str, bytes] = {}
        html_parts: list[str] = []
        for section, filename, _part, payload in _named_mime_parts(msg):
            parts.append({'attachment_id': section, 'filename': filename, 'data': payload})
            files.setdefault(section, payload)
            files.setdefault(filename, payload)
        for part in msg.walk():
            filename = part.get_filename() or ''
            if filename:
                continue
            if part.get_content_type() == 'text/html':
                payload = part.get_payload(decode=True) or b''
                charset = part.get_content_charset() or 'utf-8'
                html_parts.append(payload.decode(charset, errors='replace'))
        html = '\n'.join(html_parts)
        transfer = bool(_extract_transfer_links(html) or _TRANSFER_HINT_RE.search(html))
        return {'files': files, 'parts': parts, 'transfer': transfer}

    def read_attachment(self, message_id: str, attachment_id: str) -> bytes:
        wanted = (attachment_id or '').strip()
        if wanted and _TRANSFER_URL_RE.search(wanted):
            _fail(_TRANSFER_NOTE)
        result = self.read_attachments(message_id)
        files = result.get('files') or {}
        raw = files.get(wanted) or files.get(_decode_header_value(wanted))
        if raw is not None:
            return raw
        if result.get('transfer'):
            _fail(_TRANSFER_NOTE)
        _fail('Failed to read the email attachment.')

    def send(self, message: EmailMessage) -> dict[str, Any]:
        data_submitted = False
        recipients = [
            addr for _name, addr in getaddresses(
                message.get_all('To', []) + message.get_all('Cc', []) + message.get_all('Bcc', [])
            )
            if addr
        ]
        if not recipients:
            _fail('Failed to send the email: no valid recipients.')
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
                refused = smtp.sendmail(self.email, recipients, message.as_bytes())
        except smtplib.SMTPAuthenticationError as orig:
            raise ToolExecutionError(
                'Mailbox authorization expired. Re-authorize the mailbox in 资源库 → 云文档 → 邮箱连接.'
            ) from orig
        except smtplib.SMTPRecipientsRefused as orig:
            detail = ', '.join(
                f'{addr} ({code} {err})'
                for addr, (code, err) in (orig.recipients or {}).items()
            ) or str(orig)
            _fail(f'Failed to send the email: all recipients were rejected ({detail}).')
        except ToolExecutionError:
            raise
        except (smtplib.SMTPException, OSError) as orig:
            _raise_send_error(orig, data_submitted=data_submitted)
        if refused:
            detail = ', '.join(
                f'{addr} ({code} {err})' for addr, (code, err) in refused.items()
            )
            accepted = [addr for addr in recipients if addr not in refused]
            if accepted:
                tzinfo = _user_timezone()
                now = datetime.now(tzinfo) if tzinfo is not None else datetime.now().astimezone()
                return {
                    'id': message.get('Message-ID') or '',
                    'sent_at': now.isoformat(),
                    'partial_sent': True,
                    'accepted': accepted,
                    'refused': [
                        {'address': addr, 'code': code, 'error': str(err)}
                        for addr, (code, err) in refused.items()
                    ],
                }
            _fail(f'Failed to send the email: recipients were rejected ({detail}).')
        tzinfo = _user_timezone()
        now = datetime.now(tzinfo) if tzinfo is not None else datetime.now().astimezone()
        return {'id': message.get('Message-ID') or '', 'sent_at': now.isoformat()}


def _backend(cred: dict[str, str]):
    provider = (cred.get('provider') or '').strip().lower()
    if provider in _IMAP_ENDPOINTS:
        return _IMAPBackend(cred)
    _fail(
        'No mailbox is enabled for chat. Connect a supported mailbox in 资源库 → 云文档 → 邮箱连接.'
    )


def _pending_to_cc(draft: dict[str, Any]) -> tuple[list[str], list[str]]:
    pending = [
        str(addr).strip() for addr in (draft.get('pending_recipients') or []) if str(addr).strip()
    ]
    to_addrs = [str(addr).strip() for addr in (draft.get('to') or []) if str(addr).strip()]
    cc_addrs = [str(addr).strip() for addr in (draft.get('cc') or []) if str(addr).strip()]
    if pending:
        pending_set = {addr.lower() for addr in pending}
        to_out = [addr for addr in to_addrs if addr.lower() in pending_set]
        cc_out = [addr for addr in cc_addrs if addr.lower() in pending_set]
        used = {addr.lower() for addr in to_out + cc_out}
        to_out.extend(addr for addr in pending if addr.lower() not in used)
    else:
        to_out, cc_out = to_addrs, cc_addrs
    accepted = _address_set(draft.get('accepted_recipients'))
    if accepted:
        to_out = [addr for addr in to_out if addr.lower() not in accepted]
        cc_out = [addr for addr in cc_out if addr.lower() not in accepted]
    return to_out, cc_out


def _build_message(draft: dict[str, Any], mailbox: str) -> EmailMessage:
    message = EmailMessage()
    to_addrs, cc_addrs = _pending_to_cc(draft)
    message['From'] = mailbox
    message['To'] = ', '.join(to_addrs)
    if cc_addrs:
        message['Cc'] = ', '.join(cc_addrs)
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
        'error_code': 'partial_sent' if status == 'partial_sent' else '',
        'accepted_recipients': list(draft.get('accepted_recipients') or []),
        'refused_recipients': list(draft.get('pending_recipients') or []),
        'mailboxes': list(draft.get('mailboxes') or []),
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


def _emit_mailbox_card(draft: dict[str, Any]) -> dict[str, Any]:
    choices = list(draft.get('mailboxes') or _mailbox_choice_rows())
    draft['mailboxes'] = choices
    preview = _preview(draft)
    emails = [row.get('email') or '' for row in choices if row.get('email')]
    _write_agent_data(
        'ask_pending',
        ask_id=str(uuid.uuid4()),
        title='选择发件邮箱',
        description='未指定发件邮箱。请从已连接且已开启对话开关的邮箱中选择一个，确认后再预览发送。',
        questions=[{
            'text': '请选择用来回复或发送的邮箱',
            'type': 'single',
            'choices': emails,
            'allow_other': False,
        }],
        mail_draft=preview,
        mail_mailbox_choice=preview,
    )
    return preview


class MailToolkit:
    """Search, read, cite, and send mail through enabled NetEase, Tencent, and Gmail accounts.

    Personal and enterprise mailboxes can be enabled together. Gmail connects via
    IMAP/SMTP with a Google app password, not OAuth; that path is more user-friendly
    because it does not require a Google Cloud OAuth client or consent screen.
    Search results are tagged with mailbox/provider. When more than one mailbox is
    enabled, pass mailbox (email address or provider name) to read, attach, compose,
    or send. If the user did not name a sending mailbox, compose_draft shows a
    mailbox picker card listing only connected chat-enabled accounts; after the
    user confirms, call update_draft with that mailbox so the send preview appears.
    Do not invent a mailbox and do not call ask_user for this choice.
    Sending always requires the user to confirm the draft preview card.
    Change an existing unsent draft with update_draft instead of composing a new one.
    """

    __public_apis__ = [
        'search', 'read', 'read_thread', 'read_attachment',
        'compose_draft', 'update_draft', 'send_draft',
    ]
    __tool_auto_activate__ = [
        r'邮件|邮箱|inbox|gmail|163|126|yeah\.net|qq邮箱|企业邮|(?<!\w)email(?!\w)|(?<!\w)mail(?!\w)',
    ]

    def __init__(self) -> None:
        register_tool_auth('mail', 'dynamic_tool_auth')

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
        folder: str = '',
    ) -> dict[str, Any]:
        """List matching emails (headers only). Call read for the body of one id.

        Args:
            keyword: Free-text query matched against message bodies when supported.
            sender: Filter by From address.
            recipient: Filter by To address.
            subject: Filter by subject.
            after: Inclusive start date, YYYY-MM-DD.
            before: Inclusive end date, YYYY-MM-DD.
            mailbox: Optional email, connection id, or provider (netease163/qqmail/gmailimap).
                Email/connection id match exactly. A provider name matches every enabled
                account of that type. Empty searches all enabled mailboxes.
                If the user named a mailbox, always pass it. A mailbox_not_enabled result is final:
                do not retry and do not search other accounts.
            folder: Optional mailbox folder: inbox, sent, drafts, trash, junk, or all.
                Default all searches Inbox plus Sent, Drafts, Trash, and Junk when present.
                Result ids are folder::UID; pass that exact id to read.
        """
        requested = str(mailbox or '').strip()
        if requested:
            accounts = _lookup_accounts(requested)
            if not accounts:
                return _unavailable_mailbox(requested)
        else:
            accounts = _require_accounts()
        items: list[dict[str, Any]] = []
        errors: list[dict[str, Any]] = []
        kwargs = {
            'keyword': str(keyword or '').strip(),
            'sender': str(sender or '').strip(),
            'recipient': str(recipient or '').strip(),
            'subject': str(subject or '').strip(),
            'after': str(after or '').strip(),
            'before': str(before or '').strip(),
            'folder': str(folder or '').strip(),
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
        items.sort(key=lambda row: str(row.get('date') or ''), reverse=True)
        payload: dict[str, Any] = {
            'items': items[:20],
            'mailboxes': [cred.get('email') or '' for cred in accounts],
        }
        if errors:
            payload['errors'] = errors
        return payload

    def read(self, message_id: str, mailbox: str = '') -> dict[str, Any]:
        """Read one email body on demand. Attachments are listed only; use read_attachment to download.

        Args:
            message_id: folder::UID returned by search (IMAP UIDs are not unique across folders).
            mailbox: Optional email or provider. Required when the same id could exist in more than one mailbox.
        """
        if not str(message_id or '').strip():
            raise ToolExecutionError('message_id is required')
        requested = str(mailbox or '').strip()
        if requested and not _lookup_accounts(requested):
            return _unavailable_mailbox(requested)
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

        The first read of a message fetches that email once and writes every
        ordinary attachment into the workspace. Later reads reuse those files.
        Parseable types reuse attachment-text-cache. Types that can be saved
        but not parsed return parse_status=unsupported instead of empty text.

        Args:
            message_id: Provider message id.
            attachment_id: Attachment id or filename from read().
            mailbox: Optional email or provider when multiple mailboxes are enabled.
        """
        if not str(message_id or '').strip() or not str(attachment_id or '').strip():
            raise ToolExecutionError('message_id and attachment_id are required')

        def _download(cred: dict[str, str]) -> dict[str, Any]:
            wanted = str(attachment_id).strip()
            if _TRANSFER_URL_RE.search(wanted):
                _fail(_TRANSFER_NOTE)
            mid = str(message_id).strip()
            target, filename = _cached_incoming_attachment(cred, mid, wanted)
            save_name = filename
            if not target:
                result = _backend(cred).read_attachments(mid)
                if not isinstance(result, dict):
                    result = {}
                files = result.get('files') if isinstance(result.get('files'), dict) else {}
                parts = [part for part in (result.get('parts') or []) if isinstance(part, dict)]
                matched = next(
                    (
                        part for part in parts
                        if str(part.get('attachment_id') or '') == wanted
                        or str(part.get('filename') or '') == wanted
                    ),
                    None,
                )
                payload = None
                if matched:
                    payload = matched.get('data')
                    filename = os.path.basename(str(matched.get('filename') or filename)) or filename
                    save_name = f"{matched.get('attachment_id')}-{filename}"
                else:
                    payload = files.get(filename) or files.get(wanted)
                    save_name = filename
                ext = _attachment_ext(filename)
                if ext and ext not in _COMMON_ATTACHMENT_EXTS:
                    _fail(f'Attachment type {ext} is not supported.')
                if payload is None:
                    if result.get('transfer'):
                        _fail(_TRANSFER_NOTE)
                    _fail('Failed to read the email attachment.')
                if parts:
                    to_write = parts
                else:
                    to_write = [
                        {'attachment_id': '', 'filename': name, 'data': raw}
                        for name, raw in files.items()
                        if isinstance(raw, (bytes, bytearray))
                    ]
                for part in to_write:
                    raw = part.get('data')
                    if not isinstance(raw, (bytes, bytearray)):
                        continue
                    part_name = os.path.basename(str(part.get('filename') or 'attachment.bin'))
                    if not _is_common_attachment(part_name):
                        continue
                    aid = str(part.get('attachment_id') or '').strip()
                    stored = f'{aid}-{part_name}' if aid else part_name
                    path = _incoming_attachment_path(cred, mid, stored)
                    if os.path.isfile(path):
                        continue
                    with open(path, 'wb') as handle:
                        handle.write(raw)
                target = _incoming_attachment_path(cred, mid, save_name)
                if not os.path.isfile(target) and isinstance(payload, (bytes, bytearray)):
                    with open(target, 'wb') as handle:
                        handle.write(payload)
                if not os.path.isfile(target):
                    _fail('Failed to read the email attachment.')
            ext = _attachment_ext(filename)
            if ext and ext not in _COMMON_ATTACHMENT_EXTS:
                _fail(f'Attachment type {ext} is not supported.')
            return _parsed_mail_attachment(target, filename, mid)

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
        """Create a new or reply mail draft and show the preview card. Never send from this method.

        Always call this (or update_draft) so the user can confirm the card. Do not skip
        the preview or send without mail_draft_confirm_id from that card.

        To change recipients, subject, body, or attachments later, call update_draft
        with this draft_id. Do not compose a second draft for the same email.

        Args:
            to: Recipient email or list of recipients.
            subject: Mail subject.
            body: Plain-text body.
            cc: Optional CC addresses.
            attachment_paths: Workspace artifact path, conversation-upload filename,
                or a list of those. Card-uploaded files are stored under mail_outgoing
                and must not be mixed into the chat file picker. Arbitrary paths
                outside the workspace or conversation uploads are rejected.
            in_reply_to: Optional original Message-ID when composing a reply.
            mailbox: Sending account. Prefer the exact email. A provider name is
                accepted only when exactly one enabled account uses that provider;
                otherwise a mailbox picker is shown. If omitted and several
                accounts are enabled, this method shows a mailbox picker card and
                does not guess. After the user confirms, call update_draft with
                that mailbox (do not compose a second draft).
        """
        requested = str(mailbox or '').strip()
        if requested and not _lookup_accounts(requested):
            return _unavailable_mailbox(requested)
        cred = _resolve_sending_account(mailbox)
        recipients = _split_addresses(to)
        if not recipients:
            raise ToolExecutionError(
                'No recipients. The To field is empty. Do not retry send. '
                'Ask the user to provide at least one email address.'
            )
        paths = _resolve_attachment_paths(attachment_paths)
        now = _iso(datetime.now(timezone.utc))
        if cred is None:
            draft = {
                'draft_id': f'draft_{uuid.uuid4().hex[:16]}',
                'revision': 1,
                'mailbox': '',
                'provider': '',
                'to': recipients,
                'cc': _split_addresses(cc),
                'subject': str(subject or '').strip(),
                'body': str(body or ''),
                'attachment_paths': paths,
                'in_reply_to': str(in_reply_to or '').strip(),
                'status': 'needs_mailbox',
                'mailboxes': _mailbox_choice_rows(_lookup_accounts(requested) or None),
                'sent_at': '',
                'last_error': '',
                'created_at': now,
                'updated_at': now,
            }
            _save_draft(draft)
            return _emit_mailbox_card(draft)
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
                Accepts workspace artifacts or conversation-upload filenames.
            in_reply_to: Replace reply Message-ID when provided.
            mailbox: Sending account. Prefer the exact email. A provider name is
                accepted only when it matches exactly one enabled account.
        """
        if not str(draft_id or '').strip():
            raise ToolExecutionError('draft_id is required')
        draft = _load_draft(draft_id)
        if str(draft.get('status') or '') == 'sent':
            _fail('Cannot update a draft that was already sent.')
        cred = _resolve_sending_account(
            mailbox,
            draft_id=str(draft.get('draft_id') or ''),
            draft_mailbox=str(draft.get('mailbox') or draft.get('provider') or ''),
        )
        if cred is None:
            hint = str(mailbox or draft.get('mailbox') or draft.get('provider') or '')
            draft['status'] = 'needs_mailbox'
            draft['mailbox'] = ''
            draft['provider'] = ''
            draft['mailboxes'] = _mailbox_choice_rows(_lookup_accounts(hint) or None)
            draft['revision'] = _draft_revision(draft) + 1
            draft['updated_at'] = _iso(datetime.now(timezone.utc))
            _save_draft(draft)
            return _emit_mailbox_card(draft)
        draft['mailbox'] = cred['email']
        draft['provider'] = cred['provider']
        if str(draft.get('status') or '') == 'needs_mailbox':
            draft['status'] = 'draft'
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
        with _locked_draft(draft_id):
            return self._send_draft_locked(draft_id)

    def _send_draft_locked(self, draft_id: str) -> dict[str, Any]:
        draft = _load_draft(draft_id)
        if str(draft.get('status') or '') == 'sent':
            _fail('This draft was already sent.')
        cred = _resolve_sending_account(
            '',
            draft_id=str(draft.get('draft_id') or ''),
            draft_mailbox=str(draft.get('mailbox') or draft.get('provider') or ''),
        )
        if cred is None:
            hint = str(draft.get('mailbox') or draft.get('provider') or '')
            draft['status'] = 'needs_mailbox'
            draft['mailboxes'] = _mailbox_choice_rows(_lookup_accounts(hint) or None)
            _save_draft(draft)
            _emit_mailbox_card(draft)
            _fail(
                'Send blocked until the user confirms the sending mailbox on the picker card. '
                'Do not guess a mailbox or call ask_user. Wait for mail_mailbox_confirm.'
            )
        draft['mailbox'] = cred['email']
        draft['provider'] = cred['provider']
        if str(draft.get('status') or '') == 'needs_mailbox':
            draft['status'] = 'draft'
            draft['updated_at'] = _iso(datetime.now(timezone.utc))
            _save_draft(draft)
        confirm_id = str(_agentic_config().get('mail_draft_confirm_id') or '').strip()
        confirmed = confirm_id == str(draft_id).strip()
        if not confirmed:
            _emit_draft_card(draft)
            _fail(
                'Send blocked until the user confirms the preview card in this turn. '
                'Do not call ask_user for send authorization. Wait for mail_draft_confirm_id.'
            )
        expected_revision = _draft_revision(draft)
        if _confirm_revision() != expected_revision:
            _emit_draft_card(draft)
            _fail(
                f'This preview is stale. The draft is now revision {expected_revision}. '
                'Confirm the latest preview card; do not send from an older card.'
            )
        _apply_confirm_patch(draft)
        to_addrs, cc_addrs = _pending_to_cc(draft)
        recipients = to_addrs + cc_addrs
        if not recipients:
            draft['status'] = 'failed'
            draft['last_error'] = 'No recipients. Add at least one address in To, then confirm again.'
            _save_draft(draft)
            _emit_draft_card(draft)
            _fail(
                'Send failed: the To field is empty. Do not retry until the user adds a recipient. '
                'The preview card now shows this error.'
            )
        draft['status'] = 'sending'
        _save_draft(draft)
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
        if result.get('partial_sent'):
            refused = [
                str(item.get('address') or '').strip()
                for item in (result.get('refused') or [])
                if str(item.get('address') or '').strip()
            ]
            accepted = [str(addr).strip() for addr in (result.get('accepted') or []) if str(addr).strip()]
            draft['status'] = 'partial_sent'
            draft['accepted_recipients'] = accepted
            draft['pending_recipients'] = refused
            draft['last_error'] = (
                'Some recipients were rejected after others were already accepted. '
                f'Accepted: {", ".join(accepted)}. Refused: {", ".join(refused)}. '
                'Resend only retries the refused addresses.'
            )
            draft['sent_at'] = result.get('sent_at') or ''
            _save_draft(draft)
            _emit_draft_card(draft)
            return {
                'status': 'partial_sent',
                'draft_id': draft['draft_id'],
                'revision': _draft_revision(draft),
                'accepted': accepted,
                'refused': refused,
                'mailbox': cred['email'],
            }
        sent_at = result.get('sent_at') or _iso(datetime.now(timezone.utc))
        draft['status'] = 'sent'
        draft['sent_at'] = sent_at
        draft['last_error'] = ''
        draft['pending_recipients'] = []
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
