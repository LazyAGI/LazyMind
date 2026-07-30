import os
from dataclasses import dataclass, field


def _env(name: str, default: str) -> str:
    return (os.getenv(name) or '').strip() or default


@dataclass(frozen=True)
class Settings:
    database_dsn: str = field(default_factory=lambda: _env(
        'LAZYMIND_CHANNEL_GATEWAY_DATABASE_DSN',
        'postgresql://root:123456@db:5432/channel_gateway',
    ))
    credential_key_path: str = field(default_factory=lambda: _env(
        'LAZYMIND_CHANNEL_GATEWAY_CREDENTIAL_KEY_PATH',
        '/var/lib/lazymind/channel-gateway/master.key',
    ))
    core_base_url: str = field(default_factory=lambda: _env(
        'LAZYMIND_CHANNEL_GATEWAY_CORE_BASE_URL',
        'http://core:8000',
    ))
    core_chat_timeout_seconds: int = 7200
    wechat_ilink_base_url: str = 'https://ilinkai.weixin.qq.com'
    wechat_qr_session_ttl_seconds: int = 480
    wechat_poll_timeout_seconds: int = 40
    wechat_max_consecutive_errors: int = 3
    wechat_text_chunk_size: int = 1800
