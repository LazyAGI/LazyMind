import base64
import hashlib
import hmac
import os
from typing import Any
from urllib.parse import quote, urlparse

import httpx
from cryptography.hazmat.primitives import padding
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

from channel_gateway.wechat.domain import WeChatError

ILINK_APP_ID = 'bot'
ILINK_CLIENT_VERSION = (2 << 16) | (4 << 8) | 6
QR_BOT_TYPE = '3'
BOT_AGENT = 'lazymind-channel-gateway'
ILINK_CDN_BASE_URL = 'https://novac2c.cdn.weixin.qq.com/c2c'
_MAX_MEDIA_BYTES = 100 * 1024 * 1024


class WeChatClient:
    def __init__(self, base_url: str, poll_timeout_seconds: int):
        self._default_base_url = base_url.rstrip('/')
        self._poll_timeout_seconds = poll_timeout_seconds

    @staticmethod
    def _headers() -> dict[str, str]:
        return {
            'Accept': 'application/json',
            'iLink-App-Id': ILINK_APP_ID,
            'iLink-App-ClientVersion': str(ILINK_CLIENT_VERSION),
        }

    @staticmethod
    def _authenticated_headers(token: str) -> dict[str, str]:
        random_value = int.from_bytes(os.urandom(4), 'big')
        uin = base64.b64encode(str(random_value).encode('ascii')).decode('ascii')
        return {
            **WeChatClient._headers(),
            'AuthorizationType': 'ilink_bot_token',
            'X-WECHAT-UIN': uin,
            'Authorization': f'Bearer {token}',
        }

    @staticmethod
    def _base_info() -> dict[str, Any]:
        return {
            'channel_version': '2.4.6',
            'bot_agent': BOT_AGENT,
        }

    @staticmethod
    def _decode_response(response: httpx.Response) -> dict[str, Any]:
        if response.status_code < 200 or response.status_code >= 300:
            raise WeChatError(f'WeChat returned HTTP {response.status_code}')
        try:
            payload = response.json()
        except ValueError as exc:
            raise WeChatError('WeChat returned invalid JSON') from exc
        if not isinstance(payload, dict):
            raise WeChatError('WeChat returned an unexpected response')
        return payload

    def start_login(self) -> tuple[str, str, str]:
        endpoint = f'{self._default_base_url}/ilink/bot/get_bot_qrcode'
        try:
            response = httpx.post(
                endpoint,
                params={'bot_type': QR_BOT_TYPE},
                json={'local_token_list': []},
                headers=self._headers(),
                timeout=20.0,
            )
        except httpx.HTTPError as exc:
            raise WeChatError('Cannot reach WeChat login service') from exc
        result = self._decode_response(response)
        qrcode = str(result.get('qrcode') or '')
        qr_payload = str(result.get('qrcode_img_content') or '')
        if not qrcode or not qr_payload:
            raise WeChatError('WeChat QR response is incomplete')
        return qrcode, qr_payload, self._default_base_url

    def poll_login_status(self, qrcode: str, base_url: str, verify_code: str = '') -> dict[str, Any]:
        params = {'qrcode': qrcode}
        if verify_code:
            params['verify_code'] = verify_code
        endpoint = f'{base_url.rstrip("/")}/ilink/bot/get_qrcode_status'
        try:
            response = httpx.get(
                endpoint,
                params=params,
                headers=self._headers(),
                timeout=float(self._poll_timeout_seconds),
            )
        except httpx.HTTPError as exc:
            raise WeChatError('Cannot reach WeChat status service') from exc
        return self._decode_response(response)

    def get_updates(
        self,
        *,
        base_url: str,
        token: str,
        cursor: str,
        timeout_ms: int,
    ) -> dict[str, Any]:
        endpoint = f'{base_url.rstrip("/")}/ilink/bot/getupdates'
        timeout_seconds = max(timeout_ms / 1000.0 + 5.0, 10.0)
        try:
            response = httpx.post(
                endpoint,
                json={
                    'get_updates_buf': cursor,
                    'base_info': self._base_info(),
                },
                headers=self._authenticated_headers(token),
                timeout=timeout_seconds,
            )
        except httpx.HTTPError as exc:
            raise WeChatError('Cannot receive WeChat messages') from exc
        payload = self._decode_response(response)
        if payload.get('ret') not in (None, 0) or payload.get('errcode') not in (None, 0):
            raise WeChatError(
                f'WeChat getupdates failed: ret={payload.get("ret")} '
                f'errcode={payload.get("errcode")}'
            )
        return payload

    def notify_start(self, *, base_url: str, token: str) -> None:
        endpoint = f'{base_url.rstrip("/")}/ilink/bot/msg/notifystart'
        try:
            response = httpx.post(
                endpoint,
                json={'base_info': self._base_info()},
                headers=self._authenticated_headers(token),
                timeout=10.0,
            )
        except httpx.HTTPError as exc:
            raise WeChatError('Cannot notify WeChat adapter start') from exc
        self._decode_response(response)

    def download_media(
        self,
        *,
        media: dict[str, Any],
        aes_key_hint: str = '',
        max_bytes: int = _MAX_MEDIA_BYTES,
    ) -> bytes:
        full_url = str(media.get('full_url') or '').strip()
        encrypted_query = str(
            media.get('encrypt_query_param') or ''
        ).strip()
        url = full_url or (
            f'{ILINK_CDN_BASE_URL}/download'
            f'?encrypted_query_param={quote(encrypted_query, safe="")}'
        )
        if not (full_url or encrypted_query) or not self._valid_media_url(url):
            raise WeChatError('WeChat media URL is invalid')
        try:
            with httpx.stream('GET', url, timeout=60.0) as response:
                if response.status_code != 200:
                    raise WeChatError(
                        f'WeChat CDN returned HTTP {response.status_code}'
                    )
                content = bytearray()
                for chunk in response.iter_bytes():
                    content.extend(chunk)
                    if len(content) > max_bytes + algorithms.AES.block_size:
                        raise WeChatError('WeChat media exceeds the size limit')
        except httpx.HTTPError as exc:
            raise WeChatError('Cannot download WeChat media') from exc
        if not content:
            raise WeChatError('WeChat media is empty')
        key = self._decode_aes_key(
            aes_key_hint or str(media.get('aes_key') or '')
        )
        if key is None:
            return bytes(content)
        try:
            decryptor = Cipher(algorithms.AES(key), modes.ECB()).decryptor()
            padded = decryptor.update(bytes(content)) + decryptor.finalize()
            unpadder = padding.PKCS7(algorithms.AES.block_size).unpadder()
            plaintext = unpadder.update(padded) + unpadder.finalize()
        except ValueError as exc:
            raise WeChatError('Cannot decrypt WeChat media') from exc
        if not plaintext or len(plaintext) > max_bytes:
            raise WeChatError('WeChat media is empty or exceeds the size limit')
        return plaintext

    def send_text(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        context_token: str,
        text: str,
        client_id: str,
        run_id: str,
    ) -> None:
        endpoint = f'{base_url.rstrip("/")}/ilink/bot/sendmessage'
        try:
            response = httpx.post(
                endpoint,
                json={
                    'msg': {
                        'from_user_id': '',
                        'to_user_id': to_user_id,
                        'client_id': client_id,
                        'message_type': 2,
                        'message_state': 2,
                        'item_list': [{'type': 1, 'text_item': {'text': text}}],
                        'context_token': context_token,
                        'run_id': run_id,
                    },
                    'base_info': self._base_info(),
                },
                headers=self._authenticated_headers(token),
                timeout=20.0,
            )
        except httpx.HTTPError as exc:
            raise WeChatError('Cannot send WeChat message') from exc
        payload = self._decode_response(response)
        if payload.get('ret') not in (None, 0):
            raise WeChatError(f'WeChat sendmessage failed: ret={payload.get("ret")}')

    def upload_image(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        image: bytes,
    ) -> dict[str, Any]:
        return self._upload_item(
            base_url=base_url,
            token=token,
            to_user_id=to_user_id,
            content=image,
            kind='image',
        )

    def upload_file(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        content: bytes,
        filename: str,
    ) -> dict[str, Any]:
        return self._upload_item(
            base_url=base_url,
            token=token,
            to_user_id=to_user_id,
            content=content,
            kind='file',
            filename=filename,
        )

    def _upload_item(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        content: bytes,
        kind: str,
        filename: str = '',
    ) -> dict[str, Any]:
        if not content:
            raise WeChatError(f'Cannot send an empty {kind}')
        media_type = 1 if kind == 'image' else 3
        key_material = hmac.new(
            token.encode('utf-8'),
            f'lazymind-wechat-{kind}-v1\0'.encode()
            + hashlib.sha256(content).digest(),
            hashlib.sha256,
        ).digest()
        file_key = key_material[:16].hex()
        aes_key = key_material[16:]
        ciphertext = self._encrypt_media(content, aes_key)
        upload = self._get_upload_url(
            base_url=base_url,
            token=token,
            to_user_id=to_user_id,
            file_key=file_key,
            aes_key=aes_key,
            media_type=media_type,
            content=content,
            encrypted_size=len(ciphertext),
        )
        upload_url = str(upload.get('upload_full_url') or '').strip()
        if not upload_url:
            upload_param = str(upload.get('upload_param') or '').strip()
            if not upload_param:
                raise WeChatError(f'WeChat did not return a {kind} upload URL')
            upload_url = (
                f'{ILINK_CDN_BASE_URL}/upload'
                f'?encrypted_query_param={quote(upload_param, safe="")}'
                f'&filekey={quote(file_key, safe="")}'
            )
        if not self._valid_media_url(upload_url):
            raise WeChatError(f'WeChat {kind} upload URL is invalid')
        download_param = self._upload_media(upload_url, ciphertext)
        media = {
            'encrypt_query_param': download_param,
            # iLink's current sender serializes the hexadecimal key string as
            # the protobuf bytes field.  Encoding the raw 16 bytes produces a
            # valid-looking message that the official WeChat client cannot
            # decrypt.
            'aes_key': base64.b64encode(
                aes_key.hex().encode('ascii')
            ).decode('ascii'),
            'encrypt_type': 1,
        }
        if kind == 'image':
            return {
                'type': 2,
                'image_item': {
                    'media': media,
                    'mid_size': len(ciphertext),
                },
            }
        return {
            'type': 4,
            'file_item': {
                'media': media,
                'file_name': filename.strip() or 'lazymind-output',
                'md5': hashlib.md5(
                    content,
                    usedforsecurity=False,
                ).hexdigest(),
                'len': str(len(content)),
            },
        }

    def send_media(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        context_token: str,
        item: dict[str, Any],
        client_id: str,
        run_id: str,
    ) -> None:
        self._send_item(
            base_url=base_url,
            token=token,
            to_user_id=to_user_id,
            context_token=context_token,
            item=item,
            client_id=client_id,
            run_id=run_id,
        )

    def _get_upload_url(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        file_key: str,
        aes_key: bytes,
        media_type: int,
        content: bytes,
        encrypted_size: int,
    ) -> dict[str, Any]:
        endpoint = f'{base_url.rstrip("/")}/ilink/bot/getuploadurl'
        try:
            response = httpx.post(
                endpoint,
                json={
                    'filekey': file_key,
                    'media_type': media_type,
                    'to_user_id': to_user_id,
                    'rawsize': len(content),
                    'rawfilemd5': hashlib.md5(
                        content,
                        usedforsecurity=False,
                    ).hexdigest(),
                    'filesize': encrypted_size,
                    'no_need_thumb': True,
                    'aeskey': aes_key.hex(),
                    'base_info': self._base_info(),
                },
                headers=self._authenticated_headers(token),
                timeout=20.0,
            )
        except httpx.HTTPError as exc:
            raise WeChatError(
                'Cannot request a WeChat media upload URL'
            ) from exc
        payload = self._decode_response(response)
        if payload.get('ret') not in (None, 0):
            raise WeChatError(
                f'WeChat getuploadurl failed: ret={payload.get("ret")}'
            )
        return payload

    @staticmethod
    def _upload_media(upload_url: str, ciphertext: bytes) -> str:
        try:
            response = httpx.post(
                upload_url,
                content=ciphertext,
                headers={'Content-Type': 'application/octet-stream'},
                timeout=60.0,
            )
        except httpx.HTTPError as exc:
            raise WeChatError('Cannot upload media to WeChat CDN') from exc
        if response.status_code != 200:
            raise WeChatError(
                f'WeChat CDN returned HTTP {response.status_code}'
            )
        download_param = str(
            response.headers.get('x-encrypted-param') or ''
        ).strip()
        if not download_param:
            try:
                payload = response.json()
            except ValueError:
                payload = {}
            if isinstance(payload, dict):
                download_param = str(
                    payload.get('encrypt_query_param')
                    or payload.get('download_param')
                    or ''
                ).strip()
        if not download_param:
            raise WeChatError('WeChat CDN returned no media download token')
        return download_param

    def _send_item(
        self,
        *,
        base_url: str,
        token: str,
        to_user_id: str,
        context_token: str,
        item: dict[str, Any],
        client_id: str,
        run_id: str,
    ) -> None:
        endpoint = f'{base_url.rstrip("/")}/ilink/bot/sendmessage'
        try:
            response = httpx.post(
                endpoint,
                json={
                    'msg': {
                        'from_user_id': '',
                        'to_user_id': to_user_id,
                        'client_id': client_id,
                        'message_type': 2,
                        'message_state': 2,
                        'item_list': [item],
                        'context_token': context_token,
                        'run_id': run_id,
                    },
                    'base_info': self._base_info(),
                },
                headers=self._authenticated_headers(token),
                timeout=20.0,
            )
        except httpx.HTTPError as exc:
            raise WeChatError('Cannot send WeChat message') from exc
        payload = self._decode_response(response)
        if payload.get('ret') not in (None, 0):
            raise WeChatError(f'WeChat sendmessage failed: ret={payload.get("ret")}')

    @staticmethod
    def _encrypt_media(plaintext: bytes, aes_key: bytes) -> bytes:
        padder = padding.PKCS7(algorithms.AES.block_size).padder()
        padded = padder.update(plaintext) + padder.finalize()
        encryptor = Cipher(
            algorithms.AES(aes_key),
            modes.ECB(),
        ).encryptor()
        return encryptor.update(padded) + encryptor.finalize()

    @staticmethod
    def _decode_aes_key(value: str) -> bytes | None:
        value = value.strip()
        if not value:
            return None
        if len(value) == 32 and all(
            character in '0123456789abcdefABCDEF'
            for character in value
        ):
            return bytes.fromhex(value)
        try:
            decoded = base64.b64decode(value, validate=True)
        except ValueError as exc:
            raise WeChatError('WeChat media AES key is invalid') from exc
        if len(decoded) == 16:
            return decoded
        if len(decoded) == 32:
            hex_value = decoded.decode('ascii', errors='ignore')
            if all(
                character in '0123456789abcdefABCDEF'
                for character in hex_value
            ):
                return bytes.fromhex(hex_value)
        raise WeChatError('WeChat media AES key is invalid')

    @staticmethod
    def _valid_media_url(value: str) -> bool:
        parsed = urlparse(value)
        hostname = (parsed.hostname or '').lower().rstrip('.')
        return bool(
            parsed.scheme == 'https'
            and (
                hostname == 'weixin.qq.com'
                or hostname.endswith('.weixin.qq.com')
            )
        )
