from dataclasses import dataclass

from channel_gateway.common.application.providers import (
    AccountApplicationService,
    AccountRuntimeSupervisor,
    ConnectionApplicationService,
)
from channel_gateway.common.application.actions import ChannelActionExecutor
from channel_gateway.common.application.intents import (
    ChannelIntentClassifier,
    ExactShortcutParser,
)
from channel_gateway.common.application.messages import (
    ChannelMessageService,
    NativeConversationCoordinator,
)
from channel_gateway.common.application.routing import ChannelCommandRouter
from channel_gateway.common.application.workers import (
    DeliveryWorker,
    MessageWorker,
)
from channel_gateway.common.domain.chat import (
    BASIC_CHAT_FEATURES,
    ChannelFeatureProfile,
)
from channel_gateway.common.domain.outbound import OutboundRenderer
from channel_gateway.common.infrastructure.lazymind import LazyMindClient
from channel_gateway.common.infrastructure.postgres import GatewayStore
from channel_gateway.common.infrastructure.security import JsonCipher
from channel_gateway.common.ports.providers import RuntimeSupervisor
from channel_gateway.common.ports.providers import AccountAdapter
from channel_gateway.common.ports.providers import (
    InteractiveConnectionAdapter,
)
from channel_gateway.common.ports.messaging import DeliveryProvider
from channel_gateway.common.ports.messaging import NativeConversationSurface
from channel_gateway.feishu.connection import FeishuConnectionService
from channel_gateway.feishu.domain import FeishuAddressFactory
from channel_gateway.feishu.delivery import FeishuDeliveryProvider
from channel_gateway.feishu.receiver import LarkChannelFactory
from channel_gateway.feishu.registration import LarkAppRegistrar
from channel_gateway.feishu.storage import (
    FeishuCredentialStore,
    FeishuWorkspaceStore,
)
from channel_gateway.feishu.runtime import FeishuRuntime
from channel_gateway.feishu.service import FeishuAccountService
from channel_gateway.feishu.workspace import FeishuWorkspaceService
from channel_gateway.feishu.workspace import FeishuConversationSurface
from channel_gateway.wechat.client import WeChatClient
from channel_gateway.wechat.domain import (
    WeChatAddressFactory,
    WeChatConfig,
)
from channel_gateway.wechat.credentials import WeChatCredentialStore
from channel_gateway.wechat.delivery import WeChatDeliveryProvider
from channel_gateway.wechat.runtime import WeChatRuntime
from channel_gateway.wechat.service import (
    WeChatConnectionService,
    WeChatRuntimeSupervisor,
)


@dataclass(frozen=True)
class Settings:
    database_dsn: str = 'postgresql://root:123456@db:5432/channel_gateway'
    credential_key_path: str = '/var/lib/lazymind/channel-gateway/master.key'
    core_base_url: str = 'http://core:8000'
    core_chat_timeout_seconds: int = 7200
    wechat_ilink_base_url: str = 'https://ilinkai.weixin.qq.com'
    wechat_qr_session_ttl_seconds: int = 480
    wechat_poll_timeout_seconds: int = 40
    wechat_max_consecutive_errors: int = 3
    wechat_text_chunk_size: int = 1800
    feishu_text_chunk_size: int = 3000


@dataclass(frozen=True)
class ProviderComponents:
    connection: InteractiveConnectionAdapter
    accounts: AccountAdapter
    delivery: DeliveryProvider
    features: ChannelFeatureProfile = BASIC_CHAT_FEATURES
    surface: NativeConversationSurface | None = None


class ProviderRegistry:
    """Resolves a channel provider without exposing concrete adapters."""

    def __init__(self) -> None:
        self._providers: dict[str, ProviderComponents] = {}

    def register(
        self,
        name: str,
        provider: ProviderComponents,
    ) -> None:
        normalized = name.strip().lower()
        if not normalized:
            raise ValueError('provider name is required')
        if normalized in self._providers:
            raise ValueError(f'provider already registered: {normalized}')
        self._providers[normalized] = provider

    def connection(
        self,
        name: str,
    ) -> InteractiveConnectionAdapter | None:
        provider = self._provider(name)
        return provider.connection if provider else None

    def accounts(self, name: str) -> AccountAdapter | None:
        provider = self._provider(name)
        return provider.accounts if provider else None

    def delivery(self, name: str) -> DeliveryProvider | None:
        provider = self._provider(name)
        return provider.delivery if provider else None

    def surface(
        self,
        provider: str,
    ) -> NativeConversationSurface | None:
        resolved = self._provider(provider)
        return resolved.surface if resolved else None

    def features(self, name: str) -> ChannelFeatureProfile:
        provider = self._provider(name)
        return provider.features if provider else BASIC_CHAT_FEATURES

    def _provider(self, name: str) -> ProviderComponents | None:
        return self._providers.get(name.strip().lower())


@dataclass(frozen=True)
class GatewayComponents:
    """Runtime object graph. Only this composition root knows concrete adapters."""

    store: GatewayStore
    feishu_workspaces: FeishuWorkspaceStore
    connections: ConnectionApplicationService
    accounts: AccountApplicationService
    message_worker: MessageWorker
    delivery_worker: DeliveryWorker
    runtime_supervisors: tuple[RuntimeSupervisor, ...]

    def start(self) -> None:
        self.store.initialize()
        self.feishu_workspaces.initialize()
        self.message_worker.start()
        self.delivery_worker.start()
        for supervisor in self.runtime_supervisors:
            supervisor.start()

    def stop(self) -> None:
        for supervisor in reversed(self.runtime_supervisors):
            supervisor.stop()
        self.message_worker.stop()
        self.delivery_worker.stop()


