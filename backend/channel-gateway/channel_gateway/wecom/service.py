import asyncio
import datetime as dt
import hashlib
import json
import logging
import platform
import threading
import time
import uuid

import httpx

from channel_gateway.common.domain.channel import account_view, sanitize_channel_text
from channel_gateway.common.domain.outbound import OutboundRenderer
from channel_gateway.common.errors import GatewayError
from channel_gateway.wecom.runtime import verify_credentials


_logger = logging.getLogger(__name__)
_QR_GENERATE_URL = 'https://work.weixin.qq.com/ai/qc/generate'
_QR_QUERY_URL = 'https://work.weixin.qq.com/ai/qc/query_result'
_QR_SOURCE = 'wecom_cli_external'
_QR_TTL_SECONDS = 300
_CLI_AUTH_URL = 'https://qyapi.weixin.qq.com/cgi-bin/aibot/cli/get_cli_config'
_CLI_BASE_URL = 'https://qyapi.weixin.qq.com/cli'


def _platform_code():
    return {'Darwin': 1, 'Windows': 2, 'Linux': 3}.get(platform.system(), 0)


def _bot_label(bot_info=None, ordinal=1):
    info = bot_info or {}
    name = next((str(info.get(key) or '').strip() for key in
                 ('bot_name', 'botname', 'name', 'display_name') if str(info.get(key) or '').strip()), '')
    return name[:128] or f'企业微信机器人 {max(1, ordinal)}'


