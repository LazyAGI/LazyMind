import truststore


truststore.inject_into_ssl()

from channel_gateway.app import app  # noqa: E402


__all__ = ['app']
