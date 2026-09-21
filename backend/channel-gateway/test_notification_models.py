import unittest

from channel_gateway.app import TaskNotificationCreate
from channel_gateway.common.domain.channel import account_view


class TaskNotificationCreateTest(unittest.TestCase):
    def test_allows_gateway_to_resolve_default_recipient(self):
        payload = TaskNotificationCreate(
            event_id='event', task_id='task', schedule_id='schedule', event='succeeded',
            config_revision=1, title='title', body='body', content='summary',
            channel='wechat', account_id='account', recipient_id='',
        )
        self.assertEqual(payload.recipient_id, '')

    def test_wechat_account_exposes_pending_notification_activation(self):
        account = account_view({
            'id': 'wechat-account',
            'provider': 'wechat',
            'label': '微信 ClawBot',
            'status': 'connected',
            'runtime_status': 'running',
            'credentials_ciphertext': 'encrypted',
            'connected_at': None,
            'last_poll_at': None,
            'last_message_at': None,
            'last_error': None,
            'updated_at': None,
            'notification_ready': False,
        })

        self.assertFalse(account['capabilities']['notification_ready'])


if __name__ == '__main__':
    unittest.main()
