import hashlib
import logging
import threading
import time
from dataclasses import replace
from typing import Any

from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
)
from channel_gateway.common.domain.chat import CoreStreamUpdate
from channel_gateway.common.domain.outbound import (
    OutboundRenderer,
    inline_artifact_bytes,
)
from channel_gateway.common.errors import (
    InvalidStaticAssetError,
)
from channel_gateway.common.ports.core import StaticAssetClient
from channel_gateway.common.ports.providers import RuntimeCredentialStore
from channel_gateway.common.ports.messaging import ReplyStream
from channel_gateway.feishu.domain import (
    FeishuRuntimeError,
    workspace_card_expired,
)
from channel_gateway.feishu.ports import (
    FeishuOutboundFactory,
    FeishuWorkspaceRepository,
)
from channel_gateway.feishu.presentation import (
    FeishuPresentationRenderer,
    FeishuReplyRenderer,
    media_free_feishu_text,
    streaming_reply_card,
    task_progress_text,
)
from channel_gateway.feishu.task_monitor import find_task
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceState,
    stale_workspace_card,
)


_MAX_FEISHU_IMAGE_BYTES = 10 * 1024 * 1024
_MAX_FEISHU_FILE_BYTES = 30 * 1024 * 1024
_STREAM_STATE_CHECK_SECONDS = 1.0
_LIVE_TASK_POLL_SECONDS = 1.5


_logger = logging.getLogger(__name__)


def _expire_workspace_card(
    sender: Any,
    *,
    message_id: str,
    workspace: dict[str, Any],
) -> None:
    # Stale CardKit actions are fenced by message/revision/operation.  Do not
    # create a second user-visible replacement card for an already stale card.
    del sender, message_id, workspace


