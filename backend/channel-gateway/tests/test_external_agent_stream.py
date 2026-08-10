from __future__ import annotations

import json
import unittest
from unittest.mock import patch

from channel_gateway.common.infrastructure.lazymind import LazyMindClient


class _StreamResponse:
    status_code = 200

    def __init__(self, frames: list[dict]):
        self._lines = [
            f'data: {json.dumps(frame, ensure_ascii=False)}'
            for frame in frames
        ] + ['data: [DONE]']

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False

    def iter_lines(self):
        return iter(self._lines)


class ExternalAgentStreamContractTest(unittest.TestCase):
    def test_direct_stream_uses_canonical_snapshot_without_buffering_events(self):
        response = _StreamResponse([
            {
                'conversation_id': 'conversation-1',
                'history_id': 'history-1',
                'event': {'type': 'run_attached', 'run_id': 'run-1'},
                'snapshot': {'run_id': 'run-1', 'status': 'running'},
            },
            {
                'conversation_id': 'conversation-1',
                'history_id': 'history-1',
                'event': {
                    'type': 'agent_message_delta',
                    'run_id': 'run-1',
                    'delta': 'hello',
                },
                'snapshot': {
                    'run_id': 'run-1',
                    'status': 'running',
                    'answer': 'hello',
                },
            },
            {
                'conversation_id': 'conversation-1',
                'history_id': 'history-1',
                'event': {
                    'type': 'turn_completed',
                    'run_id': 'run-1',
                    'message': 'hello',
                    'terminal': True,
                },
                'snapshot': {
                    'run_id': 'run-1',
                    'status': 'completed',
                    'answer': 'hello',
                },
            },
        ])
        client = LazyMindClient.__new__(LazyMindClient)
        client._base_url = 'http://core'
        client._timeout = 30
        updates = []
        with patch(
            'channel_gateway.common.infrastructure.lazymind.httpx.stream',
            return_value=response,
        ):
            state = client._consume_external_agent_stream(
                owner_user_id='user-1',
                request_id='request-1',
                conversation_id='conversation-1',
                text='hello',
                on_stream=updates.append,
            )

        self.assertTrue(state.saw_done)
        self.assertEqual(state.last_message, 'hello')
        self.assertEqual(state.events, [])
        self.assertEqual(state.deltas, [])
        self.assertEqual(
            updates[-1].external_event['snapshot']['status'],
            'completed',
        )


if __name__ == '__main__':
    unittest.main()
