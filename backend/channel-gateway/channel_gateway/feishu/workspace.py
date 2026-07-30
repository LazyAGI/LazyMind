import logging
import time

from channel_gateway.common.domain.channel import (
    NativeConversationTarget,
    NativeTargetError,
)
from channel_gateway.common.ports.messaging import NativeThreadRepository
from channel_gateway.common.ports.providers import RuntimeCredentialStore
from channel_gateway.feishu.domain import (
    FeishuAddressFactory,
    FeishuAppCredentials,
    FeishuRuntimeError,
    FeishuWorkspace,
)
from channel_gateway.feishu.ports import (
    FeishuOutboundFactory,
    FeishuWorkspaceAdmin,
    FeishuWorkspaceLeaseRepository,
    FeishuWorkspaceRepository,
)


_logger = logging.getLogger(__name__)


class FeishuWorkspaceService:
    """Idempotently provisions one private thread workspace per account."""

    def __init__(
        self,
        *,
        store: FeishuWorkspaceRepository,
        leases: FeishuWorkspaceLeaseRepository,
        admin: FeishuWorkspaceAdmin,
    ):
        self._store = store
        self._leases = leases
        self._admin = admin

    def ensure(
        self,
        *,
        account_id: str,
        credentials: FeishuAppCredentials,
    ) -> FeishuWorkspace:
        current = self._store.get_by_account(account_id)
        if current and current.status == 'ready' and current.chat_id:
            return current
        lease = self._leases.acquire_runtime_lease(
            f'feishu-workspace:{account_id}'
        )
        if lease is None:
            return self._wait_for_ready(account_id)
        try:
            current = self._store.get_by_account(account_id)
            if current and current.status == 'ready' and current.chat_id:
                return current
            try:
                chat_id = (
                    current.chat_id
                    if current and current.chat_id
                    else self._admin.create_workspace(
                        credentials=credentials,
                        account_id=account_id,
                        owner_open_id=(
                            credentials.provider_account_id
                        ),
                        owner_name=credentials.display_name,
                    )
                )
                workspace = self._store.save_ready(
                    account_id=account_id,
                    chat_id=chat_id,
                    owner_open_id=credentials.provider_account_id,
                )
                return workspace
            except Exception as exc:
                self._store.mark_failed(
                    account_id=account_id,
                    owner_open_id=credentials.provider_account_id,
                    error=str(exc),
                )
                raise
        finally:
            lease.close()

    def _wait_for_ready(
        self,
        account_id: str,
    ) -> FeishuWorkspace:
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            workspace = self._store.get_by_account(account_id)
            if (
                workspace
                and workspace.status == 'ready'
                and workspace.chat_id
            ):
                return workspace
            time.sleep(0.5)
        raise FeishuRuntimeError(
            'Feishu workspace provisioning is already in progress'
        )


