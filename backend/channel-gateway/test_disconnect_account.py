import unittest

from channel_gateway.settings import Settings
from channel_gateway.wechat.service import GatewayError, WeChatConnectionService


class FakeStore:
    def __init__(self, account=None, deleted=True):
        self.account = account
        self.deleted = deleted
        self.delete_calls = []

    def get_account(self, owner_user_id, account_id):
        if (
            self.account
            and self.account['owner_user_id'] == owner_user_id
            and self.account['id'] == account_id
        ):
            return self.account
        return None

    def delete_account(self, owner_user_id, account_id):
        self.delete_calls.append((owner_user_id, account_id))
        return self.deleted


class DisconnectAccountTest(unittest.TestCase):
    def create_service(self, store, stopped):
        return WeChatConnectionService(
            settings=Settings(),
            store=store,
            cipher=object(),
            on_account_disconnected=stopped.append,
        )

    def test_disconnect_stops_runtime_and_deletes_owned_account(self):
        store = FakeStore(
            account={
                'id': 'ca_owned',
                'owner_user_id': 'user-1',
            },
        )
        stopped = []

        self.create_service(store, stopped).disconnect_account('user-1', 'ca_owned')

        self.assertEqual(stopped, ['ca_owned'])
        self.assertEqual(store.delete_calls, [('user-1', 'ca_owned')])

    def test_disconnect_does_not_reveal_another_users_account(self):
        store = FakeStore(
            account={
                'id': 'ca_owned',
                'owner_user_id': 'user-1',
            },
        )
        stopped = []

        with self.assertRaises(GatewayError) as raised:
            self.create_service(store, stopped).disconnect_account('user-2', 'ca_owned')

        self.assertEqual(raised.exception.http_status, 404)
        self.assertEqual(raised.exception.code, 'ACCOUNT_NOT_FOUND')
        self.assertEqual(stopped, [])
        self.assertEqual(store.delete_calls, [])

    def test_disconnect_reports_concurrent_state_change(self):
        store = FakeStore(
            account={
                'id': 'ca_owned',
                'owner_user_id': 'user-1',
            },
            deleted=False,
        )
        stopped = []

        with self.assertRaises(GatewayError) as raised:
            self.create_service(store, stopped).disconnect_account('user-1', 'ca_owned')

        self.assertEqual(raised.exception.http_status, 409)
        self.assertEqual(raised.exception.code, 'ACCOUNT_STATE_CHANGED')
        self.assertEqual(stopped, ['ca_owned'])


if __name__ == '__main__':
    unittest.main()