class WeComService:
    """Small connection/account/delivery adapter using the common store and worker."""
    def __init__(self, store, cipher, runtime):
        self._store, self._cipher, self._runtime = store, cipher, runtime
        self._renderer = OutboundRenderer(1800)
        self._tokens = {}
        self._token_lock = threading.Lock()

    def _next_bot_label(self, owner_user_id, bot_info=None):
        return _bot_label(bot_info, len(self._store.list_accounts(owner_user_id, 'wecom')) + 1)

    def list_accounts(self, owner_user_id):
        return {'items': [account_view(row) for row in self._store.list_accounts(owner_user_id, 'wecom')]}

    def disconnect_account(self, owner_user_id, account_id):
        if not self._store.disconnect_account(owner_user_id, account_id):
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '企业微信账号不存在')
        self._runtime.stop_account(account_id)

    def _token(self, account, *, refresh=False):
        cache_key = (account['id'], account['credential_revision'])
        with self._token_lock:
            cached = self._tokens.get(cache_key)
            if cached and not refresh and cached[1] > time.monotonic():
                return cached[0]
        credentials = self._cipher.decrypt(account['owner_user_id'], account['credentials_ciphertext'])
        now = int(time.time())
        nonce = f'cli_{int(time.time() * 1000)}_{uuid.uuid4().hex[:8]}'
        signature = hashlib.sha256(
            f"{credentials['secret']}{credentials['bot_id']}{now}{nonce}".encode()
        ).hexdigest()
        response = httpx.post(_CLI_AUTH_URL, json={
            'bot_id': credentials['bot_id'], 'time': now, 'nonce': nonce,
            'signature': signature, 'bind_source': 2,
        }, timeout=15)
        response.raise_for_status()
        body = response.json()
        token = str(body.get('token') or '')
        if body.get('errcode') or not token:
            raise RuntimeError('WECOM_CLI_AUTH_FAILED')
        with self._token_lock:
            self._tokens[cache_key] = (token, time.monotonic() + 300)
        return token

    def _cli_call(self, account, path, payload, *, refresh=False):
        response = httpx.post(
            _CLI_BASE_URL + path,
            headers={'Authorization': 'Bearer ' + self._token(account, refresh=refresh)},
            json={'payload': json.dumps(payload, ensure_ascii=False, separators=(',', ':'))},
            timeout=15,
        )
        response.raise_for_status()
        body = response.json()
        if body.get('errcode'):
            if body.get('errcode') == 853004 and not refresh:
                return self._cli_call(account, path, payload, refresh=True)
            raise RuntimeError('WECOM_CLI_REQUEST_FAILED')
        inner = json.loads(body.get('results_json') or '{}')
        error = inner.get('error') or {}
        if error:
            if error.get('code') == 853004 and not refresh:
                return self._cli_call(account, path, payload, refresh=True)
            raise RuntimeError('WECOM_CLI_REQUEST_FAILED')
        result = inner.get('result') or '{}'
        return json.loads(result) if isinstance(result, str) else result

    def sync_notification_targets(self, owner_user_id, account):
        try:
            result = self._cli_call(account, '/message/aibot/sessions/list', {})
            sessions = []
            for item in result.get('sessions') or []:
                recipient_id = str(item.get('chat_id') or '')
                if not recipient_id or len(recipient_id) > 256:
                    continue
                kind = 'group' if item.get('chat_type') == 'group' else 'conversation'
                label = str(item.get('chat_name') or recipient_id)[:256]
                sessions.append({'recipient_id': recipient_id, 'label': label, 'kind': kind})
            self._store.cache_wecom_notification_sessions(
                owner_user_id, account['id'], account['credential_revision'], sessions)
        except GatewayError:
            raise
        except Exception as exc:
            _logger.warning('wecom_sessions_sync_failed account_id=%s', account['id'])
            raise GatewayError(503, 'WECOM_SESSIONS_UNAVAILABLE',
                               '企业微信会话暂时无法读取，请稍后刷新', retryable=True) from exc

    def create_session(self, *, owner_user_id, idempotency_key, credentials=None, account_id=None):
        if idempotency_key and len(idempotency_key) > 128:
            raise GatewayError(422, 'INVALID_IDEMPOTENCY_KEY', 'Idempotency-Key 长度不能超过 128 个字符')
        if credentials:
            return self._create_credentials_session(
                owner_user_id=owner_user_id, idempotency_key=idempotency_key,
                credentials=credentials, account_id=account_id,
            )
        return self._create_qr_session(
            owner_user_id=owner_user_id, idempotency_key=idempotency_key,
            account_id=account_id,
        )

    def _create_credentials_session(self, *, owner_user_id, idempotency_key, credentials, account_id):
        if not credentials.get('bot_id') or not credentials.get('secret'):
            raise GatewayError(422, 'INVALID_REQUEST', '请填写 BotID 和 Secret')
        identity = hashlib.sha256(credentials['bot_id'].encode()).hexdigest()
        self._store.assert_identity_available(owner_user_id, 'wecom', identity)
        if account_id:
            account = self._store.get_account(owner_user_id, account_id)
            if not account:
                raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '企业微信账号不存在')
            if account['provider'] != 'wecom' or account['external_id_hash'] != identity:
                raise GatewayError(409, 'ACCOUNT_IDENTITY_MISMATCH', '重连的机器人身份与原账号不一致')
        row, created = self._store.reserve_session(
            session_id=f'cs_{uuid.uuid4().hex}', owner_user_id=owner_user_id,
            provider='wecom', idempotency_key=idempotency_key,
            expires_at=dt.datetime.now(dt.timezone.utc) + dt.timedelta(seconds=60),
            requested_account_id=account_id,
        )
        if created:
            row = self._store.update_active_session(session_id=row['id'], qr_version=row['qr_version'],
                                                    expected_revision=row['revision'], status='confirming',
                                                    message='正在验证企业微信凭据', state_ciphertext='')
            try:
                asyncio.run(verify_credentials(credentials))
            except Exception:
                self._store.mark_failed(row['id'], row['qr_version'], code='WECOM_AUTH_FAILED',
                                        message='企业微信连接失败，请检查 BotID、Secret 和网络', retryable=True)
            else:
                account = self._store.save_connected_account(
                    session_id=row['id'], qr_version=row['qr_version'], expected_revision=row['revision'],
                    owner_user_id=owner_user_id, provider='wecom', external_id_hash=identity,
                    label=self._next_bot_label(owner_user_id),
                    credentials_ciphertext=self._cipher.encrypt(owner_user_id, credentials),
                    conflict_message='该机器人已绑定其他用户', connected_message='企业微信已连接')
                if account:
                    self._runtime.restart_account(account['id'])
        return self.get_session(owner_user_id, row['id'])

    def _create_qr_session(self, *, owner_user_id, idempotency_key, account_id):
        expires_at = dt.datetime.now(dt.timezone.utc) + dt.timedelta(seconds=_QR_TTL_SECONDS)
        row, created = self._store.reserve_session(
            session_id=f'cs_{uuid.uuid4().hex}', owner_user_id=owner_user_id,
            provider='wecom', idempotency_key=idempotency_key,
            expires_at=expires_at, requested_account_id=account_id,
        )
        if created:
            row = self._prepare_qr(row, expires_at)
        return self.get_session(owner_user_id, row['id'])

    def _prepare_qr(self, row, expires_at):
        try:
            response = httpx.get(
                _QR_GENERATE_URL,
                params={'source': _QR_SOURCE, 'plat': _platform_code()},
                timeout=15,
            )
            response.raise_for_status()
            data = response.json().get('data') or {}
            scode = str(data.get('scode') or '')
            auth_url = str(data.get('auth_url') or '')
            if not scode or not auth_url:
                raise ValueError('missing QR session fields')
        except Exception as exc:
            _logger.warning('wecom_qr_create_failed session_id=%s', row['id'])
            self._store.mark_failed(
                row['id'], row['qr_version'], code='WECOM_QR_UNAVAILABLE',
                message='企业微信二维码暂时无法生成，请稍后重试', retryable=True,
            )
            raise GatewayError(
                503, 'WECOM_QR_UNAVAILABLE', '企业微信二维码暂时无法生成，请稍后重试', retryable=True,
            ) from exc
        state = {'scode': scode, 'qr_payload': auth_url}
        updated = self._store.set_qr_ready(
            row['id'], self._cipher.encrypt(str(row['owner_user_id']), state),
            expires_at, '请使用企业微信扫码并在手机上完成授权',
        )
        if not updated:
            raise GatewayError(409, 'INVALID_STATE', '连接会话状态已经变化')
        self._start_qr_worker(updated['id'], updated['qr_version'], str(updated['owner_user_id']))
        return updated

    def _start_qr_worker(self, session_id, qr_version, owner_user_id):
        threading.Thread(
            target=self._poll_qr, args=(session_id, qr_version, owner_user_id),
            name=f'wecom-login-{session_id[-8:]}-{qr_version}', daemon=True,
        ).start()

    def _poll_qr(self, session_id, qr_version, owner_user_id):
        while True:
            row = self._store.get_session(owner_user_id, session_id)
            if not row or row['qr_version'] != qr_version or row['status'] not in ('waiting_scan', 'scanned', 'confirming'):
                return
            if row['expires_at'] <= dt.datetime.now(dt.timezone.utc):
                self._store.mark_expired(session_id, qr_version)
                return
            try:
                state = self._cipher.decrypt(str(row['owner_user_id']), str(row['provider_state_ciphertext']))
                response = httpx.get(_QR_QUERY_URL, params={'scode': state['scode']}, timeout=15)
                response.raise_for_status()
                data = response.json().get('data') or {}
                if data.get('status') != 'success':
                    time.sleep(3)
                    continue
                bot_info = data.get('bot_info') or {}
                credentials = {'bot_id': str(bot_info.get('botid') or ''), 'secret': str(bot_info.get('secret') or '')}
                if not credentials['bot_id'] or not credentials['secret']:
                    raise ValueError('missing bot credentials')
                identity = hashlib.sha256(credentials['bot_id'].encode()).hexdigest()
                self._store.assert_identity_available(row['owner_user_id'], 'wecom', identity)
                requested_account_id = row.get('requested_account_id')
                if requested_account_id:
                    account = self._store.get_account(row['owner_user_id'], requested_account_id)
                    if not account or account['provider'] != 'wecom' or account['external_id_hash'] != identity:
                        self._store.mark_failed(session_id, qr_version, code='ACCOUNT_IDENTITY_MISMATCH', message='扫码授权的机器人与原账号不一致', retryable=False)
                        return
                confirming = self._store.update_active_session(
                    session_id=session_id, qr_version=qr_version,
                    expected_revision=row['revision'], status='confirming',
                    message='扫码成功，正在连接企业微信机器人',
                    state_ciphertext=row['provider_state_ciphertext'],
                )
                if not confirming:
                    return
                asyncio.run(verify_credentials(credentials))
                account = self._store.save_connected_account(
                    session_id=session_id, qr_version=qr_version,
                    expected_revision=confirming['revision'], owner_user_id=row['owner_user_id'],
                    provider='wecom', external_id_hash=identity,
                    label=self._next_bot_label(row['owner_user_id'], bot_info),
                    credentials_ciphertext=self._cipher.encrypt(row['owner_user_id'], credentials),
                    conflict_message='该机器人已绑定其他用户', connected_message='企业微信已连接',
                )
                if account:
                    self._runtime.restart_account(account['id'])
                return
            except GatewayError as exc:
                self._store.mark_failed(session_id, qr_version, code=exc.code, message=exc.message, retryable=False)
                return
            except Exception:
                _logger.exception('wecom_qr_poll_failed session_id=%s', session_id)
                self._store.mark_failed(
                    session_id, qr_version, code='WECOM_AUTH_FAILED',
                    message='企业微信扫码授权失败，请刷新二维码重试', retryable=True,
                )
                return

    def get_session(self, owner_user_id, session_id):
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(404, 'LOGIN_NOT_FOUND', '连接会话不存在')
        account = self._store.get_account(owner_user_id, row['account_id']) if row.get('account_id') else None
        error = None
        if row.get('error_code'):
            error = {'code': row['error_code'], 'message': row['error_message'],
                     'retryable': bool(row['error_retryable'])}
        qr = None
        if row['status'] in ('preparing', 'waiting_scan', 'scanned', 'confirming') and row.get('provider_state_ciphertext'):
            state = self._cipher.decrypt(str(row['owner_user_id']), str(row['provider_state_ciphertext']))
            if state.get('qr_payload'):
                qr = {'payload': state['qr_payload'], 'version': row['qr_version'], 'expires_at': row['expires_at'].isoformat()}
        allowed_actions = []
        if row['status'] in ('preparing', 'waiting_scan', 'scanned', 'confirming'):
            allowed_actions.append('cancel')
        if row['status'] == 'expired' or (row['status'] == 'failed' and row.get('error_retryable')):
            allowed_actions.append('refresh')
        return {'id': row['id'], 'provider': 'wecom', 'mode': 'qr_code' if qr or row['status'] != 'connected' else 'credentials', 'status': row['status'],
                'revision': row['revision'], 'message': row['message'], 'qr': qr, 'challenge': None,
                'poll_after_ms': 1000, 'allowed_actions': allowed_actions,
                'account': account_view(account) if account else None, 'error': error}

    def cancel_session(self, owner_user_id, session_id):
        self._store.cancel_session(owner_user_id, session_id)

    def refresh_session(self, owner_user_id, session_id):
        expires_at = dt.datetime.now(dt.timezone.utc) + dt.timedelta(seconds=_QR_TTL_SECONDS)
        row = self._store.restart_connection_session(
            owner_user_id=owner_user_id, session_id=session_id, expires_at=expires_at,
        )
        if not row:
            raise GatewayError(409, 'INVALID_STATE', '当前连接会话无法刷新')
        return self._session_view(self._prepare_qr(row, expires_at))

    def submit_challenge(self, **_kwargs):
        raise GatewayError(422, 'INVALID_REQUEST', '企业微信凭据模式不需要扫码验证码')

    def render(self, message):
        # Active bot messages support Markdown. Media is explicitly deferred to the task view.
        text = sanitize_channel_text(message.text)
        parts = self._renderer.text_parts(text)
        if message.metadata.get('artifacts'):
            parts += self._renderer.text_parts('结果包含附件，请在 LazyMind 任务中查看。')
        return parts

    def prepare_part(self, _message, _part, *, part_index, saved_state):
        return saved_state

    def send_part(self, message, part, *, part_index, idempotency_key, saved_state):
        if message.provider_context.get('transport') == 'cli':
            account = self._runtime.load_runtime_account(message.account_id)
            self._cli_call(account, '/message/aibot/send', {
                'chat_id': message.recipient_id, 'msg_type': 'markdown',
                'markdown': {'content': part['text']},
            })
        else:
            self._runtime.send(message.account_id, message.recipient_id, part['text'])
        return saved_state
