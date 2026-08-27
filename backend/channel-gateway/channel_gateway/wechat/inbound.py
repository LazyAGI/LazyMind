from __future__ import annotations

import hashlib
import json
import logging
import os
import re
import threading
from collections import OrderedDict
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from channel_gateway.common.domain.channel import InboundEnvelope
from channel_gateway.common.domain.chat import (
    CHANNEL_INBOUND_CONTEXT,
    PROVIDER_ATTACHMENT_INDEX,
    PROVIDER_MESSAGE_IDS,
    PROVIDER_REFERENCES,
    ChannelAttachment,
    ChannelExecutionContext,
)
from channel_gateway.common.ports.providers import ReceiverRepository
from channel_gateway.wechat.domain import (
    WeChatAddressFactory,
    WeChatConfig,
    WeChatError,
)
from channel_gateway.wechat.ports import WeChatReceiverClient
from channel_gateway.wechat.protocol import (
    ITEM_TYPE_FILE,
    ITEM_TYPE_IMAGE,
    ITEM_TYPE_TEXT,
    ITEM_TYPE_VOICE,
    MESSAGE_TYPE_USER,
)


_logger = logging.getLogger(__name__)
_MAX_INBOUND_IMAGE_BYTES = 20 * 1024 * 1024
_MAX_INBOUND_ATTACHMENTS = 10
_MAX_REFERENCE_TEXT_CHARS = 6_000
_FILE_KEY_CACHE_SIZE = 128
_ATTACHMENT_PROMPT = '请处理用户发送的图片和文档。'
_MEDIA_FAILURE_PROMPT = '微信图片或文档读取失败，请重新发送。'
_UNRESOLVED_REFERENCE = '当前引用消息无法解析，请重新引用或重新发送。'
_MD5 = re.compile(r'^[0-9a-f]{32}$')
_LEGACY_MESSAGE_IDS = 'wechat_message_ids'
_LEGACY_ATTACHMENT_INDEX = 'wechat_attachment_index'


@dataclass(slots=True)
class _DownloadBudget:
    remaining_bytes: int
    remaining_attachments: int = _MAX_INBOUND_ATTACHMENTS


def message_key(message: dict[str, Any]) -> str:
    message_id = str(message.get('message_id') or '').strip()
    raw = message_id or json.dumps(
        message,
        ensure_ascii=False,
        sort_keys=True,
        separators=(',', ':'),
    )
    return hashlib.sha256(raw.encode('utf-8')).hexdigest()


def message_ids(message: dict[str, Any]) -> list[str]:
    values = list(top_level_message_ids(message))
    values.extend(
        item.get('msg_id')
        for item in message.get('item_list') or []
        if isinstance(item, dict)
    )
    return list(dict.fromkeys(
        str(value).strip() for value in values if str(value or '').strip()
    ))


def top_level_message_ids(message: dict[str, Any]) -> tuple[str, ...]:
    return tuple(dict.fromkeys(
        str(value).strip()
        for value in (message.get('message_id'), message.get('msg_id'))
        if str(value or '').strip()
    ))


def message_text(message: dict[str, Any]) -> str:
    values: list[str] = []
    for item in message.get('item_list') or []:
        if not isinstance(item, dict):
            continue
        item_type = item.get('type')
        payload = (
            item.get('text_item')
            if item_type == ITEM_TYPE_TEXT
            else item.get('voice_item')
            if item_type == ITEM_TYPE_VOICE
            else None
        )
        if isinstance(payload, dict):
            text = str(payload.get('text') or '').strip()
            if text:
                values.append(text)
    return '\n'.join(values)


