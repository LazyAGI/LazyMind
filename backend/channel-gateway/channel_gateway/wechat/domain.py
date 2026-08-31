from dataclasses import dataclass

from channel_gateway.common.domain.channel import ChannelAddress


class WeChatError(RuntimeError):
    pass


@dataclass(frozen=True, slots=True)
class WeChatConfig:
    ilink_base_url: str
    qr_session_ttl_seconds: int
    poll_timeout_seconds: int
    max_consecutive_errors: int
    text_chunk_size: int
    upload_root: str
    max_inbound_media_bytes: int


class WeChatAddressFactory:
    @staticmethod
    def direct(account_id: str, sender_id: str) -> ChannelAddress:
        canonical = f'wechat:{account_id}:{sender_id}'
        return ChannelAddress(
            canonical_key=canonical,
            actor_key=canonical,
        )
