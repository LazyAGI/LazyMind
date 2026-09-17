import asyncio
import datetime as dt
import hashlib
import uuid

from channel_gateway.common.domain.channel import account_view, sanitize_channel_text
from channel_gateway.common.domain.outbound import OutboundRenderer
from channel_gateway.common.errors import GatewayError
from channel_gateway.wecom.runtime import verify_credentials


class WeComService:
    """Small connection/account/delivery adapter using the common store and worker."""
    def __init__(self, store, cipher, runtime):
        self._store, self._cipher, self._runtime = store, cipher, runtime
        self._renderer = OutboundRenderer(1800)

    def list_accounts(self, owner_user_id):
        return {'items': [account_view(row) for row in self._store.list_accounts(owner_user_id, 'wecom')]}

    def disconnect_account(self, owner_user_id, account_id):
        if not self._store.disconnect_account(owner_user_id, account_id):
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '企业微信账号不存在')
        self._runtime.stop_account(account_id)

    def create_session(self, *, owner_user_id, idempotency_key, credentials=None, account_id=None):
        if not credentials or not credentials.get('bot_id') or not credentials.get('secret'):
            raise GatewayError(422, 'INVALID_REQUEST', '请填写 BotID 和 Secret')
        if idempotency_key and len(idempotency_key) > 128:
            raise GatewayError(422, 'INVALID_IDEMPOTENCY_KEY', 'Idempotency-Key 长度不能超过 128 个字符')
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
                    label='企业微信 · ' + credentials['bot_id'],
                    credentials_ciphertext=self._cipher.encrypt(owner_user_id, credentials),
                    conflict_message='该机器人已绑定其他用户', connected_message='企业微信已连接')
                if account:
                    self._runtime.restart_account(account['id'])
        return self.get_session(owner_user_id, row['id'])

    def get_session(self, owner_user_id, session_id):
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(404, 'LOGIN_NOT_FOUND', '连接会话不存在')
        account = self._store.get_account(owner_user_id, row['account_id']) if row.get('account_id') else None
        error = None
        if row.get('error_code'):
            error = {'code': row['error_code'], 'message': row['error_message'],
                     'retryable': bool(row['error_retryable'])}
        return {'id': row['id'], 'provider': 'wecom', 'mode': 'credentials', 'status': row['status'],
                'revision': row['revision'], 'message': row['message'], 'qr': None, 'challenge': None,
                'poll_after_ms': 1000, 'allowed_actions': ['cancel'] if row['status'] == 'confirming' else [],
                'account': account_view(account) if account else None, 'error': error}

    def cancel_session(self, owner_user_id, session_id):
        self._store.cancel_session(owner_user_id, session_id)

    def refresh_session(self, owner_user_id, session_id):
        raise GatewayError(422, 'INVALID_REQUEST', '请重新提交企业微信凭据')

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
        self._runtime.send(message.account_id, message.recipient_id, part['text'])
        return saved_state
