from dataclasses import dataclass


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
