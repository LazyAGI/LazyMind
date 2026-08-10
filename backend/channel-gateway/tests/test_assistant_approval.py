from __future__ import annotations

import json
import unittest

from channel_gateway.feishu.workspace import (
    FeishuWorkspaceState,
    _assistant_request_elements,
)


def _request(
    kind: str,
    *,
    fields: list[dict] | None = None,
    actions: list[dict] | None = None,
    questions: list[dict] | None = None,
    error: str = '',
) -> dict:
    return {
        'request_id': 'request-1',
        'kind': kind,
        'summary': f'{kind} summary',
        'fields': fields or [],
        'actions': actions or [],
        'questions': questions or [],
        'error': error,
    }


class AssistantApprovalContractTest(unittest.TestCase):
    def test_all_four_core_views_render_bounded_action_ids(self) -> None:
        requests = [
            _request(
                'command_approval',
                fields=[
                    {'kind': 'command', 'value': 'echo hello'},
                    {'kind': 'cwd', 'value': '/workspace'},
                ],
                actions=[
                    {'id': 'a1', 'kind': 'allow_once'},
                    {'id': 'a2', 'kind': 'deny'},
                ],
            ),
            _request(
                'file_change_approval',
                fields=[
                    {'kind': 'file', 'value': 'update · README.md'},
                    {'kind': 'diff', 'value': '+hello', 'language': 'diff'},
                ],
                actions=[
                    {'id': 'a1', 'kind': 'allow_once'},
                    {'id': 'a2', 'kind': 'deny'},
                ],
            ),
            _request(
                'permissions_approval',
                fields=[{
                    'kind': 'permissions',
                    'value': '{"network":true}',
                    'language': 'json',
                }],
                actions=[
                    {'id': 'a1', 'kind': 'grant_turn'},
                    {'id': 'a2', 'kind': 'grant_session'},
                    {'id': 'a3', 'kind': 'deny'},
                ],
            ),
            _request(
                'user_input',
                actions=[
                    {'id': 'a1', 'kind': 'submit'},
                    {'id': 'a2', 'kind': 'deny'},
                ],
                questions=[{
                    'id': 'q1',
                    'question': 'Continue?',
                    'options': [{'label': 'Yes'}, {'label': 'No'}],
                }],
            ),
        ]
        state = FeishuWorkspaceState(
            view='assistant',
            active_operation_id='operation-1',
        )
        for request in requests:
            with self.subTest(kind=request['kind']):
                elements = _assistant_request_elements(
                    state,
                    'chat-1',
                    request,
                    'thread-1',
                    'run-1',
                )
                encoded = json.dumps(elements, ensure_ascii=False)
                self.assertLess(len(encoded.encode('utf-8')), 16 * 1024)
                self.assertIn('assistant.respond', encoded)
                self.assertIn('action_id', encoded)
                self.assertNotIn('availableDecisions', encoded)

    def test_user_input_renders_submit_and_deny(self) -> None:
        pending = _request(
            'user_input',
            actions=[
                {'id': 'a1', 'kind': 'submit'},
                {'id': 'a2', 'kind': 'deny'},
            ],
            questions=[{'id': 'q1', 'question': 'Continue?'}],
        )
        encoded = json.dumps(
            _assistant_request_elements(
                FeishuWorkspaceState(view='assistant'),
                'chat-1', pending, 'thread-1', 'run-1',
            ),
            ensure_ascii=False,
        )
        self.assertIn('form_submit', encoded)
        self.assertIn('"action_id": "a2"', encoded)

    def test_unsafe_core_view_offers_only_available_deny(self) -> None:
        pending = _request(
            'permissions_approval',
            actions=[{'id': 'a1', 'kind': 'deny'}],
            error='Codex permissions payload is invalid.',
        )
        state = FeishuWorkspaceState(
            view='assistant',
            active_operation_id='operation-1',
        )
        encoded = json.dumps(
            _assistant_request_elements(
                state, 'chat-1', pending, 'thread-1', 'run-1',
            ),
            ensure_ascii=False,
        )
        self.assertIn('Codex permissions payload is invalid.', encoded)
        self.assertIn('"action_id": "a1"', encoded)
        self.assertNotIn('operation.cancel', encoded)

if __name__ == '__main__':
    unittest.main()