class _ManagedReplyStream:
    def __init__(
        self,
        stream: ReplyStream,
        sender,
        provider_context: dict[str, Any],
        store: FeishuWorkspaceRepository,
        account_id: str,
        address_hash: str,
        *,
        core: Any = None,
        owner_user_id: str = '',
    ):
        self._stream = stream
        self._sender = sender
        self._provider_context = provider_context
        self._store = store
        self._account_id = account_id
        self._address_hash = address_hash
        self._core = core
        self._owner_user_id = owner_user_id
        self._conversation_id = ''
        self._state_access_failed = False
        self._latest_snapshot = CoreStreamUpdate()
        self._snapshot_lock = threading.Lock()
        self._task_stop = threading.Event()
        self._task_thread: threading.Thread | None = None
        self._task_anchor_id = ''

    def update(self, snapshot: CoreStreamUpdate) -> None:
        if (
            snapshot.conversation_id
            and snapshot.conversation_id != self._conversation_id
        ):
            self._activate_conversation(snapshot.conversation_id)
        with self._snapshot_lock:
            if (
                not snapshot.task_progress
                and self._latest_snapshot.task_progress
            ):
                snapshot = replace(
                    snapshot,
                    task_progress=self._latest_snapshot.task_progress,
                )
            self._latest_snapshot = snapshot
        if snapshot.task_created:
            self._start_task_progress(
                snapshot.task_created,
                snapshot.conversation_id,
            )
        self._stream.update(snapshot)

    def _start_task_progress(
        self,
        task: dict[str, Any],
        conversation_id: str,
    ) -> None:
        task_id = str(task.get('task_id') or '')[:512]
        if (
            not task_id
            or not conversation_id
            or not self._owner_user_id
            or not callable(
                getattr(self._core, 'list_conversation_tasks', None)
            )
            or self._task_thread is not None
        ):
            return
        self._task_anchor_id = task_id
        self._task_stop.clear()
        self._task_thread = threading.Thread(
            target=self._poll_task_progress,
            args=(conversation_id,),
            name='feishu-live-task',
            daemon=True,
        )
        self._task_thread.start()

    def _poll_task_progress(self, conversation_id: str) -> None:
        request_id = str(
            self._provider_context.get('workspace_operation_id')
            or self._task_anchor_id
        )
        while not self._task_stop.is_set():
            try:
                tasks = self._core.list_conversation_tasks(
                    owner_user_id=self._owner_user_id,
                    conversation_id=conversation_id,
                    request_id=f'{request_id}_live_tasks',
                    summary_only=True,
                )
                progress = task_progress_text(
                    find_task(tasks, self._task_anchor_id)
                )
                if progress:
                    with self._snapshot_lock:
                        snapshot = replace(
                            self._latest_snapshot,
                            task_created=None,
                            task_progress=progress,
                        )
                        self._latest_snapshot = snapshot
                    self._stream.update(snapshot)
            except Exception:
                _logger.warning(
                    'feishu_live_task_refresh_failed task_id=%s',
                    self._task_anchor_id,
                    exc_info=True,
                )
            self._task_stop.wait(_LIVE_TASK_POLL_SECONDS)

    def _stop_task_progress(self) -> None:
        self._task_stop.set()
        if self._task_thread is not None:
            self._task_thread.join(timeout=2)
            self._task_thread = None

    def _activate_conversation(self, conversation_id: str) -> None:
        try:
            self._store.activate_conversation(
                self._account_id,
                self._address_hash,
                conversation_id,
            )
        except Exception:
            self._log_state_failure('activate')
            return
        self._log_state_recovered()
        self._provider_context['workspace_conversation_id'] = conversation_id
        self._conversation_id = conversation_id

    def _log_state_failure(self, operation: str) -> None:
        if self._state_access_failed:
            return
        self._state_access_failed = True
        _logger.warning(
            'feishu_stream_state_%s_failed account_id=%s',
            operation,
            self._account_id,
            exc_info=True,
        )

    def _log_state_recovered(self) -> None:
        if not self._state_access_failed:
            return
        self._state_access_failed = False
        _logger.info(
            'feishu_stream_state_recovered account_id=%s',
            self._account_id,
        )

    def finish(self, final_text: str) -> bool:
        try:
            self._stop_task_progress()
            streamed = self._stream.finish(final_text)
            message_id = str(
                getattr(self._stream, 'message_id', '') or ''
            )
            if message_id:
                self._provider_context['workspace_stream_message_id'] = (
                    message_id
                )
            return streamed
        finally:
            self._sender.close()

    def abort(self) -> None:
        try:
            self._stop_task_progress()
            self._stream.abort()
        finally:
            self._sender.close()


