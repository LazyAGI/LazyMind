import unittest

from channel_gateway.common.channel_actions import ChannelReply
from channel_gateway.common.channel_message import ChannelMessageService
from channel_gateway.common.commands import ActionKind, ChatCommand
from channel_gateway.common.lazymind import LazyMindError


class _Store:
    def get_navigation_state(self, account_id, external_address_hash):
        return None

    def get_route(self, account_id, external_address_hash):
        return ''

    def get_selection_context(self, account_id, external_address_hash):
        return None


class _Shortcuts:
    def parse(self, **kwargs):
        return None


class _Classifier:
    def __init__(self, error):
        self._error = error

    def classify(self, **kwargs):
        raise self._error

    def catalog(self, **kwargs):
        return {}


class _Executor:
    def __init__(self):
        self.command = None

    def execute(self, **kwargs):
        self.command = kwargs['command']
        return ChannelReply(intent_kind=ActionKind.CHAT, text='ok')


class ChannelMessageFallbackTest(unittest.TestCase):
    def test_classifier_failure_falls_back_to_plain_chat(self):
        executor = _Executor()
        service = ChannelMessageService(
            store=_Store(),
            shortcuts=_Shortcuts(),
            classifier=_Classifier(LazyMindError('invalid classifier output')),
            executor=executor,
        )

        result = service.process(
            provider='wechat',
            account_id='account-1',
            external_address_hash='sender-1',
            owner_user_id='user-1',
            text='你好',
            request_id='request-1',
        )

        self.assertEqual(result.text, 'ok')
        self.assertIsInstance(executor.command, ChatCommand)
        self.assertEqual(executor.command.parameters.message, '你好')

    def test_unexpected_classifier_error_is_not_hidden(self):
        service = ChannelMessageService(
            store=_Store(),
            shortcuts=_Shortcuts(),
            classifier=_Classifier(RuntimeError('programming error')),
            executor=_Executor(),
        )

        with self.assertRaisesRegex(RuntimeError, 'programming error'):
            service.process(
                provider='wechat',
                account_id='account-1',
                external_address_hash='sender-1',
                owner_user_id='user-1',
                text='你好',
                request_id='request-1',
            )


if __name__ == '__main__':
    unittest.main()
