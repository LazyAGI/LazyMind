from dataclasses import replace

from channel_gateway.common.errors import GatewayError
from channel_gateway.common.domain.channel import account_view


class NotificationService:
    """Verify committed Core events, then reuse the shared channel outbox."""
    def __init__(self, store, core):
        self._store, self._core = store, core

    def enqueue(self, owner, payload):
        self._store.notification_context(owner, payload['account_id'], payload['recipient_id'], payload['channel'])
        event = self._core.verify_notification(owner, payload)
        return self._store.enqueue_notification(owner, payload, occurred_at=event.get('created_at', ''))

    def references(self, owner, account_id, cursor='', limit=20):
        if not self._store.get_account(owner, account_id):
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '频道账号不存在')
        return self._core.notification_references(owner, account_id, cursor, limit)

    def account_detail(self, owner, account_id):
        row = self._store.get_account(owner, account_id)
        if not row:
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '频道账号不存在')
        targets = self._store.notification_targets(owner, account_id, limit=2)['items']
        # An account with several known recipients requires an explicit choice.
        primary = targets[0] if len(targets) == 1 else None
        references = self.references(owner, account_id, limit=1)
        return {**account_view(row), 'primary_recipient': primary,
                'notification_reference_count': references['total']}

    def retry(self, owner, notice_id, key, confirmed):
        original = self._store.get_notification(owner, notice_id)
        payload = original['payload']
        receipt = self._store.notification_retry_receipt(owner, payload, notice_id, key)
        if receipt is not None:
            return receipt
        self._core.verify_notification(owner, payload, retry=True)
        self._store.notification_context(owner, payload['account_id'], payload['recipient_id'], payload['channel'])
        return self._store.enqueue_notification(
            owner, payload, retry_of=notice_id, idempotency_key=key, occurred_at=original['occurred_at'],
            confirm_duplicate_risk=confirmed)

    def prepare(self, outbound):
        owner = outbound.metadata['owner_user_id']
        retry = bool(outbound.metadata.get('retry_of'))
        event = self._core.verify_notification(owner, outbound.metadata['notification'], retry=retry)
        context = self._store.notification_context(
            owner, outbound.account_id, outbound.recipient_id, outbound.provider)
        self._core.claim_notification(owner, event['notification_id'], outbound.outbox_id, retry)
        return replace(outbound, provider_context=context)
