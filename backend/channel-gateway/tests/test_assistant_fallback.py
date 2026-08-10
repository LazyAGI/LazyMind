from __future__ import annotations

import hashlib
import json
import unittest
from unittest.mock import patch

from channel_gateway.common.application.conversations import ConversationResult
from channel_gateway.common.application.replies import (
    ChannelReplyBuilder,
    project_core_presentations,
)
from channel_gateway.common.domain.chat import CoreEvent, CoreTurnResult
from channel_gateway.common.domain.channel import ClaimedOutbound
from channel_gateway.common.domain.commands import (
    SCHEMA_VERSION,
    ChatCommand,
    ChatParameters,
)
from channel_gateway.common.domain.outbound import (
    OutboundRenderer,
    inline_artifact_bytes,
)
from channel_gateway.common.errors import RetryableProviderSideEffectError
from channel_gateway.feishu.delivery import FeishuDeliveryProvider
from channel_gateway.feishu.domain import FeishuRuntimeError
from channel_gateway.feishu.presentation import (
    FeishuPresentationRenderer,
    _ask_elements,
)
from channel_gateway.feishu.runtime import FeishuRuntime
from channel_gateway.feishu.task_monitor import (
    FeishuTaskCardMonitor,
    _task_bindings,
    _task_images,
    _workflow_tasks,
)
from channel_gateway.feishu.workspace import FeishuWorkspaceState


class _Sender:
    def __init__(
        self,
        *,
        retry_new_updates=0,
        fail_image_sends=0,
    ):
        self.sent = []
        self.updated = []
        self.uploaded = []
        self.closed = False
        self.retry_new_updates = retry_new_updates
        self.fail_image_sends = fail_image_sends

    def update_card(self, *, message_id, card):
        self.updated.append((message_id, card))
        if message_id == 'message-new' and self.retry_new_updates:
            self.retry_new_updates -= 1
            raise RetryableProviderSideEffectError(
                'rate limited',
                retry_after_seconds=3,
            )
        if message_id == 'message-old':
            raise FeishuRuntimeError('200740 card entity has expired')

    def send_card(self, *, chat_id, card, idempotency_key):
        if (
            str(idempotency_key).endswith(':1')
            and self.fail_image_sends
        ):
            self.fail_image_sends -= 1
            raise FeishuRuntimeError('temporary image card failure')
        self.sent.append((chat_id, card, idempotency_key))
        return (
            'message-image-2'
            if str(idempotency_key).endswith(':2')
            else 'message-image'
            if str(idempotency_key).endswith(':1')
            else 'message-new'
        )

    def upload_image(self, *, content):
        self.uploaded.append(content)
        return 'image-key'

    def close(self):
        self.closed = True


class _Channels:
    def __init__(self, sender):
        self.sender = sender

    def create_sender(self, _credentials):
        return self.sender


class _Credentials:
    @staticmethod
    def load_runtime_account(_account_id):
        return {'credentials': {}, 'owner_user_id': 'owner-1'}


class _Store:
    def __init__(
        self,
        workspace,
        *,
        race_before_save=False,
        conversation_id='',
    ):
        self.workspace = dict(workspace)
        self.calls = []
        self.race_before_save = race_before_save
        self.conversation_id = conversation_id

    def get_feishu_workspace_state(self, _account_id, _address_hash):
        return dict(self.workspace)

    def get_route(self, _account_id, _address_hash):
        return self.conversation_id

    @staticmethod
    def get_new_conversation_draft(_account_id, _address_hash):
        return {}

    @staticmethod
    def get_selection_context(_account_id, _address_hash):
        return None

    def patch_feishu_workspace_state(
        self,
        _account_id,
        _address_hash,
        patch,
        operation_id='',
    ):
        if (
            operation_id
            and self.workspace.get('active_operation_id') != operation_id
        ):
            return dict(self.workspace)
        revision = int(self.workspace.get('revision') or 0)
        self.workspace.update(patch)
        self.workspace['revision'] = revision + 1
        return dict(self.workspace)

    def save_feishu_workspace_message(
        self,
        account_id,
        address_hash,
        message_id,
        operation_id,
        expected_message_id,
        expected_revision=None,
        *,
        advance_revision=True,
    ):
        self.calls.append((
            account_id,
            address_hash,
            message_id,
            operation_id,
            expected_message_id,
            expected_revision,
        ))
        if self.race_before_save:
            self.workspace['message_id'] = 'message-current'
            self.workspace['revision'] += 1
            self.race_before_save = False
        if (
            self.workspace.get('message_id') == expected_message_id
            and self.workspace.get('active_operation_id') == operation_id
            and (
                expected_revision is None
                or self.workspace.get('revision') == expected_revision
            )
        ):
            self.workspace['message_id'] = message_id
            if advance_revision:
                self.workspace['revision'] += 1
        return dict(self.workspace)

    def save_feishu_workspace_state_if_revision(
        self,
        _account_id,
        _address_hash,
        state,
        expected_revision,
    ):
        if self.workspace.get('revision') != expected_revision:
            return False
        self.workspace = dict(state)
        return True