class FeishuDeliveryProvider:
    def __init__(
        self,
        *,
        store: FeishuWorkspaceRepository,
        credentials: RuntimeCredentialStore,
        channels: FeishuOutboundFactory,
        renderer: OutboundRenderer,
        lazymind: StaticAssetClient,
    ):
        self._store = store
        self._credentials = credentials
        self._channels = channels
        self._renderer = FeishuPresentationRenderer(renderer)
        self._lazymind = lazymind

    def open_stream(
        self,
        message: ClaimedInbound,
    ) -> ReplyStream | None:
        command_action = message.provider_context.get('command_action')
        if (
            isinstance(command_action, dict)
            and str(command_action.get('command') or '')
            != 'chat'
        ):
            return None
        chat_id = str(
            message.provider_context.get('chat_id')
            or message.recipient_id
        )
        if not chat_id:
            return None
        account = self._credentials.load_runtime_account(
            message.account_id
        )
        sender = self._channels.create_sender(account['credentials'])
        workspace_message_id = str(
            message.provider_context.get('workspace_message_id')
            or ''
        )
        reanchor_to_bottom = (
            message.provider_context.get(
                'workspace_reanchor_to_bottom'
            ) is True
        )
        management = (
            message.provider_context.get('workspace_surface') == 'management'
        )
        try:
            stream = sender.start_card_stream(
                chat_id=chat_id,
                initial_card=streaming_reply_card({
                    **message.provider_context,
                    'chat_id': chat_id,
                }),
                message_id=(
                    '' if reanchor_to_bottom else workspace_message_id
                ),
                should_render=(
                    (lambda: self._workspace_chat_is_visible(message))
                    if management or reanchor_to_bottom
                    else None
                ),
                on_message_started=(
                    (
                        lambda message_id: self._adopt_stream_message(
                            message,
                            sender,
                            message_id,
                        )
                    )
                    if reanchor_to_bottom
                    else None
                ),
            )
        except Exception:
            sender.close()
            raise
        return _ManagedReplyStream(
            stream,
            sender,
            message.provider_context,
            self._store,
            message.account_id,
            message.order_key,
            core=self._lazymind,
            owner_user_id=message.owner_user_id,
        )

    def _adopt_stream_message(
        self,
        message: ClaimedInbound,
        sender: Any,
        message_id: str,
    ) -> None:
        context = message.provider_context
        source = FeishuWorkspaceState.from_dict(
            context.get('workspace_state')
        )
        saved = self._store.save_feishu_workspace_message(
            message.account_id,
            message.order_key,
            message_id,
            source.active_operation_id,
            source.message_id,
            source.revision,
        )
        if str(saved.get('message_id') or '') != message_id:
            context['_workspace_stream_suppress_final'] = True
            sender.update_card(
                message_id=message_id,
                card=stale_workspace_card(
                    str(saved.get('output_language') or 'zh')
                ),
            )
            return
        context['workspace_state'] = saved
        context['workspace_message_id'] = message_id
        context['workspace_stream_message_id'] = message_id

    def _workspace_chat_is_visible(
        self,
        message: ClaimedInbound,
        stream: ReplyStream | None = None,
    ) -> bool:
        del stream
        context = message.provider_context
        if context.get('_workspace_stream_suppress_final'):
            return False
        now = time.monotonic()
        checked_at = float(
            context.get('_workspace_visibility_checked_at') or 0.0
        )
        if now - checked_at < _STREAM_STATE_CHECK_SECONDS:
            return bool(context.get('_workspace_chat_visible', True))
        context['_workspace_visibility_checked_at'] = now
        try:
            state = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    message.account_id,
                    message.order_key,
                )
            )
        except Exception:
            _logger.warning(
                'feishu_stream_visibility_check_failed account_id=%s',
                message.account_id,
                exc_info=True,
            )
            context['_workspace_chat_visible'] = False
            return False
        operation_id = str(
            context.get('workspace_operation_id') or ''
        )
        visible = bool(
            (not operation_id or state.active_operation_id == operation_id)
            and context.get('workspace_surface') == 'reply'
            and state.message_id
            == str(context.get('workspace_message_id') or '')
        )
        context['_workspace_chat_visible'] = visible
        return visible

    def render(
        self,
        message: ClaimedOutbound,
    ) -> list[dict[str, Any]]:
        message = self._persist_workspace_result(message)
        parts = self._renderer.render(message)
        sources = [
            str(part.get('source') or '')
            for part in parts
            if part.get('kind') in {'image', 'file'}
            and part.get('source')
        ]
        if not sources:
            return parts
        account = self._credentials.load_runtime_account(
            message.account_id
        )
        try:
            for source in sources:
                self._lazymind.validate_static_asset(
                    source=source,
                    owner_user_id=str(account['owner_user_id']),
                )
        except InvalidStaticAssetError:
            return self._renderer.render(
                replace(
                    message,
                    text=(
                        'LazyMind 没有返回可读取的图片或文件。'
                        '它可能未实际生成，或临时链接已经失效；'
                        '请重新生成。'
                    ),
                    intent_kind='failed',
                    metadata={},
                )
            )
        return parts

    def _persist_workspace_result(
        self,
        message: ClaimedOutbound,
    ) -> ClaimedOutbound:
        context = dict(message.provider_context)
        if not context.get('workspace_state'):
            return message
        surface = context.get('workspace_surface')
        if surface != 'management':
            return message
        state = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                message.account_id,
                message.order_key,
            )
        )
        source_state = FeishuWorkspaceState.from_dict(
            context.get('workspace_state')
        )
        presentations = [
            dict(item)
            for item in (
                message.metadata.get('presentations')
                if isinstance(
                    message.metadata.get('presentations'),
                    list,
                )
                else []
            )
            if isinstance(item, dict)
        ]
        workspace_action = context.get('workspace_action')
        action_kind = (
            str(workspace_action.get('kind') or '')
            if isinstance(workspace_action, dict)
            else ''
        )
        expected_conversation_id = (
            str(workspace_action.get('expected_conversation_id') or '')
            if isinstance(workspace_action, dict)
            else ''
        )
        active_conversation_id = self._store.get_route(
            message.account_id,
            message.order_key,
        ) or ''
        source_conversation_id = str(
            context.get('workspace_conversation_id') or ''
        )
        stale = bool(
            state.revision != source_state.revision
            or state.active_operation_id
            != source_state.active_operation_id
            or state.view != source_state.view
            or (
                action_kind != 'history.switch'
                and active_conversation_id != source_conversation_id
            )
            or (
                expected_conversation_id
                and active_conversation_id != expected_conversation_id
            )
        )
        if stale:
            context['_workspace_delivery_suppressed'] = True
            return replace(
                message,
                text='',
                metadata={**message.metadata, 'presentations': []},
                provider_context=context,
            )
        context['workspace_state'] = state.to_dict()
        context['workspace_conversation_id'] = active_conversation_id
        context['workspace_message_id'] = state.message_id
        context['_workspace_result_complete'] = True
        if not presentations and message.text:
            context['_workspace_notice'] = str(message.text)[:2000]
        return replace(message, provider_context=context)

    def prepare_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        saved_state: dict[str, Any],
    ) -> dict[str, Any]:
        return saved_state

    def send_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        idempotency_key: str,
        saved_state: dict[str, Any],
    ) -> dict[str, Any] | None:
        chat_id = str(
            message.provider_context.get('chat_id')
            or message.recipient_id
        )
        if not chat_id:
            raise FeishuRuntimeError(
                'Feishu destination chat is missing'
            )
        kind = str(part.get('kind') or '')
        account = self._credentials.load_runtime_account(
            message.account_id
        )
        sender = self._channels.create_sender(
            account['credentials']
        )
        try:
            if kind == 'text':
                sender.send_markdown(
                    chat_id=chat_id,
                    text=str(part.get('text') or ''),
                    idempotency_key=idempotency_key,
                )
                return
            if kind == 'card':
                if saved_state.get('message_id'):
                    return saved_state
                card = part.get('card')
                if not isinstance(card, dict):
                    raise FeishuRuntimeError(
                        'Feishu card payload is invalid'
                    )
                workspace = message.provider_context.get('workspace_state')
                workspace = (
                    dict(workspace) if isinstance(workspace, dict) else {}
                )
                expected_message_id = str(
                    workspace.get('message_id') or ''
                )
                expected_operation_id = str(
                    message.provider_context.get('workspace_operation_id')
                    or ''
                )
                expected_revision = int(workspace.get('revision') or 0)
                workspace_matches = True
                workspace_stale = False
                adopted_revision = expected_revision
                current_workspace = workspace
                if part.get('workspace') is True:
                    current_workspace = self._store.get_feishu_workspace_state(
                        message.account_id,
                        message.order_key,
                    )
                    current_workspace = (
                        dict(current_workspace)
                        if isinstance(current_workspace, dict)
                        else {}
                    )
                    current_message_id = str(
                        current_workspace.get('message_id') or ''
                    )
                    lineage_matches = bool(
                        str(
                            current_workspace.get('active_operation_id')
                            or ''
                        )
                        == expected_operation_id
                        and int(current_workspace.get('revision') or 0)
                        == expected_revision
                    )
                    if (
                        lineage_matches
                        and current_message_id
                        and current_message_id != expected_message_id
                    ):
                        expected_message_id = current_message_id
                        message.provider_context['workspace_message_id'] = (
                            current_message_id
                        )
                    workspace_matches = bool(
                        lineage_matches
                        and current_message_id == expected_message_id
                    )
                    if not workspace_matches:
                        message.provider_context['workspace_state'] = (
                            current_workspace
                        )
                        return {
                            **saved_state,
                            'message_id': str(
                                current_workspace.get('message_id') or ''
                            ),
                            'workspace_revision': int(
                                current_workspace.get('revision') or 0
                            ),
                            'workspace_operation_id': str(
                                current_workspace.get(
                                    'active_operation_id'
                                ) or ''
                            ),
                            'workspace_stale': True,
                        }
                    message.provider_context['workspace_state'] = (
                        current_workspace
                    )
                target_message_id = str(
                    part.get('replace_message_id')
                    or (
                        expected_message_id
                        if (
                            part.get('workspace') is True
                            and message.provider_context.get(
                                'workspace_reanchor_to_bottom'
                            ) is not True
                        )
                        else ''
                    )
                    or ''
                )
                if target_message_id:
                    try:
                        sender.update_card(
                            message_id=target_message_id,
                            card=card,
                        )
                        message_id = target_message_id
                    except Exception as exc:
                        if not workspace_card_expired(exc):
                            raise
                        message_id = sender.send_card(
                            chat_id=chat_id,
                            card=card,
                            idempotency_key=idempotency_key,
                        )
                else:
                    message_id = sender.send_card(
                        chat_id=chat_id,
                        card=card,
                        idempotency_key=idempotency_key,
                    )
                if part.get('workspace') is True:
                    if message_id != expected_message_id:
                        if not workspace_matches:
                            saved_workspace = current_workspace
                        else:
                            saved_workspace = (
                                self._store.save_feishu_workspace_message(
                                    message.account_id,
                                    message.order_key,
                                    message_id,
                                    expected_operation_id,
                                    expected_message_id,
                                    expected_revision,
                                )
                            )
                        adopted_message_id = str(
                            saved_workspace.get('message_id') or ''
                        )
                        adopted_operation_id = str(
                            saved_workspace.get('active_operation_id') or ''
                        )
                        adopted_revision = int(
                            saved_workspace.get('revision') or 0
                        )
                        replay_adopted = (
                            adopted_message_id == message_id
                            and adopted_operation_id == expected_operation_id
                        )
                        if replay_adopted:
                            workspace_stale = (
                                adopted_revision != expected_revision
                            )
                        else:
                            workspace_stale = True
                            _expire_workspace_card(
                                sender,
                                message_id=message_id,
                                workspace=saved_workspace,
                            )
                            message_id = adopted_message_id
                        message.provider_context['workspace_state'] = (
                            saved_workspace
                        )
                        message.provider_context[
                            'workspace_message_id'
                        ] = message_id
                        message.provider_context[
                            'workspace_stream_message_id'
                        ] = message_id
                    else:
                        saved_workspace = current_workspace
                        adopted_revision = int(
                            saved_workspace.get('revision') or 0
                        )
                return {
                    **saved_state,
                    'message_id': message_id,
                    'workspace_revision': int(
                        (
                            message.provider_context.get('workspace_state') or {}
                        ).get('revision') or adopted_revision
                    ),
                    'workspace_operation_id': str(
                        (
                            message.provider_context.get('workspace_state') or {}
                        ).get('active_operation_id')
                        or expected_operation_id
                    ),
                    'workspace_stale': workspace_stale,
                }
            source = str(part.get('source') or '')
            if kind == 'image':
                if saved_state.get('image_key'):
                    return saved_state
                caption = str(
                    part.get('caption') or part.get('alt') or ''
                )
                image_identity = hashlib.sha256(
                    (source or idempotency_key).encode('utf-8')
                ).hexdigest()
                image_delivery_id = idempotency_key[:512]
                try:
                    content = self._lazymind.download_static_image(
                        source=source,
                        owner_user_id=str(account['owner_user_id']),
                    )
                except InvalidStaticAssetError:
                    self._send_asset_failure(
                        sender=sender,
                        chat_id=chat_id,
                        idempotency_key=idempotency_key,
                        kind='图片',
                    )
                    return
                if len(content) > _MAX_FEISHU_IMAGE_BYTES:
                    raise FeishuRuntimeError('飞书图片不能超过 10 MB')
                target_message_id = str(
                    message.provider_context.get(
                        'workspace_stream_message_id'
                    )
                    or ''
                )
                if not target_message_id:
                    sender.send_image(
                        chat_id=chat_id,
                        content=content,
                        caption=caption,
                        idempotency_key=idempotency_key,
                    )
                    return {
                        **saved_state,
                        'image_key': source or idempotency_key,
                    }
                image_key = sender.upload_image(content=content)
                if not image_key:
                    raise FeishuRuntimeError('飞书图片上传失败')
                workspace = FeishuWorkspaceState.from_dict(
                    message.provider_context.get('workspace_state')
                )
                workspace.add_image(
                    image_key=image_key,
                    caption=caption,
                    identity=image_identity,
                    delivery_id=image_delivery_id,
                )
                workspace_payload = workspace.to_dict()
                try:
                    persisted = self._store.patch_feishu_workspace_state(
                        message.account_id,
                        message.order_key,
                        {'images': workspace_payload['images']},
                        operation_id=str(
                            message.provider_context.get(
                                'workspace_operation_id'
                            )
                            or ''
                        ),
                    )
                except Exception:
                    _logger.warning(
                        'feishu_image_state_save_failed account_id=%s',
                        message.account_id,
                        exc_info=True,
                    )
                    persisted = workspace_payload
                message.provider_context['workspace_state'] = persisted
                language = (
                    'en'
                    if persisted.get('output_language') == 'en'
                    else 'zh'
                )
                final_card = FeishuReplyRenderer.render(
                    provider_context=message.provider_context,
                    text=media_free_feishu_text(message.text),
                    status=(
                        '✅ **Answer complete**'
                        if language == 'en'
                        else '✅ **回答完成**'
                    ),
                )
                sender.update_card(
                    message_id=target_message_id,
                    card=final_card,
                )
                return {
                    **saved_state,
                    'image_key': image_key,
                    'message_id': target_message_id,
                    'workspace_revision': int(
                        persisted.get('revision') or 0
                    ),
                    'workspace_operation_id': '',
                    'workspace_stale': False,
                }
            if kind == 'file':
                artifact_index = str(
                    part.get('artifact_index') or ''
                )
                if artifact_index:
                    content = inline_artifact_bytes(
                        message.metadata,
                        artifact_index,
                    )
                    if content is None:
                        raise FeishuRuntimeError(
                            'LazyMind inline artifact is invalid'
                        )
                else:
                    try:
                        content = self._lazymind.download_static_file(
                            source=source,
                            owner_user_id=str(account['owner_user_id']),
                        )
                    except InvalidStaticAssetError:
                        self._send_asset_failure(
                            sender=sender,
                            chat_id=chat_id,
                            idempotency_key=idempotency_key,
                            kind='文件',
                        )
                        return
                if len(content) > _MAX_FEISHU_FILE_BYTES:
                    raise FeishuRuntimeError(
                        '飞书文件不能超过 30 MB'
                    )
                sender.send_file(
                    chat_id=chat_id,
                    content=content,
                    filename=str(
                        part.get('filename')
                        or 'lazymind-output'
                    ),
                    idempotency_key=idempotency_key,
                )
                return
            raise FeishuRuntimeError(
                'Unsupported Feishu outbound part'
            )
        finally:
            sender.close()

    @staticmethod
    def _send_asset_failure(
        *,
        sender,
        chat_id: str,
        idempotency_key: str,
        kind: str,
    ) -> None:
        sender.send_markdown(
            chat_id=chat_id,
            text=(
                f'⚠️ LazyMind 没有返回可读取的{kind}文件。'
                '它可能未实际生成，或临时链接已经失效；请重新生成。'
            ),
            idempotency_key=idempotency_key,
        )
