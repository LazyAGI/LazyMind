from __future__ import annotations

import json
import tempfile
import threading
import unittest
from pathlib import Path
from types import SimpleNamespace

from channel_gateway.common.application.actions import ChannelActionExecutor
from channel_gateway.common.application.capabilities import (
    ActionMessage,
    CapabilityActions,
)
from channel_gateway.common.application.conversations import ConversationActions
from channel_gateway.common.application.intents import (
    ExactShortcutParser,
    _resume_continuation,
)
from channel_gateway.common.application.workers import _failure_message
from channel_gateway.common.domain.chat import (
    BASIC_CHAT_FEATURES,
    ChannelAttachment,
    ChannelExecutionContext,
    ChatOptions,
    CoreStreamUpdate,
    CoreTurnResult,
)
from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
    InboundEnvelope,
)
from channel_gateway.common.domain.commands import (
    COMMAND_ADAPTER,
    RESOURCE_CHANGE_ADAPTER,
    RESOLVED_CONVERSATION_TARGET_KEY,
    RESOLVED_RESOURCE_SELECTIONS_KEY,
    SCHEMA_VERSION,
    ChatCommand,
    ChatParameters,
    ConversationNewCommand,
    ConversationNewParameters,
    ConversationWorkflowModeSetting,
    SelectionContinuation,
)
from channel_gateway.common.errors import LazyMindError
from channel_gateway.common.infrastructure.lazymind import LazyMindClient
from channel_gateway.common.infrastructure.sqlite import SQLiteGatewayStore
from channel_gateway.feishu.delivery import (
    _ManagedReplyStream,
    _external_agent_conversation_id,
)
from channel_gateway.feishu.domain import FeishuInboundAction, FeishuInboundMenu
from channel_gateway.feishu.presentation import FeishuReplyRenderer
from channel_gateway.feishu.runtime import FeishuRuntime
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceRenderer,
    FeishuWorkspaceState,
)


class _WorkspaceStore:
    def __init__(
        self,
        state: FeishuWorkspaceState,
        draft: dict | None = None,
    ):
        self.state = state.to_dict()
        self.draft = dict(draft or {})
        self.save_count = 0

    def get_feishu_workspace_state(self, _account_id, _address_hash):
        return dict(self.state)

    def get_new_conversation_draft(self, _account_id, _address_hash):
        return dict(self.draft)

    def get_navigation_state(self, _account_id, _address_hash):
        return {'mode': 'new_pending' if self.draft else 'active'}

    def save_feishu_workspace_state_if_revision(
        self,
        _account_id,
        _address_hash,
        state,
        expected_revision,
    ):
        if int(self.state.get('revision') or 0) != expected_revision:
            return False
        self.state = dict(state)
        self.save_count += 1
        return True


class _ReplyStream:
    message_id = 'message-1'

    def __init__(self):
        self.updates = []
        self.finished = []
        self.aborted = False

    def update(self, snapshot):
        self.updates.append(snapshot)

    def finish(self, text):
        self.finished.append(text)
        return True

    def abort(self):
        self.aborted = True


class _Sender:
    def __init__(self):
        self.closed = False

    def close(self):
        self.closed = True


class _ActionStore:
    def get_route(self, _account_id, _address_hash):
        return None


class _ActionClient:
    pass


class _StaleSettingStore:
    def __init__(
        self,
        state: FeishuWorkspaceState,
        *,
        conversation_id='',
    ):
        self.state = state.to_dict()
        self.ingest_count = 0
        self.ingested = []
        self.draft = {'workflow_mode': 'dynamic'}
        self.workflow_updates = 0
        self.conversation_id = conversation_id
        self.claimed_message_keys = set()

    def get_route(self, _account_id, _address_hash):
        return self.conversation_id

    @staticmethod
    def get_pending_turn(_account_id, _address_hash):
        return {}

    def get_feishu_workspace_state(self, _account_id, _address_hash):
        return dict(self.state)

    @staticmethod
    def get_navigation_state(_account_id, _address_hash):
        return {'mode': 'new_pending'}

    def get_new_conversation_draft(self, _account_id, _address_hash):
        return dict(self.draft)

    def save_feishu_workspace_state_if_revision(
        self,
        _account_id,
        _address_hash,
        state,
        expected_revision,
    ):
        if int(self.state.get('revision') or 0) != expected_revision:
            return False
        self.state = dict(state)
        return True

    def claim_feishu_workspace_and_ingest(
        self,
        _account_id,
        _address_hash,
        state,
        expected_revision,
        expected_message_id,
        expected_operation_id,
        envelope,
        _runtime_fence,
        new_conversation_action=None,
    ):
        if (
            envelope.message_key in self.claimed_message_keys
            or int(self.state.get('revision') or 0) != expected_revision
            or str(self.state.get('message_id') or '')
            != expected_message_id
            or str(self.state.get('active_operation_id') or '')
            != expected_operation_id
        ):
            return False
        draft_action = dict(new_conversation_action or {})
        if draft_action.get('kind') == 'workflow_mode':
            if self.draft.get('workflow_mode', 'dynamic') != (
                draft_action.get('expected_mode')
            ):
                return False
            self.draft['workflow_mode'] = draft_action.get('mode')
            self.workflow_updates += 1
        elif draft_action.get('kind') == 'resource_toggle':
            resource = dict(draft_action.get('resource') or {})
            resource_id = str(resource.get('id') or '')
            mentions = list(self.draft.get('mentions') or [])
            key = (resource.get('type'), resource_id)
            present = any(
                (item.get('type'), item.get('resource_id')) == key
                for item in mentions
                if isinstance(item, dict)
            )
            if present:
                mentions = [
                    item
                    for item in mentions
                    if (item.get('type'), item.get('resource_id')) != key
                ]
            else:
                mentions.append({
                    'type': resource.get('type'),
                    'resource_id': resource_id,
                    'display_name': resource.get('name') or resource_id,
                })
            self.draft['mentions'] = mentions
        self.state = dict(state)
        self.claimed_message_keys.add(envelope.message_key)
        self.ingest_count += 1
        self.ingested.append(envelope)
        return True


class _SettingsClient:
    @staticmethod
    def get_conversation_detail(**_kwargs):
        return {
            'display_name': 'Conversation',
            'enable_workflow': True,
            'workflow_mode': 'dynamic',
            'enable_subagent': False,
        }


class _ConversationRecorder:
    def __init__(self):
        self.chat_args = None

    def chat(self, **kwargs):
        self.chat_args = kwargs
        return 'ok'


class _DraftNavigationStore:
    def __init__(self, *, conversation_id: str = ''):
        self.conversation_id = conversation_id
        self.draft = {
            'workflow_mode': 'dynamic',
            'enable_workflow': True,
        }
        self.pending_turn = {}
        self.activated = ''

    def get_route(self, _account_id, _address_hash):
        return self.conversation_id

    def get_navigation_state(self, _account_id, _address_hash):
        return {
            'mode': 'active' if self.conversation_id else 'new_pending',
        }

    def get_pending_turn(self, _account_id, _address_hash):
        return dict(self.pending_turn)

    def get_new_conversation_draft(self, _account_id, _address_hash):
        return dict(self.draft)

    def begin_new_conversation(
        self,
        _account_id,
        _address_hash,
        draft=None,
    ):
        self.conversation_id = ''
        self.draft = dict(draft or {})

    def activate_conversation(
        self,
        _account_id,
        _address_hash,
        conversation_id,
        _history_next_page_token=None,
        **_kwargs,
    ):
        self.activated = conversation_id


class _ConversationClient:
    def __init__(self):
        self.options = None

    def chat(self, **kwargs):
        self.options = kwargs['options']
        return CoreTurnResult(
            conversation_id='conversation-created',
            history_id='history-1',
            answer='done',
            finish_reason='stop',
        )


class _ReplyRecorder:
    @staticmethod
    def build(**kwargs):
        return kwargs