class _Lazymind:
    @staticmethod
    def download_static_image(*, source, owner_user_id):
        return b'image'


def _workspace(message_id='message-old'):
    state = FeishuWorkspaceState(
        view='assistant',
        message_id=message_id,
        revision=3,
        active_operation_id='operation-1',
        assistant_mode='detail',
        assistant_selected_thread_id='thread-1',
    )
    return state.to_dict()


def _message(workspace):
    return ClaimedOutbound(
        outbox_id='outbox-1',
        provider='feishu',
        account_id='account-1',
        order_key='address-1',
        recipient_id='chat-1',
        provider_context={
            'chat_id': 'chat-1',
            'workspace_surface': 'assistant',
            'workspace_message_id': 'message-old',
            'workspace_stream_message_id': 'message-old',
            'workspace_operation_id': 'operation-1',
            'workspace_state': workspace,
            'assistant_view': {
                'kind': 'detail',
                'thread': {
                    'id': 'thread-1',
                    'name': 'Thread 1',
                    'cwd': '/workspace',
                    'available': True,
                    'created_by_lazymind': True,
                    'controlled_by_lazymind': False,
                },
                'turns': [],
                'offset': 0,
                'total_turns': 0,
                'snapshot': {
                    'conversation_id': 'conversation-1',
                    'status': 'completed',
                    'answer': 'done',
                    'control_release': 'unsubscribed',
                },
            },
        },
        text='done',
        intent_kind='reply',
        purpose='reply',
        metadata={},
        rendered_parts=[],
        next_part_index=0,
        provider_state={},
        attempt_count=1,
    )