class WeChatInboundNormalizer:
    """Converts one authenticated iLink message into a durable envelope."""

    def __init__(
        self,
        *,
        config: WeChatConfig,
        store: ReceiverRepository,
        client: WeChatReceiverClient,
        addresses: WeChatAddressFactory,
    ):
        self._config = config
        self._store = store
        self._client = client
        self._addresses = addresses
        self._file_aes_keys: OrderedDict[tuple[str, int], str] = OrderedDict()
        self._file_key_lock = threading.Lock()

    def normalize(
        self,
        account: dict[str, Any],
        credentials: dict[str, str],
        message: dict[str, Any],
    ) -> InboundEnvelope | None:
        if message.get('message_type') not in (None, MESSAGE_TYPE_USER):
            return None
        sender_id = str(message.get('from_user_id') or '')
        context_token = str(message.get('context_token') or '')
        if (
            sender_id != credentials['authorized_user_id']
            or not sender_id
            or not context_token
        ):
            return None

        account_id = str(account['id'])
        owner_user_id = str(account['owner_user_id'])
        current_message_key = message_key(message)
        budget = _DownloadBudget(
            remaining_bytes=self._config.max_inbound_media_bytes,
        )
        attachments, item_attachments, media_failed = self._media_from_items(
            message.get('item_list') or [],
            owner_user_id=owner_user_id,
            storage_key=current_message_key,
            budget=budget,
        )
        references, referenced_attachments, reference_failed = self._references(
            message,
            account_id=account_id,
            recipient_id=sender_id,
            owner_user_id=owner_user_id,
            storage_key=current_message_key,
            budget=budget,
        )
        attachments.extend(referenced_attachments)

        text = message_text(message)
        if references:
            quote = '\n'.join(
                str(reference.get('text') or '')
                for reference in references
                if reference.get('text')
            )
            if not quote:
                quote = (
                    _UNRESOLVED_REFERENCE
                    if reference_failed
                    else '（包含引用附件）'
                )
            text = (
                f'[引用消息]\n{quote[:_MAX_REFERENCE_TEXT_CHARS]}\n[/引用消息]\n\n'
                f'[当前消息]\n{text or "请结合引用消息处理。"}\n[/当前消息]'
            )
        elif not text and attachments:
            text = _ATTACHMENT_PROMPT
        elif not text and media_failed:
            text = _MEDIA_FAILURE_PROMPT
        if not text:
            return None

        all_message_ids = message_ids(message)
        attachment_index = self._attachment_index(
            top_level_message_ids(message),
            attachments,
            item_attachments,
        )
        execution = ChannelExecutionContext(
            attachments=tuple(attachments),
            interaction_mode='plain_text',
        )
        provider_context: dict[str, Any] = {
            'context_token': context_token,
            'session_id': str(message.get('session_id') or ''),
        }
        inbound_context: dict[str, Any] = {
            PROVIDER_MESSAGE_IDS: all_message_ids,
        }
        if attachment_index:
            inbound_context[PROVIDER_ATTACHMENT_INDEX] = attachment_index
        if references:
            inbound_context[PROVIDER_REFERENCES] = references
        provider_context[CHANNEL_INBOUND_CONTEXT] = inbound_context
        if media_failed and not attachments:
            provider_context['channel_error'] = _MEDIA_FAILURE_PROMPT

        address_hash = self._addresses.direct(account_id, sender_id).route_hash
        return InboundEnvelope(
            provider='wechat',
            account_id=account_id,
            message_key=current_message_key,
            order_key=address_hash,
            external_address_hash=address_hash,
            owner_user_id=owner_user_id,
            recipient_id=sender_id,
            text=text,
            provider_context=provider_context,
            sensitive_context={'channel_execution': execution.to_dict()},
        )

    def _references(
        self,
        message: dict[str, Any],
        *,
        account_id: str,
        recipient_id: str,
        owner_user_id: str,
        storage_key: str,
        budget: _DownloadBudget,
    ) -> tuple[list[dict[str, Any]], list[ChannelAttachment], bool]:
        sources = [
            item['ref_msg']
            for item in message.get('item_list') or []
            if isinstance(item, dict) and isinstance(item.get('ref_msg'), dict)
        ]
        if not sources and isinstance(message.get('ref_msg'), dict):
            sources.append(message['ref_msg'])

        records: list[dict[str, Any]] = []
        attachments: list[ChannelAttachment] = []
        unresolved = False
        for reference_index, reference in enumerate(sources):
            record, reference_item = self._reference_record(reference)
            reference_id = self._reference_id(reference, reference_item)
            inline_media = self._inline_media(reference_item)
            if record.get('text') or inline_media:
                record.update({
                    'resolved': True,
                    'source': 'inline',
                    'message_id': reference_id,
                })
                if inline_media and budget.remaining_attachments > 0:
                    found, _by_item, failed = self._media_from_items(
                        [reference_item],
                        owner_user_id=owner_user_id,
                        storage_key=f'{storage_key}-ref-{reference_index}',
                        budget=budget,
                    )
                    attachments.extend(found)
                    if found:
                        record['attachments'] = self._attachment_metadata(found, 'inline')
                    unresolved = unresolved or failed
            elif reference_id:
                persisted = self._find_reference(
                    account_id=account_id,
                    recipient_id=recipient_id,
                    message_id=reference_id,
                )
                if persisted is None:
                    record.update({
                        'resolved': False,
                        'message_id': reference_id,
                        'reason': 'message_not_found',
                    })
                    unresolved = True
                else:
                    persisted_text = str(persisted.get('text') or '').strip()
                    persisted_context = persisted.get('provider_context')
                    found = self._indexed_attachments(
                        persisted_context,
                        reference_id,
                    )[:budget.remaining_attachments]
                    budget.remaining_attachments -= len(found)
                    attachments.extend(found)
                    record = {
                        'resolved': True,
                        'source': 'db',
                        'message_id': reference_id,
                    }
                    if persisted_text:
                        record['text'] = persisted_text
                    if found:
                        record['attachments'] = self._attachment_metadata(found, 'db')
            elif record:
                record.update({
                    'resolved': False,
                    'message_id': '',
                    'reason': 'message_id_missing',
                })
                unresolved = True
            if record:
                records.append(record)
        return records, attachments, unresolved

    def _media_from_items(
        self,
        items: list[Any],
        *,
        owner_user_id: str,
        storage_key: str,
        budget: _DownloadBudget,
    ) -> tuple[
        list[ChannelAttachment],
        dict[str, list[ChannelAttachment]],
        bool,
    ]:
        attachments: list[ChannelAttachment] = []
        by_message_id: dict[str, list[ChannelAttachment]] = {}
        failed = False
        for item_index, item in enumerate(items):
            if budget.remaining_attachments <= 0:
                break
            if not isinstance(item, dict) or item.get('type') not in (
                ITEM_TYPE_IMAGE,
                ITEM_TYPE_FILE,
            ):
                continue
            item_type = int(item['type'])
            kind = 'image' if item_type == ITEM_TYPE_IMAGE else 'file'
            payload = item.get(f'{kind}_item')
            media = payload.get('media') if isinstance(payload, dict) else None
            if not isinstance(payload, dict) or not isinstance(media, dict):
                failed = True
                continue

            filename = str(payload.get('file_name') or '').strip()
            integrity = self._file_integrity(payload) if kind == 'file' else None
            if kind == 'file' and integrity is None:
                failed = True
                _logger.warning('wechat_inbound_file_metadata_invalid')
                continue
            declared_bytes = integrity[1] if integrity else 0
            max_plaintext_bytes = min(
                _MAX_INBOUND_IMAGE_BYTES
                if kind == 'image'
                else self._config.max_inbound_media_bytes,
                budget.remaining_bytes,
            )
            if max_plaintext_bytes <= 0 or declared_bytes > max_plaintext_bytes:
                failed = True
                continue

            def consume_downloaded_bytes(count: int) -> None:
                budget.remaining_bytes = max(0, budget.remaining_bytes - count)

            fallback_keys = self._file_key_candidates(integrity)
            try:
                content, used_aes_key = self._client.download_media(
                    media,
                    image_aeskey=(
                        str(payload.get('aeskey') or '')
                        if kind == 'image'
                        else ''
                    ),
                    max_bytes=max_plaintext_bytes,
                    max_download_bytes=budget.remaining_bytes,
                    fallback_aes_keys=fallback_keys,
                    validate_plaintext=(
                        lambda value, expected=integrity:
                        self._file_matches_integrity(value, expected)
                    ) if integrity else None,
                    on_download_bytes=consume_downloaded_bytes,
                )
                extension = self._image_extension(content) if kind == 'image' else ''
                if kind == 'image' and not extension:
                    raise WeChatError('WeChat image type is unsupported')
                path = self._save_media(
                    owner_user_id=owner_user_id,
                    storage_key=storage_key,
                    item_index=item_index,
                    filename=filename,
                    fallback_extension=extension or '.bin',
                    content=content,
                )
            except (OSError, WeChatError) as exc:
                failed = True
                _logger.warning(
                    'wechat_inbound_media_failed type=%s error=%s',
                    kind,
                    exc,
                )
                continue

            if integrity:
                self._remember_file_key(integrity, used_aes_key)
            attachment = ChannelAttachment(input_type=kind, uri=path)
            attachments.append(attachment)
            budget.remaining_attachments -= 1
            item_message_id = str(item.get('msg_id') or '').strip()
            if item_message_id:
                by_message_id.setdefault(item_message_id, []).append(attachment)
        return attachments, by_message_id, failed

    def _save_media(
        self,
        *,
        owner_user_id: str,
        storage_key: str,
        item_index: int,
        filename: str,
        fallback_extension: str,
        content: bytes,
    ) -> str:
        root = Path(self._config.upload_root).resolve()
        owner = hashlib.sha256(owner_user_id.encode('utf-8')).hexdigest()[:32]
        directory = (root / 'channels' / 'wechat' / owner / storage_key).resolve()
        if root not in directory.parents:
            raise OSError('Invalid WeChat media directory')
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        safe_name = self._safe_filename(filename)
        name = (
            f'{item_index}-{safe_name}'
            if safe_name
            else f'{item_index}-{hashlib.sha256(content).hexdigest()[:16]}'
                 f'{fallback_extension}'
        )
        target = (directory / name).resolve()
        if directory not in target.parents:
            raise OSError('Invalid WeChat media filename')
        if target.exists() and target.read_bytes() == content:
            return str(target)
        temporary = directory / f'.{name}.{os.getpid()}.tmp'
        try:
            with temporary.open('wb') as handle:
                os.chmod(temporary, 0o600)
                handle.write(content)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(temporary, target)
        finally:
            temporary.unlink(missing_ok=True)
        return str(target)

    def _find_reference(
        self,
        *,
        account_id: str,
        recipient_id: str,
        message_id: str,
    ) -> dict[str, Any] | None:
        try:
            for expected_context in (
                {
                    CHANNEL_INBOUND_CONTEXT: {
                        PROVIDER_MESSAGE_IDS: [message_id],
                    },
                },
                {_LEGACY_MESSAGE_IDS: [message_id]},
            ):
                result = self._store.find_inbound_by_provider_context(
                    provider='wechat',
                    account_id=account_id,
                    recipient_id=recipient_id,
                    expected_context=expected_context,
                )
                if result is not None:
                    return result
            return None
        except Exception:
            _logger.exception(
                'wechat_reference_lookup_failed message_id=%s',
                message_id,
            )
            return None

    def _indexed_attachments(
        self,
        provider_context: Any,
        message_id: str,
    ) -> list[ChannelAttachment]:
        if not isinstance(provider_context, dict):
            return []
        inbound_context = provider_context.get(CHANNEL_INBOUND_CONTEXT)
        index = (
            inbound_context.get(PROVIDER_ATTACHMENT_INDEX)
            if isinstance(inbound_context, dict)
            else provider_context.get(_LEGACY_ATTACHMENT_INDEX)
        )
        if not isinstance(index, dict):
            return []
        raw_attachments = index.get('attachments')
        raw_messages = index.get('messages')
        if not isinstance(raw_attachments, list) or not isinstance(raw_messages, dict):
            return []
        positions = raw_messages.get(message_id)
        if not isinstance(positions, list):
            return []
        result: list[ChannelAttachment] = []
        upload_root = Path(self._config.upload_root).resolve()
        for position in positions:
            if not isinstance(position, int) or not 0 <= position < len(raw_attachments):
                continue
            attachment = ChannelAttachment.from_dict(raw_attachments[position])
            if attachment is None or not attachment.uri:
                continue
            path = Path(attachment.uri).resolve()
            if upload_root not in path.parents or not path.is_file():
                continue
            result.append(attachment)
        return result

    @staticmethod
    def _attachment_index(
        root_message_ids: tuple[str, ...],
        attachments: list[ChannelAttachment],
        by_message_id: dict[str, list[ChannelAttachment]],
    ) -> dict[str, Any]:
        if not attachments:
            return {}
        unique: list[ChannelAttachment] = []
        positions: dict[tuple[str, str], int] = {}
        for attachment in attachments:
            key = (attachment.input_type, attachment.uri)
            if key not in positions:
                positions[key] = len(unique)
                unique.append(attachment)
        messages: dict[str, list[int]] = {}
        all_positions = list(range(len(unique)))
        for message_id in root_message_ids:
            messages[message_id] = all_positions
        for message_id, selected in by_message_id.items():
            messages[message_id] = [
                positions[(item.input_type, item.uri)] for item in selected
            ]
        return {
            'attachments': [item.to_dict() for item in unique],
            'messages': messages,
        }

    @staticmethod
    def _reference_record(
        reference: dict[str, Any],
    ) -> tuple[dict[str, Any], dict[str, Any] | None]:
        reference_item = reference.get('message_item')
        if not isinstance(reference_item, dict):
            reference_item = None
        title = str(reference.get('title') or '').strip()
        body = (
            message_text({'item_list': [reference_item]})
            if reference_item
            else ''
        )
        parts = list(dict.fromkeys(value for value in (title, body) if value))
        record = {'text': '\n'.join(parts)} if parts else {}
        return record, reference_item

    @staticmethod
    def _reference_id(
        reference: dict[str, Any],
        reference_item: dict[str, Any] | None,
    ) -> str:
        values = (
            reference_item.get('msg_id') if reference_item else None,
            reference.get('msg_id'),
            reference.get('message_id'),
        )
        return next(
            (str(value).strip() for value in values if str(value or '').strip()),
            '',
        )

    @staticmethod
    def _inline_media(reference_item: dict[str, Any] | None) -> bool:
        if not isinstance(reference_item, dict):
            return False
        item_type = reference_item.get('type')
        payload = (
            reference_item.get('image_item')
            if item_type == ITEM_TYPE_IMAGE
            else reference_item.get('file_item')
            if item_type == ITEM_TYPE_FILE
            else None
        )
        return isinstance(payload, dict) and isinstance(payload.get('media'), dict)

    @staticmethod
    def _attachment_metadata(
        attachments: list[ChannelAttachment],
        source: str,
    ) -> list[dict[str, str]]:
        return [
            {'source': source, 'input_type': attachment.input_type}
            for attachment in attachments
        ]

    @staticmethod
    def _file_integrity(payload: dict[str, Any]) -> tuple[str, int] | None:
        expected_md5 = str(payload.get('md5') or '').strip().lower()
        try:
            expected_length = int(str(payload.get('len') or '').strip())
        except ValueError:
            return None
        if expected_length <= 0 or not _MD5.fullmatch(expected_md5):
            return None
        return expected_md5, expected_length

    @staticmethod
    def _file_matches_integrity(
        content: bytes,
        integrity: tuple[str, int],
    ) -> bool:
        expected_md5, expected_length = integrity
        return (
            len(content) == expected_length
            and hashlib.md5(content, usedforsecurity=False).hexdigest()
            == expected_md5
        )

    def _file_key_candidates(
        self,
        integrity: tuple[str, int] | None,
    ) -> tuple[str, ...]:
        if integrity is None:
            return ()
        with self._file_key_lock:
            key = self._file_aes_keys.get(integrity)
            if key:
                self._file_aes_keys.move_to_end(integrity)
            return (key,) if key else ()

    def _remember_file_key(
        self,
        integrity: tuple[str, int],
        aes_key: str,
    ) -> None:
        if not aes_key:
            return
        with self._file_key_lock:
            self._file_aes_keys[integrity] = aes_key
            self._file_aes_keys.move_to_end(integrity)
            while len(self._file_aes_keys) > _FILE_KEY_CACHE_SIZE:
                self._file_aes_keys.popitem(last=False)

    @staticmethod
    def _safe_filename(value: str) -> str:
        name = Path(value.replace('\\', '/')).name.strip()
        return ''.join(
            character
            for character in name
            if character.isprintable() and character not in {'/', '\\', '\x00'}
        )[:180]

    @staticmethod
    def _image_extension(content: bytes) -> str:
        if content.startswith(b'\x89PNG\r\n\x1a\n'):
            return '.png'
        if content.startswith(b'\xff\xd8\xff'):
            return '.jpg'
        if content.startswith((b'GIF87a', b'GIF89a')):
            return '.gif'
        if len(content) >= 12 and content[:4] == b'RIFF' and content[8:12] == b'WEBP':
            return '.webp'
        return ''
