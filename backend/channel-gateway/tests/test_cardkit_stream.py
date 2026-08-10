from __future__ import annotations

import asyncio
import concurrent.futures
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from lark_channel.channel.errors import (
    FeishuChannelError,
    FeishuChannelErrorCode,
)
from channel_gateway.common.errors import RetryableProviderSideEffectError
from channel_gateway.common.domain.chat import CoreStreamUpdate
from channel_gateway.feishu.sdk import (
    _LarkCardReplyStream,
    _WorkspaceFeishuChannel,
    _stream_element_retry_delay,
)


class _ElementChannel:
    def __init__(self, errors):
        self.errors = list(errors)
        self.calls = []

    async def update_card_element_content(
        self,
        card_id,
        element_id,
        content,
        sequence,
    ):
        self.calls.append((card_id, element_id, content, sequence))
        if self.errors:
            raise self.errors.pop(0)


class _ScheduledElementChannel(_ElementChannel):
    def __init__(self, errors, finish_errors=None):
        super().__init__(errors)
        self.executor = concurrent.futures.ThreadPoolExecutor(max_workers=1)
        self.finished_cards = []
        self.finish_errors = list(finish_errors or [])

    def schedule(self, operation):
        return self.executor.submit(asyncio.run, operation)

    async def create_card_instance(self, _card):
        return 'card-1'

    async def send_card_by_reference(
        self,
        _chat_id,
        _card_id,
        receive_id_type='chat_id',
    ):
        return SimpleNamespace(
            success=True,
            message_id='message-1',
            error=None,
        )

    async def finish_streaming_card(self, card_id, sequence):
        self.finished_cards.append((card_id, sequence))
        if self.finish_errors:
            error = self.finish_errors.pop(0)
            if error is not None:
                raise error


class _Response:
    def __init__(
        self,
        code,
        msg='',
        retry_after=None,
        reset_after=None,
        status_code=None,
    ):
        self.code = code
        self.msg = msg
        headers = {}
        if retry_after is not None:
            headers['Retry-After'] = str(retry_after)
        if reset_after is not None:
            headers['x-ogw-ratelimit-reset'] = str(reset_after)
        self.raw = SimpleNamespace(
            headers=headers,
            status_code=status_code,
        )


class _CardElementAPI:
    def __init__(self, responses):
        self.responses = list(responses)
        self.requests = []

    async def acontent(self, request):
        self.requests.append(request)
        return self.responses.pop(0)


class _CardSettingsAPI:
    def __init__(self, responses):
        self.responses = list(responses)
        self.requests = []

    async def asettings(self, request):
        self.requests.append(request)
        return self.responses.pop(0)


def _workspace_channel(responses):
    api = _CardElementAPI(responses)
    client = SimpleNamespace(
        cardkit=SimpleNamespace(
            v1=SimpleNamespace(card_element=api),
        )
    )
    channel = object.__new__(_WorkspaceFeishuChannel)
    channel._driver = SimpleNamespace(_client=client)
    return channel, api


def _workspace_settings_channel(responses):
    api = _CardSettingsAPI(responses)
    client = SimpleNamespace(
        cardkit=SimpleNamespace(
            v1=SimpleNamespace(card=api),
        )
    )
    channel = object.__new__(_WorkspaceFeishuChannel)
    channel._driver = SimpleNamespace(_client=client)
    return channel, api


def _rate_limited_error(retry_after=0):
    error = FeishuChannelError(
        FeishuChannelErrorCode.RATE_LIMITED,
        'rate limited',
    )
    error.retry_after_seconds = retry_after
    return error


def _stream(channel) -> _LarkCardReplyStream:
    return _LarkCardReplyStream(
        channel=channel,
        chat_id='chat-1',
        initial_card={'elements': []},
        timeout_seconds=5,
    )