class AssistantExpiredFallbackTest(unittest.TestCase):
    def test_action_refresh_follows_same_lineage_message_replacement(
        self,
    ) -> None:
        workspace = FeishuWorkspaceState(
            view='chat',
            message_id='message-old',
            revision=4,
            active_operation_id='operation-1',
        )
        store = _Store(workspace.to_dict())

        class _RetargetingSender(_Sender):
            def update_card(self, *, message_id, card):
                self.updated.append((message_id, card))
                if message_id == 'message-old':
                    store.workspace['message_id'] = 'message-new'

        sender = _RetargetingSender()
        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._store = store
        runtime._credentials = _Credentials()
        runtime._channels = _Channels(sender)

        runtime._refresh_action_card(
            'account-1',
            'message-old',
            {'config': {}, 'elements': []},
            'address-1',
            4,
            'operation-1',
            True,
        )

        self.assertEqual(
            [message_id for message_id, _card in sender.updated],
            ['message-old', 'message-new'],
        )

    def test_setting_result_follows_same_revision_message_replacement(
        self,
    ) -> None:
        source = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-old',
            revision=4,
            active_operation_id='operation-1',
        )
        current = FeishuWorkspaceState.from_dict(source.to_dict())
        current.message_id = 'message-new'
        store = _Store(
            current.to_dict(),
            conversation_id='conversation-a',
        )
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(_Sender()),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = ClaimedOutbound(
            outbox_id='outbox-setting',
            provider='feishu',
            account_id='account-1',
            order_key='address-1',
            recipient_id='chat-1',
            provider_context={
                'chat_id': 'chat-1',
                'workspace_surface': 'management',
                'workspace_state': source.to_dict(),
                'workspace_message_id': 'message-old',
                'workspace_conversation_id': 'conversation-a',
                'command_action': {
                    'command': 'conversation.settings.update',
                },
                'workspace_action': {
                    'kind': 'setting.update',
                    'view': 'capabilities',
                    'expected_conversation_id': 'conversation-a',
                },
            },
            text='mode auto',
            intent_kind='conversation.settings.update',
            purpose='reply',
            metadata={
                'presentations': [{
                    'kind': 'conversation_settings',
                    'conversation_id': 'conversation-a',
                    'workflow_enabled': True,
                    'workflow_mode': 'auto',
                }],
            },
            rendered_parts=[],
            next_part_index=0,
            provider_state={},
            attempt_count=1,
        )

        persisted = provider._persist_workspace_result(message)

        self.assertEqual(store.workspace['message_id'], 'message-new')
        self.assertEqual(store.workspace['revision'], 4)
        self.assertEqual(
            persisted.provider_context['workspace_message_id'],
            'message-new',
        )

    def test_management_results_are_transient_and_revision_fenced(self) -> None:
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
            revision=4,
            active_operation_id='operation-1',
        )
        store = _Store(
            workspace.to_dict(),
            conversation_id='conversation-a',
        )
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(_Sender()),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )

        def setting_message(source, mode, outbox_id):
            return ClaimedOutbound(
                outbox_id=outbox_id,
                provider='feishu',
                account_id='account-1',
                order_key='address-1',
                recipient_id='chat-1',
                provider_context={
                    'chat_id': 'chat-1',
                    'workspace_surface': 'management',
                    'workspace_state': dict(source),
                    'workspace_message_id': 'message-1',
                    'workspace_conversation_id': 'conversation-a',
                    'command_action': {
                        'command': 'conversation.settings.update',
                    },
                    'workspace_action': {
                        'kind': 'setting.update',
                        'view': 'capabilities',
                        'expected_conversation_id': 'conversation-a',
                    },
                },
                text=f'mode {mode}',
                intent_kind='conversation.settings.update',
                purpose='reply',
                metadata={
                    'presentations': [
                        {
                            'kind': 'conversation_settings',
                            'conversation_id': 'conversation-a',
                            'workflow_enabled': True,
                            'workflow_mode': mode,
                        }
                    ]
                },
                rendered_parts=[],
                next_part_index=0,
                provider_state={},
                attempt_count=1,
            )

        old_auto = setting_message(store.workspace, 'auto', 'outbox-auto')
        current = FeishuWorkspaceState.from_dict(store.workspace)
        current.revision = 5
        current.active_operation_id = 'operation-2'
        store.workspace = current.to_dict()

        stale = provider._persist_workspace_result(old_auto)
        fresh_dynamic = setting_message(
            store.workspace,
            'dynamic',
            'outbox-dynamic',
        )
        fresh = provider._persist_workspace_result(fresh_dynamic)

        self.assertTrue(
            stale.provider_context['_workspace_delivery_suppressed']
        )
        self.assertEqual(stale.metadata['presentations'], [])
        self.assertNotIn(
            '_workspace_delivery_suppressed',
            fresh.provider_context,
        )
        self.assertEqual(
            fresh.metadata['presentations'][0]['workflow_mode'],
            'dynamic',
        )
        self.assertEqual(store.workspace, current.to_dict())

    def test_stale_setting_result_does_not_replace_current_conversation(self) -> None:
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
            revision=4,
            active_operation_id='operation-2',
        )
        store = _Store(
            workspace.to_dict(),
            conversation_id='conversation-b',
        )
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(_Sender()),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = ClaimedOutbound(
            outbox_id='outbox-setting-a',
            provider='feishu',
            account_id='account-1',
            order_key='address-1',
            recipient_id='chat-1',
            provider_context={
                'chat_id': 'chat-1',
                'workspace_surface': 'management',
                'workspace_state': workspace.to_dict(),
                'workspace_message_id': 'message-1',
                'command_action': {
                    'command': 'conversation.settings.update',
                },
                'workspace_action': {
                    'kind': 'setting.update',
                    'view': 'capabilities',
                    'expected_conversation_id': 'conversation-a',
                },
            },
            text='stale A',
            intent_kind='conversation.settings.update',
            purpose='reply',
            metadata={
                'presentations': [
                    {
                        'kind': 'conversation_settings',
                        'conversation_id': 'conversation-a',
                        'workflow_mode': 'auto',
                    }
                ]
            },
            rendered_parts=[],
            next_part_index=0,
            provider_state={},
            attempt_count=1,
        )

        reconciled = provider._persist_workspace_result(message)

        self.assertTrue(
            reconciled.provider_context['_workspace_delivery_suppressed']
        )
        self.assertEqual(reconciled.metadata['presentations'], [])
        self.assertEqual(store.workspace, workspace.to_dict())

    def test_normal_result_never_persists_raw_chat_presentations(self) -> None:
        workspace = FeishuWorkspaceState(
            view='chat',
            message_id='message-1',
            revision=4,
            active_operation_id='operation-1',
        )
        store = _Store(workspace.to_dict())
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(_Sender()),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = ClaimedOutbound(
            outbox_id='outbox-bounded',
            provider='feishu',
            account_id='account-1',
            order_key='address-1',
            recipient_id='chat-1',
            provider_context={
                'chat_id': 'chat-1',
                'workspace_surface': 'reply',
                'workspace_state': workspace.to_dict(),
                'workspace_operation_id': 'operation-1',
            },
            text='done',
            intent_kind='chat',
            purpose='reply',
            metadata={
                'presentations': [
                    {
                        'kind': f'oversized-{index}',
                        'payload': 'x' * (33 * 1024),
                    }
                    for index in range(30)
                ] + [{
                    'kind': 'ask',
                    'ask_id': 'ask-1',
                    'questions': [],
                }],
            },
            rendered_parts=[],
            next_part_index=0,
            provider_state={},
            attempt_count=1,
        )

        provider._persist_workspace_result(message)

        self.assertNotIn('chat_presentations', store.workspace)

    def test_assistant_outbound_stays_a_workspace_card(self) -> None:
        message = _message(_workspace())

        parts = FeishuPresentationRenderer(
            OutboundRenderer(6000)
        ).render(message)

        self.assertEqual(len(parts), 1)
        self.assertEqual(parts[0]['kind'], 'card')
        self.assertTrue(parts[0]['workspace'])
        self.assertNotIn('workspace_presentations', parts[0])
        self.assertIn('Thread 1', str(parts[0]['card']))

    def test_successfully_streamed_assistant_skips_fallback_card(self) -> None:
        message = _message(_workspace())
        message.metadata['streamed_text'] = True

        parts = FeishuPresentationRenderer(
            OutboundRenderer(6000)
        ).render(message)

        self.assertEqual(parts, [])

    def test_task_monitor_uses_rendered_task_binding(self) -> None:
        message = _message(_workspace())
        message.provider_context['workspace_surface'] = 'reply'
        message.metadata.update({
            'task_monitor': True,
            'streamed_text': True,
            'presentations': [{
                'kind': 'task',
                'task_id': 'task-1',
                'conversation_id': 'conversation-1',
                'title': 'Task 1',
            }],
        })
        message.rendered_parts.extend(
            FeishuPresentationRenderer(OutboundRenderer(6000)).render(message)
        )

        self.assertEqual(len(message.rendered_parts), 1)
        self.assertEqual(
            _task_bindings(message),
            [(0, 'task-1', 'conversation-1')],
        )

    def test_task_monitor_keeps_full_lineage_and_selects_latest_images(
        self,
    ) -> None:
        tasks = [
            {
                'task_id': f'task-{index}',
                'seq_in_conversation': index,
                'agent_type': 'workflow_step',
                'title': f'workflow:step-{index}',
                'status': 'running',
                'artifacts': [
                    {
                        'content_type': 'text',
                        'slot': 'text',
                        'seq': artifact_index,
                        'value': {'text': 'ignore'},
                    }
                    for artifact_index in range(20)
                ] + [
                    {
                        'content_type': 'image',
                        'slot': 'image',
                        'seq': artifact_index,
                        'value': {
                            'url': f'/static-files/{index}-{artifact_index}.png',
                            'caption': 'c' * 500,
                        },
                    }
                    for artifact_index in range(30)
                ],
            }
            for index in range(30)
        ]
        workflow = _workflow_tasks(tasks, 'task-0')
        images = _task_images(workflow[0])
        self.assertEqual(len(workflow), 30)
        self.assertEqual(
            [task['task_id'] for task in workflow[-20:]],
            [f'task-{index}' for index in range(10, 30)],
        )
        self.assertEqual(len(images), 20)
        self.assertEqual(
            [source for _, source, _ in images],
            [f'/static-files/0-{index}.png' for index in range(10, 30)],
        )
        self.assertTrue(all(len(key) == 64 for key, _, _ in images))
        self.assertTrue(all(len(caption) == 300 for _, _, caption in images))

    def test_oversized_ask_is_bounded_and_has_no_quick_submit_action(
        self,
    ) -> None:
        projected = project_core_presentations((CoreEvent(
            source='chat',
            type='ask_pending',
            payload={
                'ask_id': 'ask-1',
                'title': 'Title',
                'questions': [{
                    'text': 'x' * (20 * 1024),
                    'type': 'single',
                    'choices': ['yes'],
                }],
            },
        ),))[0].to_dict()

        self.assertFalse(projected['submittable'])
        self.assertEqual(len(projected['questions'][0]['text']), 1000)
        self.assertNotIn(
            'ask_answers_structured',
            str(_ask_elements([projected], {'chat_id': 'chat-1'})),
        )

    def test_reply_builder_drops_raw_ask_and_task_events(self) -> None:
        events = (
            CoreEvent(
                source='chat',
                type='ask_pending',
                payload={
                    'ask_id': 'ask-1',
                    'questions': [{
                        'text': 'x' * (20 * 1024),
                        'type': 'single',
                        'choices': ['yes'],
                    }],
                },
            ),
            CoreEvent(
                source='chat',
                type='task_created',
                payload={
                    'task_id': 'task-1',
                    'conversation_id': 'conversation-1',
                    'title': 't' * 1000,
                    'summary': 's' * (20 * 1024),
                },
            ),
            CoreEvent(
                source='chat',
                type='artifact_created',
                payload={
                    'content_type': 'text',
                    'value': '{"text":"ok"}',
                },
            ),
        )
        reply = ChannelReplyBuilder(_Store({})).build(
            command=ChatCommand(
                schema_version=SCHEMA_VERSION,
                command='chat',
                parameters=ChatParameters(message='continue'),
            ),
            result=ConversationResult(
                text='done',
                turn=CoreTurnResult(
                    conversation_id='conversation-1',
                    history_id='history-1',
                    answer='done',
                    finish_reason='stop',
                    events=events,
                ),
            ),
            account_id='account-1',
            external_address_hash='address-1',
        )

        self.assertEqual(
            [event['type'] for event in reply.core_events],
            ['artifact_created'],
        )
        ask, task = [item.to_dict() for item in reply.presentations]
        self.assertFalse(ask['submittable'])
        self.assertEqual(len(task['title']), 200)
        self.assertEqual(len(task['summary']), 1000)

    def test_reply_builder_bounds_artifacts_and_sources(self) -> None:
        events = (
            CoreEvent(
                source='chat',
                type='artifact_created',
                payload={
                    'content_type': 'text',
                    'filename': 'too-large.txt',
                    'value': {'text': 'x' * (2 * 1024 * 1024 + 1)},
                },
            ),
            *(
                CoreEvent(
                    source='chat',
                    type='artifact_created',
                    payload={
                        'content_type': 'text',
                        'filename': f'artifact-{index}.txt',
                        'value': {'text': 'ok', 'raw': 'drop-me'},
                        'raw': 'drop-me',
                    },
                )
                for index in range(25)
            ),
        )
        reply = ChannelReplyBuilder(_Store({})).build(
            command=ChatCommand(
                schema_version=SCHEMA_VERSION,
                command='chat',
                parameters=ChatParameters(message='continue'),
            ),
            result=ConversationResult(
                text='done',
                turn=CoreTurnResult(
                    conversation_id='conversation-1',
                    history_id='history-1',
                    answer='done',
                    finish_reason='stop',
                    events=events,
                    sources=tuple(
                        {
                            'url': f'https://example.com/{index}',
                            'title': 't' * 500,
                            'raw': 'drop-me',
                        }
                        for index in range(25)
                    ),
                ),
            ),
            account_id='account-1',
            external_address_hash='address-1',
        )

        self.assertEqual(len(reply.core_events), 20)
        self.assertEqual(len(reply.sources), 20)
        self.assertEqual(
            set(reply.core_events[0]),
            {'type', 'payload'},
        )
        self.assertEqual(
            set(reply.core_events[0]['payload']),
            {'content_type', 'filename', 'value'},
        )
        self.assertEqual(
            reply.core_events[0]['payload']['value'],
            {'text': 'ok'},
        )
        self.assertEqual(
            inline_artifact_bytes(
                {'core_events': list(reply.core_events)},
                '0',
            ),
            b'ok',
        )
        self.assertEqual(set(reply.sources[0]), {'url', 'title'})
        self.assertEqual(len(reply.sources[0]['title']), 200)

    def test_expired_assistant_card_adopts_replacement_message(self) -> None:
        workspace = _workspace()
        store = _Store(workspace)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(workspace)
        part = provider._renderer.render(message)[0]

        saved = provider.send_part(
            message,
            part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )

        self.assertEqual(saved['message_id'], 'message-new')
        self.assertEqual(store.workspace['message_id'], 'message-new')
        self.assertEqual(store.workspace['revision'], 4)
        self.assertEqual(len(sender.sent), 1)
        self.assertTrue(sender.closed)

    def test_expired_management_card_keeps_rendered_revision(self) -> None:
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-old',
            revision=3,
            active_operation_id='operation-1',
        ).to_dict()
        store = _Store(workspace)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(workspace)
        message.provider_context['workspace_surface'] = 'management'
        message.provider_context.pop('assistant_view', None)
        message.provider_context['_workspace_result_complete'] = True
        message.metadata['presentations'] = [{
            'kind': 'capability',
            'groups': [],
        }]
        part = provider._renderer.render(message)[0]

        saved = provider.send_part(
            message,
            part,
            part_index=0,
            idempotency_key='outbox-management:0',
            saved_state={},
        )

        self.assertEqual(saved['message_id'], 'message-new')
        self.assertFalse(saved['workspace_stale'])
        self.assertEqual(store.workspace['message_id'], 'message-new')
        self.assertEqual(store.workspace['revision'], 3)
        self.assertIn(
            '"expected_revision": 3',
            json.dumps(part['card'], ensure_ascii=False),
        )

    def test_card_adoption_recovers_before_part_state_is_saved(self) -> None:
        workspace = _workspace()
        store = _Store(workspace)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        first_message = _message(workspace)
        part = provider._renderer.render(first_message)[0]
        provider.send_part(
            first_message,
            part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )

        recovered = provider.send_part(
            _message(workspace),
            part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )

        self.assertEqual(recovered['message_id'], 'message-new')
        self.assertFalse(recovered['workspace_stale'])
        self.assertEqual(store.workspace['revision'], 4)

    def test_losing_replacement_is_marked_expired(self) -> None:
        store = _Store(_workspace(), race_before_save=True)
        sender = _Sender(retry_new_updates=1)
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(_workspace())
        part = provider._renderer.render(message)[0]

        with patch(
            'channel_gateway.feishu.delivery.time.sleep'
        ) as sleep:
            saved = provider.send_part(
                message,
                part,
                part_index=0,
                idempotency_key='outbox-1:0',
                saved_state={},
            )

        self.assertEqual(saved['message_id'], 'message-current')
        self.assertEqual(store.workspace['message_id'], 'message-current')
        self.assertEqual(
            [message_id for message_id, _card in sender.updated],
            ['message-new', 'message-new'],
        )
        self.assertIn('消息已过期', str(sender.updated[-1][1]))
        sleep.assert_called_once_with(3.0)

    def test_stale_operation_is_rejected_before_same_card_update(self) -> None:
        current = _workspace()
        current['active_operation_id'] = 'operation-2'
        current['revision'] = 4
        store = _Store(current)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(_workspace())
        part = provider._renderer.render(message)[0]

        saved = provider.send_part(
            message,
            part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )

        self.assertEqual(saved['message_id'], 'message-old')
        self.assertTrue(saved['workspace_stale'])
        self.assertEqual(len(sender.sent), 1)
        self.assertEqual(
            [message_id for message_id, _card in sender.updated],
            ['message-new'],
        )

    def test_stale_card_fences_following_image_part(self) -> None:
        current = _workspace()
        current['active_operation_id'] = 'operation-2'
        current['revision'] = 4
        store = _Store(current)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(_workspace())
        card_part = provider._renderer.render(message)[0]
        card_state = provider.send_part(
            message,
            card_part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )
        restored_message = _message(_workspace())
        restored_message.provider_state['0'] = dict(card_state)
        sent_count = len(sender.sent)
        update_count = len(sender.updated)

        image_state = provider.send_part(
            restored_message,
            {'kind': 'image', 'source': 'asset-1', 'alt': 'Plot'},
            part_index=1,
            idempotency_key='outbox-1:1',
            saved_state={},
        )

        self.assertTrue(image_state['workspace_stale'])
        self.assertEqual(len(sender.sent), sent_count)
        self.assertEqual(len(sender.updated), update_count)
        self.assertEqual(sender.uploaded, [])

    def test_assistant_image_keeps_replacement_workspace_card(self) -> None:
        workspace = _workspace()
        store = _Store(workspace)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(workspace)
        card_part = provider._renderer.render(message)[0]
        saved = provider.send_part(
            message,
            card_part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )

        restored_message = _message(_workspace())
        restored_message.provider_state['0'] = dict(saved)
        saved = provider.send_part(
            restored_message,
            {'kind': 'image', 'source': 'asset-1', 'alt': 'Plot'},
            part_index=1,
            idempotency_key='outbox-1:1',
            saved_state={},
        )

        self.assertEqual(saved['image_key'], 'image-key')
        self.assertEqual(store.workspace['message_id'], 'message-image')
        self.assertEqual(sender.sent[-1][0], 'chat-1')
        self.assertIn('Thread 1', str(sender.sent[-1][1]))
        self.assertIn('image-key', str(sender.sent[-1][1]))

    def test_image_recovers_after_state_cas_before_send(self) -> None:
        workspace = _workspace()
        store = _Store(workspace)
        sender = _Sender(fail_image_sends=1)
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(workspace)
        card_part = provider._renderer.render(message)[0]
        card_state = provider.send_part(
            message,
            card_part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )
        restored = _message(workspace)
        restored.provider_state['0'] = dict(card_state)
        image_part = {'kind': 'image', 'source': 'asset-1', 'alt': 'Plot'}

        with self.assertRaisesRegex(
            FeishuRuntimeError,
            'temporary image card failure',
        ):
            provider.send_part(
                restored,
                image_part,
                part_index=1,
                idempotency_key='outbox-1:1',
                saved_state={},
            )
        self.assertEqual(store.workspace['revision'], 5)
        self.assertEqual(len(store.workspace['images']), 1)

        recovered = provider.send_part(
            restored,
            image_part,
            part_index=1,
            idempotency_key='outbox-1:1',
            saved_state={},
        )

        self.assertEqual(recovered['message_id'], 'message-image')
        self.assertFalse(recovered['workspace_stale'])
        self.assertEqual(store.workspace['revision'], 6)
        self.assertEqual(len(sender.uploaded), 1)

    def test_image_adoption_recovers_before_part_state_is_saved(self) -> None:
        workspace = _workspace()
        store = _Store(workspace)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(workspace)
        card_part = provider._renderer.render(message)[0]
        card_state = provider.send_part(
            message,
            card_part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )
        restored = _message(workspace)
        restored.provider_state['0'] = dict(card_state)
        image_part = {'kind': 'image', 'source': 'asset-1', 'alt': 'Plot'}
        provider.send_part(
            restored,
            image_part,
            part_index=1,
            idempotency_key='outbox-1:1',
            saved_state={},
        )

        recovered = provider.send_part(
            restored,
            image_part,
            part_index=1,
            idempotency_key='outbox-1:1',
            saved_state={},
        )

        self.assertEqual(recovered['message_id'], 'message-image')
        self.assertFalse(recovered['workspace_stale'])
        self.assertEqual(store.workspace['revision'], 6)
        self.assertEqual(len(sender.uploaded), 1)

    def test_multiple_images_follow_latest_persisted_part_lineage(self) -> None:
        workspace = _workspace()
        store = _Store(workspace)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(workspace)
        card_part = provider._renderer.render(message)[0]
        card_state = provider.send_part(
            message,
            card_part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )
        message.provider_state['0'] = dict(card_state)
        first_state = provider.send_part(
            message,
            {'kind': 'image', 'source': 'asset-1', 'alt': 'Plot 1'},
            part_index=1,
            idempotency_key='outbox-1:1',
            saved_state={},
        )
        message.provider_state['1'] = dict(first_state)

        second_state = provider.send_part(
            message,
            {'kind': 'image', 'source': 'asset-2', 'alt': 'Plot 2'},
            part_index=2,
            idempotency_key='outbox-1:2',
            saved_state={},
        )

        self.assertEqual(second_state['message_id'], 'message-image-2')
        self.assertFalse(second_state['workspace_stale'])
        self.assertEqual(store.workspace['revision'], 8)
        self.assertEqual(len(store.workspace['images']), 2)
        self.assertEqual(len(sender.uploaded), 2)

    def test_repeated_image_identity_still_advances_its_part_phase(self) -> None:
        workspace = _workspace()
        store = _Store(workspace)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(workspace)
        card_part = provider._renderer.render(message)[0]
        card_state = provider.send_part(
            message,
            card_part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )
        message.provider_state['0'] = dict(card_state)
        first_state = provider.send_part(
            message,
            {'kind': 'image', 'source': 'asset-1', 'alt': 'First'},
            part_index=1,
            idempotency_key='outbox-1:1',
            saved_state={},
        )
        message.provider_state['1'] = dict(first_state)

        second_state = provider.send_part(
            message,
            {'kind': 'image', 'source': 'asset-1', 'alt': 'Second'},
            part_index=2,
            idempotency_key='outbox-1:2',
            saved_state={},
        )

        self.assertEqual(second_state['message_id'], 'message-image-2')
        self.assertFalse(second_state['workspace_stale'])
        self.assertEqual(store.workspace['revision'], 8)
        self.assertEqual(len(store.workspace['images']), 1)
        self.assertEqual(store.workspace['images'][0]['caption'], 'Second')
        self.assertEqual(len(sender.uploaded), 2)

    def test_unrelated_revision_with_same_source_is_not_recovered(self) -> None:
        workspace = _workspace()
        store = _Store(workspace)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(workspace)
        card_part = provider._renderer.render(message)[0]
        card_state = provider.send_part(
            message,
            card_part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )
        current = FeishuWorkspaceState.from_dict(store.workspace)
        current.add_image(
            image_key='image-key',
            caption='Earlier',
            identity=hashlib.sha256(b'asset-1').hexdigest(),
            delivery_id='another-outbox:1',
        )
        current.advance()
        store.workspace = current.to_dict()
        restored = _message(workspace)
        restored.provider_state['0'] = dict(card_state)
        sent_count = len(sender.sent)

        result = provider.send_part(
            restored,
            {'kind': 'image', 'source': 'asset-1', 'alt': 'Plot'},
            part_index=1,
            idempotency_key='outbox-1:1',
            saved_state={},
        )

        self.assertTrue(result['workspace_stale'])
        self.assertEqual(len(sender.sent), sent_count)
        self.assertEqual(sender.uploaded, [])

    def test_poisoned_recovery_image_key_fails_closed(self) -> None:
        workspace = _workspace()
        store = _Store(workspace)
        sender = _Sender()
        provider = FeishuDeliveryProvider(
            store=store,
            credentials=_Credentials(),
            channels=_Channels(sender),
            renderer=OutboundRenderer(6000),
            lazymind=_Lazymind(),
        )
        message = _message(workspace)
        card_part = provider._renderer.render(message)[0]
        card_state = provider.send_part(
            message,
            card_part,
            part_index=0,
            idempotency_key='outbox-1:0',
            saved_state={},
        )
        store.workspace['revision'] = 5
        store.workspace['images'] = [{
            'image_key': 'x' * 2000,
            'caption': 'Poisoned',
            'identity': hashlib.sha256(b'asset-1').hexdigest(),
            'delivery_id': 'outbox-1:1',
        }]
        restored = _message(workspace)
        restored.provider_state['0'] = dict(card_state)
        sent_count = len(sender.sent)

        result = provider.send_part(
            restored,
            {'kind': 'image', 'source': 'asset-1', 'alt': 'Plot'},
            part_index=1,
            idempotency_key='outbox-1:1',
            saved_state={},
        )

        self.assertTrue(result['workspace_stale'])
        self.assertEqual(len(sender.sent), sent_count)
        self.assertEqual(sender.uploaded, [])


if __name__ == '__main__':
    unittest.main()