def build_components(settings: Settings | None = None) -> GatewayComponents:
    resolved_settings = settings or Settings()
    wechat_config = WeChatConfig(
        ilink_base_url=resolved_settings.wechat_ilink_base_url,
        qr_session_ttl_seconds=(
            resolved_settings.wechat_qr_session_ttl_seconds
        ),
        poll_timeout_seconds=resolved_settings.wechat_poll_timeout_seconds,
        max_consecutive_errors=(
            resolved_settings.wechat_max_consecutive_errors
        ),
        text_chunk_size=resolved_settings.wechat_text_chunk_size,
    )
    store = GatewayStore(resolved_settings.database_dsn)
    feishu_workspace_store = FeishuWorkspaceStore(
        resolved_settings.database_dsn
    )
    cipher = JsonCipher(resolved_settings.credential_key_path)
    lazymind = LazyMindClient(
        resolved_settings.core_base_url,
        resolved_settings.core_chat_timeout_seconds,
    )
    wechat_client = WeChatClient(
        resolved_settings.wechat_ilink_base_url,
        resolved_settings.wechat_poll_timeout_seconds,
    )
    wechat_credentials = WeChatCredentialStore(store, cipher)
    runtime = WeChatRuntime(
        config=wechat_config,
        store=store,
        credentials=wechat_credentials,
        client=wechat_client,
        addresses=WeChatAddressFactory(),
    )
    wechat_connections = WeChatConnectionService(
        config=wechat_config,
        store=store,
        cipher=cipher,
        client=wechat_client,
        on_account_connected=runtime.restart_account,
        on_account_disconnected=runtime.stop_account,
    )
    feishu_credentials = FeishuCredentialStore(
        store=store,
        cipher=cipher,
    )
    feishu_channels = LarkChannelFactory()
    feishu_addresses = FeishuAddressFactory()
    feishu_workspaces = FeishuWorkspaceService(
        store=feishu_workspace_store,
        leases=store,
        admin=feishu_channels,
    )
    feishu_runtime = FeishuRuntime(
        store=store,
        credentials=feishu_credentials,
        channels=feishu_channels,
        addresses=feishu_addresses,
        workspaces=feishu_workspaces,
        topics=store,
    )
    feishu_accounts = FeishuAccountService(
        store=store,
        cipher=cipher,
        on_account_connected=feishu_runtime.restart_account,
        on_account_disconnected=feishu_runtime.stop_account,
    )
    feishu_connections = FeishuConnectionService(
        store=store,
        cipher=cipher,
        registrar=LarkAppRegistrar(),
        accounts=feishu_accounts,
        workspaces=feishu_workspaces,
    )
    wechat_delivery = WeChatDeliveryProvider(
        client=wechat_client,
        credentials=wechat_credentials,
        renderer=OutboundRenderer(wechat_config.text_chunk_size),
        lazymind=lazymind,
    )
    feishu_surface = FeishuConversationSurface(
        credentials=feishu_credentials,
        workspaces=feishu_workspaces,
        channels=feishu_channels,
        addresses=feishu_addresses,
        topics=store,
    )
    feishu_delivery = FeishuDeliveryProvider(
        credentials=feishu_credentials,
        channels=feishu_channels,
        renderer=OutboundRenderer(
            resolved_settings.feishu_text_chunk_size
        ),
        lazymind=lazymind,
    )
    providers = ProviderRegistry()
    providers.register(
        'wechat',
        ProviderComponents(
            connection=wechat_connections,
            accounts=wechat_connections,
            delivery=wechat_delivery,
            features=BASIC_CHAT_FEATURES,
        ),
    )
    providers.register(
        'feishu',
        ProviderComponents(
            connection=feishu_connections,
            accounts=feishu_accounts,
            delivery=feishu_delivery,
            surface=feishu_surface,
            features=ChannelFeatureProfile(
                enable_ask=True,
                enable_plugin=True,
                enable_skill=True,
                enable_subagent=True,
                enable_tasks=True,
            ),
        ),
    )
    executor = ChannelActionExecutor(
        store=store,
        feature_resolver=providers.features,
        client=lazymind,
    )
    messages = ChannelMessageService(
        router=ChannelCommandRouter(
            store=store,
            shortcuts=ExactShortcutParser(store),
            classifier=ChannelIntentClassifier(lazymind),
            feature_resolver=providers.features,
        ),
        native_conversations=NativeConversationCoordinator(
            store=store,
            surfaces=providers,
            executor=executor,
        ),
        executor=executor,
    )
    message_worker = MessageWorker(
        store=store,
        messages=messages,
    )
    delivery_worker = DeliveryWorker(
        store=store,
        providers=providers,
    )
    wechat_accounts = AccountRuntimeSupervisor(
        provider='wechat',
        store=store,
        runtime=runtime,
    )
    feishu_accounts_runtime = AccountRuntimeSupervisor(
        provider='feishu',
        store=store,
        runtime=feishu_runtime,
    )
    return GatewayComponents(
        store=store,
        feishu_workspaces=feishu_workspace_store,
        connections=ConnectionApplicationService(
            store=store,
            providers=providers,
        ),
        accounts=AccountApplicationService(
            store=store,
            providers=providers,
        ),
        message_worker=message_worker,
        delivery_worker=delivery_worker,
        runtime_supervisors=(
            WeChatRuntimeSupervisor(
                connections=wechat_connections,
                accounts=wechat_accounts,
            ),
            feishu_accounts_runtime,
            feishu_connections,
        ),
    )
