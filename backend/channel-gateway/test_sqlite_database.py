import datetime as dt
import tempfile
import unittest
from pathlib import Path

from channel_gateway.common.database import GatewayStore


class SQLiteGatewayStoreTest(unittest.TestCase):
    def setUp(self):
        self._temp_dir = tempfile.TemporaryDirectory()
        database = Path(self._temp_dir.name) / 'channel-gateway.db'
        self.store = GatewayStore(f'sqlite:///{database}')
        self.store.initialize()

    def tearDown(self):
        self._temp_dir.cleanup()

    def _connected_account(self):
        expires_at = dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5)
        self.store.reserve_session(
            session_id='cs_connected',
            owner_user_id='user-1',
            provider='wechat',
            idempotency_key=None,
            expires_at=expires_at,
        )
        return self.store.save_connected_account(
            session_id='cs_connected',
            qr_version=1,
            owner_user_id='user-1',
            provider='wechat',
            external_id_hash='wechat-user-hash',
            label='My WeChat',
            credentials_ciphertext='encrypted-credentials',
        )

    def test_empty_accounts_and_idempotent_session(self):
        self.assertEqual(self.store.list_accounts('user-1', 'wechat'), [])
        expires_at = dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5)

        row, created = self.store.reserve_session(
            session_id='cs_first',
            owner_user_id='user-1',
            provider='wechat',
            idempotency_key='request-1',
            expires_at=expires_at,
        )
        self.assertTrue(created)
        self.assertEqual(row['id'], 'cs_first')

        duplicate, duplicate_created = self.store.reserve_session(
            session_id='cs_duplicate',
            owner_user_id='user-1',
            provider='wechat',
            idempotency_key='request-1',
            expires_at=expires_at,
        )
        self.assertFalse(duplicate_created)
        self.assertEqual(duplicate['id'], 'cs_first')
        self.assertIsNotNone(
            self.store.recoverable_sessions()[0]['expires_at'].tzinfo,
        )

        ready = self.store.set_qr_ready('cs_first', 'encrypted-state', expires_at)
        self.assertEqual(ready['status'], 'waiting_scan')
        account = self.store.save_connected_account(
            session_id='cs_first',
            qr_version=1,
            owner_user_id='user-1',
            provider='wechat',
            external_id_hash='wechat-user-hash',
            label='My WeChat',
            credentials_ciphertext='encrypted-credentials',
        )
        self.assertEqual(account['status'], 'connected')
        accounts = self.store.list_accounts('user-1', 'wechat')
        self.assertEqual([row['id'] for row in accounts], [account['id']])
        self.assertEqual(accounts[0]['label'], 'My WeChat')

    def test_message_and_navigation_state(self):
        account = self._connected_account()
        account_id = account['id']
        address = 'external-address'

        lease = self.store.acquire_runtime_lease(account_id)
        self.assertIsNotNone(lease.execute('SELECT 1'))
        self.assertIsNone(self.store.acquire_runtime_lease(account_id))
        self.store.release_runtime_lease(lease)
        replacement_lease = self.store.acquire_runtime_lease(account_id)
        self.assertIsNotNone(replacement_lease)
        self.store.release_runtime_lease(replacement_lease)

        self.assertTrue(self.store.mark_welcome_sent(account_id))
        self.store.save_checkpoint(account_id, 'cursor-1', 25000)
        self.assertEqual(self.store.get_checkpoint(account_id)['cursor'], 'cursor-1')

        self.assertTrue(self.store.claim_message(account_id, 'message-1', 'worker-1'))
        self.assertTrue(
            self.store.save_pending_reply(
                account_id,
                'message-1',
                'worker-1',
                'reply',
                'chat',
                'wechat-user',
                'context-token',
            ),
        )
        self.assertEqual(
            self.store.get_pending_reply(account_id, 'message-1')['response_text'],
            'reply',
        )
        self.assertTrue(self.store.save_reply_media(account_id, 'message-1', 'media'))
        self.assertEqual(self.store.get_reply_media(account_id, 'message-1'), 'media')
        self.assertEqual(len(self.store.pending_replies(account_id)), 1)
        self.assertTrue(
            self.store.mark_message_processed(
                account_id,
                'message-1',
                'processed',
                claim_owner='worker-1',
            ),
        )

        self.store.begin_new_conversation(account_id, address, {'topic': 'desktop'})
        self.assertEqual(
            self.store.get_new_conversation_draft(account_id, address),
            {'topic': 'desktop'},
        )
        self.store.save_pending_turn(account_id, address, {'text': 'hello'})
        self.assertEqual(
            self.store.get_pending_turn(account_id, address),
            {'text': 'hello'},
        )
        expires_at = dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5)
        self.store.save_selection_snapshot(
            account_id,
            address,
            'conversation',
            [{'id': 'conversation-1'}],
            expires_at,
        )
        self.assertEqual(
            self.store.get_selection_snapshot(account_id, address),
            [{'id': 'conversation-1'}],
        )
        self.store.clear_selection_snapshot(account_id, address)
        self.assertIsNone(self.store.get_selection_snapshot(account_id, address))
        self.store.activate_conversation(
            account_id,
            address,
            'conversation-1',
            consume_pending_turn=True,
        )
        self.assertEqual(self.store.get_route(account_id, address), 'conversation-1')
        self.assertEqual(self.store.get_pending_turn(account_id, address), {})

        self.assertTrue(self.store.delete_account('user-1', account_id))
        self.assertEqual(self.store.list_accounts('user-1', 'wechat'), [])


if __name__ == '__main__':
    unittest.main()
