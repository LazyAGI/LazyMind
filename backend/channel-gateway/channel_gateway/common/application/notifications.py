from dataclasses import replace

from channel_gateway.common.errors import GatewayError
from channel_gateway.common.domain.channel import account_view


class NotificationService:
    """Verify committed Core events, then reuse the shared channel outbox."""
    def __init__(self, store, core, feishu_accounts=None, wecom=None):
        self._store, self._core = store, core
        self._feishu = feishu_accounts
        self._wecom = wecom

    def notification_targets(self, owner, account_id, cursor='', recipient_id='', limit=20):
        account = self._store.get_account(owner, account_id)
        if not account:
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '频道账号不存在')
        if account['provider'] == 'wecom' and not recipient_id:
            try:
                self._wecom.sync_notification_targets(owner, account)
            except GatewayError:
                cached = self._store.notification_targets(owner, account_id, limit=1)['items']
                if not cached:
                    raise
        return self._store.notification_targets(
            owner, account_id, cursor=cursor, recipient_id=recipient_id, limit=limit)

    def enqueue(self, owner, payload):
        if not payload.get('recipient_id') and payload.get('account_id'):
            account = self._store.get_account(owner, payload['account_id'])
            if account:
                recipient_id = account.get('default_recipient_id') or ''
                if recipient_id:
                    payload = {**payload, 'recipient_id': recipient_id}
        self._validate_target(owner, payload['account_id'], payload['recipient_id'], payload['channel'])
        event = self._core.verify_notification(owner, payload)
        return self._store.enqueue_notification(owner, payload, occurred_at=event.get('created_at', ''),
                                                source_notification_id=event['notification_id'])

    def references(self, owner, account_id, cursor='', limit=20):
        if not self._store.get_account(owner, account_id):
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '频道账号不存在')
        return self._core.notification_references(owner, account_id, cursor, limit)

    def account_detail(self, owner, account_id, *, include_references=True):
        row = self._store.get_account(owner, account_id)
        if not row:
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '频道账号不存在')
        targets = self._store.notification_targets(owner, account_id, limit=2)['items']
        # An account with several known recipients requires an explicit choice.
        primary = targets[0] if len(targets) == 1 else None
        references = self.references(owner, account_id, limit=1) if include_references else {'total': 0}
        default = self._store.notification_targets(owner, account_id,
                                                   recipient_id=row['default_recipient_id'])['items'] if row.get(
                                                       'default_recipient_id') else []
        return {**account_view(row), 'primary_recipient': primary,
                'default_recipient': default[0] if default else None,
                'notification_reference_count': references['total']}

    def notification_groups(self, owner, account_id, cursor='', limit=100):
        return self._feishu.notification_groups(owner, account_id, cursor, limit)

    def _validate_target(self, owner, account_id, recipient_id, provider):
        context = self._store.notification_context(owner, account_id, recipient_id, provider)
        if provider == 'feishu':
            targets = self._store.notification_targets(owner, account_id, recipient_id=recipient_id)['items']
            if targets and targets[0].get('kind') == 'group':
                self._feishu.validate_notification_group(owner, account_id, recipient_id)
        return context

    def set_default_recipient(self, owner, account_id, recipient_id):
        account = self._store.get_account(owner, account_id)
        if not account:
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '频道账号不存在')
        if recipient_id:
            self._validate_target(owner, account_id, recipient_id, account['provider'])
        row = self._store.set_default_recipient(owner, account_id, recipient_id, account['credential_revision'])
        return account_view(row)

    def retry(self, owner, notice_id, key, confirmed):
        original = self._store.get_notification(owner, notice_id)
        payload = original['payload']
        receipt = self._store.notification_retry_receipt(owner, payload, notice_id, key)
        if receipt is not None:
            return receipt
        event = self._core.verify_notification(owner, payload, retry=True)
        self._validate_target(owner, payload['account_id'], payload['recipient_id'], payload['channel'])
        return self._store.enqueue_notification(
            owner, payload, retry_of=notice_id, idempotency_key=key, occurred_at=original['occurred_at'],
            confirm_duplicate_risk=confirmed, source_notification_id=event['notification_id'])

    def prepare(self, outbound):
        owner = outbound.metadata['owner_user_id']
        retry = bool(outbound.metadata.get('retry_of'))
        event = self._core.verify_notification(owner, outbound.metadata['notification'], retry=retry)
        context = self._validate_target(
            owner, outbound.account_id, outbound.recipient_id, outbound.provider)
        self._core.claim_notification(owner, event['notification_id'], outbound.outbox_id, retry)
        return replace(outbound, provider_context=context)