def _streaming_card(channel) -> _LarkCardReplyStream:
    return _LarkCardReplyStream(
        channel=channel,
        chat_id='chat-1',
        initial_card={
            'config': {'streaming_mode': True},
            'body': {
                'elements': [
                    {
                        'tag': 'markdown',
                        'element_id': 'lazymind_status',
                        'content': '',
                    },
                    {
                        'tag': 'markdown',
                        'element_id': 'lazymind_answer',
                        'content': '',
                    },
                ],
            },
        },
        timeout_seconds=5,
    )


class CardKitElementRetryTest(unittest.TestCase):
    def test_rate_limited_element_update_retries_same_sequence(self) -> None:
        channel, api = _workspace_channel([
            _Response(
                99991400,
                'request trigger frequency limit',
                reset_after=3,
            ),
            _Response(0),
        ])
        rendered = {}

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(),
        ) as sleep:
            sequence = asyncio.run(
                _stream(channel)._update_element(
                    'card-1',
                    'answer',
                    'hello',
                    4,
                    rendered,
                )
            )

        self.assertEqual(sequence, 5)
        self.assertEqual(len(api.requests), 2)
        request_bodies = [request.request_body for request in api.requests]
        self.assertEqual(
            [body.sequence for body in request_bodies],
            [5, 5],
        )
        self.assertEqual(
            [body.content for body in request_bodies],
            ['hello', 'hello'],
        )
        self.assertEqual(
            request_bodies[0].uuid,
            request_bodies[1].uuid,
        )
        self.assertEqual(
            sum(call.args[0] for call in sleep.await_args_list),
            3.0,
        )
        self.assertEqual(rendered, {'answer': 'hello'})

    def test_missing_response_code_fails_closed(self) -> None:
        channel, api = _workspace_channel([_Response(None)])
        rendered = {}

        with self.assertRaises(FeishuChannelError):
            asyncio.run(
                _stream(channel)._update_element(
                    'card-1',
                    'answer',
                    'hello',
                    0,
                    rendered,
                )
            )

        self.assertEqual(len(api.requests), 1)
        self.assertEqual(rendered, {})

    def test_http_429_without_known_body_code_is_rate_limited(self) -> None:
        channel, api = _workspace_channel([
            _Response(
                -1,
                'too many requests',
                reset_after=0.5,
                status_code=429,
            ),
            _Response(0, status_code=200),
        ])

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(),
        ) as sleep:
            asyncio.run(
                _stream(channel)._update_element(
                    'card-1',
                    'answer',
                    'hello',
                    0,
                    {},
                )
            )

        self.assertEqual(len(api.requests), 2)
        self.assertEqual(
            sum(call.args[0] for call in sleep.await_args_list),
            0.5,
        )

    def test_stale_stream_is_not_replayed_after_rate_limit(self) -> None:
        channel = _ElementChannel([_rate_limited_error()])
        stream = _stream(channel)
        stream._should_render = lambda: False

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(),
        ) as sleep, self.assertRaisesRegex(
            RuntimeError,
            'no longer current',
        ):
            asyncio.run(
                stream._update_element(
                    'card-1',
                    'answer',
                    'hello',
                    0,
                    {},
                )
            )

        self.assertEqual(len(channel.calls), 1)
        sleep.assert_not_awaited()

    def test_abort_during_backoff_prevents_replay(self) -> None:
        channel = _ElementChannel([_rate_limited_error(0.5)])
        stream = _stream(channel)

        async def abort_stream(_delay):
            stream.abort()

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(side_effect=abort_stream),
        ), self.assertRaisesRegex(RuntimeError, 'cancelled'):
            asyncio.run(
                stream._update_element(
                    'card-1',
                    'answer',
                    'hello',
                    0,
                    {},
                )
            )

        self.assertEqual(len(channel.calls), 1)

    def test_abort_after_finish_claim_does_not_cancel_retry(self) -> None:
        stream = _stream(_ElementChannel([]))
        stream._closed = True

        stream.abort()

        self.assertFalse(stream._abort_requested)

    def test_abort_during_backoff_closes_streaming_mode(self) -> None:
        channel = _ScheduledElementChannel([
            _rate_limited_error(0.5),
        ])
        stream = _streaming_card(channel)

        async def request_abort(_delay):
            stream._abort_requested = True

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(side_effect=request_abort),
        ):
            stream.update(CoreStreamUpdate(answer='hello'))
            self.assertTrue(stream.finish('hello'))

        self.assertEqual(len(channel.calls), 1)
        self.assertEqual(channel.finished_cards, [('card-1', 1)])
        channel.executor.shutdown(wait=True)

    def test_abort_close_failure_makes_stream_finish_fail_closed(self) -> None:
        channel = _ScheduledElementChannel(
            [_rate_limited_error(0.5)],
            finish_errors=[_rate_limited_error() for _ in range(5)],
        )
        stream = _streaming_card(channel)

        async def request_abort(_delay):
            stream._abort_requested = True

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(side_effect=request_abort),
        ):
            stream.update(CoreStreamUpdate(answer='hello'))
            self.assertFalse(stream.finish('hello'))

        self.assertEqual(len(channel.calls), 1)
        self.assertEqual(
            channel.finished_cards,
            [('card-1', 1) for _ in range(5)],
        )
        channel.executor.shutdown(wait=True)

    def test_stale_stream_during_backoff_closes_streaming_mode(self) -> None:
        channel = _ScheduledElementChannel([
            _rate_limited_error(0.5),
        ])
        stream = _streaming_card(channel)
        current = True
        stream._should_render = lambda: current

        async def make_stale(_delay):
            nonlocal current
            current = False

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(side_effect=make_stale),
        ):
            stream.update(CoreStreamUpdate(answer='hello'))
            self.assertTrue(stream.finish('hello'))

        self.assertEqual(len(channel.calls), 1)
        self.assertEqual(channel.finished_cards, [('card-1', 1)])
        channel.executor.shutdown(wait=True)

    def test_retry_budget_rejects_late_provider_retry_after(self) -> None:
        channel = _ElementChannel([_rate_limited_error(60)])

        with self.assertRaises(FeishuChannelError):
            asyncio.run(
                _stream(channel)._update_element(
                    'card-1',
                    'answer',
                    'hello',
                    0,
                    {},
                )
            )

        self.assertEqual(len(channel.calls), 1)

    def test_retry_budget_limits_slow_provider_call(self) -> None:
        class SlowChannel(_ElementChannel):
            async def update_card_element_content(self, *args):
                self.calls.append(args)
                await asyncio.sleep(1)

        channel = SlowChannel([])
        with patch(
            'channel_gateway.feishu.sdk._STREAM_ELEMENT_RETRY_BUDGET_SECONDS',
            0.01,
        ), self.assertRaises(asyncio.TimeoutError):
            asyncio.run(
                _stream(channel)._update_element(
                    'card-1',
                    'answer',
                    'hello',
                    0,
                    {},
                )
            )

        self.assertEqual(len(channel.calls), 1)

    def test_stream_close_retries_explicit_rate_limit(self) -> None:
        channel = _ScheduledElementChannel(
            [],
            finish_errors=[_rate_limited_error(0.5), None],
        )
        stream = _streaming_card(channel)

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(),
        ) as sleep:
            asyncio.run(stream._finish_streaming_card('card-1', 4))

        self.assertEqual(
            channel.finished_cards,
            [('card-1', 4), ('card-1', 4)],
        )
        self.assertEqual(
            sum(call.args[0] for call in sleep.await_args_list),
            0.5,
        )
        channel.executor.shutdown(wait=True)

    def test_stream_close_preserves_http_rate_limit_headers(self) -> None:
        channel, api = _workspace_settings_channel([
            _Response(
                -1,
                'too many requests',
                reset_after=3,
                status_code=429,
            ),
            _Response(0, status_code=200),
        ])

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(),
        ) as sleep:
            asyncio.run(
                _stream(channel)._finish_streaming_card('card-1', 4)
            )

        self.assertEqual(len(api.requests), 2)
        request_bodies = [request.request_body for request in api.requests]
        self.assertEqual([body.sequence for body in request_bodies], [4, 4])
        self.assertEqual(request_bodies[0].uuid, request_bodies[1].uuid)
        self.assertEqual(
            sum(call.args[0] for call in sleep.await_args_list),
            3.0,
        )

    def test_stream_close_exhaustion_stays_within_one_retry_round(self) -> None:
        channel = _ScheduledElementChannel(
            [],
            finish_errors=[_rate_limited_error() for _ in range(5)],
        )
        stream = _streaming_card(channel)

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(),
        ), self.assertRaisesRegex(
            RuntimeError,
            'stream close failed',
        ):
            asyncio.run(stream._finish_streaming_card('card-1', 4))

        self.assertEqual(
            channel.finished_cards,
            [('card-1', 4) for _ in range(5)],
        )
        channel.executor.shutdown(wait=True)

    def test_exhausted_rate_limit_keeps_rendered_state_uncommitted(self) -> None:
        channel = _ElementChannel([
            _rate_limited_error() for _ in range(5)
        ])
        rendered = {}

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(),
        ), self.assertRaises(FeishuChannelError):
            asyncio.run(
                _stream(channel)._update_element(
                    'card-1',
                    'answer',
                    'hello',
                    0,
                    rendered,
                )
            )

        self.assertEqual(len(channel.calls), 5)
        self.assertEqual(rendered, {})

    def test_exhausted_rate_limit_makes_stream_finish_fail_closed(self) -> None:
        channel = _ScheduledElementChannel([
            _rate_limited_error() for _ in range(5)
        ])
        stream = _LarkCardReplyStream(
            channel=channel,
            chat_id='chat-1',
            initial_card={
                'config': {'streaming_mode': True},
                'body': {
                    'elements': [
                        {
                            'tag': 'markdown',
                            'element_id': 'lazymind_status',
                            'content': '',
                        },
                        {
                            'tag': 'markdown',
                            'element_id': 'lazymind_answer',
                            'content': '',
                        },
                    ],
                },
            },
            timeout_seconds=5,
        )

        with patch(
            'channel_gateway.feishu.sdk.asyncio.sleep',
            new=AsyncMock(),
        ):
            stream.update(CoreStreamUpdate(
                thinking='',
                answer='hello',
                thinking_seconds=None,
            ))
            self.assertFalse(stream.finish('hello'))

        self.assertEqual(len(channel.calls), 5)
        self.assertEqual(channel.finished_cards, [('card-1', 1)])
        channel.executor.shutdown(wait=True)

    def test_direct_cardkit_rate_code_is_retryable(self) -> None:
        error = FeishuChannelError(
            FeishuChannelErrorCode.UNKNOWN,
            "update_card_element_content failed: "
            "{'code': 99991402, 'msg': 'too many requests'}"
        )

        self.assertEqual(_stream_element_retry_delay(error, 0), 0.5)

    def test_ambiguous_retryable_error_is_not_replayed(self) -> None:
        error = RetryableProviderSideEffectError(
            'provider may have accepted the request',
            retry_after_seconds=1,
        )

        self.assertIsNone(_stream_element_retry_delay(error, 0))

    def test_non_retryable_element_error_is_not_replayed(self) -> None:
        error = RuntimeError(
            "update_card_element_content failed: "
            "{'code': 230001, 'msg': 'invalid content'}"
        )
        channel = _ElementChannel([error])

        with self.assertRaisesRegex(RuntimeError, 'invalid content'):
            asyncio.run(
                _stream(channel)._update_element(
                    'card-1',
                    'answer',
                    'hello',
                    0,
                    {},
                )
            )

        self.assertEqual(len(channel.calls), 1)


if __name__ == '__main__':
    unittest.main()