class FeishuConversationSurface:
    """Projects LazyMind conversation boundaries onto Feishu topics."""

    def __init__(
        self,
        *,
        credentials: RuntimeCredentialStore,
        workspaces: FeishuWorkspaceService,
        channels: FeishuOutboundFactory,
        addresses: FeishuAddressFactory,
        topics: NativeThreadRepository,
    ):
        self._credentials = credentials
        self._workspaces = workspaces
        self._channels = channels
        self._addresses = addresses
        self._topics = topics

    def open_conversation(
        self,
        *,
        account_id: str,
        owner_user_id: str,
        current_external_address_hash: str,
        current_provider_context: dict,
        has_current_conversation: bool,
        operation_kind: str,
        request_id: str,
        request_text: str,
        prepared_command: dict,
        grounding_messages: list[str],
        prepared_catalog: dict,
    ) -> NativeConversationTarget | None:
        if (
            current_provider_context.get('surface')
            == 'native_thread'
            and not has_current_conversation
            and operation_kind != 'conversation.new'
        ):
            return self._open_current_topic(
                account_id=account_id,
                current_external_address_hash=(
                    current_external_address_hash
                ),
                current_provider_context=current_provider_context,
                operation_kind=operation_kind,
                request_id=request_id,
                prepared_command=prepared_command,
                grounding_messages=grounding_messages,
                prepared_catalog=prepared_catalog,
            )
        operation_reserved = False
        created_target = None
        try:
            account = self._credentials.load_runtime_account(account_id)
            if str(account['owner_user_id']) != owner_user_id:
                raise FeishuRuntimeError(
                    'Feishu account owner does not match'
                )
            credentials = account['credentials']
            workspace = self._workspaces.ensure(
                account_id=account_id,
                credentials=credentials,
            )
            operation = self._topics.reserve_native_operation(
                account_id=account_id,
                provider='feishu',
                operation_id=request_id,
                operation_kind=operation_kind,
                container_id=workspace.chat_id,
                source_external_address_hash=(
                    current_external_address_hash
                ),
                prepared_command=prepared_command,
                grounding_messages=grounding_messages,
                prepared_catalog=prepared_catalog,
            )
            operation_reserved = True
            if str(operation.get('operation_kind') or '') != operation_kind:
                raise FeishuRuntimeError(
                    'Native operation kind does not match prepared command'
                )
            existing_root = str(
                operation.get('root_message_id') or ''
            )
            existing_hash = str(
                operation.get('external_address_hash') or ''
            )
            if existing_root and existing_hash:
                existing_container = str(
                    operation.get('container_id') or ''
                )
                if not existing_container:
                    raise FeishuRuntimeError(
                        'Native operation container is missing'
                    )
                self._topics.record_native_thread(
                    account_id=account_id,
                    provider='feishu',
                    container_id=existing_container,
                    root_message_id=existing_root,
                    thread_id='',
                    external_address_hash=existing_hash,
                    operation_id=request_id,
                )
                return self._target(
                    sender_id=credentials.provider_account_id,
                    chat_id=existing_container,
                    root_message_id=existing_root,
                    external_address_hash=existing_hash,
                    operation_id=request_id,
                    operation_kind=operation_kind,
                    reused=True,
                    cached_result=(
                        operation.get('result_json')
                        if operation.get('status') == 'ready'
                        else None
                    ),
                )
            summary = request_text.strip()
            if len(summary) > 300:
                summary = f'{summary[:300]}…'
            heading = (
                'LazyMind 新会话'
                if operation_kind == 'conversation.new'
                else 'LazyMind 会话'
            )
            text = f'**{heading}**'
            if summary:
                text = f'{text}\n\n来自你的请求：{summary}'
            sender = self._channels.create_sender(credentials)
            try:
                root_message_id = sender.send_markdown(
                    chat_id=workspace.chat_id,
                    text=text,
                    reply_to=None,
                    reply_in_thread=False,
                    idempotency_key=(
                        f'feishu-native-conversation:{request_id}'
                    ),
                )
            finally:
                sender.close()
            address = self._addresses.workspace_thread(
                account_id,
                workspace.chat_id,
                root_message_id,
                credentials.provider_account_id,
            )
            created_target = self._target(
                sender_id=credentials.provider_account_id,
                chat_id=workspace.chat_id,
                root_message_id=root_message_id,
                external_address_hash=address.route_hash,
                operation_id=request_id,
                operation_kind=operation_kind,
                reused=False,
                cached_result=None,
            )
            operation = self._topics.attach_native_operation_target(
                account_id=account_id,
                provider='feishu',
                operation_id=request_id,
                root_message_id=root_message_id,
                external_address_hash=address.route_hash,
            )
            self._topics.record_native_thread(
                account_id=account_id,
                provider='feishu',
                container_id=workspace.chat_id,
                root_message_id=root_message_id,
                thread_id='',
                external_address_hash=address.route_hash,
                operation_id=request_id,
            )
        except Exception as exc:
            try:
                if operation_reserved:
                    self._topics.defer_native_operation(
                        account_id=account_id,
                        provider='feishu',
                        operation_id=request_id,
                        error=exc.__class__.__name__,
                    )
            except Exception:
                _logger.exception(
                    'feishu_native_operation_failure_record_failed '
                    'account_id=%s operation_id=%s',
                    account_id,
                    request_id,
                )
            if created_target is not None:
                raise NativeTargetError(created_target) from exc
            raise
        return self._target(
            sender_id=credentials.provider_account_id,
            chat_id=workspace.chat_id,
            root_message_id=root_message_id,
            external_address_hash=address.route_hash,
            operation_id=request_id,
            operation_kind=operation_kind,
            reused=False,
            cached_result=(
                operation.get('result_json')
                if operation.get('status') == 'ready'
                else None
            ),
        )

    def _open_current_topic(
        self,
        *,
        account_id: str,
        current_external_address_hash: str,
        current_provider_context: dict,
        operation_kind: str,
        request_id: str,
        prepared_command: dict,
        grounding_messages: list[str],
        prepared_catalog: dict,
    ) -> NativeConversationTarget:
        chat_id = str(current_provider_context.get('chat_id') or '')
        root_message_id = str(
            current_provider_context.get('root_message_id')
            or current_provider_context.get('message_id')
            or ''
        )
        if not chat_id or not root_message_id:
            raise FeishuRuntimeError(
                'Current Feishu topic address is incomplete'
            )
        operation_reserved = False
        try:
            operation = self._topics.reserve_native_operation(
                account_id=account_id,
                provider='feishu',
                operation_id=request_id,
                operation_kind=operation_kind,
                container_id=chat_id,
                source_external_address_hash=(
                    current_external_address_hash
                ),
                prepared_command=prepared_command,
                grounding_messages=grounding_messages,
                prepared_catalog=prepared_catalog,
            )
            operation_reserved = True
            if str(operation.get('operation_kind') or '') != operation_kind:
                raise FeishuRuntimeError(
                    'Native operation kind does not match prepared command'
                )
            existing_root = str(
                operation.get('root_message_id') or ''
            )
            existing_hash = str(
                operation.get('external_address_hash') or ''
            )
            if (
                (existing_root and existing_root != root_message_id)
                or (
                    existing_hash
                    and existing_hash != current_external_address_hash
                )
            ):
                raise FeishuRuntimeError(
                    'Native operation is already bound to another topic'
                )
            operation = self._topics.attach_native_operation_target(
                account_id=account_id,
                provider='feishu',
                operation_id=request_id,
                root_message_id=root_message_id,
                external_address_hash=current_external_address_hash,
            )
            self._topics.record_native_thread(
                account_id=account_id,
                provider='feishu',
                container_id=chat_id,
                root_message_id=root_message_id,
                thread_id=str(
                    current_provider_context.get('thread_id') or ''
                ),
                external_address_hash=current_external_address_hash,
                operation_id=request_id,
            )
        except Exception as exc:
            if operation_reserved:
                try:
                    self._topics.defer_native_operation(
                        account_id=account_id,
                        provider='feishu',
                        operation_id=request_id,
                        error=exc.__class__.__name__,
                    )
                except Exception:
                    _logger.exception(
                        'feishu_native_operation_failure_record_failed '
                        'account_id=%s operation_id=%s',
                        account_id,
                        request_id,
                    )
            raise
        context = dict(current_provider_context)
        context.update(
            {
                'surface': 'native_thread',
                'root_message_id': root_message_id,
                'reply_in_thread': True,
            }
        )
        return NativeConversationTarget(
            external_address_hash=current_external_address_hash,
            recipient_id=chat_id,
            provider_context=context,
            operation_id=request_id,
            operation_kind=operation_kind,
            reused=bool(existing_root and existing_hash),
            cached_result=(
                dict(operation.get('result_json'))
                if (
                    operation.get('status') == 'ready'
                    and isinstance(operation.get('result_json'), dict)
                )
                else None
            ),
        )

    @staticmethod
    def _target(
        *,
        sender_id: str,
        chat_id: str,
        root_message_id: str,
        external_address_hash: str,
        operation_id: str,
        operation_kind: str,
        reused: bool,
        cached_result,
    ) -> NativeConversationTarget:
        return NativeConversationTarget(
            external_address_hash=external_address_hash,
            recipient_id=chat_id,
            provider_context={
                'message_id': root_message_id,
                'chat_id': chat_id,
                'sender_id': sender_id,
                'surface': 'native_thread',
                'root_message_id': root_message_id,
                'root_id': '',
                'parent_id': '',
                'thread_id': '',
                'reply_in_thread': True,
            },
            operation_id=operation_id,
            operation_kind=operation_kind,
            reused=reused,
            cached_result=(
                dict(cached_result)
                if isinstance(cached_result, dict)
                else None
            ),
        )