class ChannelExecutionContextTest(unittest.TestCase):
    @staticmethod
    def _worker():
        return SimpleNamespace(
            app_id='app-1',
            lease=SimpleNamespace(fence='fence-1'),
        )

    @staticmethod
    def _runtime_for_action_store(store):
        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._lock = threading.RLock()
        runtime._owner_routes = {('app-1', 'sender-1'): 'account-1'}
        runtime._accounts = {
            'account-1': SimpleNamespace(
                account_id='account-1',
                owner_user_id='owner-1',
                sender_id='sender-1',
            )
        }
        runtime._addresses = SimpleNamespace(
            direct=lambda *_args: SimpleNamespace(route_hash='address-1')
        )
        runtime._store = store
        runtime._direct_chats = {}
        runtime._schedule_action_card_refresh = (
            lambda *_args, **_kwargs: None
        )
        return runtime

    @staticmethod
    def _pending_mode_action():
        return FeishuInboundAction(
            message_id='message-1',
            chat_id='chat-1',
            sender_id='sender-1',
            action='command',
            text='切换 Workflow 执行方式',
            selection='',
            selection_id='',
            intended_chat_id='',
            ask_answers_structured=None,
            command_action={
                'schema_version': '1',
                'command': 'capability.list',
                'parameters': {
                    'capabilities': [
                        'knowledge_base',
                        'skill',
                        'workflow',
                        'tool',
                    ],
                    'evidence': ['切换 Workflow 执行方式'],
                },
            },
            workspace_action={
                'kind': 'new_session.workflow_mode',
                'mode': 'auto',
                'expected_mode': 'dynamic',
                'expected_view': 'capabilities',
                'expected_revision': 7,
                'expected_operation_id': 'new-session-1',
            },
        )

    def test_sqlite_workspace_patch_advances_revision_and_fences_stale_save(
        self,
    ) -> None:
        with tempfile.TemporaryDirectory() as directory:
            store = SQLiteGatewayStore(f'sqlite:///{directory}/gateway.db')
            store.initialize()
            account = store.connect_referenced_account(
                owner_user_id='owner-1',
                provider='feishu',
                external_id_hash='external-1',
                label='Feishu',
                credentials_ciphertext='ciphertext',
                status='connected',
            )
            account_id = str((account or {}).get('id') or '')
            initial = FeishuWorkspaceState(
                view='chat',
                revision=7,
                active_operation_id='operation-1',
            )
            store.begin_new_conversation(account_id, 'address-1', {})
            seeded = store.patch_feishu_workspace_state(
                account_id,
                'address-1',
                initial.to_dict(),
            )
            self.assertTrue(
                store.save_feishu_workspace_state_if_revision(
                    account_id,
                    'address-1',
                    initial.to_dict(),
                    seeded['revision'],
                )
            )

            patched = store.patch_feishu_workspace_state(
                account_id,
                'address-1',
                {'show_sources': False},
                operation_id='operation-1',
            )

            self.assertEqual(patched['revision'], 8)
            stale = FeishuWorkspaceState.from_dict(initial.to_dict())
            stale.show_process = False
            stale.advance()
            self.assertFalse(
                store.save_feishu_workspace_state_if_revision(
                    account_id,
                    'address-1',
                    stale.to_dict(),
                    7,
                )
            )
            current = store.get_feishu_workspace_state(
                account_id,
                'address-1',
            )
            self.assertFalse(current['show_sources'])
            self.assertEqual(current['revision'], 8)

            adopted = store.save_feishu_workspace_message(
                account_id,
                'address-1',
                'message-new',
                'operation-1',
                '',
                8,
                advance_revision=False,
            )
            self.assertEqual(adopted['message_id'], 'message-new')
            self.assertEqual(adopted['revision'], 8)

    def test_sqlite_workspace_save_initializes_missing_workspace(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            store = SQLiteGatewayStore(f'sqlite:///{directory}/gateway.db')
            store.initialize()
            account = store.connect_referenced_account(
                owner_user_id='owner-1',
                provider='feishu',
                external_id_hash='external-1',
                label='Feishu',
                credentials_ciphertext='ciphertext',
                status='connected',
            )
            account_id = str((account or {}).get('id') or '')
            for suffix, view in (
                ('absent', 'settings'),
                ('draft', 'assistant'),
            ):
                address = f'initial-{suffix}'
                if suffix == 'draft':
                    store.begin_new_conversation(
                        account_id,
                        address,
                        {'workflow_mode': 'dynamic'},
                    )
                target = FeishuWorkspaceState(
                    view=view,
                    message_id=f'message-{suffix}',
                    revision=1,
                    active_operation_id=f'operation-{suffix}',
                )

                self.assertTrue(
                    store.save_feishu_workspace_state_if_revision(
                        account_id,
                        address,
                        target.to_dict(),
                        0,
                    )
                )
                self.assertEqual(
                    store.get_feishu_workspace_state(account_id, address),
                    target.to_dict(),
                )
                self.assertFalse(
                    store.save_feishu_workspace_state_if_revision(
                        account_id,
                        address,
                        target.to_dict(),
                        0,
                    )
                )
                if suffix == 'draft':
                    self.assertEqual(
                        store.get_new_conversation_draft(
                            account_id,
                            address,
                        )['workflow_mode'],
                        'dynamic',
                    )

    def test_sqlite_task_artifact_children_and_parent_projection_are_fenced(
        self,
    ) -> None:
        with tempfile.TemporaryDirectory() as directory:
            store = SQLiteGatewayStore(f'sqlite:///{directory}/gateway.db')
            store.initialize()
            account = store.connect_referenced_account(
                owner_user_id='owner-1',
                provider='feishu',
                external_id_hash='external-1',
                label='Feishu',
                credentials_ciphertext='ciphertext',
                status='connected',
            )
            account_id = str((account or {}).get('id') or '')
            parent = ClaimedOutbound(
                outbox_id='co_parent',
                provider='feishu',
                account_id=account_id,
                order_key='address-1',
                recipient_id='chat-1',
                provider_context={'chat_id': 'chat-1'},
                text='',
                intent_kind='chat',
                purpose='reply',
                metadata={'task_monitor': True},
                rendered_parts=[{
                    'kind': 'card',
                    'task_id': 'task-1',
                    'conversation_id': 'conversation-1',
                }],
                next_part_index=1,
                provider_state={},
                attempt_count=1,
            )
            with store._connect() as connection:
                connection.execute(
                    """
                    INSERT INTO channel_outbox(
                        id, account_id, dedupe_key, provider, order_key,
                        recipient_id, provider_context, text, intent_kind,
                        purpose, metadata, rendered_parts, status
                    ) VALUES(
                        %s, %s, %s, %s, %s,
                        %s, %s::jsonb, '', 'chat',
                        'reply', %s::jsonb, %s::jsonb, 'sent'
                    )
                    """,
                    (
                        parent.outbox_id,
                        account_id,
                        'parent-task',
                        parent.provider,
                        parent.order_key,
                        parent.recipient_id,
                        json.dumps(parent.provider_context),
                        json.dumps(parent.metadata),
                        json.dumps(parent.rendered_parts),
                    ),
                )
            artifacts = [
                {
                    'artifact_key': str(index) * 64,
                    'source': f'/static-files/{index}.png',
                    'caption': f'image {index}',
                    'delivery_id': f'delivery-{index}',
                }
                for index in (1, 2)
            ]

            first = store.sync_task_artifact_outbounds(
                parent=parent,
                part_index=0,
                artifacts=artifacts,
            )
            replay = store.sync_task_artifact_outbounds(
                parent=parent,
                part_index=0,
                artifacts=artifacts,
            )

            self.assertEqual(first, replay)
            self.assertEqual(first['total'], 2)
            self.assertEqual(first['inflight'], 2)
            self.assertEqual(
                len(store.list_sent_task_outbounds(provider='feishu', limit=10)),
                1,
            )
            state = {
                'message_id': 'message-1',
                'task_monitor': {
                    'version': 4,
                    'signature': 'signature-1',
                    'workflow_terminal': False,
                    'delivery_settled': False,
                },
            }
            saved = store.compare_and_save_sent_task_monitor_state(
                outbox_id=parent.outbox_id,
                part_index=0,
                expected_revision=0,
                state=state,
                complete=False,
            )
            self.assertEqual(
                saved['task_monitor']['monitor_revision'],
                1,
            )
            stale = store.compare_and_save_sent_task_monitor_state(
                outbox_id=parent.outbox_id,
                part_index=0,
                expected_revision=0,
                state={**state, 'message_id': 'message-stale'},
                complete=False,
            )
            self.assertEqual(stale['message_id'], 'message-1')
            completed = store.compare_and_save_sent_task_monitor_state(
                outbox_id=parent.outbox_id,
                part_index=0,
                expected_revision=1,
                state={
                    **state,
                    'task_monitor': {
                        **state['task_monitor'],
                        'workflow_terminal': True,
                        'delivery_settled': True,
                    },
                },
                complete=True,
            )
            self.assertEqual(
                completed['task_monitor']['monitor_revision'],
                2,
            )
            self.assertEqual(
                store.list_sent_task_outbounds(provider='feishu', limit=10),
                [],
            )

    def test_sqlite_workspace_and_inbox_claim_is_atomic_and_deduplicated(
        self,
    ) -> None:
        with tempfile.TemporaryDirectory() as directory:
            store = SQLiteGatewayStore(f'sqlite:///{directory}/gateway.db')
            store.initialize()
            account = store.connect_referenced_account(
                owner_user_id='owner-1',
                provider='feishu',
                external_id_hash='external-1',
                label='Feishu',
                credentials_ciphertext='ciphertext',
                status='connected',
            )
            account_id = str((account or {}).get('id') or '')
            lease = store.acquire_runtime_lease(account_id)
            self.assertIsNotNone(lease)
            initial = FeishuWorkspaceState(
                view='chat',
                revision=4,
                active_operation_id='operation-old',
                message_id='message-old',
            )
            store.begin_new_conversation(account_id, 'address-1', {})
            seeded = store.patch_feishu_workspace_state(
                account_id,
                'address-1',
                initial.to_dict(),
            )
            self.assertTrue(
                store.save_feishu_workspace_state_if_revision(
                    account_id,
                    'address-1',
                    initial.to_dict(),
                    seeded['revision'],
                )
            )
            target = FeishuWorkspaceState.from_dict(initial.to_dict())
            target.begin_operation('message-new')
            target.message_id = 'card-new'
            target.advance()
            envelope = InboundEnvelope(
                provider='feishu',
                account_id=account_id,
                message_key='message-new',
                order_key='address-1',
                external_address_hash='address-1',
                owner_user_id='owner-1',
                recipient_id='chat-1',
                text='hello',
                provider_context={'workspace_state': target.to_dict()},
            )
            try:
                self.assertTrue(
                    store.claim_feishu_workspace_and_ingest(
                        account_id,
                        'address-1',
                        target.to_dict(),
                        4,
                        'message-old',
                        'operation-old',
                        envelope,
                        lease.fence,
                        new_conversation_action={
                            'kind': 'resource_toggle',
                            'resource': {
                                'type': 'knowledge_base',
                                'id': 'kb-1',
                                'name': 'KB 1',
                            },
                        },
                    )
                )
                persisted = store.get_feishu_workspace_state(
                    account_id,
                    'address-1',
                )
                self.assertEqual(persisted['message_id'], 'card-new')
                self.assertTrue(
                    store.has_active_inbound(account_id, 'address-1')
                )
                self.assertEqual(
                    store.get_new_conversation_draft(
                        account_id,
                        'address-1',
                    )['search_config']['dataset_list'],
                    [{'id': 'kb-1'}],
                )
                self.assertFalse(
                    store.claim_feishu_workspace_and_ingest(
                        account_id,
                        'address-1',
                        target.to_dict(),
                        4,
                        'message-old',
                        'operation-old',
                        envelope,
                        lease.fence,
                        new_conversation_action={
                            'kind': 'resource_toggle',
                            'resource': {
                                'type': 'knowledge_base',
                                'id': 'kb-1',
                                'name': 'KB 1',
                            },
                        },
                    )
                )
                self.assertEqual(
                    store.get_new_conversation_draft(
                        account_id,
                        'address-1',
                    )['search_config']['dataset_list'],
                    [{'id': 'kb-1'}],
                )
                queued = FeishuWorkspaceState.from_dict(persisted)
                queued.begin_operation('message-queued')
                queued.advance()
                queued_envelope = InboundEnvelope(
                    provider='feishu',
                    account_id=account_id,
                    message_key='message-queued',
                    order_key='address-1',
                    external_address_hash='address-1',
                    owner_user_id='owner-1',
                    recipient_id='chat-1',
                    text='queued',
                    provider_context={
                        'workspace_state': queued.to_dict(),
                    },
                )
                self.assertFalse(
                    store.claim_feishu_workspace_and_ingest(
                        account_id,
                        'address-1',
                        queued.to_dict(),
                        persisted['revision'],
                        'card-new',
                        'message-new',
                        queued_envelope,
                        lease.fence,
                    )
                )

                terminal = FeishuWorkspaceState.from_dict(persisted)
                terminal.view = 'settings'
                terminal.advance()
                self.assertTrue(
                    store.save_feishu_workspace_state_if_revision(
                        account_id,
                        'address-1',
                        terminal.to_dict(),
                        persisted['revision'],
                    )
                )
                self.assertFalse(
                    store.claim_feishu_workspace_and_ingest(
                        account_id,
                        'address-1',
                        target.to_dict(),
                        terminal.revision,
                        'card-new',
                        'message-new',
                        envelope,
                        lease.fence,
                    )
                )
                current = store.get_feishu_workspace_state(
                    account_id,
                    'address-1',
                )
                self.assertEqual(current['view'], 'settings')
                self.assertEqual(current['revision'], terminal.revision)

                store.begin_new_conversation(
                    account_id,
                    'address-2',
                    {'workflow_mode': 'dynamic'},
                )
                mode_source = FeishuWorkspaceState(
                    revision=1,
                    active_operation_id='new-session-2',
                )
                store.patch_feishu_workspace_state(
                    account_id,
                    'address-2',
                    mode_source.to_dict(),
                )
                mode_target = FeishuWorkspaceState.from_dict(
                    mode_source.to_dict()
                )
                mode_target.advance()
                mode_envelope = InboundEnvelope(
                    provider='feishu',
                    account_id=account_id,
                    message_key='mode-action',
                    order_key='address-2',
                    external_address_hash='address-2',
                    owner_user_id='owner-1',
                    recipient_id='chat-1',
                    text='mode',
                    provider_context={
                        'workspace_state': mode_target.to_dict(),
                    },
                )
                self.assertTrue(
                    store.claim_feishu_workspace_and_ingest(
                        account_id,
                        'address-2',
                        mode_target.to_dict(),
                        1,
                        '',
                        'new-session-2',
                        mode_envelope,
                        lease.fence,
                        new_conversation_action={
                            'kind': 'workflow_mode',
                            'expected_mode': 'dynamic',
                            'mode': 'auto',
                        },
                    )
                )
                self.assertEqual(
                    store.get_new_conversation_draft(
                        account_id,
                        'address-2',
                    )['workflow_mode'],
                    'auto',
                )
            finally:
                if lease is not None:
                    lease.close()

    def test_sqlite_management_claim_rejects_invalid_lineage_and_draft(
        self,
    ) -> None:
        with tempfile.TemporaryDirectory() as directory:
            store = SQLiteGatewayStore(f'sqlite:///{directory}/gateway.db')
            store.initialize()
            account = store.connect_referenced_account(
                owner_user_id='owner-1',
                provider='feishu',
                external_id_hash='external-1',
                label='Feishu',
                credentials_ciphertext='ciphertext',
                status='connected',
            )
            account_id = str((account or {}).get('id') or '')
            lease = store.acquire_runtime_lease(account_id)
            self.assertIsNotNone(lease)

            def rejected_claim(
                suffix: str,
                *,
                action: dict,
                revision_offset: int = 0,
                expected_operation_id: str | None = None,
                activate: bool = False,
            ) -> None:
                address = f'negative-{suffix}'
                store.begin_new_conversation(
                    account_id,
                    address,
                    {'workflow_mode': 'dynamic'},
                )
                source = FeishuWorkspaceState(
                    view='capabilities',
                    message_id=f'card-{suffix}',
                    active_operation_id=f'operation-{suffix}',
                )
                source = FeishuWorkspaceState.from_dict(
                    store.patch_feishu_workspace_state(
                        account_id,
                        address,
                        source.to_dict(),
                    )
                )
                if activate:
                    store.activate_conversation(
                        account_id,
                        address,
                        f'conversation-{suffix}',
                    )
                before_workspace = store.get_feishu_workspace_state(
                    account_id,
                    address,
                )
                before_draft = store.get_new_conversation_draft(
                    account_id,
                    address,
                )
                before_route = store.get_route(account_id, address)
                target = FeishuWorkspaceState.from_dict(source.to_dict())
                target.begin_operation(f'action-{suffix}')
                target.advance()
                envelope = InboundEnvelope(
                    provider='feishu',
                    account_id=account_id,
                    message_key=f'action-{suffix}',
                    order_key=address,
                    external_address_hash=address,
                    owner_user_id='owner-1',
                    recipient_id='chat-1',
                    text='action',
                    provider_context={
                        'workspace_state': target.to_dict(),
                    },
                )
                self.assertFalse(
                    store.claim_feishu_workspace_and_ingest(
                        account_id,
                        address,
                        target.to_dict(),
                        source.revision + revision_offset,
                        source.message_id,
                        (
                            source.active_operation_id
                            if expected_operation_id is None
                            else expected_operation_id
                        ),
                        envelope,
                        lease.fence,
                        new_conversation_action=action,
                    )
                )
                self.assertEqual(
                    store.get_feishu_workspace_state(account_id, address),
                    before_workspace,
                )
                self.assertEqual(
                    store.get_new_conversation_draft(account_id, address),
                    before_draft,
                )
                self.assertEqual(
                    store.get_route(account_id, address),
                    before_route,
                )
                self.assertFalse(store.has_active_inbound(account_id, address))

            try:
                rejected_claim(
                    'revision',
                    revision_offset=1,
                    action={
                        'kind': 'workflow_mode',
                        'expected_mode': 'dynamic',
                        'mode': 'auto',
                    },
                )
                rejected_claim(
                    'operation',
                    expected_operation_id='wrong-operation',
                    action={
                        'kind': 'workflow_mode',
                        'expected_mode': 'dynamic',
                        'mode': 'auto',
                    },
                )
                rejected_claim(
                    'mode',
                    action={
                        'kind': 'workflow_mode',
                        'expected_mode': 'auto',
                        'mode': 'dynamic',
                    },
                )
                rejected_claim(
                    'action',
                    action={'kind': 'unknown'},
                )
                rejected_claim(
                    'route',
                    activate=True,
                    action={
                        'kind': 'workflow_mode',
                        'expected_mode': 'dynamic',
                        'mode': 'auto',
                    },
                )
            finally:
                if lease is not None:
                    lease.close()

    def test_generic_stream_does_not_offer_unfenced_cancel(self) -> None:
        state = FeishuWorkspaceState(
            view='chat',
            message_id='card-1',
            revision=3,
            active_operation_id='operation-1',
        )
        card = FeishuReplyRenderer.render(
            provider_context={
                'chat_id': 'chat-1',
                'workspace_state': state.to_dict(),
            },
            text='',
            streaming=True,
        )
        self.assertFalse(any(
            element.get('name') == 'cancel_generation'
            for element in card['body']['elements']
        ))

    def test_assistant_cancel_failure_returns_error_card(self) -> None:
        workspace = FeishuWorkspaceState(
            view='assistant',
            assistant_mode='detail',
            assistant_selected_thread_id='thread-1',
            message_id='message-1',
            revision=3,
            active_operation_id='operation-1',
        )
        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._read_assistant_detail = lambda **_kwargs: {
            'kind': 'detail',
            'snapshot': {
                'conversation_id': 'conversation-1',
                'run_id': 'run-1',
                'status': 'running',
            },
        }

        def fail_interrupt(**_kwargs):
            raise RuntimeError('interrupt failed')

        runtime._core = SimpleNamespace(
            interrupt_external_conversation=fail_interrupt,
        )
        runtime._store = _WorkspaceStore(workspace)
        inbound = ClaimedInbound(
            inbox_id='inbox-1',
            provider='feishu',
            account_id='account-1',
            message_key='operation-1',
            order_key='address-1',
            external_address_hash='address-1',
            owner_user_id='owner-1',
            recipient_id='chat-1',
            text='cancel',
            provider_context={
                'workspace_surface': 'assistant',
                'workspace_state': workspace.to_dict(),
                'assistant_action': {
                    'kind': 'assistant.cancel',
                    'run_id': 'run-1',
                },
            },
            attempt_count=1,
        )

        result = runtime.handle_inbound_action(inbound)

        self.assertIsNotNone(result)
        self.assertIn(
            'interrupt failed',
            json.dumps(result.provider_context['assistant_view']),
        )
        self.assertEqual(runtime._store.state['revision'], 4)

    def test_assistant_action_replay_does_not_repeat_core_side_effect(self) -> None:
        source = FeishuWorkspaceState(
            view='assistant',
            assistant_mode='detail',
            assistant_selected_thread_id='thread-1',
            message_id='message-1',
            revision=3,
            active_operation_id='operation-1',
        )
        current = FeishuWorkspaceState.from_dict(source.to_dict())
        current.advance()
        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._store = _WorkspaceStore(current)
        runtime._read_assistant_detail = lambda **_kwargs: {
            'kind': 'detail',
            'snapshot': {'status': 'ready'},
        }

        def unexpected_execute(**_kwargs):
            raise AssertionError('Core action must not be repeated')

        runtime._execute_remote_assistant_action = unexpected_execute
        inbound = ClaimedInbound(
            inbox_id='inbox-1',
            provider='feishu',
            account_id='account-1',
            message_key='operation-1',
            order_key='address-1',
            external_address_hash='address-1',
            owner_user_id='owner-1',
            recipient_id='chat-1',
            text='',
            provider_context={
                'workspace_surface': 'assistant',
                'workspace_state': source.to_dict(),
                'assistant_action': {'kind': 'assistant.release'},
            },
            attempt_count=2,
        )

        result = runtime.handle_inbound_action(inbound)

        self.assertIsNotNone(result)
        self.assertEqual(
            result.provider_context['workspace_state']['revision'],
            4,
        )
        self.assertEqual(runtime._store.save_count, 0)
        self.assertEqual(
            result.provider_context['assistant_view']['snapshot']['status'],
            'ready',
        )

    def test_settings_menu_renders_the_persisted_target_lineage(self) -> None:
        workspace = FeishuWorkspaceState(
            view='chat',
            message_id='message-old',
            revision=3,
            active_operation_id='operation-old',
        )

        class Store:
            state = workspace.to_dict()

            @classmethod
            def get_feishu_workspace_state(cls, *_args):
                return dict(cls.state)

            @staticmethod
            def get_route(*_args):
                return ''

            @staticmethod
            def get_navigation_state(*_args):
                return {'mode': 'active'}

            @staticmethod
            def get_new_conversation_draft(*_args):
                return {}

            @classmethod
            def save_feishu_workspace_state_if_revision(
                cls,
                _account_id,
                _address_hash,
                state,
                expected_revision,
            ):
                if cls.state['revision'] != expected_revision:
                    return False
                cls.state = dict(state)
                return True

        class Sender:
            cards = []

            @classmethod
            def send_card(cls, **kwargs):
                cls.cards.append(dict(kwargs))
                return 'message-new'

            @staticmethod
            def close():
                return None

        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._lock = threading.RLock()
        runtime._owner_routes = {('app-1', 'sender-1'): 'account-1'}
        runtime._accounts = {
            'account-1': SimpleNamespace(
                account_id='account-1',
                owner_user_id='owner-1',
                sender_id='sender-1',
            )
        }
        runtime._direct_chats = {'account-1': 'chat-1'}
        runtime._addresses = SimpleNamespace(
            direct=lambda *_args: SimpleNamespace(route_hash='address-1')
        )
        runtime._store = Store()
        runtime._credentials = SimpleNamespace(
            load_runtime_account=lambda *_args: {'credentials': object()}
        )
        runtime._channels = SimpleNamespace(
            create_sender=lambda *_args: Sender()
        )
        menu = FeishuInboundMenu(
            event_id='event-1',
            sender_id='sender-1',
            event_key='lazymind_settings',
        )
        worker = SimpleNamespace(
            app_id='app-1',
            lease=SimpleNamespace(fence='fence-1'),
        )

        runtime._handle_menu(worker, menu)
        runtime._handle_menu(worker, menu)

        self.assertEqual(len(Sender.cards), 1)
        self.assertEqual(Store.state['message_id'], 'message-new')
        self.assertEqual(Store.state['revision'], 4)
        rendered = json.dumps(Sender.cards[0]['card'], ensure_ascii=False)
        self.assertIn('"expected_revision": 4', rendered)
        self.assertIn(
            f'"expected_operation_id": "{Store.state["active_operation_id"]}"',
            rendered,
        )

    def test_first_settings_and_assistant_menu_create_workspace(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            store = SQLiteGatewayStore(f'sqlite:///{directory}/gateway.db')
            store.initialize()
            account = store.connect_referenced_account(
                owner_user_id='owner-1',
                provider='feishu',
                external_id_hash='external-1',
                label='Feishu',
                credentials_ciphertext='ciphertext',
                status='connected',
            )
            account_id = str((account or {}).get('id') or '')
            lease = store.acquire_runtime_lease(account_id)
            self.assertIsNotNone(lease)

            try:
                for view, event_key in (
                    ('settings', 'lazymind_settings'),
                    ('assistant', 'lazymind_assistant'),
                ):
                    with self.subTest(view=view):
                        chat_id = f'chat-{view}'
                        captured = []
                        scheduled = []

                        class Sender:
                            @staticmethod
                            def send_card_to_user_with_chat(**kwargs):
                                captured.append(dict(kwargs))
                                return f'message-{view}', chat_id

                            @staticmethod
                            def close():
                                return None

                        runtime = FeishuRuntime.__new__(FeishuRuntime)
                        runtime._lock = threading.RLock()
                        runtime._owner_routes = {
                            ('app-1', 'sender-1'): account_id
                        }
                        runtime._accounts = {
                            account_id: SimpleNamespace(
                                account_id=account_id,
                                owner_user_id='owner-1',
                                sender_id='sender-1',
                            )
                        }
                        runtime._direct_chats = {}
                        runtime._addresses = SimpleNamespace(
                            direct=lambda _account, chat, _sender: (
                                SimpleNamespace(route_hash=f'address-{chat}')
                            )
                        )
                        runtime._store = store
                        runtime._credentials = SimpleNamespace(
                            load_runtime_account=lambda *_args: {
                                'credentials': object()
                            }
                        )
                        runtime._channels = SimpleNamespace(
                            create_sender=lambda *_args: Sender()
                        )
                        runtime._schedule_action_card_refresh = (
                            lambda *_args, **_kwargs: None
                        )
                        runtime._schedule_assistant_threads = (
                            lambda *_args, **kwargs: scheduled.append(kwargs)
                        )

                        menu = FeishuInboundMenu(
                            event_id=f'event-{view}',
                            sender_id='sender-1',
                            event_key=event_key,
                        )
                        worker = SimpleNamespace(
                            app_id='app-1',
                            lease=lease,
                        )
                        runtime._handle_menu(worker, menu)
                        runtime._handle_menu(worker, menu)

                        persisted = store.get_feishu_workspace_state(
                            account_id,
                            f'address-{chat_id}',
                        )
                        self.assertEqual(persisted['view'], view)
                        self.assertEqual(
                            persisted['message_id'],
                            f'message-{view}',
                        )
                        self.assertEqual(persisted['revision'], 1)
                        self.assertEqual(len(captured), 1)
                        self.assertEqual(
                            len(scheduled),
                            2 if view == 'assistant' else 0,
                        )
            finally:
                if lease is not None:
                    lease.close()

    def test_release_failure_reconciles_same_message_sessions_view(self) -> None:
        initial = FeishuWorkspaceState(
            view='assistant',
            assistant_mode='detail',
            assistant_selected_thread_id='thread-1',
            message_id='message-1',
            revision=3,
            active_operation_id='operation-1',
        )

        class Store:
            state = initial.to_dict()

            @classmethod
            def get_feishu_workspace_state(cls, *_args):
                return dict(cls.state)

            @staticmethod
            def get_route(*_args):
                return ''

            @staticmethod
            def has_active_inbound(*_args):
                return False

        class Sender:
            updates = []

            @classmethod
            def update_card(cls, **kwargs):
                cls.updates.append(dict(kwargs))

            @staticmethod
            def close():
                return None

        scheduled = []
        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._lock = threading.RLock()
        runtime._owner_routes = {('app-1', 'sender-1'): 'account-1'}
        runtime._accounts = {
            'account-1': SimpleNamespace(
                account_id='account-1',
                owner_user_id='owner-1',
                sender_id='sender-1',
            )
        }
        runtime._direct_chats = {'account-1': 'chat-1'}
        runtime._addresses = SimpleNamespace(
            direct=lambda *_args: SimpleNamespace(route_hash='address-1')
        )
        runtime._store = Store()
        runtime._credentials = SimpleNamespace(
            load_runtime_account=lambda *_args: {'credentials': object()}
        )
        runtime._channels = SimpleNamespace(
            create_sender=lambda *_args: Sender()
        )
        runtime._read_assistant_detail = lambda **_kwargs: {
            'kind': 'detail',
            'snapshot': {
                'conversation_id': 'conversation-1',
                'run_id': 'run-1',
                'status': 'completed',
            },
        }

        def fail_release(_state, **_kwargs):
            current = FeishuWorkspaceState.from_dict(Store.state)
            current.assistant_mode = 'sessions'
            current.active_operation_id = 'operation-2'
            current.advance()
            Store.state = current.to_dict()
            raise RuntimeError('release failed')

        runtime._release_idle_assistant = fail_release
        runtime._load_assistant_threads = lambda *_args: {
            'kind': 'sessions',
            'threads': [],
        }
        runtime._schedule_action_card_refresh = (
            lambda *args, **kwargs: scheduled.append((args, kwargs))
        )

        runtime._handle_menu(
            SimpleNamespace(
                app_id='app-1',
                lease=SimpleNamespace(fence='fence-1'),
            ),
            FeishuInboundMenu(
                event_id='event-settings',
                sender_id='sender-1',
                event_key='lazymind_settings',
            ),
        )

        self.assertEqual(len(Sender.updates), 1)
        self.assertEqual(len(scheduled), 1)
        self.assertEqual(scheduled[0][0][1], 'message-1')
        self.assertEqual(scheduled[0][1]['expected_revision'], 4)
        self.assertEqual(
            scheduled[0][1]['expected_operation_id'],
            'operation-2',
        )

    def test_workspace_drops_legacy_presentation_snapshots(self) -> None:
        state = FeishuWorkspaceState.from_dict({
            'view': 'settings',
            'view_snapshots': {
                'settings': {
                    'presentations': [{
                        'kind': 'conversation_settings',
                        'workflow_mode': 'auto',
                    }],
                },
            },
        })

        self.assertNotIn('view_snapshots', state.to_dict())

    def test_conversation_settings_presentation_is_core_projection(
        self,
    ) -> None:
        capabilities = CapabilityActions(
            store=_DraftNavigationStore(
                conversation_id='conversation-1'
            ),
            client=_SettingsClient(),
        )

        _text, presentation = capabilities.conversation_settings(
            account_id='account-1',
            external_address_hash='address-1',
            owner_user_id='owner-1',
            request_id='request-1',
        )

        self.assertEqual(
            presentation.to_dict(),
            {
                'kind': 'conversation_settings',
                'dataset_ids': [],
                'workflow_enabled': True,
                'workflow_mode': 'dynamic',
                'subagent_enabled': False,
            },
        )

    def test_round_trip_keeps_provider_neutral_execution_inputs(self) -> None:
        context = ChannelExecutionContext(
            attachments=(
                ChannelAttachment(
                    input_type='image',
                    input_base64='data:image/png;base64,AA==',
                ),
            ),
            ask_answers_structured={'ask_id': 'ask-1'},
            thinking_depth='high',
            external_agent_conversation_id='conversation-1',
            include_capability_settings=True,
        )

        restored = ChannelExecutionContext.from_provider_context({
            'channel_execution': context.to_dict(),
        })

        self.assertEqual(restored, context)

    def test_legacy_provider_magic_keys_are_not_execution_inputs(self) -> None:
        restored = ChannelExecutionContext.from_provider_context({
            'workspace_resources': [{'type': 'workflow', 'id': 'wf-1'}],
            'workspace_mentions': [{'type': 'workflow', 'id': 'wf-1'}],
            'external_agent_binding': {
                'conversation_id': 'conversation-1',
                'provider': 'codex',
                'provider_thread_id': 'thread-1',
            },
            'chat_inputs': [{'input_type': 'image', 'input_base64': 'x'}],
            'workspace_ask_validated': True,
            'ask_answers_structured': {'ask_id': 'ask-1'},
        })

        self.assertEqual(restored, ChannelExecutionContext())

    def test_empty_attachment_fails_closed(self) -> None:
        restored = ChannelExecutionContext.from_dict({
            'schema_version': '1',
            'resources': [
                {'type': 'unknown', 'id': 'x', 'name': 'bad'},
            ],
            'attachments': [
                {'input_type': 'image'},
                {'input_type': 'file', 'uri': 'file:///tmp/a'},
            ],
        })

        self.assertEqual(len(restored.attachments), 1)
        self.assertEqual(restored.attachments[0].input_type, 'file')

    def test_external_agent_execution_and_failure_message_use_typed_target(
        self,
    ) -> None:
        context = ChannelExecutionContext(
            external_agent_conversation_id='conversation-1',
        )
        provider_context = {'channel_execution': context.to_dict()}

        conversation_id = (
            ChannelActionExecutor._external_agent_conversation(context)
        )

        self.assertEqual(conversation_id, 'conversation-1')
        self.assertEqual(
            _failure_message(provider_context, RuntimeError('offline')),
            '外部智能体执行失败：offline',
        )

    def test_managed_stream_fork_commits_new_thread_with_old_thread_guard(
        self,
    ) -> None:
        state = FeishuWorkspaceState(
            view='assistant',
            assistant_mode='detail',
            assistant_selected_thread_id='thread-before',
            active_operation_id='operation-1',
            revision=7,
        )
        store = _WorkspaceStore(state)
        stream = _ReplyStream()
        sender = _Sender()
        execution = ChannelExecutionContext(
            external_agent_conversation_id='conversation-1',
        )
        provider_context = {
            'channel_execution': execution.to_dict(),
            'workspace_operation_id': 'operation-1',
            'workspace_state': state.to_dict(),
        }
        managed = _ManagedReplyStream(
            stream,
            sender,
            provider_context,
            store,
            'account-1',
            'address-1',
        )

        managed.update(CoreStreamUpdate(external_event={
            'event': {
                'type': 'thread_forked',
                'thread_id': 'thread-after',
            },
            'snapshot': {
                'conversation_id': 'conversation-1',
                'run_id': 'run-1',
                'status': 'running',
            },
        }))

        self.assertEqual(
            store.state['assistant_selected_thread_id'],
            'thread-after',
        )
        self.assertNotIn('assistant_status', store.state)
        self.assertEqual(store.state['revision'], 8)
        self.assertEqual(
            _external_agent_conversation_id(provider_context),
            'conversation-1',
        )
        self.assertEqual(len(stream.updates), 1)

    def test_action_executor_consumes_typed_attachment_and_ask(
        self,
    ) -> None:
        executor = ChannelActionExecutor(
            store=_ActionStore(),
            client=_ActionClient(),
        )
        conversations = _ConversationRecorder()
        executor._conversations = conversations
        executor._replies = _ReplyRecorder()
        execution = ChannelExecutionContext(
            attachments=(
                ChannelAttachment(
                    input_type='image',
                    input_base64='data:image/png;base64,AA==',
                ),
            ),
            ask_answers_structured={'ask_id': 'ask-1'},
            thinking_depth='high',
        )

        reply = executor.execute(
            command=ChatCommand(
                schema_version=SCHEMA_VERSION,
                command='chat',
                parameters=ChatParameters(message='继续'),
            ),
            account_id='account-1',
            external_address_hash='address-1',
            owner_user_id='owner-1',
            request_id='request-1',
            grounding_messages=('继续',),
            catalog={
                'workflow': [
                    {'id': 'wf-1', 'name': '审阅流程', 'enabled': True},
                ],
                'tool': [],
            },
            provider='feishu',
            provider_context={
                'channel_execution': execution.to_dict(),
            },
        )

        self.assertEqual(reply['result'], 'ok')
        self.assertEqual(
            conversations.chat_args['ask_answers_structured'],
            {'ask_id': 'ask-1'},
        )
        self.assertNotIn('ask_already_validated', conversations.chat_args)
        self.assertEqual(
            conversations.chat_args['inputs'][0]['input_type'],
            'image',
        )
        self.assertNotIn('workflow_mode', conversations.chat_args)
        self.assertNotIn('workspace_dataset_ids', conversations.chat_args)
        self.assertNotIn('enable_workflow', conversations.chat_args)
        self.assertEqual(conversations.chat_args['thinking_depth'], 'high')

    def test_feishu_edge_keeps_execution_context_free_of_workspace_policy(
        self,
    ) -> None:
        workspace = FeishuWorkspaceState(
            thinking_depth='high',
        )

        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._store = _WorkspaceStore(
            workspace,
            {'workflow_mode': 'auto'},
        )
        provider_context = runtime._workspace_provider_context(
            account_id='account-1',
            address_hash='address-1',
            workspace=workspace,
            chat_id='chat-1',
            conversation_id='',
            workspace_action={
                'kind': 'navigate',
                'view': 'capabilities',
            },
        )
        execution = ChannelExecutionContext.from_provider_context(
            provider_context
        )

        self.assertTrue(execution.include_capability_settings)
        self.assertEqual(execution.thinking_depth, 'high')
        self.assertEqual(provider_context['pending_workflow_mode'], 'auto')
        self.assertTrue(provider_context['new_conversation_pending'])
        self.assertNotIn('resources', execution.to_dict())

    def test_capability_card_without_pending_draft_has_no_mode_action(
        self,
    ) -> None:
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
        )
        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._store = _WorkspaceStore(workspace)
        provider_context = runtime._workspace_provider_context(
            account_id='account-1',
            address_hash='address-1',
            workspace=workspace,
            chat_id='chat-1',
            conversation_id='',
        )

        card = FeishuWorkspaceRenderer.render(
            provider_context=provider_context,
            presentations=[{'kind': 'capability', 'groups': []}],
        )
        rendered = json.dumps(card, ensure_ascii=False)

        self.assertFalse(provider_context['new_conversation_pending'])
        self.assertIn('先新建会话，再选择首轮能力', rendered)
        self.assertNotIn('new_session.workflow_mode', rendered)

    def test_capability_catalog_replaces_context_surface(self) -> None:
        legacy = FeishuWorkspaceState.from_dict({
            'view': 'context',
            'context_category': 'skill',
            'context_page': 2,
        })
        self.assertEqual(legacy.view, 'chat')
        self.assertNotIn('context_category', legacy.to_dict())
        self.assertNotIn('context_page', legacy.to_dict())

        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
            revision=4,
            active_operation_id='operation-1',
            capability_category='skill',
            capability_page=1,
        )
        items = [
            {'id': f'skill-{index}', 'name': f'Skill {index}'}
            for index in range(1, 13)
        ]
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                'chat_id': 'chat-1',
                'workspace_state': workspace.to_dict(),
                'new_conversation_pending': True,
                'pending_workflow_mode': 'dynamic',
                'pending_capability_resources': [
                    {
                        'type': 'skill',
                        'id': 'skill-11',
                        'name': 'Skill 11',
                    }
                ],
                '_workspace_result_complete': True,
            },
            presentations=[{
                'kind': 'capability',
                'groups': [{
                    'resource_type': 'skill',
                    'label': 'Skill',
                    'items': items,
                }],
            }],
        )
        rendered = json.dumps(card, ensure_ascii=False)

        self.assertIn('管理新会话能力', rendered)
        self.assertIn('Skill 11', rendered)
        self.assertIn('Skill 12', rendered)
        self.assertNotIn('Skill 1"', rendered)
        self.assertIn('"kind": "capability.open"', rendered)
        self.assertIn('"kind": "capability.page"', rendered)
        self.assertIn('"kind": "capability.toggle"', rendered)
        self.assertIn('"capabilities": ["skill"]', rendered)
        self.assertNotIn('context.open', rendered)
        self.assertNotIn('context.page', rendered)
        self.assertNotIn('context.toggle', rendered)

    def test_capability_overview_always_links_full_catalog(self) -> None:
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
            revision=4,
            active_operation_id='operation-1',
        )
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                'chat_id': 'chat-1',
                'workspace_state': workspace.to_dict(),
                'new_conversation_pending': True,
                '_workspace_result_complete': True,
            },
            presentations=[{
                'kind': 'capability',
                'groups': [{
                    'resource_type': 'knowledge_base',
                    'label': '知识库',
                    'items': [{'id': 'kb-1', 'name': 'KB 1'}],
                }],
            }],
        )
        rendered = json.dumps(card, ensure_ascii=False)

        self.assertIn('管理全部能力', rendered)
        self.assertIn('"kind": "capability.open"', rendered)
        self.assertIn(
            '"capabilities": ["knowledge_base", "skill", "workflow", "tool"]',
            rendered,
        )

    def test_card_capability_catalog_does_not_replace_text_selection(self) -> None:
        calls: list[str] = []
        capabilities = CapabilityActions(
            store=SimpleNamespace(
                clear_selection_snapshot=lambda *_args: calls.append('clear'),
                save_selection_snapshot=lambda *_args, **_kwargs: calls.append(
                    'save'
                ),
            ),
            client=_ActionClient(),
        )

        capabilities.list_capabilities(
            kinds=['tool'],
            catalog={'tool': [{'id': 'tool-1', 'name': 'Tool 1'}]},
            account_id='account-1',
            external_address_hash='address-1',
            features=BASIC_CHAT_FEATURES,
            save_selection=False,
        )

        self.assertEqual(calls, [])

    def test_action_executor_only_preserves_selection_for_management(self) -> None:
        calls: list[str] = []
        store = SimpleNamespace(
            get_route=lambda *_args: None,
            clear_selection_snapshot=lambda *_args: calls.append('clear'),
            save_selection_snapshot=lambda *_args, **_kwargs: calls.append(
                'save'
            ),
            get_selection_context=lambda *_args: None,
        )
        executor = ChannelActionExecutor(store=store, client=_ActionClient())
        executor._replies = _ReplyRecorder()
        command = COMMAND_ADAPTER.validate_python({
            'schema_version': SCHEMA_VERSION,
            'command': 'capability.list',
            'parameters': {
                'capabilities': ['tool'],
                'evidence': ['list tools'],
            },
        })
        kwargs = {
            'command': command,
            'account_id': 'account-1',
            'external_address_hash': 'address-1',
            'owner_user_id': 'owner-1',
            'request_id': 'request-1',
            'grounding_messages': ('list tools',),
            'catalog': {'tool': [{'id': 'tool-1', 'name': 'Tool 1'}]},
            'provider': 'feishu',
        }

        executor.execute(
            **kwargs,
            provider_context={
                'surface': 'card',
                'workspace_surface': 'management',
            },
        )
        self.assertEqual(calls, [])

        executor.execute(
            **kwargs,
            provider_context={
                'surface': 'card',
                'workspace_surface': 'reply',
            },
        )
        self.assertEqual(calls, ['clear', 'save'])

    def test_capability_catalog_keeps_prompt_as_direct_chat(self) -> None:
        capabilities = CapabilityActions(
            store=SimpleNamespace(
                clear_selection_snapshot=lambda *_args: None,
            ),
            client=_ActionClient(),
        )
        _text, presentation = capabilities.list_capabilities(
            kinds=['prompt'],
            catalog={
                'prompt': [{
                    'id': 'prompt-1',
                    'name': 'Summarize',
                    'content': 'Summarize this document',
                }],
            },
            account_id='account-1',
            external_address_hash='address-1',
            features=BASIC_CHAT_FEATURES,
        )
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
            revision=4,
            active_operation_id='operation-1',
            capability_category='prompt',
        )
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                'chat_id': 'chat-1',
                'workspace_state': workspace.to_dict(),
                'new_conversation_pending': True,
                '_workspace_result_complete': True,
            },
            presentations=[presentation.to_dict()],
        )
        rendered = json.dumps(card, ensure_ascii=False)

        self.assertIn('"command": "chat"', rendered)
        self.assertIn('"kind": "prompt.run"', rendered)
        self.assertIn('"message": "Summarize this document"', rendered)
        self.assertNotIn('"resource": {"type": "prompt"', rendered)

    def test_selection_continuation_persists_only_resolved_items(self) -> None:
        captured: dict = {}

        def save_selection_snapshot(*_args, **kwargs):
            captured.update(kwargs)
            captured['items'] = _args[3]

        capabilities = CapabilityActions(
            store=SimpleNamespace(
                get_selection_snapshot=lambda *_args, **_kwargs: None,
                save_selection_snapshot=save_selection_snapshot,
            ),
            client=_ActionClient(),
        )
        source_command = COMMAND_ADAPTER.validate_python({
            'schema_version': SCHEMA_VERSION,
            'command': 'capability.configure',
            'parameters': {
                'resource_changes': [
                    {
                        'resource_type': 'knowledge_base',
                        'selector': {'kind': 'name', 'value': 'KB One'},
                        'operation': 'use',
                        'scope': 'conversation',
                        'evidence': 'KB One',
                    },
                    {
                        'resource_type': 'tool',
                        'selector': {'kind': 'name', 'value': 'Search'},
                        'operation': 'use',
                        'scope': 'turn',
                        'evidence': 'Search',
                    },
                    {
                        'resource_type': 'knowledge_base',
                        'selector': {'kind': 'name', 'value': 'KB Later'},
                        'operation': 'use',
                        'scope': 'turn',
                        'evidence': 'KB Later',
                    },
                ],
                'evidence': ['KB One', 'Search', 'KB Later'],
            },
        })
        catalog = {
            'knowledge_base': [{
                'id': 'kb-1',
                'name': 'KB One',
                'raw': 'x' * 100_000,
            }],
            'tool': [
                {
                    'id': 'tool-1',
                    'name': 'Search One',
                    'raw': 'z' * 100_000,
                },
                {
                    'id': 'tool-2',
                    'name': 'Search Two',
                    'raw': 'z' * 100_000,
                },
            ],
            'unrelated': 'y' * 100_000,
        }

        with self.assertRaises(ActionMessage):
            capabilities.resolve_changes(
                list(source_command.parameters.resource_changes),
                catalog,
                account_id='account-1',
                external_address_hash='address-1',
                source_command=source_command,
                source_messages=('Use KB One and Search',),
            )

        continuation = captured['continuation']
        serialized = json.dumps(continuation, ensure_ascii=False)
        self.assertLess(len(serialized), 2_000)
        self.assertEqual(
            captured['items'],
            [
                {
                    'id': 'tool-1',
                    'name': 'Search One',
                    'can_disable': True,
                },
                {
                    'id': 'tool-2',
                    'name': 'Search Two',
                    'can_disable': True,
                },
            ],
        )
        self.assertNotIn('prepared_catalog', continuation)
        self.assertEqual(
            continuation['prepared_resources']['0'],
            {
                'resource_type': 'knowledge_base',
                'item': {
                    'id': 'kb-1',
                    'name': 'KB One',
                    'can_disable': True,
                },
            },
        )

        resumed = _resume_continuation(continuation, '1', '1')
        self.assertIsNotNone(resumed)
        self.assertEqual(
            resumed.prepared_catalog[RESOLVED_RESOURCE_SELECTIONS_KEY]['0'][
                'items'
            ][0]['id'],
            'kb-1',
        )
        accumulated = CapabilityActions._continuation_resources(
            resumed.prepared_catalog,
            [
                (
                    source_command.parameters.resource_changes[0],
                    [{'id': 'kb-1', 'name': 'KB One'}],
                ),
                (
                    source_command.parameters.resource_changes[1],
                    [{'id': 'tool-1', 'name': 'Search One'}],
                ),
            ],
        )
        resumed_again = _resume_continuation(
            SelectionContinuation(
                selection_field='resource_change',
                command=resumed.command.model_dump(mode='json'),
                grounding_messages=['Use KB One and Search'],
                resource_change_index=2,
                prepared_resources=accumulated,
            ).model_dump(mode='json'),
            '1',
            '1',
        )
        self.assertEqual(
            set(
                resumed_again.prepared_catalog[
                    RESOLVED_RESOURCE_SELECTIONS_KEY
                ]
            ),
            {'0', '1'},
        )
        with self.assertRaises(ValueError):
            SelectionContinuation.model_validate({
                **continuation,
                'prepared_catalog': {'raw': 'legacy'},
            })

    def test_selection_resume_revalidates_fresh_catalog(self) -> None:
        parser = ExactShortcutParser(SimpleNamespace(
            get_selection_context=lambda *_args: {
                'kind': 'tool',
                'items': [{'id': 'tool-1', 'name': 'Tool 1'}],
                'continuation': {
                    'command': 'capability.configure',
                    'resource_change': {'resource_type': 'tool'},
                },
            },
        ))
        with self.assertRaises(LazyMindError):
            parser.parse(
                account_id='account-1',
                external_address_hash='address-1',
                text='1',
            )

        disable = RESOURCE_CHANGE_ADAPTER.validate_python({
            'resource_type': 'tool',
            'selector': {'kind': 'name', 'value': 'Tool 1'},
            'operation': 'disable',
            'scope': 'global',
            'evidence': 'disable Tool 1',
        })
        capabilities = CapabilityActions(
            store=SimpleNamespace(),
            client=_ActionClient(),
        )
        catalog = {
            RESOLVED_RESOURCE_SELECTIONS_KEY: {
                '0': {
                    'resource_type': 'tool',
                    'items': [{
                        'id': 'tool-1',
                        'name': 'Tool 1',
                        'can_disable': True,
                    }],
                },
            },
            'tool': [{
                'id': 'tool-1',
                'name': 'Tool 1',
                'can_disable': False,
            }],
        }
        resolved = capabilities.resolve_changes(
            [disable],
            catalog,
            account_id='account-1',
            external_address_hash='address-1',
        )
        self.assertFalse(resolved[0][1][0]['can_disable'])

        catalog['tool'] = []
        with self.assertRaises(ActionMessage):
            capabilities.resolve_changes(
                [disable],
                catalog,
                account_id='account-1',
                external_address_hash='address-1',
            )

        use_index = RESOURCE_CHANGE_ADAPTER.validate_python({
            'resource_type': 'tool',
            'selector': {'kind': 'index', 'value': '1'},
            'operation': 'use',
            'scope': 'turn',
            'evidence': '1',
        })
        capabilities = CapabilityActions(
            store=SimpleNamespace(
                get_selection_snapshot=lambda *_args, **_kwargs: [{
                    'id': 'removed-tool',
                    'name': 'Removed',
                }],
            ),
            client=_ActionClient(),
        )
        with self.assertRaises(ActionMessage):
            capabilities.resolve_changes(
                [use_index],
                {'tool': [{'id': 'tool-2', 'name': 'Tool 2'}]},
                account_id='account-1',
                external_address_hash='address-1',
            )

    def test_switch_resource_continuation_keeps_resolved_target(self) -> None:
        command = COMMAND_ADAPTER.validate_python({
            'schema_version': SCHEMA_VERSION,
            'command': 'conversation.switch',
            'parameters': {
                'target': {'kind': 'index', 'value': '1'},
                'message': '',
                'resource_changes': [{
                    'resource_type': 'tool',
                    'selector': {'kind': 'name', 'value': 'Search'},
                    'operation': 'use',
                    'scope': 'turn',
                    'evidence': 'Search',
                }],
                'evidence': ['switch', 'Search'],
            },
        })
        raw = {
            'selection_field': 'resource_change',
            'command': command.model_dump(mode='json'),
            'grounding_messages': ['switch and Search'],
            'resource_change_index': 0,
        }
        with self.assertRaises(ValueError):
            SelectionContinuation.model_validate(raw)

        raw['prepared_conversation_target'] = {
            'conversation_id': 'conversation-1',
        }
        resumed = _resume_continuation(raw, '1', '1')

        self.assertEqual(
            resumed.prepared_catalog[RESOLVED_CONVERSATION_TARGET_KEY],
            {'conversation_id': 'conversation-1'},
        )

    def test_capability_catalog_actions_share_one_workspace_view(self) -> None:
        runtime = FeishuRuntime.__new__(FeishuRuntime)
        workspace = FeishuWorkspaceState(
            view='capabilities',
            revision=3,
            active_operation_id='operation-1',
        )

        runtime._apply_workspace_action(
            workspace=workspace,
            action={'kind': 'capability.open', 'category': 'workflow'},
        )
        self.assertEqual(workspace.view, 'capabilities')
        self.assertEqual(workspace.capability_category, 'workflow')
        self.assertEqual(workspace.capability_page, 0)

        runtime._apply_workspace_action(
            workspace=workspace,
            action={'kind': 'capability.page', 'page': 2},
        )
        runtime._apply_workspace_action(
            workspace=workspace,
            action={'kind': 'capability.toggle'},
        )
        self.assertEqual(workspace.capability_category, 'workflow')
        self.assertEqual(workspace.capability_page, 2)

        runtime._apply_workspace_action(
            workspace=workspace,
            action={'kind': 'navigate', 'view': 'capabilities'},
        )
        self.assertEqual(workspace.capability_category, '')
        self.assertEqual(workspace.capability_page, 0)

    def test_existing_capability_card_only_mutates_conversation_settings(
        self,
    ) -> None:
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
            revision=4,
            active_operation_id='operation-1',
            capability_category='workflow',
        )
        presentations = [
                {
                    'kind': 'capability',
                    'groups': [
                        {
                            'resource_type': 'knowledge_base',
                            'label': '知识库',
                            'items': [
                                {'id': 'kb-1', 'name': 'KB 1', 'enabled': True},
                            ],
                        },
                        {
                            'resource_type': 'workflow',
                            'label': '工作流',
                            'items': [
                                {'id': 'wf-1', 'name': 'Workflow', 'enabled': True},
                            ],
                        },
                    ],
                },
                {
                    'kind': 'conversation_settings',
                    'dataset_ids': ['kb-1'],
                    'workflow_enabled': True,
                    'workflow_mode': 'dynamic',
                    'subagent_enabled': True,
                    'personalization_enabled': True,
                },
            ]

        card = FeishuWorkspaceRenderer.render(
            provider_context={
                'chat_id': 'chat-1',
                'workspace_conversation_id': 'conversation-1',
                'workspace_state': workspace.to_dict(),
            },
            presentations=presentations,
        )
        rendered = json.dumps(card, ensure_ascii=False)

        self.assertIn('conversation.settings.update', rendered)
        self.assertIn('expected_conversation_id', rendered)
        self.assertIn('"setting": "workflow_enabled"', rendered)
        self.assertIn('"setting": "subagent"', rendered)
        self.assertNotIn('"setting": "knowledge_base"', rendered)
        self.assertNotIn('"setting": "skill"', rendered)
        self.assertNotIn('"setting": "tool"', rendered)
        self.assertNotIn('"setting": "personalization"', rendered)
        self.assertNotIn('capability.save', rendered)
        self.assertNotIn('"kind": "capability.open"', rendered)

    def test_history_switch_action_carries_workspace_lineage(self) -> None:
        workspace = FeishuWorkspaceState(
            view='conversations',
            message_id='message-1',
            revision=7,
            active_operation_id='history-1',
        )
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                'chat_id': 'chat-1',
                'workspace_state': workspace.to_dict(),
                '_workspace_result_complete': True,
            },
            presentations=[{
                'kind': 'selection',
                'selection_id': 'selection-1',
                'options': [{'value': '1', 'label': 'Conversation'}],
            }],
        )
        rendered = json.dumps(card, ensure_ascii=False)

        self.assertIn('"kind": "history.switch"', rendered)
        self.assertIn('"expected_view": "conversations"', rendered)
        self.assertIn('"expected_revision": 7', rendered)
        self.assertIn('"expected_operation_id": "history-1"', rendered)

    def test_stale_history_switch_does_not_enqueue(self) -> None:
        workspace = FeishuWorkspaceState(
            view='conversations',
            message_id='message-1',
            revision=8,
            active_operation_id='history-1',
        )
        store = _StaleSettingStore(workspace)
        runtime = self._runtime_for_action_store(store)
        action = FeishuInboundAction(
            message_id='message-1',
            chat_id='chat-1',
            sender_id='sender-1',
            action='select',
            text='1',
            selection='1',
            selection_id='selection-1',
            intended_chat_id='',
            ask_answers_structured=None,
            command_action=None,
            workspace_action={
                'kind': 'history.switch',
                'view': 'conversations',
                'expected_view': 'conversations',
                'expected_revision': 7,
                'expected_operation_id': 'history-1',
            },
        )

        self.assertIsNone(runtime._handle_action(self._worker(), action))
        self.assertEqual(store.ingest_count, 0)
        self.assertEqual(store.state['revision'], 8)

    def test_stale_workspace_actions_share_one_lineage_guard(self) -> None:
        for kind, action_type in (
            ('preference', 'local'),
            ('maintenance.clear_conversation', 'command'),
            ('prompt.run', 'command'),
        ):
            with self.subTest(kind=kind):
                workspace = FeishuWorkspaceState(
                    view='settings',
                    message_id='message-1',
                    revision=8,
                    active_operation_id='operation-1',
                )
                store = _StaleSettingStore(workspace)
                runtime = self._runtime_for_action_store(store)
                action = FeishuInboundAction(
                    message_id='message-1',
                    chat_id='chat-1',
                    sender_id='sender-1',
                    action=action_type,
                    text=kind,
                    selection='',
                    selection_id='',
                    intended_chat_id='',
                    ask_answers_structured=None,
                    command_action=(
                        {'schema_version': '1', 'command': 'chat'}
                        if action_type == 'command'
                        else None
                    ),
                    workspace_action={
                        'kind': kind,
                        'expected_view': 'settings',
                        'expected_revision': 7,
                        'expected_operation_id': 'operation-1',
                    },
                )

                result = runtime._handle_action(self._worker(), action)
                if action_type == 'local':
                    self.assertIsInstance(result, dict)
                    self.assertIn(
                        '"expected_revision": 8',
                        json.dumps(result, ensure_ascii=False),
                    )
                else:
                    self.assertIsNone(result)
                self.assertEqual(store.ingest_count, 0)
                self.assertEqual(store.state['revision'], 8)

    def test_new_session_workflow_mode_updates_only_pending_draft(self) -> None:
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
            revision=7,
            active_operation_id='new-session-1',
        )
        store = _StaleSettingStore(workspace)
        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._lock = threading.RLock()
        runtime._owner_routes = {('app-1', 'sender-1'): 'account-1'}
        runtime._accounts = {
            'account-1': SimpleNamespace(
                account_id='account-1',
                owner_user_id='owner-1',
                sender_id='sender-1',
            )
        }
        runtime._addresses = SimpleNamespace(
            direct=lambda *_args: SimpleNamespace(route_hash='address-1')
        )
        runtime._store = store
        runtime._direct_chats = {}
        runtime._schedule_action_card_refresh = lambda *_args, **_kwargs: None
        worker = SimpleNamespace(
            app_id='app-1',
            lease=SimpleNamespace(fence='fence-1'),
        )
        action = self._pending_mode_action()

        card = runtime._handle_action(worker, action)

        self.assertIsNone(card)
        self.assertEqual(store.draft['workflow_mode'], 'auto')
        self.assertEqual(store.workflow_updates, 1)
        self.assertEqual(store.state['revision'], 8)
        self.assertEqual(store.ingest_count, 1)
        self.assertEqual(
            store.ingested[0].provider_context['command_action']['command'],
            'capability.list',
        )

    def test_new_conversation_draft_reaches_initial_workflow_settings(
        self,
    ) -> None:
        draft = CapabilityActions.options_from_dict({
            'workflow_mode': 'dynamic',
            'enable_workflow': True,
        })
        options = CapabilityActions.merge_options(ChatOptions(), draft)
        payload = LazyMindClient.__new__(LazyMindClient)._chat_payload(
            text='执行写作流程',
            conversation_id='',
            options=options,
        )

        self.assertEqual(options.workflow_mode, 'dynamic')
        self.assertTrue(options.enable_workflow)
        self.assertEqual(payload['workflow_mode'], 'dynamic')
        self.assertEqual(
            payload['initial_workflow_settings'],
            {'enable_workflow': True, 'workflow_mode': 'dynamic'},
        )
        self.assertIsNone(
            CapabilityActions.options_from_dict({
                'workflow_mode': 'invalid',
            }).workflow_mode
        )

    def test_explicit_new_conversation_persists_displayed_default_mode(
        self,
    ) -> None:
        store = _DraftNavigationStore()
        store.draft = {}
        conversations = ConversationActions(
            store=store,
            client=_ConversationClient(),
            capabilities=CapabilityActions(
                store=store,
                client=_ActionClient(),
            ),
        )

        conversations.new(
            account_id='account-1',
            external_address_hash='address-1',
            owner_user_id='owner-1',
            request_id='request-new',
            message='',
            changes=[],
            source_command=ConversationNewCommand(
                schema_version=SCHEMA_VERSION,
                command='conversation.new',
                parameters=ConversationNewParameters(
                    message='',
                    resource_changes=[],
                    evidence=['新建会话'],
                ),
            ),
            source_messages=('新建会话',),
            catalog={},
            features=BASIC_CHAT_FEATURES,
        )

        self.assertEqual(store.draft.get('workflow_mode'), 'dynamic')

    def test_stale_capability_toggle_cannot_reverse_fresh_selection(
        self,
    ) -> None:
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
            revision=7,
            active_operation_id='new-session-1',
        )
        store = _StaleSettingStore(workspace)
        runtime = self._runtime_for_action_store(store)
        action = FeishuInboundAction(
            message_id='message-1',
            chat_id='chat-1',
            sender_id='sender-1',
            action='command',
            text='切换 Workflow',
            selection='',
            selection_id='',
            intended_chat_id='',
            ask_answers_structured=None,
            command_action={
                'schema_version': '1',
                'command': 'capability.list',
                'parameters': {
                    'capabilities': [
                        'knowledge_base',
                        'skill',
                        'workflow',
                        'tool',
                    ],
                    'evidence': ['切换 Workflow'],
                },
            },
            workspace_action={
                'kind': 'capability.toggle',
                'scope': 'conversation',
                'category': 'workflow',
                'expected_view': 'capabilities',
                'expected_revision': 7,
                'expected_operation_id': 'new-session-1',
                'resource': {
                    'type': 'workflow',
                    'id': 'writer-workflow',
                    'name': 'AI Writer',
                },
            },
        )

        self.assertIsNone(runtime._handle_action(self._worker(), action))
        self.assertIsNone(runtime._handle_action(self._worker(), action))
        persisted = FeishuWorkspaceState.from_dict(store.state)
        self.assertEqual(
            store.draft['mentions'][0]['resource_id'],
            'writer-workflow',
        )
        self.assertEqual(persisted.revision, 8)
        self.assertEqual(store.ingest_count, 1)

    def test_conversation_worker_fresh_reads_pending_workflow_mode(
        self,
    ) -> None:
        store = _DraftNavigationStore()
        client = _ConversationClient()
        capabilities = CapabilityActions(store=store, client=_ActionClient())
        conversations = ConversationActions(
            store=store,
            client=client,
            capabilities=capabilities,
        )

        conversations.chat(
            account_id='account-1',
            external_address_hash='address-1',
            owner_user_id='owner-1',
            request_id='request-1',
            message='执行写作流程',
            changes=[],
            catalog={},
            features=BASIC_CHAT_FEATURES,
        )

        self.assertIsNotNone(client.options)
        self.assertEqual(client.options.workflow_mode, 'dynamic')
        self.assertTrue(client.options.enable_workflow)
        self.assertEqual(store.activated, 'conversation-created')

        existing_store = _DraftNavigationStore(
            conversation_id='conversation-existing'
        )
        existing_client = _ConversationClient()
        existing = ConversationActions(
            store=existing_store,
            client=existing_client,
            capabilities=CapabilityActions(
                store=existing_store,
                client=_ActionClient(),
            ),
        )
        existing.chat(
            account_id='account-1',
            external_address_hash='address-1',
            owner_user_id='owner-1',
            request_id='request-2',
            message='继续',
            changes=[],
            catalog={},
            features=BASIC_CHAT_FEATURES,
        )

        self.assertIsNone(existing_client.options.workflow_mode)
        self.assertIsNone(existing_client.options.enable_workflow)

    def test_stale_conversation_setting_is_rejected_before_side_effect(
        self,
    ) -> None:
        store = _DraftNavigationStore(
            conversation_id='conversation-current'
        )
        capabilities = CapabilityActions(
            store=store,
            client=_ActionClient(),
        )

        with self.assertRaisesRegex(ActionMessage, '会话已切换'):
            capabilities.update_conversation_setting(
                change=ConversationWorkflowModeSetting(
                    setting='workflow_mode',
                    mode='auto',
                ),
                expected_conversation_id='conversation-stale',
                catalog={},
                account_id='account-1',
                external_address_hash='address-1',
                owner_user_id='owner-1',
                request_id='request-1',
            )

    def test_stale_setting_action_does_not_enqueue_remote_command(self) -> None:
        workspace = FeishuWorkspaceState(
            view='settings',
            message_id='message-1',
        )
        store = _StaleSettingStore(workspace)
        runtime = FeishuRuntime.__new__(FeishuRuntime)
        runtime._lock = threading.RLock()
        runtime._owner_routes = {('app-1', 'sender-1'): 'account-1'}
        runtime._accounts = {
            'account-1': SimpleNamespace(
                account_id='account-1',
                owner_user_id='owner-1',
                sender_id='sender-1',
            )
        }
        runtime._addresses = SimpleNamespace(
            direct=lambda *_args: SimpleNamespace(route_hash='address-1')
        )
        runtime._store = store
        runtime._direct_chats = {}
        worker = SimpleNamespace(
            app_id='app-1',
            lease=SimpleNamespace(fence='fence-1'),
        )
        action = FeishuInboundAction(
            message_id='message-1',
            chat_id='chat-1',
            sender_id='sender-1',
            action='command',
            text='切换 Workflow 执行方式',
            selection='',
            selection_id='',
            intended_chat_id='',
            ask_answers_structured=None,
            command_action={'command': 'settings'},
            workspace_action={
                'kind': 'setting.update',
                'view': 'capabilities',
            },
        )

        self.assertIsNone(runtime._handle_action(worker, action))
        self.assertEqual(store.ingest_count, 0)
        self.assertEqual(
            FeishuWorkspaceState.from_dict(store.state).view,
            'settings',
        )

    def test_setting_action_requires_matching_inner_conversation_guard(
        self,
    ) -> None:
        workspace = FeishuWorkspaceState(
            view='capabilities',
            message_id='message-1',
            revision=7,
            active_operation_id='operation-1',
        )
        store = _StaleSettingStore(
            workspace,
            conversation_id='conversation-a',
        )
        runtime = self._runtime_for_action_store(store)
        action = FeishuInboundAction(
            message_id='message-1',
            chat_id='chat-1',
            sender_id='sender-1',
            action='command',
            text='切换 Workflow 执行方式',
            selection='',
            selection_id='',
            intended_chat_id='',
            ask_answers_structured=None,
            command_action={
                'schema_version': '1',
                'command': 'conversation.settings.update',
                'parameters': {
                    'change': {
                        'setting': 'workflow_mode',
                        'mode': 'auto',
                    },
                    'evidence': ['切换 Workflow 执行方式'],
                },
            },
            workspace_action={
                'kind': 'setting.update',
                'view': 'capabilities',
                'expected_view': 'capabilities',
                'expected_revision': 7,
                'expected_operation_id': 'operation-1',
                'expected_conversation_id': 'conversation-a',
            },
        )

        self.assertIsNone(runtime._handle_action(self._worker(), action))
        self.assertEqual(store.ingest_count, 0)
        self.assertEqual(store.state['revision'], 7)

        action.command_action['parameters'][
            'expected_conversation_id'
        ] = 'conversation-a'
        self.assertIsNone(runtime._handle_action(self._worker(), action))
        self.assertEqual(store.ingest_count, 1)
        self.assertEqual(store.state['revision'], 8)
        self.assertNotEqual(store.state['active_operation_id'], 'operation-1')
        self.assertEqual(
            store.ingested[0].provider_context['workspace_operation_id'],
            store.state['active_operation_id'],
        )

    def test_common_application_does_not_parse_feishu_workspace_keys(
        self,
    ) -> None:
        application = (
            Path(__file__).parents[1]
            / 'channel_gateway'
            / 'common'
            / 'application'
        )
        source = '\n'.join(
            path.read_text(encoding='utf-8')
            for path in (
                application / 'actions.py',
                application / 'routing.py',
                application / 'workers.py',
            )
        )
        for legacy_key in (
            'workspace_resources',
            'workspace_mentions',
            'external_agent_binding',
            'chat_inputs',
            'workspace_ask_validated',
        ):
            with self.subTest(legacy_key=legacy_key):
                self.assertNotIn(legacy_key, source)


if __name__ == '__main__':
    unittest.main()
